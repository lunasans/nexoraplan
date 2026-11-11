package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"nexoraplan/internal/db"
)

const maxCents = 100_000_000_00 // 100 Mio. €

var colorRe = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// paymentMethods: Zahlungsarten von Abos und Verträgen ("" = nicht angegeben).
var paymentMethods = map[string]bool{
	"": true, "sepa": true, "standing_order": true, "transfer": true, "card": true, "paypal": true, "cash": true, "other": true,
}

// cardNumberRe erkennt 12 und mehr Ziffern am Stück (auch mit Leerzeichen/Bindestrichen)
// oder eine IBAN – beides soll nicht im Klartext in der Notiz landen.
var cardNumberRe = regexp.MustCompile(`(?:\d[ -]?){12,}|\b[A-Z]{2}\d{2}(?:[ ]?[A-Z0-9]{4}){3,}`)

// monthParam liest ?month=2026-11; ohne Angabe der aktuelle Monat.
func (s *Server) monthParam(r *http.Request) (time.Time, bool) {
	raw := r.URL.Query().Get("month")
	if raw == "" {
		t := s.today()
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC), true
	}
	t, err := time.Parse("2006-01", raw)
	return t, err == nil && t.Year() >= 1970 && t.Year() <= 2200
}

type categorySummary struct {
	db.Category
	SpentCents  int64 `json:"spent_cents"`  // Ausgaben ohne Abos/Verträge – zählen gegen das Kategoriebudget
	FixedCents  int64 `json:"fixed_cents"`  // davon aus Abos/Verträgen gebucht
	IncomeCents int64 `json:"income_cents"` // Einnahmen
}

// budgetPlan: Was im Monat für Einkäufe, Freizeit usw. frei ist.
//
// Mit Gesamtbudget: Gesamtbudget minus Abos & Verträge, anteilig umgerechnet
// (240 € jährlich = 20 € je Monat). Ohne Gesamtbudget: Summe der Kategoriebudgets.
type budgetPlan struct {
	TotalCents     int64 `json:"total_cents"`     // Gesamtbudget pro Monat, 0 = keins
	ContractsCents int64 `json:"contracts_cents"` // Abos & Verträge (Ausgaben), anteilig
	FreeCents      int64 `json:"free_cents"`      // frei planbar
	AllocatedCents int64 `json:"allocated_cents"` // davon auf Kategorien verteilt
	FromPockets    bool  `json:"from_pockets"`    // Gesamtbudget = Summe der Pockets
}

type contractShare struct {
	db.Recurring
	MonthlyCents int64 `json:"monthly_cents"`
}

// budgetTotal: Gibt es Pockets, ist ihre Summe das Gesamtbudget (fromPockets),
// sonst der von Hand eingetragene Betrag (0 = keiner).
func (s *Server) budgetTotal(ctx context.Context) (total int64, pockets []db.Pocket, err error) {
	if pockets, err = s.repo.Pockets(ctx); err != nil {
		return 0, nil, err
	}
	if len(pockets) > 0 {
		for _, p := range pockets {
			total += p.MonthlyCents
		}
		return total, pockets, nil
	}
	raw, err := s.repo.Setting(ctx, db.SettingBudgetTotal)
	if err != nil || raw == "" {
		return 0, pockets, err
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, pockets, nil
	}
	return n, pockets, nil
}

// pocketSummary: ein Pocket im Monat. Drauf geht MonthlyCents; davon brauchen
// die Abos/Verträge anteilig ContractsCents. Reicht das nicht, ist ShortCents > 0.
type pocketSummary struct {
	db.Pocket
	ContractsCents int64 `json:"contracts_cents"` // Abos & Verträge anteilig
	Contracts      int   `json:"contracts"`
	FreeCents      int64 `json:"free_cents"`      // drauf − Verträge
	SpentCents     int64 `json:"spent_cents"`     // Ausgaben in den Kategorien des Pockets (ohne Verträge)
	RemainingCents int64 `json:"remaining_cents"` // frei − Ausgaben
	ShortCents     int64 `json:"short_cents"`     // so viel fehlt monatlich für die Verträge
	AllocatedCents int64 `json:"allocated_cents"` // Kategoriebudgets im Pocket
	// Forecast: Vorschau (ohne Einzelposten), nur mit eingetragenem Stand
	Forecast *pocketForecast `json:"forecast"`
}

// pocketSummaries teilt Verträge und Ausgaben auf die Pockets auf. Der letzte
// Eintrag (ID 0) sammelt, was keinem Pocket zugeordnet ist – nur wenn es etwas gibt.
func pocketSummaries(pockets []db.Pocket, contracts []contractShare, cats []categorySummary) []pocketSummary {
	out := make([]pocketSummary, 0, len(pockets)+1)
	index := map[uint64]int{}
	for _, p := range pockets {
		index[p.ID] = len(out)
		out = append(out, pocketSummary{Pocket: p})
	}
	none := pocketSummary{Pocket: db.Pocket{Name: "Ohne Pocket", Color: "#a8a5a0"}}
	at := func(id *uint64) *pocketSummary {
		if id != nil {
			if i, ok := index[*id]; ok {
				return &out[i]
			}
		}
		return &none
	}
	for _, x := range contracts {
		p := at(x.PocketID)
		p.ContractsCents += x.MonthlyCents
		p.Contracts++
	}
	for _, c := range cats {
		p := at(c.PocketID)
		p.SpentCents += c.SpentCents
		p.AllocatedCents += c.BudgetCents
	}
	if none.Contracts > 0 || none.SpentCents > 0 {
		out = append(out, none)
	}
	for i := range out {
		p := &out[i]
		p.FreeCents = p.MonthlyCents - p.ContractsCents
		p.RemainingCents = p.FreeCents - p.SpentCents
		if p.ID != 0 && p.ContractsCents > p.MonthlyCents {
			p.ShortCents = p.ContractsCents - p.MonthlyCents
		}
	}
	return out
}

// planFor rechnet den Plan für den Monat month (erster Tag, UTC).
func planFor(total int64, recs []db.Recurring, cats []db.Category, month time.Time) (budgetPlan, []contractShare) {
	p := budgetPlan{TotalCents: total}
	contracts := []contractShare{}
	for _, x := range recs {
		if x.Kind != "expense" || !x.ActiveIn(month) {
			continue
		}
		share := x.MonthlyShare()
		p.ContractsCents += share
		contracts = append(contracts, contractShare{Recurring: x, MonthlyCents: share})
	}
	for _, c := range cats {
		p.AllocatedCents += c.BudgetCents
	}
	if total > 0 {
		p.FreeCents = total - p.ContractsCents
	} else {
		p.FreeCents = p.AllocatedCents
	}
	return p, contracts
}

// countsAgainstBudget: Abos/Verträge sind im Plan schon anteilig abgezogen und
// zählen nicht noch einmal. Ohne Gesamtbudget zählen nur Kategorien mit Budget.
func countsAgainstBudget(t db.Transaction, total int64, budgeted map[uint64]bool) bool {
	if t.Kind != "expense" || t.RecurringID != nil {
		return false
	}
	return total > 0 || (t.CategoryID != nil && budgeted[*t.CategoryID])
}

// PUT /api/budget/total {"total_cents"} – Gesamtbudget pro Monat, 0 entfernt es.
func (s *Server) handleBudgetTotal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TotalCents int64 `json:"total_cents"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if pockets, err := s.repo.Pockets(r.Context()); err != nil {
		internalError(w, r, err)
		return
	} else if len(pockets) > 0 {
		writeError(w, http.StatusConflict, "Das Gesamtbudget ist die Summe deiner Pockets – ändere dort die monatlichen Beträge")
		return
	}
	if req.TotalCents < 0 || req.TotalCents > maxCents {
		writeError(w, http.StatusBadRequest, "Betrag ungültig")
		return
	}
	value := ""
	if req.TotalCents > 0 {
		value = strconv.FormatInt(req.TotalCents, 10)
	}
	if err := s.repo.SetSettings(r.Context(), map[string]string{db.SettingBudgetTotal: value}); err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"total_cents": req.TotalCents})
}

// budgetView ist die Monatsübersicht – für /api/budget und die Benachrichtigungen.
type budgetView struct {
	Month          string            `json:"month"`
	Today          string            `json:"today"`
	IncomeCents    int64             `json:"income_cents"`
	ExpenseCents   int64             `json:"expense_cents"`
	BalanceCents   int64             `json:"balance_cents"`
	BudgetCents    int64             `json:"budget_cents"`
	Uncategorized  int64             `json:"uncategorized_cents"`
	Plan           budgetPlan        `json:"plan"`
	VariableCents  int64             `json:"variable_cents"`  // Ausgaben, die gegen den freien Teil zählen
	RemainingCents int64             `json:"remaining_cents"` // frei − Ausgaben
	Contracts      []contractShare   `json:"contracts"`       // Abos & Verträge dieses Monats mit Monatsanteil
	Pockets        []pocketSummary   `json:"pockets"`
	Categories     []categorySummary `json:"categories"`
	Transactions   []db.Transaction  `json:"transactions"`
}

// computeBudget rechnet die Übersicht für den Monat month (erster Tag, UTC).
func (s *Server) computeBudget(ctx context.Context, month time.Time) (budgetView, error) {
	var v budgetView
	if err := s.bookRecurring(ctx); err != nil {
		return v, err
	}
	cats, err := s.repo.Categories(ctx)
	if err != nil {
		return v, err
	}
	txs, err := s.repo.Transactions(ctx, month.Format(db.DateLayout), month.AddDate(0, 1, 0).Format(db.DateLayout))
	if err != nil {
		return v, err
	}
	recs, err := s.repo.RecurringList(ctx)
	if err != nil {
		return v, err
	}
	total, pockets, err := s.budgetTotal(ctx)
	if err != nil {
		return v, err
	}
	plan, contracts := planFor(total, recs, cats, month)
	plan.FromPockets = len(pockets) > 0

	summary := make([]categorySummary, len(cats))
	index := map[uint64]int{}
	budgeted := map[uint64]bool{}
	for i, c := range cats {
		summary[i] = categorySummary{Category: c}
		index[c.ID] = i
		budgeted[c.ID] = c.BudgetCents > 0
	}
	var income, expenses, uncategorized, variable int64
	for _, t := range txs {
		if t.Kind == "income" {
			income += t.AmountCents
		} else {
			expenses += t.AmountCents
		}
		if countsAgainstBudget(t, total, budgeted) {
			variable += t.AmountCents
		}
		i, ok := -1, false
		if t.CategoryID != nil {
			i, ok = index[*t.CategoryID]
		}
		switch {
		case t.Kind == "income":
			if ok {
				summary[i].IncomeCents += t.AmountCents
			}
		case !ok:
			uncategorized += t.AmountCents
		case t.RecurringID != nil:
			summary[i].FixedCents += t.AmountCents
		default:
			summary[i].SpentCents += t.AmountCents
		}
	}
	ps := pocketSummaries(pockets, contracts, summary)
	if slices.ContainsFunc(pockets, func(p db.Pocket) bool { return p.BalanceCents != nil }) {
		frecs, fcats, ftxs, err := s.forecastInputs(ctx)
		if err != nil {
			return v, err
		}
		for i := range ps {
			if ps[i].ID == 0 {
				continue
			}
			if f := forecastPocket(ps[i].Pocket, frecs, pocketCategories(ps[i].Pocket, fcats), ftxs, s.today()); f != nil {
				f.Events = nil // die Liste lädt die Seite bei Bedarf
				ps[i].Forecast = f
			}
		}
	}
	return budgetView{
		Month: month.Format("2006-01"), Today: s.today().Format(db.DateLayout),
		IncomeCents: income, ExpenseCents: expenses, BalanceCents: income - expenses,
		BudgetCents: plan.AllocatedCents, Uncategorized: uncategorized, Plan: plan,
		VariableCents: variable, RemainingCents: plan.FreeCents - variable,
		Contracts: contracts, Pockets: ps, Categories: summary, Transactions: txs,
	}, nil
}

// GET /api/budget?month=2026-11 – Monatsübersicht mit allen Buchungen.
func (s *Server) handleBudget(w http.ResponseWriter, r *http.Request) {
	month, ok := s.monthParam(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "Monat ungültig")
		return
	}
	v, err := s.computeBudget(r.Context(), month)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// --- Kategorien -------------------------------------------------------------

func (s *Server) handleCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := s.repo.Categories(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, cats)
}

func (s *Server) readCategory(w http.ResponseWriter, r *http.Request) (db.Category, bool) {
	var c db.Category
	if !decodeJSON(w, r, &c) {
		return c, false
	}
	c.Name = strings.TrimSpace(c.Name)
	c.Color = strings.ToLower(strings.TrimSpace(c.Color))
	if c.Color == "" {
		c.Color = "#76736e"
	}
	switch {
	case c.Name == "" || utf8.RuneCountInString(c.Name) > 100:
		writeError(w, http.StatusBadRequest, "Bitte einen Namen mit höchstens 100 Zeichen angeben")
	case !colorRe.MatchString(c.Color):
		writeError(w, http.StatusBadRequest, "Farbe ungültig")
	case c.BudgetCents < 0 || c.BudgetCents > maxCents:
		writeError(w, http.StatusBadRequest, "Budget ungültig")
	default:
		return c, s.checkPocket(w, r, c.PocketID)
	}
	return c, false
}

func (s *Server) handleCategoryCreate(w http.ResponseWriter, r *http.Request) {
	c, ok := s.readCategory(w, r)
	if !ok {
		return
	}
	c, err := s.repo.CreateCategory(r.Context(), c)
	if errors.Is(err, db.ErrExists) {
		writeError(w, http.StatusConflict, "Diese Kategorie gibt es schon")
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) handleCategoryUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	c, ok := s.readCategory(w, r)
	if !ok {
		return
	}
	c.ID = id
	err := s.repo.UpdateCategory(r.Context(), c)
	if errors.Is(err, db.ErrExists) {
		writeError(w, http.StatusConflict, "Diese Kategorie gibt es schon")
		return
	}
	if err != nil {
		dbError(w, r, err, "Kategorie")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleCategoryDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.repo.DeleteCategory(r.Context(), id); err != nil {
		dbError(w, r, err, "Kategorie")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Buchungen --------------------------------------------------------------

// checkMoney prüft Art, Betrag und Kategorie (gemeinsam für Buchungen und feste Buchungen).
func (s *Server) checkMoney(w http.ResponseWriter, r *http.Request, kind string, cents int64, cat *uint64) bool {
	switch {
	case kind != "expense" && kind != "income":
		writeError(w, http.StatusBadRequest, "Art muss Ausgabe oder Einnahme sein")
		return false
	case cents <= 0 || cents > maxCents:
		writeError(w, http.StatusBadRequest, "Bitte einen Betrag über 0 angeben")
		return false
	}
	if cat != nil {
		exists, err := s.repo.CategoryExists(r.Context(), *cat)
		if err != nil {
			internalError(w, r, err)
			return false
		}
		if !exists {
			writeError(w, http.StatusBadRequest, "Kategorie nicht gefunden")
			return false
		}
	}
	return true
}

func validDate(s string) bool {
	t, err := time.Parse(db.DateLayout, s)
	return err == nil && t.Year() >= 1970 && t.Year() <= 2200
}

func (s *Server) readTransaction(w http.ResponseWriter, r *http.Request) (db.Transaction, bool) {
	var req struct {
		Kind        string  `json:"kind"`
		AmountCents int64   `json:"amount_cents"`
		Day         string  `json:"day"`
		CategoryID  *uint64 `json:"category_id"`
		Note        string  `json:"note"`
	}
	if !decodeJSON(w, r, &req) {
		return db.Transaction{}, false
	}
	t := db.Transaction{Kind: req.Kind, AmountCents: req.AmountCents, Day: req.Day, CategoryID: req.CategoryID, Note: strings.TrimSpace(req.Note)}
	if !validDate(t.Day) {
		writeError(w, http.StatusBadRequest, "Datum ungültig")
		return t, false
	}
	if utf8.RuneCountInString(t.Note) > 500 {
		writeError(w, http.StatusBadRequest, "Die Notiz ist zu lang")
		return t, false
	}
	return t, s.checkMoney(w, r, t.Kind, t.AmountCents, t.CategoryID)
}

func (s *Server) handleTransactionCreate(w http.ResponseWriter, r *http.Request) {
	t, ok := s.readTransaction(w, r)
	if !ok {
		return
	}
	t, err := s.repo.CreateTransaction(r.Context(), t)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) handleTransactionUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	t, ok := s.readTransaction(w, r)
	if !ok {
		return
	}
	t.ID = id
	if err := s.repo.UpdateTransaction(r.Context(), t); err != nil {
		dbError(w, r, err, "Buchung")
		return
	}
	t, err := s.repo.Transaction(r.Context(), id)
	if err != nil {
		dbError(w, r, err, "Buchung")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleTransactionDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.repo.DeleteTransaction(r.Context(), id); err != nil {
		dbError(w, r, err, "Buchung")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Feste Buchungen --------------------------------------------------------

func (s *Server) handleRecurringList(w http.ResponseWriter, r *http.Request) {
	list, err := s.repo.RecurringList(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) readRecurring(w http.ResponseWriter, r *http.Request) (db.Recurring, bool) {
	var req struct {
		Title       string  `json:"title"`
		Kind        string  `json:"kind"`
		AmountCents int64   `json:"amount_cents"`
		CategoryID  *uint64 `json:"category_id"`
		Day         int     `json:"day"`
		Interval    int     `json:"interval_months"`
		StartsOn    string  `json:"starts_on"`
		EndsOn      *string `json:"ends_on"`
		PocketID    *uint64 `json:"pocket_id"`
		Payment     string  `json:"payment_method"`
		PaymentNote string  `json:"payment_note"`
	}
	if !decodeJSON(w, r, &req) {
		return db.Recurring{}, false
	}
	x := db.Recurring{Title: strings.TrimSpace(req.Title), Kind: req.Kind, AmountCents: req.AmountCents,
		CategoryID: req.CategoryID, Day: req.Day, IntervalMonths: req.Interval, StartsOn: req.StartsOn, EndsOn: req.EndsOn,
		PaymentMethod: req.Payment, PaymentNote: strings.TrimSpace(req.PaymentNote), PocketID: req.PocketID}
	if x.IntervalMonths == 0 {
		x.IntervalMonths = 1
	}
	if x.EndsOn != nil && *x.EndsOn == "" {
		x.EndsOn = nil
	}
	switch {
	case x.Title == "" || utf8.RuneCountInString(x.Title) > 200:
		writeError(w, http.StatusBadRequest, "Bitte eine Bezeichnung mit höchstens 200 Zeichen angeben")
	case x.IntervalMonths != 1 && x.IntervalMonths != 3 && x.IntervalMonths != 6 && x.IntervalMonths != 12:
		writeError(w, http.StatusBadRequest, "Abstand muss monatlich, vierteljährlich, halbjährlich oder jährlich sein")
	case x.Day < 1 || x.Day > 31:
		writeError(w, http.StatusBadRequest, "Tag muss zwischen 1 und 31 liegen")
	case !paymentMethods[x.PaymentMethod]:
		writeError(w, http.StatusBadRequest, "Zahlungsart unbekannt")
	case utf8.RuneCountInString(x.PaymentNote) > 100:
		writeError(w, http.StatusBadRequest, "Die Notiz zur Zahlung ist zu lang (höchstens 100 Zeichen)")
	case cardNumberRe.MatchString(x.PaymentNote):
		// Volle Karten- oder Kontonummern gehören nicht in einen Planer
		writeError(w, http.StatusBadRequest, "Bitte keine vollständige Karten- oder Kontonummer speichern – die letzten 4 Ziffern reichen")
	case !validDate(x.StartsOn):
		writeError(w, http.StatusBadRequest, "Beginn ungültig")
	case x.EndsOn != nil && (!validDate(*x.EndsOn) || *x.EndsOn < x.StartsOn):
		writeError(w, http.StatusBadRequest, "Ende ungültig")
	default:
		return x, s.checkMoney(w, r, x.Kind, x.AmountCents, x.CategoryID) && s.checkPocket(w, r, x.PocketID)
	}
	return x, false
}

func (s *Server) handleRecurringCreate(w http.ResponseWriter, r *http.Request) {
	x, ok := s.readRecurring(w, r)
	if !ok {
		return
	}
	x, err := s.repo.CreateRecurring(r.Context(), x)
	if err != nil {
		internalError(w, r, err)
		return
	}
	// Schon fällige Monate gleich buchen
	if err := s.bookRecurring(r.Context()); err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, x)
}

func (s *Server) handleRecurringUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	x, ok := s.readRecurring(w, r)
	if !ok {
		return
	}
	x.ID = id
	if err := s.repo.UpdateRecurring(r.Context(), x); err != nil {
		dbError(w, r, err, "Feste Buchung")
		return
	}
	x, err := s.repo.RecurringByID(r.Context(), id)
	if err != nil {
		dbError(w, r, err, "Feste Buchung")
		return
	}
	writeJSON(w, http.StatusOK, x)
}

func (s *Server) handleRecurringDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.repo.DeleteRecurring(r.Context(), id); err != nil {
		dbError(w, r, err, "Feste Buchung")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
