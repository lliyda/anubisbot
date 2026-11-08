package tasks

import (
	"path/filepath"
	"testing"
	"time"

	"anubisbot/internal/config"
)

func TestSyncWalletNameAndUpdateEditable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.json")
	st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	presets := map[string]config.Preset{
		"fixed_price": {DefaultSchedule: "hourly", Label: "固定价"},
		"lgns_burn":   {DefaultSchedule: "hourly", Label: "LGNS"},
		"helix": {
			DefaultSchedule: "daily_1159_cst",
			Label:           "Helix",
			DefaultParams:   config.PresetParams{BurnAmount: "500000000000", GasLimit: 675000},
		},
	}
	now := time.Date(2026, 11, 6, 15, 30, 0, 0, time.FixedZone("CST", 8*3600))

	emptyName, err := st.Create(Task{
		WalletID: "w1", WalletName: "鑫鑫", WalletAddress: "0x1",
		TaskType: "fixed_price", Enabled: true, Params: TaskParams{GasLimit: 500000},
	}, presets)
	if err != nil {
		t.Fatal(err)
	}
	if emptyName.Name != "" {
		t.Fatalf("Create must not auto-fill name from Label, got %q", emptyName.Name)
	}

	a, err := st.Create(Task{
		Name: "n1", WalletID: "w1", WalletName: "鑫鑫", WalletAddress: "0x1",
		TaskType: "lgns_burn", Enabled: true, Params: TaskParams{GasLimit: 500000},
	}, presets)
	if err != nil {
		t.Fatal(err)
	}

	helix, err := st.Create(Task{
		WalletID: "w2", WalletName: "乙", WalletAddress: "0x2",
		TaskType: "helix", Enabled: false,
		Params: TaskParams{GasLimit: 675000, BurnAmount: "500000000000", Args: []string{"500000000000"}},
	}, presets)
	if err != nil {
		t.Fatal(err)
	}

	n, err := st.SyncWalletName("w1", "新名")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("sync count=%d want 2", n)
	}
	for _, task := range st.List() {
		if task.WalletID == "w1" && task.WalletName != "新名" {
			t.Fatalf("walletName not synced: %+v", task)
		}
	}

	gwei := 5000.0
	updated, err := st.UpdateEditable(a.ID, "备注", "lgns_burn", "on", true, TaskParams{
		GasLimit: 500000, GasPriceGwei: &gwei,
	}, presets, now)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "备注" || updated.Params.GasPriceGwei == nil || *updated.Params.GasPriceGwei != 5000 {
		t.Fatalf("update editable failed: %+v", updated)
	}
	if updated.EarlyBroadcast != "on" {
		t.Fatalf("earlyBroadcast want on, got %q", updated.EarlyBroadcast)
	}
	if !updated.StopAfterSuccess {
		t.Fatalf("stopAfterSuccess want true")
	}

	_, err = st.UpdateEditable(a.ID, "x", "fixed_price", "follow", true, TaskParams{}, presets, now)
	if err == nil || err.Error() != "wallet already has this task type" {
		t.Fatalf("want dup type error, got %v", err)
	}

	gwei2 := 1000.0
	kept, err := st.UpdateEditable(helix.ID, "", "helix", "off", false, TaskParams{
		GasLimit: 675000, GasPriceGwei: &gwei2,
	}, presets, now)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Params.BurnAmount != "500000000000" {
		t.Fatalf("burnAmount wiped: %+v", kept.Params)
	}
	if len(kept.Params.Args) != 1 || kept.Params.Args[0] != "500000000000" {
		t.Fatalf("args wiped: %+v", kept.Params)
	}
	if kept.Params.GasPriceGwei == nil || *kept.Params.GasPriceGwei != 1000 {
		t.Fatalf("gwei not applied: %+v", kept.Params)
	}
	if kept.EarlyBroadcast != "off" {
		t.Fatalf("earlyBroadcast want off, got %q", kept.EarlyBroadcast)
	}
	if kept.StopAfterSuccess {
		t.Fatalf("stopAfterSuccess want false")
	}

	switched, err := st.UpdateEditable(a.ID, "helix任务", "helix", "follow", false, TaskParams{GasLimit: 675000}, presets, now)
	if err != nil {
		t.Fatal(err)
	}
	if switched.TaskType != "helix" || switched.Schedule != "daily_1159_cst" {
		t.Fatalf("type switch failed: %+v", switched)
	}
}
