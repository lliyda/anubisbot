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
	st.SetPresets(presets)
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
	if emptyName.Schedule != ScheduleFollow {
		t.Fatalf("Create empty schedule want follow, got %q", emptyName.Schedule)
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
	updated, err := st.UpdateEditable(a.ID, "备注", "lgns_burn", "", "on", true, TaskParams{
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

	_, err = st.UpdateEditable(a.ID, "x", "fixed_price", "", "follow", true, TaskParams{}, presets, now)
	if err == nil || err.Error() != "wallet already has this task type" {
		t.Fatalf("want dup type error, got %v", err)
	}

	gwei2 := 1000.0
	kept, err := st.UpdateEditable(helix.ID, "", "helix", "", "off", false, TaskParams{
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

	switched, err := st.UpdateEditable(a.ID, "helix任务", "helix", "", "follow", false, TaskParams{GasLimit: 675000}, presets, now)
	if err != nil {
		t.Fatal(err)
	}
	if switched.TaskType != "helix" || switched.Schedule != ScheduleFollow {
		t.Fatalf("type switch failed: %+v", switched)
	}
	if got := st.EffectiveSchedule(switched); got != "daily_1159_cst" {
		t.Fatalf("effective after type switch=%q", got)
	}
	wantFire := ComputeFireAt("daily_1159_cst", now, switched.Params.FireDelayMs, nil)
	if switched.FireAt == nil || !switched.FireAt.Equal(wantFire) {
		t.Fatalf("type switch fireAt=%v want %v", switched.FireAt, wantFire)
	}

	custom, err := st.UpdateEditable(helix.ID, "", "helix", "hourly", "follow", false, TaskParams{GasLimit: 675000}, presets, now)
	if err != nil {
		t.Fatal(err)
	}
	if custom.Schedule != "hourly" {
		t.Fatalf("custom schedule want hourly, got %q", custom.Schedule)
	}
}

func TestFollowScheduleEffectiveAndRecompute(t *testing.T) {
	dir := t.TempDir()
	st, err := Load(filepath.Join(dir, "tasks.json"))
	if err != nil {
		t.Fatal(err)
	}
	presets := map[string]config.Preset{
		"fixed_price": {DefaultSchedule: "hourly", Label: "固定价"},
	}
	st.SetPresets(presets)
	now := time.Date(2026, 11, 6, 15, 30, 0, 0, time.FixedZone("CST", 8*3600))

	followTask, err := st.Create(Task{
		WalletID: "w1", WalletName: "a", WalletAddress: "0x1",
		TaskType: "fixed_price", Schedule: ScheduleFollow, Enabled: true,
		Params: TaskParams{GasLimit: 1},
	}, presets)
	if err != nil {
		t.Fatal(err)
	}
	override, err := st.Create(Task{
		WalletID: "w2", WalletName: "b", WalletAddress: "0x2",
		TaskType: "fixed_price", Schedule: "hourly", Enabled: true,
		Params: TaskParams{GasLimit: 1},
	}, presets)
	if err != nil {
		t.Fatal(err)
	}
	if st.EffectiveSchedule(followTask) != "hourly" {
		t.Fatalf("follow effective=%q", st.EffectiveSchedule(followTask))
	}

	presets["fixed_price"] = config.Preset{DefaultSchedule: "daily_1159_cst", Label: "固定价"}
	st.SetPresets(presets)
	if st.EffectiveSchedule(followTask) != "daily_1159_cst" {
		t.Fatalf("after preset change effective=%q", st.EffectiveSchedule(followTask))
	}
	got, ok := st.Get(followTask.ID)
	if !ok || got.Schedule != ScheduleFollow {
		t.Fatalf("follow task schedule mutated: %+v", got)
	}
	if st.EffectiveSchedule(override) != "hourly" {
		t.Fatalf("override should stay hourly, got %q", st.EffectiveSchedule(override))
	}

	n, err := st.RecomputeFollowers("fixed_price", now)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("recompute followers n=%d", n)
	}
	got, _ = st.Get(followTask.ID)
	want := ComputeFireAt("daily_1159_cst", now, 0, nil)
	if got.FireAt == nil || !got.FireAt.Equal(want) {
		t.Fatalf("follow fireAt=%v want %v", got.FireAt, want)
	}
}
