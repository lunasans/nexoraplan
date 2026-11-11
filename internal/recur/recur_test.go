package recur

import (
	"testing"
	"time"
)

var berlin = mustLoc("Europe/Berlin")

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

func d(y int, m time.Month, day, h, mi int) time.Time {
	return time.Date(y, m, day, h, mi, 0, 0, berlin)
}

func dates(ts []time.Time) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Format("2006-01-02 15:04")
	}
	return out
}

func check(t *testing.T, rule string, start time.Time, from, to time.Time, want ...string) {
	t.Helper()
	r, err := Parse(rule, berlin)
	if err != nil {
		t.Fatalf("Parse(%q): %v", rule, err)
	}
	got := dates(r.Between(start, from, to))
	if len(got) != len(want) {
		t.Fatalf("%s:\n got  %v\n want %v", rule, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s:\n got  %v\n want %v", rule, got, want)
		}
	}
}

func TestDaily(t *testing.T) {
	check(t, "FREQ=DAILY;COUNT=3", d(2026, 11, 9, 8, 0), d(2026, 1, 1, 0, 0), d(2027, 1, 1, 0, 0),
		"2026-11-09 08:00", "2026-11-10 08:00", "2026-11-11 08:00")
	check(t, "FREQ=DAILY;INTERVAL=2", d(2026, 11, 9, 8, 0), d(2026, 11, 12, 0, 0), d(2026, 11, 16, 0, 0),
		"2026-11-13 08:00", "2026-11-15 08:00")
}

// Wanduhrzeit bleibt über die Zeitumstellung (29.03.2026) erhalten.
func TestDaylightSaving(t *testing.T) {
	check(t, "FREQ=WEEKLY", d(2026, 3, 23, 9, 0), d(2026, 3, 20, 0, 0), d(2026, 4, 3, 0, 0),
		"2026-03-23 09:00", "2026-03-30 09:00")
	r, _ := Parse("FREQ=WEEKLY", berlin)
	got := r.Between(d(2026, 3, 23, 9, 0), d(2026, 3, 29, 0, 0), d(2026, 4, 3, 0, 0))
	if got[0].UTC().Hour() != 7 {
		t.Fatalf("nach der Umstellung 9:00 MESZ = 7:00 UTC, bekommen %v", got[0].UTC())
	}
}

func TestWeeklyByDay(t *testing.T) {
	// Mo, Mi, Fr ab Mittwoch – der Montag davor zählt nicht
	check(t, "FREQ=WEEKLY;BYDAY=MO,WE,FR;COUNT=5", d(2026, 11, 11, 18, 30), d(2026, 1, 1, 0, 0), d(2027, 1, 1, 0, 0),
		"2026-11-11 18:30", "2026-11-13 18:30", "2026-11-16 18:30", "2026-11-18 18:30", "2026-11-20 18:30")
	// alle zwei Wochen dienstags
	check(t, "FREQ=WEEKLY;INTERVAL=2;BYDAY=TU", d(2026, 11, 10, 10, 0), d(2026, 11, 1, 0, 0), d(2026, 12, 15, 0, 0),
		"2026-11-10 10:00", "2026-11-24 10:00", "2026-12-08 10:00")
}

func TestUntil(t *testing.T) {
	check(t, "FREQ=DAILY;UNTIL=20261112T070000Z", d(2026, 11, 10, 8, 0), d(2026, 1, 1, 0, 0), d(2027, 1, 1, 0, 0),
		"2026-11-10 08:00", "2026-11-11 08:00", "2026-11-12 08:00")
	check(t, "FREQ=DAILY;UNTIL=20261111", time.Date(2026, 11, 10, 0, 0, 0, 0, berlin), d(2026, 1, 1, 0, 0), d(2027, 1, 1, 0, 0),
		"2026-11-10 00:00", "2026-11-11 00:00")
}

func TestMonthly(t *testing.T) {
	// 31. – fällt in Monaten ohne 31. aus
	check(t, "FREQ=MONTHLY;COUNT=4", d(2026, 10, 31, 12, 0), d(2026, 1, 1, 0, 0), d(2028, 1, 1, 0, 0),
		"2026-10-31 12:00", "2026-12-31 12:00", "2027-01-31 12:00", "2027-03-31 12:00")
	// zweiter Montag
	check(t, "FREQ=MONTHLY;BYDAY=2MO", d(2026, 11, 9, 19, 0), d(2026, 11, 1, 0, 0), d(2027, 2, 1, 0, 0),
		"2026-11-09 19:00", "2026-12-14 19:00", "2027-01-11 19:00")
	// letzter Freitag
	check(t, "FREQ=MONTHLY;BYDAY=-1FR", d(2026, 11, 27, 15, 0), d(2026, 11, 1, 0, 0), d(2027, 1, 1, 0, 0),
		"2026-11-27 15:00", "2026-12-25 15:00")
	// letzter Werktag (SOGo/Outlook: BYDAY=MO..FR;BYSETPOS=-1)
	check(t, "FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1", d(2026, 11, 30, 9, 0), d(2026, 11, 1, 0, 0), d(2027, 2, 1, 0, 0),
		"2026-11-30 09:00", "2026-12-31 09:00", "2027-01-29 09:00")
	// letzter Tag des Monats
	check(t, "FREQ=MONTHLY;BYMONTHDAY=-1", d(2027, 1, 31, 9, 0), d(2027, 1, 1, 0, 0), d(2027, 4, 1, 0, 0),
		"2027-01-31 09:00", "2027-02-28 09:00", "2027-03-31 09:00")
}

func TestYearly(t *testing.T) {
	// Geburtstag am 29.02. nur in Schaltjahren
	check(t, "FREQ=YEARLY", time.Date(2024, 2, 29, 0, 0, 0, 0, berlin), d(2024, 1, 1, 0, 0), d(2029, 1, 1, 0, 0),
		"2024-02-29 00:00", "2028-02-29 00:00")
	check(t, "FREQ=YEARLY;BYMONTH=3,9", d(2026, 3, 15, 8, 0), d(2026, 1, 1, 0, 0), d(2028, 1, 1, 0, 0),
		"2026-03-15 08:00", "2026-09-15 08:00", "2027-03-15 08:00", "2027-09-15 08:00")
}

// Abfrage weit nach dem Beginn: COUNT zählt trotzdem ab dem ersten Termin.
func TestCountBeforeWindow(t *testing.T) {
	check(t, "FREQ=WEEKLY;COUNT=10", d(2026, 1, 5, 9, 0), d(2026, 3, 1, 0, 0), d(2026, 12, 1, 0, 0),
		"2026-03-02 09:00", "2026-03-09 09:00")
}

func TestRoundTrip(t *testing.T) {
	for _, s := range []string{
		"FREQ=WEEKLY;INTERVAL=2;COUNT=5;BYDAY=MO,FR",
		"FREQ=MONTHLY;BYDAY=-1FR",
		"FREQ=DAILY;UNTIL=20261231T225959Z",
		"FREQ=YEARLY;UNTIL=20301231;BYMONTH=2",
	} {
		r, err := Parse(s, berlin)
		if err != nil {
			t.Fatal(err)
		}
		if r.String() != s {
			t.Errorf("String() = %q, erwartet %q", r.String(), s)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, s := range []string{"", "INTERVAL=2", "FREQ=SECONDLY", "FREQ=DAILY;INTERVAL=0", "FREQ=WEEKLY;BYDAY=XX", "FREQ=MONTHLY;BYMONTHDAY=40"} {
		if _, err := Parse(s, berlin); err == nil {
			t.Errorf("Parse(%q) ohne Fehler", s)
		}
	}
}

func TestLast(t *testing.T) {
	r, _ := Parse("FREQ=WEEKLY;COUNT=3", berlin)
	last, ok := r.Last(d(2026, 11, 2, 9, 0))
	if !ok || !last.Equal(d(2026, 11, 16, 9, 0)) {
		t.Fatalf("Last = %v %v", last, ok)
	}
	r, _ = Parse("FREQ=WEEKLY", berlin)
	if _, ok := r.Last(d(2026, 11, 2, 9, 0)); ok {
		t.Fatal("unbegrenzte Serie hat kein Ende")
	}
}
