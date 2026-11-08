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
	"anubisbot/internal/tokenbal"

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
	fanoutErr    map[string]string    // first RPC error for this fireKey
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
	if s.fanoutErr == nil {
		s.fanoutErr = map[string]string{}
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

func (s *Scheduler) rpcTimeout() time.Duration {
	ms := 12000
	if s.Settings != nil {
		if v := s.Settings.Get().RpcTimeoutMs; v > 0 {
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
		// Enabled not required: run-once/dry-run may arm a disabled task.
		if t.Status != "armed" || t.FireAt == nil {
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
	// Parallelize across wallets (each task has its own key/nonce).
	signConc := 8
	if s.Runner != nil && s.Runner.Concurrency > 0 {
		signConc = s.Runner.Concurrency
	}
	sem := make(chan struct{}, signConc)
	var signWG sync.WaitGroup
	for _, t := range toSign {
		t := t
		signWG.Add(1)
		sem <- struct{}{}
		go func() {
			defer signWG.Done()
			defer func() { <-sem }()
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
		}()
	}
	signWG.Wait()

	for _, t := range s.Tasks.List() {
		// armed: not yet started; fired: first RPC(s) already sent — must keep
		// scheduling later-lead RPCs until this fireKey finishes.
		// Enabled not required: run-once/dry-run may arm a disabled task.
		if t.FireAt == nil {
			continue
		}
		if t.Status != "armed" && t.Status != "fired" {
			continue
		}
		fireAt := t.FireAt.UTC()
		fireKey := fireAt.UnixNano()
		fk := fanKey(t.ID, fireKey)

		s.mu.Lock()
		if s.finishedMark[fk] {
			s.mu.Unlock()
			continue
		}
		if _, ok := s.fanoutNeed[fk]; !ok {
			s.fanoutNeed[fk] = len(activeIDs)
			s.fanoutOk[fk] = 0
			s.fanoutDone[fk] = 0
			s.fanoutHash[fk] = ""
			s.fanoutRPC[fk] = ""
			s.fanoutRTT[fk] = 0
			s.fanoutErr[fk] = ""
		}
		cached := s.cache[t.ID]
		live := s.Runner != nil && s.Runner.Mode == "live"
		ready := cached != nil && (cached.Tx != nil || cached.Sim || !live)
		s.mu.Unlock()

		if !ready {
			continue
		}

		nRPC := len(activeIDs)
		for _, rpcID := range activeIDs {
			lead := leads[rpcID]
			sk := sendKey(t.ID, fireKey, rpcID)

			s.mu.Lock()
			already := s.sentFanout[sk]
			if already {
				s.mu.Unlock()
				continue
			}
			s.sentFanout[sk] = true
			cached = s.cache[t.ID]
			taskCopy := t
			cachedCopy := cached
			rpcCopy := rpcID
			rttCopy := rtts[rpcID]
			leadCopy := lead
			fkCopy := fk
			fireKeyCopy := fireKey
			fireAtCopy := fireAt
			maxLeadCopy := maxLead
			nRPCCopy := nRPC
			s.mu.Unlock()

			go func() {
				globalEarly := true
				if s.Settings != nil {
					globalEarly = s.Settings.Get().EarlyBroadcastOn()
				}
				early := taskCopy.EffectiveEarlyBroadcast(globalEarly)
				sendAt := fireAtCopy
				sendMode := "fireAt"
				if early && leadCopy > 0 {
					sendAt = fireAtCopy.Add(-time.Duration(leadCopy) * time.Millisecond)
					sendMode = "fireAt-halfRTT"
				}
				if d := sendAt.Sub(s.Syncer.NowChain()); d > 0 {
					timer := time.NewTimer(d)
					select {
					case <-ctx.Done():
						timer.Stop()
						return
					case <-timer.C:
					}
				}

				wake := s.Syncer.NowChain()
				s.mu.Lock()
				if !s.firedMark[fkCopy] {
					s.firedMark[fkCopy] = true
					tt := taskCopy
					tt.Status = "fired"
					wakeCopy := wake
					tt.LastRunAt = &wakeCopy
					_ = s.Tasks.Update(tt)
					s.Logs.Add(logstore.Entry{
						TaskID: taskCopy.ID, Wallet: taskCopy.WalletName, Event: "fired",
						Message: fmt.Sprintf("fanout start sendAt=%s rpcs=%d (halfRTT max=%dms)", sendMode, nRPCCopy, maxLeadCopy),
					})
				}
				s.mu.Unlock()

				rpcCtx, cancel := context.WithTimeout(ctx, s.rpcTimeout())
				defer cancel()
				res := s.Runner.SendToRPC(rpcCtx, taskCopy, cachedCopy, rpcCopy, rttCopy, leadCopy)

				s.mu.Lock()
				s.fanoutDone[fkCopy]++
				if res.Error == "" {
					s.fanoutOk[fkCopy]++
					if res.TxHash != "" && s.fanoutHash[fkCopy] == "" {
						s.fanoutHash[fkCopy] = res.TxHash
						s.fanoutRPC[fkCopy] = res.RPCID
						s.fanoutRTT[fkCopy] = rttCopy
					}
					if !res.Sim {
						if _, ok := s.fanoutSendAt[fkCopy]; !ok {
							s.fanoutSendAt[fkCopy] = time.Now()
						}
					}
				} else if s.fanoutErr[fkCopy] == "" {
					s.fanoutErr[fkCopy] = res.Error
				}
				done := s.fanoutDone[fkCopy]
				need := s.fanoutNeed[fkCopy]
				okN := s.fanoutOk[fkCopy]
				shouldFin := done >= need && need > 0 && !s.finishedMark[fkCopy]
				if shouldFin {
					s.finishedMark[fkCopy] = true
				}
				s.mu.Unlock()

				if shouldFin {
					s.finishFanout(taskCopy.ID, fireKeyCopy, okN > 0)
				}
			}()
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
				to := s.rpcTimeout()
				ctx, cancel := context.WithTimeout(context.Background(), to)
				rec, err := s.RPC.WaitReceipt(ctx, lastRPC, lastHash, to)
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
					receiptAt := time.Now()
					bn := rec.BlockNumber.Uint64()
					extra["blockNumber"] = bn
					extra["txIndex"] = uint64(rec.TransactionIndex)
					fireAt := time.Unix(0, fireKey).UTC()
					if !sendAt.IsZero() {
						ms := receiptAt.Sub(sendAt).Milliseconds()
						if ms < 0 {
							ms = 0
						}
						extra["includeMs"] = ms
					}
					if openMs := receiptAt.Sub(fireAt).Milliseconds(); openMs >= 0 {
						extra["openMs"] = openMs
					} else {
						extra["openMs"] = int64(0)
					}
					extra["fireAtLabel"] = fireAtLabelCST(fireAt)
					if hb := hourBlockOf(ctx, s.RPC, lastRPC, bn, fireAt); hb > 0 {
						extra["hourBlock"] = hb
					}
					if rec.EffectiveGasPrice != nil {
						fee := new(big.Int).Mul(new(big.Int).SetUint64(rec.GasUsed), rec.EffectiveGasPrice)
						extra["gasFeeDAI"] = tokenbal.Format(fee, 18)
					}
					if rec.Status == 0 {
						anyOK = false
						extra["ok"] = false
						tt.Status = "failed"
						reason := s.RPC.RevertReason(ctx, lastRPC, lastHash, rec.BlockNumber)
						if reason != "" {
							extra["revertReason"] = reason
						}
						tt.LastError = rpc.FormatRevertedOnChain(reason)
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
			s.mu.Lock()
			errMsg := s.fanoutErr[fk]
			s.mu.Unlock()
			if errMsg != "" {
				tt.LastError = "all rpc fanout failed · " + errMsg
			} else {
				tt.LastError = "all rpc fanout failed"
			}
		}
	}
	extra["ok"] = anyOK
	effSch := tt.Schedule
	if s.Tasks != nil {
		effSch = s.Tasks.EffectiveSchedule(tt)
	}
	if effSch == "once" || (anyOK && tt.StopAfterSuccess) {
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

	isBond := burnbond.IsBondClaimContract(preset.To)
	if anyOK && successRec != nil && isBond {
		if formatted, _, n := burnbond.SumClaimedLGNS(successRec); n > 0 && formatted != "" {
			extra["claimedLGNS"] = formatted
			delete(extra, "claimedPending")
		} else {
			extra["claimedPending"] = true
		}
	}

	msg := "失败"
	ev := "result"
	if anyOK {
		msg = formatSuccessMessage(extra, amt, elapsedMs)
	} else if tt.LastError != "" {
		// UI appends includeMs after the tx link; keep message as amount · error only.
		if amt != "" {
			msg = amt + " · " + tt.LastError
		} else {
			msg = tt.LastError
		}
	}
	entry := s.Logs.Add(logstore.Entry{
		TaskID: t.ID, Wallet: t.WalletName, Event: ev,
		Message: msg, TxHash: lastHash, RPCID: lastRPC, RTTMs: lastRTT,
		Error: tt.LastError, Extra: extra,
	})
	if anyOK && successRec != nil && isBond {
		if _, pending := extra["claimedPending"].(bool); pending {
			go s.backfillClaimedAmount(entry.ID, successRec, amt)
		}
	}

	s.mu.Lock()
	delete(s.cache, taskID)
	delete(s.fanoutSendAt, fk)
	delete(s.fanoutHash, fk)
	delete(s.fanoutRPC, fk)
	delete(s.fanoutRTT, fk)
	delete(s.fanoutErr, fk)
	s.mu.Unlock()

	if effSch != "once" {
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
	s.Logs.Patch(entryID, func(e *logstore.Entry) {
		if e.Extra == nil {
			e.Extra = map[string]any{}
		}
		e.Extra["claimedLGNS"] = formatted
		delete(e.Extra, "claimedPending")
		elapsed := int64(0)
		if v, ok := e.Extra["elapsedMs"].(int64); ok {
			elapsed = v
		}
		amt := amtLabel
		if a, ok := e.Extra["amount"].(string); ok && a != "" {
			amt = a
		}
		e.Message = formatSuccessMessage(e.Extra, amt, elapsed)
	})
}

func fireAtLabelCST(fireAt time.Time) string {
	if fireAt.IsZero() {
		return ""
	}
	t := fireAt.In(timesync.CST())
	return fmt.Sprintf("%d点%02d分", t.Hour(), t.Minute())
}

func formatSuccessMessage(extra map[string]any, amt string, elapsedMs int64) string {
	title := "成功"
	if v, ok := extra["claimedLGNS"].(string); ok && v != "" {
		title = "成功 => " + v + " LGNS"
	} else if pending, ok := extra["claimedPending"].(bool); ok && pending {
		title = "成功 正在读取链上数据..."
	}
	if bn, ok := extra["blockNumber"].(uint64); ok {
		idx, _ := extra["txIndex"].(uint64)
		parts := []string{title, amt}
		label, _ := extra["fireAtLabel"].(string)
		if hb, ok := extra["hourBlock"].(uint64); ok && hb > 0 {
			if label != "" {
				parts = append(parts, fmt.Sprintf("%s 第 %d 块", label, hb))
			} else {
				parts = append(parts, fmt.Sprintf("整点第 %d 块", hb))
			}
		}
		parts = append(parts, fmt.Sprintf("第 %d 笔", idx+1), fmt.Sprintf("高度 %d", bn))
		return strings.Join(parts, " · ")
	}
	return fmt.Sprintf("%s · %s · %dms", title, amt, elapsedMs)
}

// hourBlockOf returns 1-based block ordinal relative to fireAt.
// Finds the last block with Time < fireAt (prev), returns bn-prev; 0 if unknown.
func hourBlockOf(ctx context.Context, rpcMgr *rpc.Manager, rpcID string, bn uint64, fireAt time.Time) uint64 {
	if rpcMgr == nil || bn == 0 || fireAt.IsZero() {
		return 0
	}
	cli, err := rpcMgr.ClientByID(rpcID)
	if err != nil {
		cli, err = rpcMgr.Client()
		if err != nil {
			return 0
		}
	}
	fireUnix := uint64(fireAt.Unix())
	hdr, err := cli.HeaderByNumber(ctx, new(big.Int).SetUint64(bn))
	if err != nil || hdr == nil {
		return 0
	}
	if hdr.Time < fireUnix {
		return 0
	}
	const maxBack = 180
	cur := bn
	for i := 0; i < maxBack; i++ {
		if cur == 0 {
			return bn + 1
		}
		prevNum := cur - 1
		prev, err := cli.HeaderByNumber(ctx, new(big.Int).SetUint64(prevNum))
		if err != nil || prev == nil {
			return 0
		}
		if prev.Time < fireUnix {
			return bn - prevNum
		}
		cur = prevNum
	}
	return 0
}

func (s *Scheduler) maybeRearm(id string, now time.Time) {
	t, ok := s.Tasks.Get(id)
	if !ok || !t.Enabled {
		return
	}
	if s.Tasks == nil || !s.Tasks.IsRepeatingTask(t) {
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
