package server

import (
	"testing"
	"time"

	"nexoraplan/internal/db"
)

func ptrStr(s string) *string { return &s }
func ptrI64(n int64) *int64   { return &n }
func ptrU64(n uint64) *uint64 { return &n }
func day(s string) time.Time  { t, _ := time.Parse(db.DateLayout, s); return t }

// Pocket „Jährlich“: 50 € am 1., zahlt Hausrat vierteljährlich und Haftpflicht jährlich.
func jaehrlich(balance int64, on string) (db.Pocket, []db.Recurring) {
	p := db.Pocket{ID: 7, Name: "Jährlich", MonthlyCents: 5000, DepositDay: 1, BalanceCents: ptrI64(balance), BalanceOn: ptrStr(on)}
	recs := []db.Recurring{
		{Title: "Hausrat", Kind: "expense", AmountCents: 7500, Day: 1, IntervalMonths: 3, StartsOn: "2026-12-01", PocketID: ptrU64(7)},
		{Title: "Haftpflicht", Kind: "expense", AmountCents: 24000, Day: 15, IntervalMonths: 12, StartsOn: "2027-01-15", PocketID: ptrU64(7)},
		{Title: "Anderes Pocket", Kind: "expense", AmountCents: 99900, Day: 1, IntervalMonths: 1, StartsOn: "2026-01-01", PocketID: ptrU64(8)},
		{Title: "Gehalt", Kind: "income", AmountCents: 300000, Day: 1, IntervalMonths: 1, StartsOn: "2026-01-01", PocketID: ptrU64(7)},
	}
	return p, recs
}

func TestForecastFindsTheFirstShortfall(t *testing.T) {
	p, recs := jaehrlich(3000, "2026-11-11")
	f := forecastPocket(p, recs, nil, nil, day("2026-11-11"))
	if f == nil || f.TodayCents != 3000 {
		t.Fatalf("Vorschau: %+v", f)
	}
	// 01.12. +50 −75 → 5 €, 01.01. +50 → 55 €, 15.01. −240 → −185 €
	if f.ShortOn == nil || *f.ShortOn != "2027-01-15" || f.ShortCents != 18500 || f.ShortTitle != "Haftpflicht" {
		t.Fatalf("Engpass: %v %d %q", f.ShortOn, f.ShortCents, f.ShortTitle)
	}
	if f.Events[0].Day != "2026-12-01" || f.Events[0].Kind != "deposit" || f.Events[1].Title != "Hausrat" || f.Events[1].BalanceCents != 500 {
		t.Fatalf("Reihenfolge am 01.12.: %+v", f.Events[:2])
	}
	for _, e := range f.Events {
		if e.Title == "Anderes Pocket" || e.Title == "Gehalt" {
			t.Fatalf("fremder Posten in der Vorschau: %+v", e)
		}
	}
	if f.Until != "2027-11-11" || f.Events[len(f.Events)-1].Day > "2027-11-11" {
		t.Fatalf("Zeitraum bis %s, letzter Posten %s", f.Until, f.Events[len(f.Events)-1].Day)
	}
}

func TestForecastWithEnoughBalance(t *testing.T) {
	p, recs := jaehrlich(25000, "2026-11-11")
	f := forecastPocket(p, recs, nil, nil, day("2026-11-11"))
	if f.ShortOn != nil {
		t.Fatalf("kein Engpass erwartet, bekommen %s", *f.ShortOn)
	}
	// niedrigster Stand nach der Haftpflicht am 15.01.: 250 + 50 − 75 + 50 − 240 = 35 €
	if f.LowestCents != 3500 || f.LowestOn != "2027-01-15" {
		t.Fatalf("niedrigster Stand %d am %s", f.LowestCents, f.LowestOn)
	}
}

// Stand von früher: Eingänge, Abbuchungen und Ausgaben seither ergeben den Stand heute.
func TestForecastFromAnOlderBalance(t *testing.T) {
	p, recs := jaehrlich(10000, "2026-11-20")
	cats := map[uint64]bool{3: true}
	txs := []db.Transaction{
		{Kind: "expense", AmountCents: 1500, Day: "2026-12-10", CategoryID: ptrU64(3), Note: "Geschenk"},
		{Kind: "expense", AmountCents: 999, Day: "2026-12-11", CategoryID: ptrU64(4)},                          // andere Kategorie
		{Kind: "expense", AmountCents: 7500, Day: "2026-12-01", CategoryID: ptrU64(3), RecurringID: ptrU64(1)}, // schon als Vertrag gezählt
	}
	f := forecastPocket(p, recs, cats, txs, day("2026-12-15"))
	// 100 + 50 − 75 (Hausrat 01.12.) − 15 (Geschenk) = 60 €
	if f.TodayCents != 6000 {
		t.Fatalf("heute %d", f.TodayCents)
	}
	if f.Events[0].Day != "2027-01-01" {
		t.Fatalf("Liste beginnt nach heute, bekommen %s", f.Events[0].Day)
	}
}

func TestForecastNeedsABalance(t *testing.T) {
	p, recs := jaehrlich(0, "2026-11-11")
	p.BalanceCents = nil
	if forecastPocket(p, recs, nil, nil, day("2026-11-11")) != nil {
		t.Fatal("ohne Stand keine Vorschau")
	}
	p, _ = jaehrlich(0, "2026-12-01") // Stand in der Zukunft
	if forecastPocket(p, recs, nil, nil, day("2026-11-11")) != nil {
		t.Fatal("Stand in der Zukunft ergibt keine Vorschau")
	}
}
