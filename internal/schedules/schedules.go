package schedules

import (
	"fmt"
	"strings"
	"time"

	"anubisbot/internal/timesync"
)

// Rule is an alarm-style schedule definition (CST).
type Rule struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Period   string `json:"period"`             // daily | weekly (custom); ignored for once
	Weekday  string `json:"weekday,omitempty"`  // every | mon…sun | mon,fri,wed (weekly, multi)
	HourMode string `json:"hourMode,omitempty"` // every_hour | at_hour
	Hour     int    `json:"hour,omitempty"`     // 0–23 when at_hour
	Minute   int    `json:"minute,omitempty"`   // 0–59
	Builtin  bool   `json:"builtin,omitempty"`
}

const (
	IDHourly      = "hourly"
	IDDaily1159   = "daily_1159_cst"
	IDOnce        = "once"
	PeriodDaily   = "daily"
	PeriodWeekly  = "weekly"
	WeekdayEvery  = "every"
	HourEveryHour = "every_hour"
	HourAtHour    = "at_hour"
)

// Builtins returns the three read-only system schedules.
func Builtins() []Rule {
	return []Rule{
		{ID: IDHourly, Label: "每整点", Builtin: true, Period: PeriodDaily, HourMode: HourEveryHour, Minute: 0},
		{ID: IDDaily1159, Label: "每日 11:59", Builtin: true, Period: PeriodDaily, HourMode: HourAtHour, Hour: 11, Minute: 59},
		{ID: IDOnce, Label: "仅一次", Builtin: true},
	}
}

func IsBuiltinID(id string) bool {
	switch id {
	case IDHourly, IDDaily1159, IDOnce:
		return true
	default:
		return false
	}
}

func IsRepeatingID(id string, custom map[string]Rule) bool {
	if id == "" || id == IDOnce {
		return false
	}
	if IsBuiltinID(id) {
		return id == IDHourly || id == IDDaily1159
	}
	_, ok := custom[id]
	return ok
}

// ValidateCustom checks a user-defined rule (id/label required; not a builtin id).
func ValidateCustom(r *Rule) error {
	if r == nil {
		return fmt.Errorf("empty rule")
	}
	r.ID = strings.TrimSpace(r.ID)
	r.Label = strings.TrimSpace(r.Label)
	r.Period = strings.TrimSpace(strings.ToLower(r.Period))
	r.Weekday = strings.TrimSpace(strings.ToLower(r.Weekday))
	r.HourMode = strings.TrimSpace(strings.ToLower(r.HourMode))
	r.Builtin = false
	if r.ID == "" {
		return fmt.Errorf("id required")
	}
	if IsBuiltinID(r.ID) {
		return fmt.Errorf("id reserved for builtin")
	}
	if !idREOK(r.ID) {
		return fmt.Errorf("invalid id")
	}
	if r.Label == "" {
		return fmt.Errorf("label required")
	}
	switch r.Period {
	case PeriodDaily, PeriodWeekly:
	default:
		return fmt.Errorf("period must be daily or weekly")
	}
	if r.Period == PeriodWeekly {
		norm, err := normalizeWeekdays(r.Weekday)
		if err != nil {
			return err
		}
		r.Weekday = norm
	} else {
		r.Weekday = ""
	}
	switch r.HourMode {
	case HourEveryHour, HourAtHour:
	default:
		return fmt.Errorf("hourMode must be every_hour or at_hour")
	}
	if r.Minute < 0 || r.Minute > 59 {
		return fmt.Errorf("minute must be 0–59")
	}
	if r.HourMode == HourAtHour {
		if r.Hour < 0 || r.Hour > 23 {
			return fmt.Errorf("hour must be 0–23")
		}
	} else {
		r.Hour = 0
	}
	return nil
}

func idREOK(id string) bool {
	if len(id) < 2 || len(id) > 32 {
		return false
	}
	for i, c := range id {
		if c >= 'a' && c <= 'z' {
			continue
		}
		if c >= '0' && c <= '9' && i > 0 {
			continue
		}
		if c == '_' && i > 0 {
			continue
		}
		return false
	}
	return true
}

var weekdayOrder = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

func validWeekdayToken(w string) bool {
	switch w {
	case WeekdayEvery, "mon", "tue", "wed", "thu", "fri", "sat", "sun":
		return true
	default:
		return false
	}
}

// normalizeWeekdays accepts "every", single day, or comma-separated multi.
// Returns "every" or sorted unique days like "fri,mon".
func normalizeWeekdays(raw string) (string, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" || raw == WeekdayEvery {
		return WeekdayEvery, nil
	}
	parts := strings.Split(raw, ",")
	seen := map[string]bool{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if p == WeekdayEvery {
			return WeekdayEvery, nil
		}
		if !validWeekdayToken(p) {
			return "", fmt.Errorf("invalid weekday")
		}
		seen[p] = true
	}
	if len(seen) == 0 {
		return WeekdayEvery, nil
	}
	if len(seen) == 7 {
		return WeekdayEvery, nil
	}
	out := make([]string, 0, len(seen))
	for _, d := range weekdayOrder {
		if seen[d] {
			out = append(out, d)
		}
	}
	return strings.Join(out, ","), nil
}

func parseWeekdays(weekday string) []string {
	weekday = strings.TrimSpace(strings.ToLower(weekday))
	if weekday == "" || weekday == WeekdayEvery {
		return nil // nil → every day
	}
	parts := strings.Split(weekday, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" && validWeekdayToken(p) && p != WeekdayEvery {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func weekdayMatch(t time.Time, weekday string) bool {
	days := parseWeekdays(weekday)
	if days == nil {
		return true
	}
	for _, d := range days {
		want, ok := weekdayToTime(d)
		if ok && t.Weekday() == want {
			return true
		}
	}
	return false
}

func weekdayToTime(w string) (time.Weekday, bool) {
	switch w {
	case "sun":
		return time.Sunday, true
	case "mon":
		return time.Monday, true
	case "tue":
		return time.Tuesday, true
	case "wed":
		return time.Wednesday, true
	case "thu":
		return time.Thursday, true
	case "fri":
		return time.Friday, true
	case "sat":
		return time.Saturday, true
	default:
		return 0, false
	}
}

// NextFireAt returns the next fire time in CST for a custom (or builtin-shaped) rule.
// Does not handle "once".
func NextFireAt(r Rule, now time.Time, delayMs int) time.Time {
	loc := timesync.CST()
	t := now.In(loc)
	weekday := r.Weekday
	if r.Period != PeriodWeekly {
		weekday = WeekdayEvery
	}

	const maxSteps = 24*14 + 2 // two weeks of hours
	if r.HourMode == HourEveryHour {
		cand := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), r.Minute, 0, 0, loc)
		if delayMs > 0 {
			cand = cand.Add(time.Duration(delayMs) * time.Millisecond)
		}
		if !t.Before(cand) {
			cand = cand.Add(time.Hour)
		}
		for i := 0; i < maxSteps; i++ {
			if weekdayMatch(cand, weekday) {
				return cand
			}
			cand = cand.Add(time.Hour)
		}
		return cand
	}

	// at_hour
	cand := time.Date(t.Year(), t.Month(), t.Day(), r.Hour, r.Minute, 0, 0, loc)
	if delayMs > 0 {
		cand = cand.Add(time.Duration(delayMs) * time.Millisecond)
	}
	if !t.Before(cand) {
		cand = cand.Add(24 * time.Hour)
	}
	for i := 0; i < 14; i++ {
		if weekdayMatch(cand, weekday) {
			return cand
		}
		cand = cand.Add(24 * time.Hour)
	}
	return cand
}

// ComputeFireAt resolves builtins + custom map. once is only used for IDOnce.
func ComputeFireAt(id string, now time.Time, delayMs int, once *time.Time, custom map[string]Rule) time.Time {
	switch id {
	case IDHourly:
		return timesync.NextHourCST(now)
	case IDDaily1159:
		return timesync.Next1159CST(now, delayMs)
	case IDOnce:
		if once != nil && once.After(now) {
			return *once
		}
		return now.Add(5 * time.Second)
	default:
		if r, ok := custom[id]; ok {
			return NextFireAt(r, now, delayMs)
		}
		return timesync.NextHourCST(now)
	}
}

// Fingerprint is a stable semantic key for dedupe (ignores id/label).
func Fingerprint(r Rule) string {
	period := r.Period
	weekday := ""
	if period == PeriodWeekly {
		weekday, _ = normalizeWeekdays(r.Weekday)
	}
	hour := 0
	if r.HourMode == HourAtHour {
		hour = r.Hour
	}
	return fmt.Sprintf("%s|%s|%s|%d|%d", period, weekday, r.HourMode, hour, r.Minute)
}

func weekdayIDToken(weekday string) string {
	norm, _ := normalizeWeekdays(weekday)
	days := parseWeekdays(norm)
	if days == nil {
		return WeekdayEvery
	}
	// Compact letters: mon=m tue=t wed=w thu=h fri=f sat=a sun=u
	letter := map[string]byte{
		"mon": 'm', "tue": 't', "wed": 'w', "thu": 'h', "fri": 'f', "sat": 'a', "sun": 'u',
	}
	var b strings.Builder
	for _, d := range days {
		if c, ok := letter[d]; ok {
			b.WriteByte(c)
		}
	}
	if b.Len() == 0 {
		return WeekdayEvery
	}
	return b.String()
}

// AutoID builds a stable custom schedule id from rule fields.
func AutoID(r Rule) string {
	var b strings.Builder
	if r.Period == PeriodWeekly {
		b.WriteString("w_")
		b.WriteString(weekdayIDToken(r.Weekday))
		b.WriteByte('_')
	} else {
		b.WriteString("d_")
	}
	if r.HourMode == HourEveryHour {
		b.WriteString(fmt.Sprintf("eh_m%d", r.Minute))
	} else {
		b.WriteString(fmt.Sprintf("h%d_m%d", r.Hour, r.Minute))
	}
	id := b.String()
	if len(id) > 32 {
		id = id[:32]
	}
	return id
}

// AutoLabel builds the Chinese display name from rule fields (ignores Label).
func AutoLabel(r Rule) string {
	hourPart := ""
	if r.HourMode == HourEveryHour {
		hourPart = fmt.Sprintf("每整点第%d分", r.Minute)
	} else {
		hourPart = fmt.Sprintf("%02d:%02d", r.Hour, r.Minute)
	}
	switch r.Period {
	case PeriodWeekly:
		norm, _ := normalizeWeekdays(r.Weekday)
		days := parseWeekdays(norm)
		var dayPart string
		if days == nil {
			dayPart = "每周每天"
		} else if len(days) == 1 {
			dayPart = "每周" + weekdayCN(days[0])
		} else {
			dayPart = "每周" + weekdayListCN(norm)
		}
		return dayPart + " " + hourPart
	default:
		if r.HourMode == HourEveryHour {
			return hourPart
		}
		return "每天 " + hourPart
	}
}

// Describe builds a short Chinese label from rule fields (fallback when Label empty).
func Describe(r Rule) string {
	if strings.TrimSpace(r.Label) != "" {
		return r.Label
	}
	return AutoLabel(r)
}

// ApplyAutoIdentity fills id/label from fields when creating (id empty → AutoID).
func ApplyAutoIdentity(r *Rule) {
	if r == nil {
		return
	}
	r.Label = AutoLabel(*r)
	if strings.TrimSpace(r.ID) == "" {
		r.ID = AutoID(*r)
	}
}

func weekdayCN(w string) string {
	switch w {
	case WeekdayEvery, "":
		return "天"
	case "mon":
		return "一"
	case "tue":
		return "二"
	case "wed":
		return "三"
	case "thu":
		return "四"
	case "fri":
		return "五"
	case "sat":
		return "六"
	case "sun":
		return "日"
	default:
		return w
	}
}

func weekdayListCN(weekday string) string {
	days := parseWeekdays(weekday)
	if days == nil {
		return "天"
	}
	if len(days) == 1 {
		return weekdayCN(days[0])
	}
	parts := make([]string, 0, len(days))
	for _, d := range days {
		parts = append(parts, weekdayCN(d))
	}
	return strings.Join(parts, "、")
}
