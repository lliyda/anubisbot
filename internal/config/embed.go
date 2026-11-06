package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed defaults/chain.json
var defaultChainJSON []byte

//go:embed defaults/presets.json
var defaultPresetsJSON []byte

//go:embed defaults/wallets.example.json
var defaultWalletsExampleJSON []byte

// EnsureRuntime creates missing runtime config/data files under root.
// Existing files are never overwritten. Returns names of files that were seeded.
func EnsureRuntime(root string) ([]string, error) {
	if root == "" {
		root = "."
	}
	var seeded []string
	write := func(rel string, body []byte) error {
		path := filepath.Join(root, rel)
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return err
		}
		seeded = append(seeded, rel)
		return nil
	}

	if err := write("config/chain.json", ensureNL(defaultChainJSON)); err != nil {
		return seeded, fmt.Errorf("seed chain.json: %w", err)
	}
	if err := write("config/presets.json", ensureNL(defaultPresetsJSON)); err != nil {
		return seeded, fmt.Errorf("seed presets.json: %w", err)
	}
	if err := write("config/wallets.example.json", ensureNL(defaultWalletsExampleJSON)); err != nil {
		return seeded, fmt.Errorf("seed wallets.example.json: %w", err)
	}
	// SIM wallets: prefer example content; LIVE starts empty.
	if err := write("config/wallets.sim.json", ensureNL(defaultWalletsExampleJSON)); err != nil {
		return seeded, fmt.Errorf("seed wallets.sim.json: %w", err)
	}
	if err := write("config/wallets.live.json", []byte("{\"wallets\":[]}\n")); err != nil {
		return seeded, fmt.Errorf("seed wallets.live.json: %w", err)
	}
	emptyTasks := []byte("{\"tasks\":[]}\n")
	if err := write("data/tasks.sim.json", emptyTasks); err != nil {
		return seeded, fmt.Errorf("seed tasks.sim.json: %w", err)
	}
	if err := write("data/tasks.live.json", emptyTasks); err != nil {
		return seeded, fmt.Errorf("seed tasks.live.json: %w", err)
	}
	return seeded, nil
}

func ensureNL(b []byte) []byte {
	if len(b) == 0 {
		return []byte("\n")
	}
	if b[len(b)-1] != '\n' {
		out := make([]byte, len(b)+1)
		copy(out, b)
		out[len(b)] = '\n'
		return out
	}
	return b
}
