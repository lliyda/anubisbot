package panel

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

type Config struct {
	Listen      string `json:"listen"`
	BotHTTP     string `json:"botHTTP"`
	BotExe      string `json:"botExe"`
	PanelExe    string `json:"panelExe"`
	GithubRepo  string `json:"githubRepo"`
	AssetName   string `json:"assetName"`
	GithubToken string `json:"githubToken,omitempty"` // optional; else GH_TOKEN / gh auth token
}

func Defaults() Config {
	return Config{
		Listen:     "127.0.0.1:47192",
		BotHTTP:    "http://127.0.0.1:47193",
		BotExe:     "anubisbot.exe",
		PanelExe:   "anubisbot-panel.exe",
		GithubRepo: "",
		AssetName:  "anubisbot-windows-amd64.zip",
	}
}

func needsGithubRepo(s string) bool {
	s = strings.TrimSpace(s)
	return s == "" || strings.EqualFold(s, "OWNER/REPO")
}

var githubRepoRe = regexp.MustCompile(`(?i)(?:github\.com[:/]|git@github\.com:)([^/\s]+)/([^/\s]+?)(?:\.git)?/?$`)

// DetectGithubRepo reads git remote origin and returns owner/repo when hosted on GitHub.
func DetectGithubRepo(root string) string {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	url := strings.TrimSpace(string(out))
	m := githubRepoRe.FindStringSubmatch(url)
	if len(m) != 3 {
		return ""
	}
	owner := m[1]
	name := strings.TrimSuffix(m[2], ".git")
	if owner == "" || name == "" {
		return ""
	}
	return owner + "/" + name
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
			s.fillGithubRepoLocked()
			_ = s.saveLocked()
			return s, nil
		}
		return nil, err
	}
	b = stripUTF8BOM(b)
	var cur Config
	if err := json.Unmarshal(b, &cur); err != nil {
		return nil, err
	}
	s.cur = merge(cur)
	if needsGithubRepo(s.cur.GithubRepo) {
		s.fillGithubRepoLocked()
		_ = s.saveLocked()
	}
	return s, nil
}

func (s *Store) fillGithubRepoLocked() {
	if !needsGithubRepo(s.cur.GithubRepo) {
		return
	}
	if repo := DetectGithubRepo(s.root); repo != "" {
		s.cur.GithubRepo = repo
	}
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
	if strings.TrimSpace(in.GithubToken) != "" {
		d.GithubToken = strings.TrimSpace(in.GithubToken)
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
		Listen:      first(patch.Listen, s.cur.Listen),
		BotHTTP:     first(patch.BotHTTP, s.cur.BotHTTP),
		BotExe:      first(patch.BotExe, s.cur.BotExe),
		PanelExe:    first(patch.PanelExe, s.cur.PanelExe),
		GithubRepo:  first(patch.GithubRepo, s.cur.GithubRepo),
		AssetName:   first(patch.AssetName, s.cur.AssetName),
		GithubToken: first(patch.GithubToken, s.cur.GithubToken),
	})
	if strings.TrimSpace(patch.GithubToken) == "-" {
		cur.GithubToken = ""
	}
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

func stripUTF8BOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
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
