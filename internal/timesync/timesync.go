package timesync

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/beevik/ntp"
)

const (
	defaultNTPHost     = "pool.ntp.org"
	defaultNTPInterval = 30
	minNTPInterval     = 5
)

type Sync struct {
	mu          sync.RWMutex
	offset      time.Duration // local + offset ≈ NTP/true UTC（毫秒级）
	rttMs       int64         // 最近一次 RPC RTT
	updated     time.Time
	ntpHost     string
	intervalSec int
	kick        chan struct{}
}

func New() *Sync {
	return &Sync{
		ntpHost:     defaultNTPHost,
		intervalSec: defaultNTPInterval,
		kick:        make(chan struct{}, 1),
	}
}

// SetRTT records latest RPC latency; does not change clock offset.
func (s *Sync) SetRTT(rttMs int64) {
	s.mu.Lock()
	s.rttMs = rttMs
	s.mu.Unlock()
}

// Update is kept for callers that still pass chain header time.
// Clock offset is NTP-based; chainUnix is ignored. rttMs updates RTT only.
func (s *Sync) Update(_ int64, rttMs int64) {
	s.SetRTT(rttMs)
}

// ConfigureNTP updates host/interval and triggers an immediate sync.
func (s *Sync) ConfigureNTP(host string, intervalSec int) {
	host = strings.TrimSpace(host)
	if host == "" {
		host = defaultNTPHost
	}
	if intervalSec < minNTPInterval {
		intervalSec = minNTPInterval
	}
	s.mu.Lock()
	s.ntpHost = host
	s.intervalSec = intervalSec
	s.mu.Unlock()
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

func (s *Sync) ntpConfig() (host string, intervalSec int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	host = s.ntpHost
	if host == "" {
		host = defaultNTPHost
	}
	intervalSec = s.intervalSec
	if intervalSec < minNTPInterval {
		intervalSec = defaultNTPInterval
	}
	return host, intervalSec
}

// SyncNTP queries NTP and stores ClockOffset (millisecond-capable).
func (s *Sync) SyncNTP() error {
	host, _ := s.ntpConfig()
	resp, err := ntp.Query(host)
	if err != nil {
		return err
	}
	if err := resp.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	s.offset = resp.ClockOffset
	s.updated = time.Now()
	s.mu.Unlock()
	return nil
}

// StartNTP refreshes offset on a configurable interval (default 30s, min 5s).
func (s *Sync) StartNTP(ctx context.Context) {
	go func() {
		_ = s.SyncNTP()
		for {
			_, intervalSec := s.ntpConfig()
			timer := time.NewTimer(time.Duration(intervalSec) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-s.kick:
				timer.Stop()
				_ = s.SyncNTP()
			case <-timer.C:
				_ = s.SyncNTP()
			}
		}
	}()
}

func (s *Sync) OffsetMs() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.offset.Milliseconds()
}

func (s *Sync) RTTMs() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rttMs
}

func (s *Sync) NowChain() time.Time {
	s.mu.RLock()
	off := s.offset
	s.mu.RUnlock()
	return time.Now().Add(off)
}

func (s *Sync) Snapshot() (offsetMs, rttMs int64, updated time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.offset.Milliseconds(), s.rttMs, s.updated
}

// NextHourCST returns next top-of-hour in Asia/Shanghai.
func NextHourCST(now time.Time) time.Time {
	loc := cst()
	t := now.In(loc)
	next := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc).Add(time.Hour)
	return next
}

// Next1159CST returns next 11:59:00 Asia/Shanghai (+ optional delay).
func Next1159CST(now time.Time, delayMs int) time.Time {
	loc := cst()
	t := now.In(loc)
	target := time.Date(t.Year(), t.Month(), t.Day(), 11, 59, 0, 0, loc)
	if delayMs > 0 {
		target = target.Add(time.Duration(delayMs) * time.Millisecond)
	}
	if !t.Before(target) {
		target = target.Add(24 * time.Hour)
	}
	return target
}

func cst() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}

// CST returns Asia/Shanghai (or fixed +08).
func CST() *time.Location { return cst() }
