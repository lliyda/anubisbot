package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type RPCEndpoint struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Builtin bool   `json:"builtin,omitempty"`
}

type ChainConfig struct {
	ChainID         int64             `json:"chainId"`
	Explorer        string            `json:"explorer"`
	ActiveRpcID     string            `json:"activeRpcId"`
	ActiveRpcIDs    []string          `json:"activeRpcIds,omitempty"`
	RPCs            []RPCEndpoint     `json:"rpcs"`
	Tokens          map[string]string `json:"tokens"`
	Contracts       map[string]string `json:"contracts,omitempty"`
	RTTGreenMs      int               `json:"rttGreenMs"`
	RTTYellowMs     int               `json:"rttYellowMs"`
	PingIntervalSec int               `json:"pingIntervalSec"`
}

type PresetParams struct {
	Args         []string `json:"args,omitempty"`
	ClaimArg     int64    `json:"claimArg,omitempty"`     // legacy
	BurnAmount   string   `json:"burnAmount,omitempty"`   // legacy
	GasLimit     uint64   `json:"gasLimit,omitempty"`
	GasPriceGwei *float64 `json:"gasPriceGwei,omitempty"`
}

type Preset struct {
	Label            string          `json:"label"`
	Description      string          `json:"description"`
	Icon             string          `json:"icon"`
	To               string          `json:"to"`
	ABI              json.RawMessage `json:"abi"`
	Function         string          `json:"function,omitempty"`
	Method           string          `json:"method,omitempty"`   // derived from ABI (compat)
	Selector         string          `json:"selector,omitempty"` // derived from ABI (compat)
	DefaultSchedule  string          `json:"defaultSchedule"`
	DefaultParams    PresetParams    `json:"defaultParams"`
	NeedsLgnsApprove bool            `json:"needsLgnsApprove"`
}

type Env struct {
	Mode             string
	HTTPAddr         string
	ChainConfigPath  string
	PresetsPath      string
	WalletsPath      string
	TasksPath        string
	BatchConcurrency int
	SignLeadMs       int
	OpsToken         string
}

func LoadEnv() Env {
	return Env{
		Mode:             getenv("MODE", "sim"),
		HTTPAddr:         getenv("HTTP_ADDR", "127.0.0.1:47193"),
		ChainConfigPath:  getenv("CHAIN_CONFIG", "config/chain.json"),
		PresetsPath:      getenv("PRESETS", "config/presets.json"),
		WalletsPath:      getenv("WALLETS", "config/wallets.local.json"),
		TasksPath:        getenv("TASKS", "data/tasks.json"),
		BatchConcurrency: getenvInt("BATCH_CONCURRENCY", 16),
		SignLeadMs:       getenvInt("SIGN_LEAD_MS", 800),
		OpsToken:         os.Getenv("OPS_TOKEN"),
	}
}

// ModeDataPaths returns per-mode wallets / secrets / tasks file paths.
func ModeDataPaths(mode string) (walletsPath, secretsPath, tasksPath string) {
	if mode == "live" {
		return "config/wallets.live.json", "data/secrets.live.json", "data/tasks.live.json"
	}
	return "config/wallets.sim.json", "data/secrets.sim.json", "data/tasks.sim.json"
}

// MigrateLegacyModeFiles copies old shared files into sim paths once.
func MigrateLegacyModeFiles() {
	copyIfMissing("config/wallets.local.json", "config/wallets.sim.json")
	copyIfMissing("data/tasks.json", "data/tasks.sim.json")
	copyIfMissing("data/secrets.local.json", "data/secrets.sim.json")
}

func copyIfMissing(src, dst string) {
	if _, err := os.Stat(dst); err == nil {
		return
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(dst), 0o755)
	_ = os.WriteFile(dst, b, 0o644)
}

func LoadChain(path string) (*ChainConfig, error) {
	var c ChainConfig
	if err := readJSON(path, &c); err != nil {
		return nil, err
	}
	if c.PingIntervalSec <= 0 {
		c.PingIntervalSec = 5
	}
	if len(c.ActiveRpcIDs) == 0 && c.ActiveRpcID != "" {
		c.ActiveRpcIDs = []string{c.ActiveRpcID}
	}
	if c.RTTGreenMs <= 0 {
		c.RTTGreenMs = 150
	}
	if c.RTTYellowMs <= 0 {
		c.RTTYellowMs = 400
	}
	return &c, nil
}

func SaveChain(path string, c *ChainConfig) error {
	return writeJSON(path, c)
}

func LoadPresets(path string) (map[string]Preset, error) {
	var m map[string]Preset
	if err := readJSON(path, &m); err != nil {
		return nil, err
	}
	if EnsureBondClaimABI(m) {
		_ = SavePresets(path, m)
	}
	return m, nil
}

// bondClaimABIFragment is claim(uint256) for dual-mode bond presets.
const bondClaimABIFragment = `{"type":"function","name":"claim","stateMutability":"nonpayable","inputs":[{"name":"index","type":"uint256"}],"outputs":[]}`

// EnsureBondClaimABI adds claim(uint256) to fixed_price / lgns_burn ABI when missing.
// Returns true if any preset was modified.
func EnsureBondClaimABI(m map[string]Preset) bool {
	changed := false
	for _, id := range []string{"fixed_price", "lgns_burn"} {
		p, ok := m[id]
		if !ok {
			continue
		}
		raw := bytes.TrimSpace(p.ABI)
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		if bytes.Contains(raw, []byte(`"name":"claim"`)) || bytes.Contains(raw, []byte(`"name": "claim"`)) {
			continue
		}
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil {
			continue
		}
		arr = append(arr, json.RawMessage(bondClaimABIFragment))
		compact, err := json.Marshal(arr)
		if err != nil {
			continue
		}
		p.ABI = compact
		if strings.TrimSpace(p.Function) == "" {
			p.Function = "claimAll"
			p.Method = "claimAll"
		}
		m[id] = p
		changed = true
	}
	return changed
}

func SavePresets(path string, m map[string]Preset) error {
	return writeJSON(path, m)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func getenv(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func getenvInt(k string, def int) int {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
