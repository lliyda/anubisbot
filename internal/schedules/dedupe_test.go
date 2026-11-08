package schedules

import "testing"

func TestFingerprintAndAutoID(t *testing.T) {
	hourly := Rule{Period: PeriodDaily, HourMode: HourEveryHour, Minute: 0}
	if Fingerprint(hourly) != Fingerprint(Builtins()[0]) {
		t.Fatalf("hourly fingerprint mismatch: %s vs %s", Fingerprint(hourly), Fingerprint(Builtins()[0]))
	}
	id := AutoID(Rule{Period: PeriodDaily, HourMode: HourEveryHour, Minute: 30})
	if id != "d_eh_m30" {
		t.Fatalf("AutoID=%q", id)
	}
	lab := AutoLabel(Rule{Period: PeriodWeekly, Weekday: "fri", HourMode: HourAtHour, Hour: 15, Minute: 30})
	if lab != "每周五 15:30" {
		t.Fatalf("AutoLabel=%q", lab)
	}
	multi := AutoLabel(Rule{Period: PeriodWeekly, Weekday: "fri,mon", HourMode: HourAtHour, Hour: 9, Minute: 0})
	if multi != "每周一、五 09:00" {
		t.Fatalf("multi AutoLabel=%q", multi)
	}
	id2 := AutoID(Rule{Period: PeriodWeekly, Weekday: "fri,mon", HourMode: HourEveryHour, Minute: 0})
	if id2 != "w_mf_eh_m0" {
		t.Fatalf("multi AutoID=%q", id2)
	}
}

func TestCreateRejectsDuplicate(t *testing.T) {
	dir := t.TempDir()
	st, err := Load(dir + "/schedules.json")
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.Create(Rule{Period: PeriodDaily, HourMode: HourEveryHour, Minute: 0})
	if err == nil || err.Error() != "duplicate schedule" {
		t.Fatalf("want duplicate vs builtin hourly, got %v", err)
	}
	r, err := st.Create(Rule{Period: PeriodDaily, HourMode: HourEveryHour, Minute: 30})
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != "d_eh_m30" || r.Label == "" {
		t.Fatalf("created=%+v", r)
	}
	_, err = st.Create(Rule{Period: PeriodDaily, HourMode: HourEveryHour, Minute: 30})
	if err == nil || err.Error() != "duplicate schedule" {
		t.Fatalf("want duplicate custom, got %v", err)
	}
}
