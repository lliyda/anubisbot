package schedules

import (
	"testing"
	"time"

	"anubisbot/internal/timesync"
)

func cstTime(y int, m time.Month, d, h, min, sec int) time.Time {
	return time.Date(y, m, d, h, min, sec, 0, timesync.CST())
}

func TestNextFireAt_EveryHour(t *testing.T) {
	r := Rule{Period: PeriodDaily, HourMode: HourEveryHour, Minute: 30}
	now := cstTime(2026, 11, 8, 12, 15, 0)
	got := NextFireAt(r, now, 0)
	want := cstTime(2026, 11, 8, 12, 30, 0)
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	now2 := cstTime(2026, 11, 8, 12, 30, 0)
	got2 := NextFireAt(r, now2, 0)
	want2 := cstTime(2026, 11, 8, 13, 30, 0)
	if !got2.Equal(want2) {
		t.Fatalf("at exact: got %v want %v", got2, want2)
	}
}

func TestNextFireAt_AtHourDaily(t *testing.T) {
	r := Rule{Period: PeriodDaily, HourMode: HourAtHour, Hour: 11, Minute: 59}
	now := cstTime(2026, 11, 8, 12, 0, 0)
	got := NextFireAt(r, now, 0)
	want := cstTime(2026, 11, 9, 11, 59, 0)
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	now2 := cstTime(2026, 11, 8, 10, 0, 0)
	got2 := NextFireAt(r, now2, 0)
	want2 := cstTime(2026, 11, 8, 11, 59, 0)
	if !got2.Equal(want2) {
		t.Fatalf("same day: got %v want %v", got2, want2)
	}
}

func TestNextFireAt_WeeklyFriday(t *testing.T) {
	// 2026-11-08 is Sunday
	r := Rule{Period: PeriodWeekly, Weekday: "fri", HourMode: HourAtHour, Hour: 15, Minute: 30}
	now := cstTime(2026, 11, 8, 12, 0, 0)
	got := NextFireAt(r, now, 0)
	want := cstTime(2026, 11, 13, 15, 30, 0) // Friday
	if !got.Equal(want) {
		t.Fatalf("got %v (%s) want %v", got, got.Weekday(), want)
	}
}

func TestNextFireAt_WeeklyMulti(t *testing.T) {
	// Sunday → next is Monday 09:00 (mon,fri)
	r := Rule{Period: PeriodWeekly, Weekday: "fri,mon", HourMode: HourAtHour, Hour: 9, Minute: 0}
	now := cstTime(2026, 11, 8, 12, 0, 0)
	got := NextFireAt(r, now, 0)
	want := cstTime(2026, 11, 9, 9, 0, 0) // Monday
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestNextFireAt_WeeklyEveryHourMonday(t *testing.T) {
	r := Rule{Period: PeriodWeekly, Weekday: "mon", HourMode: HourEveryHour, Minute: 0}
	// Sunday 12:30 → next Mon 13:00? Wait: at Sun 12:30, next :00 is Sun 13:00, not Monday.
	// Keep advancing hours until Monday.
	now := cstTime(2026, 11, 8, 12, 30, 0) // Sunday
	got := NextFireAt(r, now, 0)
	if got.Weekday() != time.Monday {
		t.Fatalf("weekday=%s want Monday, got %v", got.Weekday(), got)
	}
	if got.Minute() != 0 || got.Second() != 0 {
		t.Fatalf("want top of hour, got %v", got)
	}
}

func TestComputeFireAt_BuiltinAndCustom(t *testing.T) {
	now := cstTime(2026, 11, 8, 12, 15, 0)
	custom := map[string]Rule{
		"every_30": {ID: "every_30", Period: PeriodDaily, HourMode: HourEveryHour, Minute: 30},
	}
	got := ComputeFireAt("every_30", now, 0, nil, custom)
	want := cstTime(2026, 11, 8, 12, 30, 0)
	if !got.Equal(want) {
		t.Fatalf("custom: got %v want %v", got, want)
	}
	h := ComputeFireAt(IDHourly, now, 0, nil, custom)
	if !h.Equal(timesync.NextHourCST(now)) {
		t.Fatalf("hourly mismatch %v", h)
	}
}
