package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Settings struct {
	SignLeadMs       int     `json:"signLeadMs"`
	SendSlackMs      int     `json:"sendSlackMs"`
	BatchConcurrency int     `json:"batchConcurrency"`
	GasMultiplier    float64 `json:"gasMultiplier"`
	NtpHost          string  `json:"ntpHost"`
	NtpIntervalSec   int     `json:"ntpIntervalSec"`
	RpcTimeoutMs     int     `json:"rpcTimeoutMs"` // 广播发送 + 等收据共用
	Mode             string  `json:"mode"`         // sim | live，重启后恢复
}

func Defaults() Settings {
	return Settings{
		SignLeadMs:       800,
		SendSlackMs:      80,
		BatchConcurrency: 16,
		GasMultiplier:    1.2,
		NtpHost:          "pool.ntp.org",
		NtpIntervalSec:   30,
		RpcTimeoutMs:     12000,
		Mode:             "sim",
	}
}

type Store struct {
	mu   sync.RWMutex
	path string
	cur  Settings
}

func Load(path string) (*Store, error) {
	dir := filepath.Dir(path)
	_ = os.MkdirAll(dir, 0o755)
	s := &Store{path: path, cur: Defaults()}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			_ = s.saveLocked()
			return s, nil
		}
		return nil, err
	}
	var cur Settings
	if err := json.Unmarshal(b, &cur); err != nil {
		return nil, err
	}
	s.cur = mergeDefaults(cur)
	return s, nil
}

func mergeDefaults(in Settings) Settings {
	d := Defaults()
	if in.SignLeadMs > 0 {
		d.SignLeadMs = in.SignLeadMs
	}
	if in.SendSlackMs >= 0 {
		d.SendSlackMs = in.SendSlackMs
	}
	if in.BatchConcurrency > 0 {
		d.BatchConcurrency = in.BatchConcurrency
	}
	if in.GasMultiplier > 0 {
		d.GasMultiplier = in.GasMultiplier
	}
	if host := strings.TrimSpace(in.NtpHost); host != "" {
		d.NtpHost = host
	}
	if in.NtpIntervalSec >= 5 {
		d.NtpIntervalSec = in.NtpIntervalSec
	}
	if in.RpcTimeoutMs > 0 {
		d.RpcTimeoutMs = in.RpcTimeoutMs
	}
	if in.Mode == "live" || in.Mode == "sim" {
		d.Mode = in.Mode
	}
	return d
}

func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

func (s *Store) Update(patch Settings) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.cur
	if patch.SignLeadMs > 0 {
		cur.SignLeadMs = patch.SignLeadMs
	}
	if patch.SendSlackMs >= 0 {
		cur.SendSlackMs = patch.SendSlackMs
	}
	if patch.BatchConcurrency > 0 {
		cur.BatchConcurrency = patch.BatchConcurrency
	}
	if patch.GasMultiplier > 0 {
		cur.GasMultiplier = patch.GasMultiplier
	}
	if host := strings.TrimSpace(patch.NtpHost); host != "" {
		cur.NtpHost = host
	}
	if patch.NtpIntervalSec >= 5 {
		cur.NtpIntervalSec = patch.NtpIntervalSec
	}
	if patch.RpcTimeoutMs > 0 {
		cur.RpcTimeoutMs = patch.RpcTimeoutMs
	}
	if patch.Mode == "live" || patch.Mode == "sim" {
		cur.Mode = patch.Mode
	}
	s.cur = cur
	if err := s.saveLocked(); err != nil {
		return Settings{}, err
	}
	return cur, nil
}

func (s *Store) SetMode(mode string) error {
	if mode != "sim" && mode != "live" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cur.Mode = mode
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s.cur, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(b, '\n'), 0o644)
}
