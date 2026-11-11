package db

import (
	"testing"
	"time"
)

func days(ts []time.Time) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Format(DateLayout)
	}
	return out
}

func TestDueDaysIntervals(t *testing.T) {
	until := time.Date(2028, 12, 31, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		x    Recurring
		want []string
	}{
		{Recurring{Day: 15, IntervalMonths: 12, StartsOn: "2026-03-15"}, []string{"2026-03-15", "2027-03-15", "2028-03-15"}},
		// vierteljährlich am 31.: im April und Oktober am Monatsletzten
		{Recurring{Day: 31, IntervalMonths: 3, StartsOn: "2028-01-31"}, []string{"2028-01-31", "2028-04-30", "2028-07-31", "2028-10-31"}},
		{Recurring{Day: 1, IntervalMonths: 6, StartsOn: "2028-02-01", EndsOn: ptr("2028-12-31")}, []string{"2028-02-01", "2028-08-01"}},
		// Jahresbeitrag am 29.02.: in Jahren ohne Schalttag am 28.
		{Recurring{Day: 29, IntervalMonths: 12, StartsOn: "2028-02-29"}, []string{"2028-02-29"}},
	}
	for _, c := range cases {
		got := days(DueDays(c.x, time.Time{}, until))
		if len(got) != len(c.want) {
			t.Errorf("%+v: %v, erwartet %v", c.x, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%+v: %v, erwartet %v", c.x, got, c.want)
				break
			}
		}
	}
	leap := DueDays(Recurring{Day: 29, IntervalMonths: 12, StartsOn: "2024-02-29"}, time.Time{}, time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC))
	if d := days(leap); len(d) != 2 || d[1] != "2025-02-28" {
		t.Errorf("29.02. ohne Schaltjahr: %v", d)
	}
}

func TestMonthlyShareAndActive(t *testing.T) {
	x := Recurring{AmountCents: 24000, IntervalMonths: 12, StartsOn: "2026-03-15", EndsOn: ptr("2027-02-28")}
	if x.MonthlyShare() != 2000 {
		t.Errorf("Anteil %d", x.MonthlyShare())
	}
	if (Recurring{AmountCents: 1000, IntervalMonths: 3}).MonthlyShare() != 333 {
		t.Error("1000/3 soll auf 333 runden")
	}
	for month, want := range map[string]bool{"2026-02-01": false, "2026-03-01": true, "2027-02-01": true, "2027-03-01": false} {
		m, _ := time.Parse(DateLayout, month)
		if x.ActiveIn(m) != want {
			t.Errorf("ActiveIn(%s) = %v", month, !want)
		}
	}
}

func ptr(s string) *string { return &s }

func TestTopupDays(t *testing.T) {
	first := time.Date(2026, 11, 11, 15, 0, 0, 0, time.UTC) // Pocket am 11.11. angelegt
	got := days(TopupDays(1, first, time.Time{}, time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC)))
	// Die Auffüllung vom 01.11. zählt mit (Monat des Anlegens)
	if len(got) != 3 || got[0] != "2026-11-01" || got[2] != "2027-01-01" {
		t.Fatalf("Tag 1: %v", got)
	}
	got = days(TopupDays(31, first, time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC), time.Date(2027, 2, 28, 0, 0, 0, 0, time.UTC)))
	if len(got) != 3 || got[0] != "2026-12-31" || got[2] != "2027-02-28" {
		t.Fatalf("Tag 31 nach dem 30.11.: %v", got)
	}
}
