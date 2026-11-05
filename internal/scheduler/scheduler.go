package scheduler

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"anubisbot/internal/batch"
	"anubisbot/internal/burnbond"
	"anubisbot/internal/logstore"
	"anubisbot/internal/rpc"
	"anubisbot/internal/settings"
	"anubisbot/internal/tasks"
	"anubisbot/internal/timesync"

	"github.com/ethereum/go-ethereum/core/types"
)

type Scheduler struct {
	Tasks    *tasks.Store
	Syncer   *timesync.Sync
	Runner   *batch.Runner
	Logs     *logstore.Store
	RPC      *rpc.Manager
	Settings *settings.Store

	mu     sync.Mutex
	cancel context.CancelFunc
	cache  map[string]*batch.SignedTx // taskID -> signed for current fireAt
	signed map[string]int64           // taskID -> fireAt unix nano already signed
	// sentFanout: "taskID|fireKey|rpcID" -> true
	sentFanout map[string]bool
	// fanoutOk / fanoutDone counts per "taskID|fireKey"
	fanoutOk     map[string]int
	fanoutDone   map[string]int
	fanoutNeed   map[string]int
	fanoutHash   map[string]string // best tx hash for this fire
	fanoutRPC    map[string]string // rpc that first accepted best hash
	fanoutRTT    map[string]int64
	firedMark    map[string]bool      // task marked fired for this fireKey
	finishedMark map[string]bool      // fanout completed for fireKey
	fanoutSendAt map[string]time.Time // fanKey -> first successful send wall clock
}

func (s *Scheduler) Start(parent context.Context) {
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	if s.cache == nil {
		s.cache = map[string]*batch.SignedTx{}
	}
	if s.signed == nil {
		s.signed = map[string]int64{}
	}
	if s.sentFanout == nil {
		s.sentFanout = map[string]bool{}
	}
	if s.fanoutOk == nil {
		s.fanoutOk = map[string]int{}
	}
	if s.fanoutDone == nil {
		s.fanoutDone = map[string]int{}
	}
	if s.fanoutNeed == nil {
		s.fanoutNeed = map[string]int{}
	}
	if s.fanoutHash == nil {
		s.fanoutHash = map[string]string{}
	}
	if s.fanoutRPC == nil {
		s.fanoutRPC = map[string]string{}
	}
	if s.fanoutRTT == nil {
		s.fanoutRTT = map[string]int64{}
	}
	if s.firedMark == nil {
		s.firedMark = map[string]bool{}
	}
	if s.finishedMark == nil {
		s.finishedMark = map[string]bool{}
	}
	if s.fanoutSendAt == nil {
		s.fanoutSendAt = map[string]time.Time{}
	}
	s.mu.Unlock()
	go s.loop(ctx)
}

func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

func (s *Scheduler) loop(ctx context.Context) {
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) signLead() time.Duration {
	ms := 800
	if s.Settings != nil {
		if v := s.Settings.Get().SignLeadMs; v > 0 {
			ms = v
		}
	}
	return time.Duration(ms) * time.Millisecond
}

func fanKey(taskID string, fireKey int64) string {
	return fmt.Sprintf("%s|%d", taskID, fireKey)
}

func sendKey(taskID string, fireKey int64, rpcID string) string {
	return fmt.Sprintf("%s|%d|%s", taskID, fireKey, rpcID)
}

func (s *Scheduler) tick(ctx context.Context) {
	now := s.Syncer.NowChain()
	maxLead := s.RPC.MaxLeadMs()
	signLead := s.signLead()
	activeIDs := s.RPC.ActiveIDs()
	leads := s.RPC.Leads()
	rtts := s.RPC.RTTs()

	var toSign []tasks.Task
	for _, t := range s.Tasks.List() {
		if t.Status != "armed" || !t.Enabled || t.FireAt == nil {
			continue
		}
		fireAt := t.FireAt.UTC()
		fireKey := fireAt.UnixNano()
		signAt := fireAt.Add(-time.Duration(maxLead)*time.Millisecond - signLead)

		s.mu.Lock()
		alreadySigned := s.signed[t.ID] == fireKey
		s.mu.Unlock()

		if !alreadySigned && !now.Before(signAt) {
			toSign = append(toSign, t)
		}
	}

	// Sign before any fanout send so all RPCs share one nonce/hash.
	for _, t := range toSign {
		fireKey := t.FireAt.UTC().UnixNano()
		signed, err := s.Runner.Sign(ctx, t, t.FireAt.UTC())
		s.mu.Lock()
		if err != nil {
			s.Logs.Add(logstore.Entry{
				TaskID: t.ID, Wallet: t.WalletName, Event: "sign_fail",
				Message: err.Error(),
			})
		} else {
			s.cache[t.ID] = signed
			s.signed[t.ID] = fireKey
			s.Logs.Add(logstore.Entry{
				TaskID: t.ID, Wallet: t.WalletName, Event: "signed",
				Message: fmt.Sprintf("pre-signed for fireAt=%s maxLead=%dms", t.FireAt.Format(time.RFC3339), maxLead),
			})
		}
		s.mu.Unlock()
	}

	for _, t := range s.Tasks.List() {
		if t.Status != "armed" || !t.Enabled || t.FireAt == nil {
			continue
		}
		fireAt := t.FireAt.UTC()
		fireKey := fireAt.UnixNano()
		fk := fanKey(t.ID, fireKey)

		s.mu.Lock()
		if _, ok := s.fanoutNeed[fk]; !ok {
			s.fanoutNeed[fk] = len(activeIDs)
			s.fanoutOk[fk] = 0
			s.fanoutDone[fk] = 0
			s.fanoutHash[fk] = ""
			s.fanoutRPC[fk] = ""
			s.fanoutRTT[fk] = 0
		}
		cached := s.cache[t.ID]
		live := s.Runner != nil && s.Runner.Mode == "live"
		ready := cached != nil && (cached.Tx != nil || cached.Sim || !live)
		s.mu.Unlock()

		if !ready {
			continue
		}

		for _, rpcID := range activeIDs {
			lead := leads[rpcID]
			sendAt := fireAt.Add(-time.Duration(lead) * time.Millisecond)
			sk := sendKey(t.ID, fireKey, rpcID)

			s.mu.Lock()
			already := s.sentFanout[sk]
			s.mu.Unlock()
			if already || now.Before(sendAt) {
				continue
			}

			s.mu.Lock()
			s.sentFanout[sk] = true
			cached = s.cache[t.ID]
			if !s.firedMark[fk] {
				s.firedMark[fk] = true
				tt := t
				tt.Status = "fired"
				nowCopy := now
				tt.LastRunAt = &nowCopy
				_ = s.Tasks.Update(tt)
				s.Logs.Add(logstore.Entry{
					TaskID: t.ID, Wallet: t.WalletName, Event: "fired",
					Message: fmt.Sprintf("fanout start maxLead=%dms rpcs=%d", maxLead, len(activeIDs)),
				})
			}
			s.mu.Unlock()

			rtt := rtts[rpcID]
			res := s.Runner.SendToRPC(ctx, t, cached, rpcID, rtt, lead)
			s.mu.Lock()
			s.fanoutDone[fk]++
			if res.Error == "" {
				s.fanoutOk[fk]++
				if res.TxHash != "" && s.fanoutHash[fk] == "" {
					s.fanoutHash[fk] = res.TxHash
					s.fanoutRPC[fk] = res.RPCID
					s.fanoutRTT[fk] = rtt
				}
				if !res.Sim {
					if _, ok := s.fanoutSendAt[fk]; !ok {
						s.fanoutSendAt[fk] = time.Now()
					}
				}
			}
			done := s.fanoutDone[fk]
			need := s.fanoutNeed[fk]
			okN := s.fanoutOk[fk]
			s.mu.Unlock()

			if done >= need && need > 0 {
				s.mu.Lock()
				alreadyFin := s.finishedMark[fk]
				if !alreadyFin {
					s.finishedMark[fk] = true
				}
				s.mu.Unlock()
				if !alreadyFin {
					s.finishFanout(t.ID, fireKey, okN > 0)
				}
			}
		}
	}
}

func (s *Scheduler) finishFanout(taskID string, fireKey int64, anyOK bool) {
	fk := fanKey(taskID, fireKey)
	t, ok := s.Tasks.Get(taskID)
	if !ok {
		return
	}
	tt := t
	preset, _ := s.Runner.Presets[t.TaskType]
	amt := batch.AmountLabel(t, preset)
	extra := map[string]any{"amount": amt, "ok": anyOK}
	start := time.Unix(0, fireKey)

	s.mu.Lock()
	sendAt := s.fanoutSendAt[fk]
	lastHash := s.fanoutHash[fk]
	lastRPC := s.fanoutRPC[fk]
	lastRTT := s.fanoutRTT[fk]
	s.mu.Unlock()

	var successRec *types.Receipt
	live := s.Runner != nil && s.Runner.Mode == "live"
	if anyOK {
		tt.LastError = ""
		if lastHash != "" {
			tt.LastTxHash = lastHash
		}
		tt.LastRpcID = lastRPC
		tt.LastRttMs = &lastRTT

		simHash := strings.HasPrefix(lastHash, "sim-")
		if live && !simHash {
			if lastHash == "" || s.RPC == nil {
				anyOK = false
				extra["ok"] = false
				tt.Status = "failed"
				tt.LastError = "broadcast ok but missing tx hash"
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
				rec, err := s.RPC.WaitReceipt(ctx, lastRPC, lastHash, 10*time.Second)
				if err != nil || rec == nil {
					anyOK = false
					extra["ok"] = false
					tt.Status = "failed"
					if err != nil {
						tt.LastError = "receipt timeout: " + err.Error()
					} else {
						tt.LastError = "receipt unavailable"
					}
				} else {
					extra["blockNumber"] = rec.BlockNumber.Uint64()
					extra["txIndex"] = uint64(rec.TransactionIndex)
					if !sendAt.IsZero() {
						if includeMs := inclusionMs(ctx, s.RPC, lastRPC, rec.BlockNumber, sendAt); includeMs >= 0 {
							extra["includeMs"] = includeMs
						}
					}
					if rec.Status == 0 {
						anyOK = false
						extra["ok"] = false
						tt.Status = "failed"
						tt.LastError = "tx reverted on-chain"
					} else {
						tt.Status = "success"
						successRec = rec
					}
				}
				cancel()
			}
		} else {
			// sim / non-live: accept without receipt
			tt.Status = "success"
		}
	} else {
		tt.Status = "failed"
		if tt.LastError == "" {
			tt.LastError = "all rpc fanout failed"
		}
	}
	extra["ok"] = anyOK
	if tt.Schedule == "once" {
		tt.Enabled = false
		tt.FireAt = nil
		if tt.Status == "armed" {
			tt.Status = "idle"
		}
	}
	_ = s.Tasks.Update(tt)

	now := time.Now()
	elapsedMs := now.Sub(start).Milliseconds()
	if elapsedMs < 0 {
		elapsedMs = 0
	}
	extra["elapsedMs"] = elapsedMs

	includeSuffix := ""
	if v, ok := extra["includeMs"].(int64); ok {
		includeSuffix = fmt.Sprintf(" · 上链 %dms", v)
	}

	msg := "失败"
	ev := "result"
	if anyOK {
		msg = "成功"
		if bn, ok := extra["blockNumber"].(uint64); ok {
			idx, _ := extra["txIndex"].(uint64)
			msg = fmt.Sprintf("成功 · %s · 块内第 %d 笔 · 高度 %d%s", amt, idx+1, bn, includeSuffix)
		} else {
			msg = fmt.Sprintf("成功 · %s · %dms", amt, elapsedMs)
		}
	} else if tt.LastError != "" {
		msg = "失败 · " + tt.LastError + includeSuffix
	}
	entry := s.Logs.Add(logstore.Entry{
		TaskID: t.ID, Wallet: t.WalletName, Event: ev,
		Message: msg, TxHash: lastHash, RPCID: lastRPC, RTTMs: lastRTT,
		Error: tt.LastError, Extra: extra,
	})
	if anyOK && successRec != nil && burnbond.IsBondClaimContract(preset.To) {
		go s.backfillClaimedAmount(entry.ID, successRec, amt)
	}

	s.mu.Lock()
	delete(s.cache, taskID)
	delete(s.fanoutSendAt, fk)
	delete(s.fanoutHash, fk)
	delete(s.fanoutRPC, fk)
	delete(s.fanoutRTT, fk)
	s.mu.Unlock()

	if tt.Schedule != "once" {
		s.maybeRearm(taskID, s.Syncer.NowChain())
	}
}

func (s *Scheduler) backfillClaimedAmount(entryID string, rec *types.Receipt, amtLabel string) {
	if s.Logs == nil || entryID == "" || rec == nil {
		return
	}
	formatted, _, n := burnbond.SumClaimedLGNS(rec)
	if n == 0 || formatted == "" {
		return
	}
	newAmt := amtLabel + " · " + formatted + " LGNS"
	s.Logs.Patch(entryID, func(e *logstore.Entry) {
		if e.Extra == nil {
			e.Extra = map[string]any{}
		}
		e.Extra["amount"] = newAmt
		e.Extra["claimedLGNS"] = formatted
		if e.Message != "" && amtLabel != "" {
			e.Message = strings.Replace(e.Message, amtLabel, newAmt, 1)
		}
	})
}

func inclusionMs(ctx context.Context, rpcMgr *rpc.Manager, rpcID string, blockNum *big.Int, sendAt time.Time) int64 {
	if rpcMgr == nil || blockNum == nil || sendAt.IsZero() {
		return -1
	}
	cli, err := rpcMgr.ClientByID(rpcID)
	if err != nil {
		cli, err = rpcMgr.Client()
		if err != nil {
			return -1
		}
	}
	hdr, err := cli.HeaderByNumber(ctx, blockNum)
	if err != nil || hdr == nil {
		return -1
	}
	blockTime := time.Unix(int64(hdr.Time), 0)
	ms := blockTime.Sub(sendAt).Milliseconds()
	if ms < 0 {
		return 0
	}
	return ms
}

func (s *Scheduler) maybeRearm(id string, now time.Time) {
	t, ok := s.Tasks.Get(id)
	if !ok || !t.Enabled {
		return
	}
	if t.Schedule != "hourly" && t.Schedule != "daily_1159_cst" {
		return
	}
	if t.Status != "success" && t.Status != "failed" && t.Status != "fired" {
		return
	}
	nt, err := s.Tasks.Arm(id, now, nil)
	if err != nil {
		return
	}
	s.Logs.Add(logstore.Entry{
		TaskID: id, Wallet: nt.WalletName, Event: "requeued",
		Message: "requeued next run at " + nt.FireAt.Format(time.RFC3339),
	})
}

func (s *Scheduler) ArmEnabled(now time.Time) {
	for _, t := range s.Tasks.List() {
		if t.Enabled && (t.Status == "idle" || t.Status == "success" || t.Status == "failed") {
			_, _ = s.Tasks.Arm(t.ID, now, nil)
		}
	}
}

func (s *Scheduler) CurrentLeadMs() int64 {
	return s.RPC.MaxLeadMs()
}

func (s *Scheduler) CurrentLeads() map[string]int64 {
	return s.RPC.Leads()
}
