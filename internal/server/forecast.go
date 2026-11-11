package server

import (
	"context"
	"net/http"
	"slices"
	"time"

	"nexoraplan/internal/db"
)

// Vorschau für ein Pocket: Reicht der Stand an jedem Fälligkeitstag?
//
// Ausgangspunkt ist der eingetragene Stand (BalanceCents am Tag BalanceOn).
// Danach kommen dazu: der monatliche Betrag am DepositDay; ab: jede Abbuchung
// der zugeordneten Abos/Verträge an ihrem Fälligkeitstag und – bis heute – die
// gebuchten Ausgaben der Kategorien des Pockets. Künftige Einkäufe kennt die
// App nicht; die Vorschau zeigt, was die Verträge brauchen.

const forecastMonths = 12

type forecastEvent struct {
	Day          string `json:"day"`
	Title        string `json:"title"`
	Kind         string `json:"kind"`         // deposit | contract | expense
	AmountCents  int64  `json:"amount_cents"` // + Eingang, − Abbuchung
	BalanceCents int64  `json:"balance_cents"`
}

type pocketForecast struct {
	StartCents  int64   `json:"start_cents"`
	StartOn     string  `json:"start_on"`
	TodayCents  int64   `json:"today_cents"` // voraussichtlicher Stand heute
	Until       string  `json:"until"`
	LowestCents int64   `json:"lowest_cents"` // niedrigster Stand ab heute
	LowestOn    string  `json:"lowest_on"`
	ShortOn     *string `json:"short_on"`    // erster Tag ab heute mit Stand unter 0
	ShortCents  int64   `json:"short_cents"` // so viel fehlt an diesem Tag
	ShortTitle  string  `json:"short_title"` // Abbuchung, die nicht gedeckt ist
	// Events: alles ab morgen bis Until (für die Liste in der Oberfläche)
	Events []forecastEvent `json:"events,omitempty"`
}

// forecastPocket rechnet die Vorschau; nil, wenn kein Stand eingetragen ist.
// cats: IDs der Kategorien des Pockets; txs: Buchungen seit dem Stand.
func forecastPocket(p db.Pocket, recs []db.Recurring, cats map[uint64]bool, txs []db.Transaction, today time.Time) *pocketForecast {
	if p.BalanceCents == nil || p.BalanceOn == nil {
		return nil
	}
	start, err := time.Parse(db.DateLayout, *p.BalanceOn)
	if err != nil || start.After(today) {
		return nil
	}
	until := today.AddDate(0, forecastMonths, 0)
	var events []forecastEvent
	add := func(day time.Time, title, kind string, amount int64) {
		events = append(events, forecastEvent{Day: day.Format(db.DateLayout), Title: title, Kind: kind, AmountCents: amount})
	}

	var income *db.Recurring
	if p.IncomeID != nil {
		if i := slices.IndexFunc(recs, func(x db.Recurring) bool { return x.ID == *p.IncomeID }); i >= 0 {
			income = &recs[i]
		}
	}
	if p.MonthlyCents > 0 && income != nil {
		// Einkommensverteiler: Auffüllung an jedem Gehaltstag
		for _, day := range db.DueDays(*income, start, until) {
			add(day, "Auffüllung aus "+income.Title, "deposit", p.MonthlyCents)
		}
	} else if p.MonthlyCents > 0 {
		depositDay := min(max(p.DepositDay, 1), 31)
		for m := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC); !m.After(until); m = m.AddDate(0, 1, 0) {
			day := time.Date(m.Year(), m.Month(), min(depositDay, m.AddDate(0, 1, -1).Day()), 0, 0, 0, 0, time.UTC)
			if day.After(start) && !day.After(until) {
				add(day, "Monatlicher Betrag", "deposit", p.MonthlyCents)
			}
		}
	}
	for _, x := range recs {
		if x.Kind != "expense" || x.PocketID == nil || *x.PocketID != p.ID {
			continue
		}
		for _, day := range db.DueDays(x, start, until) {
			add(day, x.Title, "contract", -x.AmountCents)
		}
	}
	for _, t := range txs {
		day, err := time.Parse(db.DateLayout, t.Day)
		if err != nil || !day.After(start) || day.After(today) || t.Kind != "expense" || t.RecurringID != nil ||
			t.CategoryID == nil || !cats[*t.CategoryID] {
			continue
		}
		title := t.Note
		if title == "" {
			title = "Ausgabe"
		}
		add(day, title, "expense", -t.AmountCents)
	}

	// Nach Tag; am selben Tag zuerst der Eingang
	slices.SortStableFunc(events, func(a, b forecastEvent) int {
		if a.Day != b.Day {
			if a.Day < b.Day {
				return -1
			}
			return 1
		}
		switch {
		case a.AmountCents > 0 && b.AmountCents <= 0:
			return -1
		case b.AmountCents > 0 && a.AmountCents <= 0:
			return 1
		}
		return 0
	})

	f := &pocketForecast{StartCents: *p.BalanceCents, StartOn: *p.BalanceOn, Until: until.Format(db.DateLayout)}
	todayKey := today.Format(db.DateLayout)
	balance := *p.BalanceCents
	i := 0
	for ; i < len(events) && events[i].Day <= todayKey; i++ {
		balance += events[i].AmountCents
		events[i].BalanceCents = balance
	}
	f.TodayCents, f.LowestCents, f.LowestOn = balance, balance, todayKey
	f.Events = []forecastEvent{}
	for ; i < len(events); i++ {
		balance += events[i].AmountCents
		events[i].BalanceCents = balance
		f.Events = append(f.Events, events[i])
		if balance < f.LowestCents {
			f.LowestCents, f.LowestOn = balance, events[i].Day
		}
		if balance < 0 && f.ShortOn == nil {
			day := events[i].Day
			f.ShortOn, f.ShortCents, f.ShortTitle = &day, -balance, events[i].Title
		}
	}
	if f.TodayCents < 0 && f.ShortOn == nil {
		f.ShortOn, f.ShortCents, f.ShortTitle = &todayKey, -f.TodayCents, "heutiger Stand"
	}
	return f
}

// forecastInputs lädt, was die Vorschau braucht.
func (s *Server) forecastInputs(ctx context.Context) ([]db.Recurring, []db.Category, []db.Transaction, error) {
	recs, err := s.repo.RecurringList(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	cats, err := s.repo.Categories(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	// Buchungen höchstens ein Jahr zurück – ältere Stände sind ohnehin überholt
	from := s.today().AddDate(-1, 0, 0)
	txs, err := s.repo.Transactions(ctx, from.Format(db.DateLayout), s.today().AddDate(0, 0, 1).Format(db.DateLayout))
	return recs, cats, txs, err
}

func pocketCategories(p db.Pocket, cats []db.Category) map[uint64]bool {
	out := map[uint64]bool{}
	for _, c := range cats {
		if c.PocketID != nil && *c.PocketID == p.ID {
			out[c.ID] = true
		}
	}
	return out
}

// GET /api/pockets/{id}/forecast – Vorschau mit allen Ein- und Ausgängen.
func (s *Server) handlePocketForecast(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	pockets, err := s.repo.Pockets(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	i := slices.IndexFunc(pockets, func(p db.Pocket) bool { return p.ID == id })
	if i < 0 {
		writeError(w, http.StatusNotFound, "Pocket nicht gefunden")
		return
	}
	recs, cats, txs, err := s.forecastInputs(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	f := forecastPocket(pockets[i], recs, pocketCategories(pockets[i], cats), txs, s.today())
	if f == nil {
		writeError(w, http.StatusConflict, "Für dieses Pocket ist kein Stand eingetragen")
		return
	}
	writeJSON(w, http.StatusOK, f)
}
