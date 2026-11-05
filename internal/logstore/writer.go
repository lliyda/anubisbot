package logstore

import (
	"bytes"
	"sync"
)

// LineWriter tees text lines into the log store (for mirroring process stdout/log).
type LineWriter struct {
	Store *Store
	Event string

	mu  sync.Mutex
	buf []byte
}

func (w *LineWriter) Write(p []byte) (int, error) {
	if w == nil || w.Store == nil {
		return len(p), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := string(bytes.TrimSpace(bytes.ReplaceAll(w.buf[:i], []byte("\r"), nil)))
		w.buf = w.buf[i+1:]
		if line == "" {
			continue
		}
		ev := w.Event
		if ev == "" {
			ev = "console"
		}
		w.Store.Add(Entry{Event: ev, Message: line})
	}
	return len(p), nil
}
