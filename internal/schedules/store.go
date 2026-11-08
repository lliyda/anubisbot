package schedules

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type fileShape struct {
	Schedules []Rule `json:"schedules"`
}

// Store persists custom schedules; builtins are always merged on List/Get.
type Store struct {
	mu   sync.RWMutex
	path string
	byID map[string]Rule
}

func Load(path string) (*Store, error) {
	dir := filepath.Dir(path)
	_ = os.MkdirAll(dir, 0o755)
	s := &Store{path: path, byID: map[string]Rule{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			_ = s.saveLocked()
			return s, nil
		}
		return nil, err
	}
	var f fileShape
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	for _, r := range f.Schedules {
		r.Builtin = false
		if IsBuiltinID(r.ID) {
			continue
		}
		if err := ValidateCustom(&r); err != nil {
			continue
		}
		s.byID[r.ID] = r
	}
	return s, nil
}

func (s *Store) saveLocked() error {
	list := make([]Rule, 0, len(s.byID))
	for _, r := range s.byID {
		rr := r
		rr.Builtin = false
		list = append(list, rr)
	}
	b, err := json.MarshalIndent(fileShape{Schedules: list}, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// List returns builtins first, then custom rules.
func (s *Store) List() []Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := Builtins()
	custom := make([]Rule, 0, len(s.byID))
	for _, r := range s.byID {
		rr := r
		rr.Builtin = false
		if rr.Label == "" {
			rr.Label = Describe(rr)
		}
		custom = append(custom, rr)
	}
	sort.Slice(custom, func(i, j int) bool { return custom[i].ID < custom[j].ID })
	return append(out, custom...)
}

func (s *Store) CustomMap() map[string]Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]Rule, len(s.byID))
	for k, v := range s.byID {
		out[k] = v
	}
	return out
}

func (s *Store) Exists(id string) bool {
	if IsBuiltinID(id) {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.byID[id]
	return ok
}

func (s *Store) IsRepeating(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return IsRepeatingID(id, s.byID)
}

func (s *Store) Get(id string) (Rule, bool) {
	for _, b := range Builtins() {
		if b.ID == id {
			return b, true
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.byID[id]
	return r, ok
}

func (s *Store) ComputeFireAt(id string, now time.Time, delayMs int, once *time.Time) time.Time {
	s.mu.RLock()
	custom := make(map[string]Rule, len(s.byID))
	for k, v := range s.byID {
		custom[k] = v
	}
	s.mu.RUnlock()
	return ComputeFireAt(id, now, delayMs, once, custom)
}

func (s *Store) fingerprintTaken(fp, exceptID string) bool {
	for _, b := range Builtins() {
		if b.ID == IDOnce {
			continue
		}
		if Fingerprint(b) == fp {
			return true
		}
	}
	for id, r := range s.byID {
		if exceptID != "" && id == exceptID {
			continue
		}
		if Fingerprint(r) == fp {
			return true
		}
	}
	return false
}

func (s *Store) Create(r Rule) (Rule, error) {
	// Normalize fields first so AutoID/AutoLabel see clean values.
	r.Period = strings.TrimSpace(strings.ToLower(r.Period))
	r.Weekday = strings.TrimSpace(strings.ToLower(r.Weekday))
	r.HourMode = strings.TrimSpace(strings.ToLower(r.HourMode))
	if r.Period == PeriodDaily {
		r.Weekday = ""
	} else if norm, err := normalizeWeekdays(r.Weekday); err == nil {
		r.Weekday = norm
	}
	if r.HourMode == HourEveryHour {
		r.Hour = 0
	}
	r.ID = ""
	ApplyAutoIdentity(&r)
	if err := ValidateCustom(&r); err != nil {
		return Rule{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[r.ID]; ok {
		return Rule{}, fmt.Errorf("duplicate schedule")
	}
	if s.fingerprintTaken(Fingerprint(r), "") {
		return Rule{}, fmt.Errorf("duplicate schedule")
	}
	s.byID[r.ID] = r
	if err := s.saveLocked(); err != nil {
		delete(s.byID, r.ID)
		return Rule{}, err
	}
	return r, nil
}

func (s *Store) Update(id string, r Rule) (Rule, error) {
	if IsBuiltinID(id) {
		return Rule{}, fmt.Errorf("builtin schedule is read-only")
	}
	r.ID = id
	r.Period = strings.TrimSpace(strings.ToLower(r.Period))
	r.Weekday = strings.TrimSpace(strings.ToLower(r.Weekday))
	r.HourMode = strings.TrimSpace(strings.ToLower(r.HourMode))
	if r.Period == PeriodDaily {
		r.Weekday = ""
	} else if norm, err := normalizeWeekdays(r.Weekday); err == nil {
		r.Weekday = norm
	}
	if r.HourMode == HourEveryHour {
		r.Hour = 0
	}
	r.Label = AutoLabel(r)
	if err := ValidateCustom(&r); err != nil {
		return Rule{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[id]; !ok {
		return Rule{}, fmt.Errorf("schedule not found")
	}
	if s.fingerprintTaken(Fingerprint(r), id) {
		return Rule{}, fmt.Errorf("duplicate schedule")
	}
	s.byID[id] = r
	if err := s.saveLocked(); err != nil {
		return Rule{}, err
	}
	return r, nil
}

func (s *Store) Delete(id string) error {
	if IsBuiltinID(id) {
		return fmt.Errorf("builtin schedule is read-only")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[id]; !ok {
		return fmt.Errorf("schedule not found")
	}
	delete(s.byID, id)
	return s.saveLocked()
}
