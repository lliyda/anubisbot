package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

type BotManager struct {
	cfg *Store

	mu     sync.Mutex
	cmd    *exec.Cmd
	logs   []string
	logMu  sync.Mutex
}

func NewBotManager(cfg *Store) *BotManager {
	return &BotManager{cfg: cfg}
}

func (m *BotManager) AppendLog(line string) {
	m.logMu.Lock()
	defer m.logMu.Unlock()
	ts := time.Now().Format("15:04:05")
	m.logs = append(m.logs, ts+" "+line)
	if len(m.logs) > 200 {
		m.logs = m.logs[len(m.logs)-200:]
	}
}

func (m *BotManager) Logs() []string {
	m.logMu.Lock()
	defer m.logMu.Unlock()
	out := make([]string, len(m.logs))
	copy(out, m.logs)
	return out
}

func (m *BotManager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil || m.cmd.Process == nil {
		return false
	}
	// cheap liveness: process still exists (Windows: signal 0 not portable; check health instead too)
	return m.cmd.ProcessState == nil || !m.cmd.ProcessState.Exited()
}

func (m *BotManager) FetchHealth(ctx context.Context) (map[string]any, error) {
	cfg := m.cfg.Get()
	url := strings.TrimRight(cfg.BotHTTP, "/") + "/api/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	cli := &http.Client{Timeout: 2 * time.Second}
	res, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body, nil
}

func (m *BotManager) Healthy(ctx context.Context) bool {
	_, err := m.FetchHealth(ctx)
	return err == nil
}

func (m *BotManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != nil && m.cmd.Process != nil && (m.cmd.ProcessState == nil || !m.cmd.ProcessState.Exited()) {
		return fmt.Errorf("bot already running")
	}
	exe := m.cfg.BotExePath()
	if _, err := os.Stat(exe); err != nil {
		return fmt.Errorf("bot exe not found: %s", exe)
	}
	cmd := exec.Command(exe)
	cmd.Dir = m.cfg.Root()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	m.cmd = cmd
	m.AppendLog("started " + filepath.Base(exe) + " pid=" + fmt.Sprint(cmd.Process.Pid))
	go func() {
		_ = cmd.Wait()
		m.AppendLog("bot exited")
		m.mu.Lock()
		if m.cmd == cmd {
			m.cmd = nil
		}
		m.mu.Unlock()
	}()
	return nil
}

func (m *BotManager) Stop() error {
	m.mu.Lock()
	cmd := m.cmd
	m.mu.Unlock()

	// Prefer graceful: if we own the process, kill it; also kill by listening port via taskkill fallback
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		m.AppendLog("killed owned bot process")
	}
	// Also stop any orphan anubisbot on bot port by image name (best-effort Windows)
	if runtime.GOOS == "windows" {
		exeName := filepath.Base(m.cfg.Get().BotExe)
		_ = exec.Command("taskkill", "/F", "/IM", exeName).Run()
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !m.Healthy(context.Background()) {
			m.mu.Lock()
			m.cmd = nil
			m.mu.Unlock()
			m.AppendLog("bot stopped")
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("bot still responding after stop")
}

func (m *BotManager) Restart() error {
	_ = m.Stop()
	time.Sleep(500 * time.Millisecond)
	if err := m.Start(); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if m.Healthy(context.Background()) {
			m.AppendLog("bot healthy after restart")
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("bot started but health check timed out")
}

func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
