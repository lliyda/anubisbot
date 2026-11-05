package logstore

import (
	"bufio"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Entry struct {
	ID      string         `json:"id"`
	At      time.Time      `json:"at"`
	TaskID  string         `json:"taskId,omitempty"`
	Wallet  string         `json:"wallet,omitempty"`
	Event   string         `json:"event"`
	Message string         `json:"message"`
	RPCID   string         `json:"rpcId,omitempty"`
	RTTMs   int64          `json:"rttMs,omitempty"`
	TxHash  string         `json:"txHash,omitempty"`
	Error   string         `json:"error,omitempty"`
	Extra   map[string]any `json:"extra,omitempty"`
}

type Store struct {
	mu        sync.RWMutex
	entries   []Entry
	max       int
	seq       int
	path      string
	fileMu    sync.Mutex
	fileLines int
}

func New(max int) *Store {
	if max <= 0 {
		max = 500
	}
	return &Store{max: max}
}

// NewWithFile creates a store that loads and appends JSONL at path.
func NewWithFile(max int, path string) *Store {
	s := New(max)
	s.path = path
	if path == "" {
		return s
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	s.loadFile()
	return s
}

func (s *Store) loadFile() {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	f, err := os.Open(s.path)
	if err != nil {
		return
	}
	defer f.Close()
	var all []Entry
	sc := bufio.NewScanner(f)
	// allow long log lines
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	lines := 0
	for sc.Scan() {
		lines++
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			log.Printf("logstore: skip bad line: %v", err)
			continue
		}
		all = append(all, e)
	}
	if len(all) > s.max {
		all = all[len(all)-s.max:]
	}
	s.mu.Lock()
	s.entries = all
	s.seq = len(all)
	s.mu.Unlock()
	s.fileLines = lines
	if lines > s.max*4 {
		s.rewriteFileLocked()
	}
}

func (s *Store) rewriteFileLocked() {
	s.mu.RLock()
	snap := append([]Entry(nil), s.entries...)
	s.mu.RUnlock()
	tmp := s.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		log.Printf("logstore: rewrite create: %v", err)
		return
	}
	w := bufio.NewWriter(f)
	for _, e := range snap {
		b, err := json.Marshal(e)
		if err != nil {
			continue
		}
		_, _ = w.Write(b)
		_ = w.WriteByte('\n')
	}
	_ = w.Flush()
	_ = f.Close()
	if err := os.Rename(tmp, s.path); err != nil {
		log.Printf("logstore: rewrite rename: %v", err)
		return
	}
	s.fileLines = len(snap)
}

func (s *Store) appendFile(e Entry) {
	if s.path == "" {
		return
	}
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("logstore: append open: %v", err)
		return
	}
	_, _ = f.Write(b)
	_, _ = f.Write([]byte("\n"))
	_ = f.Close()
	s.fileLines++
	if s.fileLines > s.max*4 {
		s.rewriteFileLocked()
	}
}

func (s *Store) Add(e Entry) Entry {
	s.mu.Lock()
	s.seq++
	e.ID = time.Now().Format("150405") + "-" + itoa(s.seq)
	if e.At.IsZero() {
		e.At = time.Now()
	}
	s.entries = append(s.entries, e)
	if len(s.entries) > s.max {
		s.entries = s.entries[len(s.entries)-s.max:]
	}
	out := e
	s.mu.Unlock()
	s.appendFile(out)
	return out
}

// Patch updates an in-memory entry by ID and rewrites the JSONL file.
func (s *Store) Patch(id string, fn func(*Entry)) bool {
	if id == "" || fn == nil {
		return false
	}
	s.mu.Lock()
	idx := -1
	for i := range s.entries {
		if s.entries[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.mu.Unlock()
		return false
	}
	fn(&s.entries[idx])
	s.mu.Unlock()

	if s.path == "" {
		return true
	}
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	s.rewriteFileLocked()
	return true
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
