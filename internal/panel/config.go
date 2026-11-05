package panel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Config struct {
	Listen     string `json:"listen"`
	BotHTTP    string `json:"botHTTP"`
	BotExe     string `json:"botExe"`
	PanelExe   string `json:"panelExe"`
	GithubRepo string `json:"githubRepo"`
	AssetName  string `json:"assetName"`
}

func Defaults() Config {
	return Config{
		Listen:     "127.0.0.1:47192",
		BotHTTP:    "http://127.0.0.1:47193",
		BotExe:     "anubisbot.exe",
		PanelExe:   "anubisbot-panel.exe",
		GithubRepo: "OWNER/REPO",
		AssetName:  "anubisbot-windows-amd64.zip",
	}
}

type Store struct {
	mu   sync.RWMutex
	path string
	cur  Config
	root string // working directory (anubisbot root)
}

func Load(root, path string) (*Store, error) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	s := &Store{path: path, cur: Defaults(), root: root}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			_ = s.saveLocked()
			return s, nil
		}
		return nil, err
	}
	var cur Config
	if err := json.Unmarshal(b, &cur); err != nil {
		return nil, err
	}
	s.cur = merge(cur)
	return s, nil
}

func merge(in Config) Config {
	d := Defaults()
	if strings.TrimSpace(in.Listen) != "" {
		d.Listen = strings.TrimSpace(in.Listen)
	}
	if strings.TrimSpace(in.BotHTTP) != "" {
		d.BotHTTP = strings.TrimSpace(in.BotHTTP)
	}
	if strings.TrimSpace(in.BotExe) != "" {
		d.BotExe = strings.TrimSpace(in.BotExe)
	}
	if strings.TrimSpace(in.PanelExe) != "" {
		d.PanelExe = strings.TrimSpace(in.PanelExe)
	}
	if strings.TrimSpace(in.GithubRepo) != "" {
		d.GithubRepo = strings.TrimSpace(in.GithubRepo)
	}
	if strings.TrimSpace(in.AssetName) != "" {
		d.AssetName = strings.TrimSpace(in.AssetName)
	}
	return d
}

func (s *Store) Root() string {
	return s.root
}

func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

func (s *Store) Update(patch Config) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := merge(Config{
		Listen:     first(patch.Listen, s.cur.Listen),
		BotHTTP:    first(patch.BotHTTP, s.cur.BotHTTP),
		BotExe:     first(patch.BotExe, s.cur.BotExe),
		PanelExe:   first(patch.PanelExe, s.cur.PanelExe),
		GithubRepo: first(patch.GithubRepo, s.cur.GithubRepo),
		AssetName:  first(patch.AssetName, s.cur.AssetName),
	})
	s.cur = cur
	if err := s.saveLocked(); err != nil {
		return Config{}, err
	}
	return cur, nil
}

func first(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return b
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s.cur, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(b, '\n'), 0o644)
}

func (s *Store) BotExePath() string {
	cfg := s.Get()
	if filepath.IsAbs(cfg.BotExe) {
		return cfg.BotExe
	}
	return filepath.Join(s.root, cfg.BotExe)
}
