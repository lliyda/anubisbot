package logstore

import (
	"sync"
	"time"
)

type Entry struct {
	ID        string         `json:"id"`
	At        time.Time      `json:"at"`
	TaskID    string         `json:"taskId,omitempty"`
	Wallet    string         `json:"wallet,omitempty"`
	Event     string         `json:"event"`
	Message   string         `json:"message"`
	RPCID     string         `json:"rpcId,omitempty"`
	RTTMs     int64          `json:"rttMs,omitempty"`
	TxHash    string         `json:"txHash,omitempty"`
	Error     string         `json:"error,omitempty"`
	Extra     map[string]any `json:"extra,omitempty"`
}

type Store struct {
	mu      sync.RWMutex
	entries []Entry
	max     int
	seq     int
}

func New(max int) *Store {
	if max <= 0 {
		max = 500
	}
	return &Store{max: max}
}

func (s *Store) Add(e Entry) Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	e.ID = time.Now().Format("150405") + "-" + itoa(s.seq)
	if e.At.IsZero() {
		e.At = time.Now()
	}
	s.entries = append(s.entries, e)
	if len(s.entries) > s.max {
		s.entries = s.entries[len(s.entries)-s.max:]
	}
	return e
}

func (s *Store) List(limit int, taskID string, failedOnly bool) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > len(s.entries) {
		limit = len(s.entries)
	}
	out := make([]Entry, 0, limit)
	for i := len(s.entries) - 1; i >= 0 && len(out) < limit; i-- {
		e := s.entries[i]
		if taskID != "" && e.TaskID != taskID {
			continue
		}
		if failedOnly && e.Error == "" && e.Event != "failed" {
			continue
		}
		out = append(out, e)
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
