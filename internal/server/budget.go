package server

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"nexoraplan/internal/db"
)

const maxCents = 100_000_000_00 // 100 Mio. €

var colorRe = regexp.MustCompile(`^#[0-9a-f]{6}$`)

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
	SpentCents  int64 `json:"spent_cents"`
	IncomeCents int64 `json:"income_cents"`
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
	summary := make([]categorySummary, len(cats))
	index := map[uint64]int{}
	var budget int64
	for i, c := range cats {
		summary[i] = categorySummary{Category: c}
		index[c.ID] = i
		budget += c.BudgetCents
	}
	var income, expenses, uncategorized int64
	for _, t := range txs {
		if t.Kind == "income" {
			income += t.AmountCents
		} else {
			expenses += t.AmountCents
		}
		if t.CategoryID == nil {
			if t.Kind == "expense" {
				uncategorized += t.AmountCents
			}
			continue
		}
		if i, ok := index[*t.CategoryID]; ok {
			if t.Kind == "income" {
				summary[i].IncomeCents += t.AmountCents
			} else {
				summary[i].SpentCents += t.AmountCents
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"month":               month.Format("2006-01"),
		"today":               s.today().Format(db.DateLayout),
		"income_cents":        income,
		"expense_cents":       expenses,
		"balance_cents":       income - expenses,
		"budget_cents":        budget,
		"uncategorized_cents": uncategorized,
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
		StartsOn    string  `json:"starts_on"`
		EndsOn      *string `json:"ends_on"`
	}
	if !decodeJSON(w, r, &req) {
		return db.Recurring{}, false
	}
	x := db.Recurring{Title: strings.TrimSpace(req.Title), Kind: req.Kind, AmountCents: req.AmountCents,
		CategoryID: req.CategoryID, Day: req.Day, StartsOn: req.StartsOn, EndsOn: req.EndsOn}
	if x.EndsOn != nil && *x.EndsOn == "" {
		x.EndsOn = nil
	}
	switch {
	case x.Title == "" || utf8.RuneCountInString(x.Title) > 200:
		writeError(w, http.StatusBadRequest, "Bitte eine Bezeichnung mit höchstens 200 Zeichen angeben")
	case x.Day < 1 || x.Day > 31:
		writeError(w, http.StatusBadRequest, "Tag muss zwischen 1 und 31 liegen")
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
