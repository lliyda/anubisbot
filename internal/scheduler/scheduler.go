package scheduler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"anubisbot/internal/batch"
	"anubisbot/internal/logstore"
	"anubisbot/internal/rpc"
	"anubisbot/internal/settings"
	"anubisbot/internal/tasks"
	"anubisbot/internal/timesync"
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
	fanoutOk   map[string]int
	fanoutDone map[string]int
	fanoutNeed map[string]int
	firedMark    map[string]bool // task marked fired for this fireKey
	finishedMark map[string]bool // fanout completed for fireKey
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
	if s.firedMark == nil {
		s.firedMark = map[string]bool{}
	}
	if s.finishedMark == nil {
		s.finishedMark = map[string]bool{}
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

		// per-RPC send windows
		fk := fanKey(t.ID, fireKey)
		s.mu.Lock()
		if _, ok := s.fanoutNeed[fk]; !ok {
			s.fanoutNeed[fk] = len(activeIDs)
			s.fanoutOk[fk] = 0
			s.fanoutDone[fk] = 0
		}
		s.mu.Unlock()

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
			cached := s.cache[t.ID]
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
					s.finishFanout(t.ID, fireKey, okN > 0, res.TxHash, rpcID, rtt)
				}
			}
		}
	}

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
}

func (s *Scheduler) finishFanout(taskID string, fireKey int64, anyOK bool, lastHash, lastRPC string, lastRTT int64) {
	fk := fanKey(taskID, fireKey)
	t, ok := s.Tasks.Get(taskID)
	if !ok {
		return
	}
	tt := t
	preset, _ := s.Runner.Presets[t.TaskType]
	amt := batch.AmountLabel(t, preset)
	extra := map[string]any{"amount": amt, "ok": anyOK}

	if anyOK {
		tt.Status = "success"
		tt.LastError = ""
		if lastHash != "" {
			tt.LastTxHash = lastHash
		}
		tt.LastRpcID = lastRPC
		tt.LastRttMs = &lastRTT
		if lastHash != "" && !strings.HasPrefix(lastHash, "sim-") && s.RPC != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			rec, err := s.RPC.WaitReceipt(ctx, lastRPC, lastHash, 10*time.Second)
			cancel()
			if err == nil && rec != nil {
				extra["blockNumber"] = rec.BlockNumber.Uint64()
				extra["txIndex"] = uint64(rec.TransactionIndex)
				if rec.Status == 0 {
					anyOK = false
					extra["ok"] = false
					tt.Status = "failed"
					tt.LastError = "tx reverted on-chain"
				}
			}
		}
	} else {
		tt.Status = "failed"
		if tt.LastError == "" {
			tt.LastError = "all rpc fanout failed"
		}
	}
	if tt.Schedule == "once" {
		tt.Enabled = false
		tt.FireAt = nil
		if tt.Status == "armed" {
			tt.Status = "idle"
		}
	}
	_ = s.Tasks.Update(tt)

	msg := "失败"
	ev := "result"
	if anyOK {
		msg = "成功"
		if bn, ok := extra["blockNumber"].(uint64); ok {
			idx, _ := extra["txIndex"].(uint64)
			msg = fmt.Sprintf("成功 · %s · 块内第 %d 笔 · 高度 %d", amt, idx+1, bn)
		} else {
			msg = fmt.Sprintf("成功 · %s", amt)
		}
	} else if tt.LastError != "" {
		msg = "失败 · " + tt.LastError
	}
	s.Logs.Add(logstore.Entry{
		TaskID: t.ID, Wallet: t.WalletName, Event: ev,
		Message: msg, TxHash: lastHash, RPCID: lastRPC, RTTMs: lastRTT,
		Error: tt.LastError, Extra: extra,
	})

	s.mu.Lock()
	delete(s.cache, taskID)
	s.mu.Unlock()

	if tt.Schedule != "once" {
		s.maybeRearm(taskID, s.Syncer.NowChain())
	}
	_ = fk
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
