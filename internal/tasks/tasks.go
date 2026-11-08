package tasks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"anubisbot/internal/config"
	"anubisbot/internal/timesync"

	"github.com/google/uuid"
)

type TaskParams struct {
	Args         []string `json:"args,omitempty"`
	ClaimMode    string   `json:"claimMode,omitempty"`    // ""|"all"|"index"; empty → all
	ClaimArg     int64    `json:"claimArg,omitempty"`     // legacy; index mode may be 0
	BurnAmount   string   `json:"burnAmount,omitempty"`   // legacy
	GasLimit     uint64   `json:"gasLimit,omitempty"`
	GasPriceGwei *float64 `json:"gasPriceGwei,omitempty"`
	FireDelayMs  int      `json:"fireDelayMs,omitempty"`
}

// NormalizeClaimMode maps raw values to all|index.
func NormalizeClaimMode(v string) string {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "index":
		return "index"
	default:
		return "all"
	}
}

// EffectiveClaimMode returns all or index for bond claim packing.
func (p TaskParams) EffectiveClaimMode() string {
	return NormalizeClaimMode(p.ClaimMode)
}

type Task struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	WalletID      string     `json:"walletId"`
	WalletName    string     `json:"walletName"`
	WalletAddress string     `json:"walletAddress"`
	TaskType      string     `json:"taskType"`
	Schedule      string     `json:"schedule"`
	FireAt        *time.Time `json:"fireAt"`
	Enabled       bool       `json:"enabled"`
	Params        TaskParams `json:"params"`
	// EarlyBroadcast: "on" | "off" | "follow" (empty → follow global settings).
	EarlyBroadcast string `json:"earlyBroadcast,omitempty"`
	// StopAfterSuccess: when true, disable the task after one successful run.
	StopAfterSuccess bool       `json:"stopAfterSuccess"`
	Status           string     `json:"status"`
	LastRunAt      *time.Time `json:"lastRunAt"`
	LastTxHash     string     `json:"lastTxHash,omitempty"`
	LastError      string     `json:"lastError,omitempty"`
	LastRpcID      string     `json:"lastRpcId,omitempty"`
	LastRttMs      *int64     `json:"lastRttMs,omitempty"`
}

// NormalizeEarlyBroadcast maps raw values to on|off|follow.
func NormalizeEarlyBroadcast(v string) string {
	switch v {
	case "on", "off":
		return v
	default:
		return "follow"
	}
}

// EffectiveEarlyBroadcast resolves per-task override against the global setting.
func (t Task) EffectiveEarlyBroadcast(global bool) bool {
	switch NormalizeEarlyBroadcast(t.EarlyBroadcast) {
	case "on":
		return true
	case "off":
		return false
	default:
		return global
	}
}

type fileShape struct {
	Tasks []Task `json:"tasks"`
}

// ScheduleFollow means the task uses presets[taskType].DefaultSchedule at runtime.
const ScheduleFollow = "follow"

// ScheduleComputer resolves next fire times for builtin + custom schedules.
type ScheduleComputer interface {
	ComputeFireAt(schedule string, now time.Time, delayMs int, once *time.Time) time.Time
	IsRepeating(schedule string) bool
}

type Store struct {
	mu        sync.RWMutex
	path      string
	list      []Task
	presets   map[string]config.Preset
	Schedules ScheduleComputer // optional; nil → builtin-only ComputeFireAt
}

// SetSchedules wires custom schedule resolution (call once at startup).
func (s *Store) SetSchedules(c ScheduleComputer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Schedules = c
}

// SetPresets wires task-type defaults for ScheduleFollow resolution.
func (s *Store) SetPresets(p map[string]config.Preset) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.presets = p
}

// IsFollowSchedule reports whether raw means "use type default".
func IsFollowSchedule(raw string) bool {
	s := strings.TrimSpace(raw)
	return s == "" || s == ScheduleFollow
}

// EffectiveSchedule resolves follow/empty to the task type's defaultSchedule.
func (s *Store) EffectiveSchedule(t Task) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.effectiveScheduleLocked(t)
}

func (s *Store) effectiveScheduleLocked(t Task) string {
	if !IsFollowSchedule(t.Schedule) {
		return strings.TrimSpace(t.Schedule)
	}
	if p, ok := s.presets[t.TaskType]; ok {
		if d := strings.TrimSpace(p.DefaultSchedule); d != "" {
			return d
		}
	}
	return "hourly"
}

func Load(path string) (*Store, error) {
	dir := filepath.Dir(path)
	_ = os.MkdirAll(dir, 0o755)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		_ = os.WriteFile(path, []byte(`{"tasks":[]}`+"\n"), 0o644)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f fileShape
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	return &Store{path: path, list: f.Tasks}, nil
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(fileShape{Tasks: s.list}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(b, '\n'), 0o644)
}

func (s *Store) List() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Task, len(s.list))
	copy(out, s.list)
	return out
}

func (s *Store) Get(id string) (Task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.list {
		if t.ID == id {
			return t, true
		}
	}
	return Task{}, false
}

// SyncWalletName updates walletName on every task bound to walletID.
func (s *Store) SyncWalletName(walletID, name string) (int, error) {
	if walletID == "" {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for i := range s.list {
		if s.list[i].WalletID != walletID {
			continue
		}
		if s.list[i].WalletName == name {
			continue
		}
		s.list[i].WalletName = name
		n++
	}
	if n == 0 {
		return 0, nil
	}
	if err := s.saveLocked(); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) Create(t Task, presets map[string]config.Preset) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.ID == "" {
		t.ID = uuid.NewString()[:8]
	}
	if t.Status == "" {
		t.Status = "idle"
	}
	t.EarlyBroadcast = NormalizeEarlyBroadcast(t.EarlyBroadcast)
	if IsFollowSchedule(t.Schedule) {
		t.Schedule = ScheduleFollow
	} else {
		t.Schedule = strings.TrimSpace(t.Schedule)
	}
	if p, ok := presets[t.TaskType]; ok {
		if t.Params.GasLimit == 0 {
			t.Params.GasLimit = p.DefaultParams.GasLimit
		}
		if t.Params.ClaimArg == 0 && p.DefaultParams.ClaimArg != 0 {
			t.Params.ClaimArg = p.DefaultParams.ClaimArg
		}
		if t.Params.BurnAmount == "" {
			t.Params.BurnAmount = p.DefaultParams.BurnAmount
		}
		// Name stays empty unless the client set it — never auto-fill from Label.
	}
	s.list = append(s.list, t)
	if err := s.saveLocked(); err != nil {
		return Task{}, err
	}
	return t, nil
}

// SyncAllSchedules forces every task's schedule to match its preset defaultSchedule.
func (s *Store) SyncAllSchedules(presets map[string]config.Preset, now time.Time) (int, error) {
	if len(presets) == 0 {
		return 0, nil
	}
	n := 0
	for tt, p := range presets {
		if p.DefaultSchedule == "" {
			continue
		}
		c, err := s.SyncScheduleForType(tt, p.DefaultSchedule, now)
		if err != nil {
			return n, err
		}
		n += c
	}
	return n, nil
}

// SyncScheduleForType sets schedule on all tasks of taskType and normalizes fireAt.
func (s *Store) SyncScheduleForType(taskType, schedule string, now time.Time) (int, error) {
	return s.SyncScheduleFollowers(taskType, "", schedule, now)
}

// SyncScheduleFollowers updates tasks of taskType to toSchedule.
// If fromSchedule is non-empty, only tasks still on fromSchedule are updated (custom schedules kept).
func (s *Store) SyncScheduleFollowers(taskType, fromSchedule, toSchedule string, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for i := range s.list {
		if s.list[i].TaskType != taskType {
			continue
		}
		if fromSchedule != "" && s.list[i].Schedule != fromSchedule {
			continue
		}
		t := s.list[i]
		before := t
		t.Schedule = toSchedule
		s.normalizeFireAtLocked(&t, now)
		if taskFireEqual(before, t) {
			continue
		}
		s.list[i] = t
		n++
	}
	if n == 0 {
		return 0, nil
	}
	if err := s.saveLocked(); err != nil {
		return 0, err
	}
	return n, nil
}

// RecomputeFollowers renormalizes fireAt for follow-schedule tasks of taskType (does not change Schedule).
func (s *Store) RecomputeFollowers(taskType string, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for i := range s.list {
		if s.list[i].TaskType != taskType || !IsFollowSchedule(s.list[i].Schedule) {
			continue
		}
		t := s.list[i]
		before := t
		s.normalizeFireAtLocked(&t, now)
		if taskFireEqual(before, t) {
			continue
		}
		s.list[i] = t
		n++
	}
	if n == 0 {
		return 0, nil
	}
	if err := s.saveLocked(); err != nil {
		return 0, err
	}
	return n, nil
}

// SyncBySchedule renormalizes fireAt for all tasks using the given schedule id
// (including follow tasks whose effective schedule resolves to it).
func (s *Store) SyncBySchedule(schedule string, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for i := range s.list {
		t0 := s.list[i]
		if t0.Schedule != schedule && s.effectiveScheduleLocked(t0) != schedule {
			continue
		}
		t := t0
		before := t
		t.FireAt = nil
		s.normalizeFireAtLocked(&t, now)
		if taskFireEqual(before, t) {
			continue
		}
		s.list[i] = t
		n++
	}
	if n == 0 {
		return 0, nil
	}
	if err := s.saveLocked(); err != nil {
		return 0, err
	}
	return n, nil
}

// NormalizeAllFireAts repairs stale/misaligned next-run times (e.g. hourly showing 19:17).
func (s *Store) NormalizeAllFireAts(now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for i := range s.list {
		t := s.list[i]
		before := t
		s.normalizeFireAtLocked(&t, now)
		if taskFireEqual(before, t) {
			continue
		}
		s.list[i] = t
		n++
	}
	if n == 0 {
		return 0, nil
	}
	if err := s.saveLocked(); err != nil {
		return 0, err
	}
	return n, nil
}

func taskFireEqual(a, b Task) bool {
	if a.Schedule != b.Schedule || a.Status != b.Status || a.Enabled != b.Enabled {
		return false
	}
	if a.FireAt == nil && b.FireAt == nil {
		return true
	}
	if a.FireAt == nil || b.FireAt == nil {
		return false
	}
	return a.FireAt.Equal(*b.FireAt)
}

func hourlyAlignedCST(t time.Time) bool {
	c := t.In(timesync.CST())
	return c.Minute() == 0 && c.Second() == 0
}

func (s *Store) isRepeating(schedule string) bool {
	if s.Schedules != nil {
		return s.Schedules.IsRepeating(schedule)
	}
	return schedule == "hourly" || schedule == "daily_1159_cst"
}

// IsRepeatingSchedule reports whether the schedule auto-rearms after a run.
func (s *Store) IsRepeatingSchedule(schedule string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isRepeating(schedule)
}

// IsRepeatingTask reports whether the task's effective schedule auto-rearms.
func (s *Store) IsRepeatingTask(t Task) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isRepeating(s.effectiveScheduleLocked(t))
}

func (s *Store) computeFireAt(schedule string, now time.Time, delayMs int, once *time.Time) time.Time {
	if s.Schedules != nil {
		return s.Schedules.ComputeFireAt(schedule, now, delayMs, once)
	}
	return ComputeFireAt(schedule, now, delayMs, once)
}

func (s *Store) normalizeFireAtLocked(t *Task, now time.Time) {
	eff := s.effectiveScheduleLocked(*t)
	if eff == "once" {
		if !t.Enabled {
			// keep future once time for editing; drop past leftovers
			if t.FireAt != nil && !t.FireAt.After(now) {
				t.FireAt = nil
			}
			if t.Status == "armed" {
				t.Status = "idle"
			}
		}
		return
	}
	if !s.isRepeating(eff) {
		return
	}
	if !t.Enabled {
		if t.FireAt != nil {
			t.FireAt = nil
		}
		if t.Status == "armed" {
			t.Status = "idle"
		}
		return
	}
	want := s.computeFireAt(eff, now, t.Params.FireDelayMs, nil)
	need := t.FireAt == nil || !t.FireAt.After(now) || t.Status != "armed"
	if eff == "hourly" && t.FireAt != nil && !hourlyAlignedCST(*t.FireAt) {
		need = true
	}
	if t.FireAt != nil && !t.FireAt.Equal(want) {
		need = true
	}
	if need {
		fa := want
		t.FireAt = &fa
		t.Status = "armed"
		t.LastError = ""
	}
}

func (s *Store) Patch(id string, patch map[string]any) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		if s.list[i].ID != id {
			continue
		}
		b, _ := json.Marshal(s.list[i])
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		for k, v := range patch {
			m[k] = v
		}
		nb, _ := json.Marshal(m)
		var t Task
		if err := json.Unmarshal(nb, &t); err != nil {
			return Task{}, err
		}
		s.list[i] = t
		if err := s.saveLocked(); err != nil {
			return Task{}, err
		}
		return t, nil
	}
	return Task{}, fmt.Errorf("task not found")
}

// UpdateEditable updates name, taskType, schedule, earlyBroadcast, stopAfterSuccess, and editable gas/args from the edit dialog.
// Other params fields (claimArg, burnAmount, fireDelayMs) are preserved.
// Empty schedule keeps the current value; empty on type change falls back to ScheduleFollow.
func (s *Store) UpdateEditable(id, name, taskType, schedule, earlyBroadcast string, stopAfterSuccess bool, params TaskParams, presets map[string]config.Preset, now time.Time) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if presets != nil {
		s.presets = presets
	}
	idx := -1
	for i := range s.list {
		if s.list[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Task{}, fmt.Errorf("task not found")
	}
	t := s.list[idx]
	if taskType == "" {
		taskType = t.TaskType
	}
	typeChanged := taskType != t.TaskType
	schedule = strings.TrimSpace(schedule)
	var newPreset config.Preset
	if typeChanged {
		p, ok := presets[taskType]
		if !ok {
			return Task{}, fmt.Errorf("unknown task type")
		}
		for _, other := range s.list {
			if other.ID == id {
				continue
			}
			if other.WalletID == t.WalletID && other.TaskType == taskType {
				return Task{}, fmt.Errorf("wallet already has this task type")
			}
		}
		t.TaskType = taskType
		if schedule == "" {
			schedule = ScheduleFollow
		}
		newPreset = p
	} else if _, ok := presets[taskType]; taskType != "" && !ok {
		return Task{}, fmt.Errorf("unknown task type")
	}
	if IsFollowSchedule(schedule) && schedule != "" {
		schedule = ScheduleFollow
	}
	scheduleChanged := false
	if schedule != "" && schedule != t.Schedule {
		t.Schedule = schedule
		scheduleChanged = true
	}
	merged := t.Params
	if params.GasLimit != 0 {
		merged.GasLimit = params.GasLimit
	}
	merged.GasPriceGwei = params.GasPriceGwei
	if strings.TrimSpace(params.ClaimMode) != "" || params.Args != nil {
		mode := NormalizeClaimMode(params.ClaimMode)
		if strings.TrimSpace(params.ClaimMode) == "" && len(params.Args) > 0 {
			mode = "index"
		}
		merged.ClaimMode = mode
		if params.Args != nil {
			merged.Args = append([]string(nil), params.Args...)
		}
		if mode == "all" {
			merged.Args = nil
			merged.ClaimArg = 0
		} else if len(merged.Args) > 0 {
			if n, err := strconv.ParseInt(strings.TrimSpace(merged.Args[0]), 10, 64); err == nil {
				merged.ClaimArg = n
			}
		} else {
			merged.ClaimArg = params.ClaimArg
			merged.Args = []string{strconv.FormatInt(merged.ClaimArg, 10)}
		}
	}
	if typeChanged {
		// Switching task type resets claim mode to default (all).
		merged.ClaimMode = "all"
		merged.Args = nil
		merged.ClaimArg = 0
		if merged.BurnAmount == "" && newPreset.DefaultParams.BurnAmount != "" {
			merged.BurnAmount = newPreset.DefaultParams.BurnAmount
		}
		if merged.GasLimit == 0 && newPreset.DefaultParams.GasLimit != 0 {
			merged.GasLimit = newPreset.DefaultParams.GasLimit
		}
	}
	t.Name = name
	t.EarlyBroadcast = NormalizeEarlyBroadcast(earlyBroadcast)
	t.StopAfterSuccess = stopAfterSuccess
	t.Params = merged
	if typeChanged || scheduleChanged {
		// Force next-run recompute from new schedule + now.
		t.FireAt = nil
	}
	s.normalizeFireAtLocked(&t, now)
	s.list[idx] = t
	if err := s.saveLocked(); err != nil {
		return Task{}, err
	}
	return t, nil
}

func (s *Store) Update(t Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		if s.list[i].ID == t.ID {
			s.list[i] = t
			return s.saveLocked()
		}
	}
	return fmt.Errorf("task not found")
}

func (s *Store) SetEnabled(id string, enabled bool) (Task, error) {
	return s.Patch(id, map[string]any{"enabled": enabled})
}

// SetOnceFireAt sets fireAt for once-schedule tasks. If enabled, status becomes armed.
func (s *Store) SetOnceFireAt(id string, fireAt time.Time) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		if s.list[i].ID != id {
			continue
		}
		t := s.list[i]
		if s.effectiveScheduleLocked(t) != "once" {
			return Task{}, fmt.Errorf("fireAt editable only when schedule is once")
		}
		fa := fireAt.UTC()
		t.FireAt = &fa
		if t.Enabled {
			t.Status = "armed"
			t.LastError = ""
		}
		s.list[i] = t
		if err := s.saveLocked(); err != nil {
			return Task{}, err
		}
		return t, nil
	}
	return Task{}, fmt.Errorf("task not found")
}

func ComputeFireAt(schedule string, now time.Time, delayMs int, once *time.Time) time.Time {
	switch schedule {
	case "hourly":
		return timesync.NextHourCST(now)
	case "daily_1159_cst":
		return timesync.Next1159CST(now, delayMs)
	case "once":
		if once != nil && once.After(now) {
			return *once
		}
		return now.Add(5 * time.Second)
	default:
		return timesync.NextHourCST(now)
	}
}

// Disable turns off the task and clears the fire queue.
func (s *Store) Disable(id string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		if s.list[i].ID != id {
			continue
		}
		t := s.list[i]
		t.Enabled = false
		t.Status = "idle"
		t.FireAt = nil
		s.list[i] = t
		if err := s.saveLocked(); err != nil {
			return Task{}, err
		}
		return t, nil
	}
	return Task{}, fmt.Errorf("task not found")
}

func (s *Store) Arm(id string, now time.Time, fireAtOverride *time.Time) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		if s.list[i].ID != id {
			continue
		}
		t := s.list[i]
		var fa time.Time
		if fireAtOverride != nil {
			fa = fireAtOverride.UTC()
			// keep original schedule (dry-run must not rewrite hourly/1159 to once)
		} else {
			fa = s.computeFireAt(s.effectiveScheduleLocked(t), now, t.Params.FireDelayMs, t.FireAt)
		}
		t.FireAt = &fa
		t.Status = "armed"
		t.LastError = ""
		s.list[i] = t
		if err := s.saveLocked(); err != nil {
			return Task{}, err
		}
		return t, nil
	}
	return Task{}, fmt.Errorf("task not found")
}

func (s *Store) Cancel(id string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		if s.list[i].ID != id {
			continue
		}
		t := s.list[i]
		t.Status = "idle"
		t.FireAt = nil
		s.list[i] = t
		if err := s.saveLocked(); err != nil {
			return Task{}, err
		}
		return t, nil
	}
	return Task{}, fmt.Errorf("task not found")
}

func (s *Store) ArmedDue(now time.Time) []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Task
	for _, t := range s.list {
		if t.Status == "armed" && t.Enabled && t.FireAt != nil && !t.FireAt.After(now) {
			out = append(out, t)
		}
	}
	return out
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		if s.list[i].ID != id {
			continue
		}
		s.list = append(s.list[:i], s.list[i+1:]...)
		return s.saveLocked()
	}
	return fmt.Errorf("task not found")
}

// WalletRef is the minimal wallet info needed to seed default tasks.
type WalletRef struct {
	ID      string
	Name    string
	Address string
}

// EnsureDefaults creates one idle disabled task per missing preset type.
func EnsureDefaults(s *Store, wal WalletRef, presets map[string]config.Preset) (int, error) {
	if wal.ID == "" {
		return 0, nil
	}
	types := []string{"fixed_price", "lgns_burn", "helix"}
	have := map[string]bool{}
	for _, t := range s.List() {
		have[t.TaskType] = true
	}
	added := 0
	for _, tt := range types {
		if have[tt] {
			continue
		}
		p, ok := presets[tt]
		if !ok {
			continue
		}
		task := Task{
			Name:          p.Label,
			WalletID:      wal.ID,
			WalletName:    wal.Name,
			WalletAddress: wal.Address,
			TaskType:      tt,
			Schedule:      ScheduleFollow,
			Enabled:       false,
			Status:        "idle",
			Params: TaskParams{
				ClaimArg:    p.DefaultParams.ClaimArg,
				BurnAmount:  p.DefaultParams.BurnAmount,
				GasLimit:    p.DefaultParams.GasLimit,
				GasPriceGwei: p.DefaultParams.GasPriceGwei,
			},
		}
		if _, err := s.Create(task, presets); err != nil {
			return added, err
		}
		added++
	}
	return added, nil
}
