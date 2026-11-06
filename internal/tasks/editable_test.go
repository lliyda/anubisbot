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
		"helix":       {DefaultSchedule: "daily_1159_cst", Label: "Helix"},
	}
	now := time.Date(2026, 11, 6, 15, 30, 0, 0, time.FixedZone("CST", 8*3600))

	a, err := st.Create(Task{
		Name: "n1", WalletID: "w1", WalletName: "鑫鑫", WalletAddress: "0x1",
		TaskType: "fixed_price", Enabled: true, Params: TaskParams{GasLimit: 500000},
	}, presets)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.Create(Task{
		Name: "n2", WalletID: "w1", WalletName: "鑫鑫", WalletAddress: "0x1",
		TaskType: "lgns_burn", Enabled: true,
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
	updated, err := st.UpdateEditable(a.ID, "备注", "fixed_price", TaskParams{
		GasLimit: 500000, GasPriceGwei: &gwei,
	}, presets, now)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "备注" || updated.Params.GasPriceGwei == nil || *updated.Params.GasPriceGwei != 5000 {
		t.Fatalf("update editable failed: %+v", updated)
	}

	_, err = st.UpdateEditable(a.ID, "x", "lgns_burn", TaskParams{}, presets, now)
	if err == nil || err.Error() != "wallet already has this task type" {
		t.Fatalf("want dup type error, got %v", err)
	}

	switched, err := st.UpdateEditable(a.ID, "helix任务", "helix", TaskParams{GasLimit: 675000}, presets, now)
	if err != nil {
		t.Fatal(err)
	}
	if switched.TaskType != "helix" || switched.Schedule != "daily_1159_cst" {
		t.Fatalf("type switch failed: %+v", switched)
	}
}
