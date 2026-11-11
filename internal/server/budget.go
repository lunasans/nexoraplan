package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
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
}

type contractShare struct {
	db.Recurring
	MonthlyCents int64 `json:"monthly_cents"`
}

func (s *Server) budgetTotal(ctx context.Context) (int64, error) {
	raw, err := s.repo.Setting(ctx, db.SettingBudgetTotal)
	if err != nil || raw == "" {
		return 0, err
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, nil
	}
	return n, nil
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

// GET /api/budget?month=2026-11 – Monatsübersicht mit allen Buchungen.
func (s *Server) handleBudget(w http.ResponseWriter, r *http.Request) {
	month, ok := s.monthParam(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "Monat ungültig")
		return
	}
	ctx := r.Context()
	if err := s.bookRecurring(ctx); err != nil {
		internalError(w, r, err)
		return
	}
	cats, err := s.repo.Categories(ctx)
	if err != nil {
		internalError(w, r, err)
		return
	}
	txs, err := s.repo.Transactions(ctx, month.Format(db.DateLayout), month.AddDate(0, 1, 0).Format(db.DateLayout))
	if err != nil {
		internalError(w, r, err)
		return
	}
	recs, err := s.repo.RecurringList(ctx)
	if err != nil {
		internalError(w, r, err)
		return
	}
	total, err := s.budgetTotal(ctx)
	if err != nil {
		internalError(w, r, err)
		return
	}
	plan, contracts := planFor(total, recs, cats, month)

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
	writeJSON(w, http.StatusOK, map[string]any{
		"month":               month.Format("2006-01"),
		"today":               s.today().Format(db.DateLayout),
		"income_cents":        income,
		"expense_cents":       expenses,
		"balance_cents":       income - expenses,
		"budget_cents":        plan.AllocatedCents,
		"uncategorized_cents": uncategorized,
		"plan":                plan,
		"variable_cents":      variable, // Ausgaben, die gegen den freien Teil zählen
		"remaining_cents":     plan.FreeCents - variable,
		"contracts":           contracts, // Abos & Verträge dieses Monats mit Monatsanteil
		"categories":          summary,
		"transactions":        txs,
	})
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

func readCategory(w http.ResponseWriter, r *http.Request) (db.Category, bool) {
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
		return c, true
	}
	return c, false
}

func (s *Server) handleCategoryCreate(w http.ResponseWriter, r *http.Request) {
	c, ok := readCategory(w, r)
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
	c, ok := readCategory(w, r)
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
		Payment     string  `json:"payment_method"`
		PaymentNote string  `json:"payment_note"`
	}
	if !decodeJSON(w, r, &req) {
		return db.Recurring{}, false
	}
	x := db.Recurring{Title: strings.TrimSpace(req.Title), Kind: req.Kind, AmountCents: req.AmountCents,
		CategoryID: req.CategoryID, Day: req.Day, IntervalMonths: req.Interval, StartsOn: req.StartsOn, EndsOn: req.EndsOn,
		PaymentMethod: req.Payment, PaymentNote: strings.TrimSpace(req.PaymentNote)}
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
		return x, s.checkMoney(w, r, x.Kind, x.AmountCents, x.CategoryID)
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
