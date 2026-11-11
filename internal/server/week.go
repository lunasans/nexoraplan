package server

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"nexoraplan/internal/db"
)

// monday liefert den Montag der Kalenderwoche von t (Mitternacht in t.Location()).
func monday(t time.Time) time.Time {
	y, m, d := t.Date()
	offset := (int(t.Weekday()) + 6) % 7
	return time.Date(y, m, d-offset, 0, 0, 0, 0, t.Location())
}

// weekParam liest ?date=… (ein beliebiger Tag der Woche); ohne Angabe heute.
func (s *Server) weekParam(r *http.Request) (time.Time, bool) {
	raw := r.URL.Query().Get("date")
	if raw == "" {
		return monday(s.now().In(s.loc)), true
	}
	t, err := time.ParseInLocation(db.DateLayout, raw, s.loc)
	if err != nil || t.Year() < 1970 || t.Year() > 2200 {
		return time.Time{}, false
	}
	return monday(t), true
}

type weekDay struct {
	Date         string           `json:"date"`
	Events       []EventOut       `json:"events"`
	Tasks        []db.Task        `json:"tasks"`
	Transactions []db.Transaction `json:"transactions"`
	SpentCents   int64            `json:"spent_cents"`
	IncomeCents  int64            `json:"income_cents"`
}

// GET /api/week?date=2026-11-11 – alles für die Wochenansicht auf einmal.
func (s *Server) handleWeek(w http.ResponseWriter, r *http.Request) {
	mon, ok := s.weekParam(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "Datum ungültig")
		return
	}
	ctx := r.Context()
	from, to := mon, mon.AddDate(0, 0, 7)
	if err := s.bookRecurring(ctx); err != nil {
		internalError(w, r, err)
		return
	}

	events, remoteErr, err := s.calendar.Occurrences(ctx, from, to)
	if err != nil {
		internalError(w, r, err)
		return
	}
	tasks, err := s.repo.Tasks(ctx, mon.Format(db.DateLayout))
	if err != nil {
		internalError(w, r, err)
		return
	}
	txs, err := s.repo.Transactions(ctx, from.Format(db.DateLayout), to.Format(db.DateLayout))
	if err != nil {
		internalError(w, r, err)
		return
	}
	cats, err := s.repo.Categories(ctx)
	if err != nil {
		internalError(w, r, err)
		return
	}
	var monthly int64
	for _, c := range cats {
		monthly += c.BudgetCents
	}

	days := make([]weekDay, 7)
	index := map[string]int{}
	var planned, spent int64
	for i := range days {
		d := mon.AddDate(0, 0, i)
		key := d.Format(db.DateLayout)
		index[key] = i
		days[i] = weekDay{Date: key, Events: []EventOut{}, Tasks: []db.Task{}, Transactions: []db.Transaction{}}
		// Monatsbudget anteilig je Tag – eine Woche über den Monatswechsel rechnet beide Monate richtig.
		daysInMonth := time.Date(d.Year(), d.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
		planned += monthly / int64(daysInMonth)
	}
	var openTasks []db.Task
	weekTasks := []db.Task{}
	for _, t := range tasks {
		if t.Day == nil {
			weekTasks = append(weekTasks, t)
		} else if i, ok := index[*t.Day]; ok {
			days[i].Tasks = append(days[i].Tasks, t)
		}
		if !t.Done {
			openTasks = append(openTasks, t)
		}
	}
	for _, e := range events {
		// Mehrtägige Termine an jedem berührten Tag zeigen
		for i := range days {
			dayStart := mon.AddDate(0, 0, i)
			if overlaps(e.Start, e.End, dayStart, dayStart.AddDate(0, 0, 1)) {
				days[i].Events = append(days[i].Events, e)
			}
		}
	}
	for _, t := range txs {
		i, ok := index[t.Day]
		if !ok {
			continue
		}
		days[i].Transactions = append(days[i].Transactions, t)
		if t.Kind == "expense" {
			days[i].SpentCents += t.AmountCents
			spent += t.AmountCents
		} else {
			days[i].IncomeCents += t.AmountCents
		}
	}
	year, kw := mon.ISOWeek()
	writeJSON(w, http.StatusOK, map[string]any{
		"start":          mon.Format(db.DateLayout),
		"end":            mon.AddDate(0, 0, 6).Format(db.DateLayout),
		"today":          s.now().In(s.loc).Format(db.DateLayout),
		"iso_year":       year,
		"iso_week":       kw,
		"days":           days,
		"week_tasks":     weekTasks,
		"open_tasks":     len(openTasks),
		"budget":         map[string]int64{"planned_cents": planned, "spent_cents": spent},
		"categories":     cats,
		"calendar_error": errText(remoteErr),
	})
}

// --- Aufgaben ---------------------------------------------------------------

type taskRequest struct {
	Week  *string `json:"week"`
	Day   *string `json:"day"`
	Title *string `json:"title"`
	Done  *bool   `json:"done"`
	// ClearDay: Tag entfernen (Aufgabe gilt für die ganze Woche)
	ClearDay bool `json:"clear_day"`
}

func cleanTitle(s string) (string, string) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return "", "Bitte einen Titel angeben"
	case utf8.RuneCountInString(s) > 500:
		return "", "Der Titel ist zu lang"
	}
	return s, ""
}

// mondayOf prüft ein Datum und liefert den Montag seiner Woche.
func (s *Server) mondayOf(day string) (string, bool) {
	t, err := time.ParseInLocation(db.DateLayout, day, s.loc)
	if err != nil {
		return "", false
	}
	return monday(t).Format(db.DateLayout), true
}

// POST /api/tasks {"week" | "day", "title"}
func (s *Server) handleTaskCreate(w http.ResponseWriter, r *http.Request) {
	var req taskRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	title, msg := cleanTitle(deref(req.Title))
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	var week string
	var ok bool
	switch {
	case req.Day != nil:
		week, ok = s.mondayOf(*req.Day)
	case req.Week != nil:
		week, ok = s.mondayOf(*req.Week)
	}
	if !ok {
		writeError(w, http.StatusBadRequest, "Woche oder Tag ungültig")
		return
	}
	t, err := s.repo.CreateTask(r.Context(), week, req.Day, title)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// PATCH /api/tasks/{id} – nur die angegebenen Felder ändern sich.
func (s *Server) handleTaskUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req taskRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	t, err := s.repo.Task(r.Context(), id)
	if err != nil {
		dbError(w, r, err, "Aufgabe")
		return
	}
	if req.Title != nil {
		title, msg := cleanTitle(*req.Title)
		if msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		t.Title = title
	}
	if req.Done != nil {
		t.Done = *req.Done
	}
	switch {
	case req.Day != nil:
		week, ok := s.mondayOf(*req.Day)
		if !ok {
			writeError(w, http.StatusBadRequest, "Tag ungültig")
			return
		}
		t.Day, t.Week = req.Day, week
	case req.ClearDay:
		t.Day = nil
	}
	if req.Week != nil && req.Day == nil {
		week, ok := s.mondayOf(*req.Week)
		if !ok {
			writeError(w, http.StatusBadRequest, "Woche ungültig")
			return
		}
		t.Week, t.Day = week, nil
	}
	if err := s.repo.UpdateTask(r.Context(), t); err != nil {
		dbError(w, r, err, "Aufgabe")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleTaskDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.repo.DeleteTask(r.Context(), id); err != nil {
		dbError(w, r, err, "Aufgabe")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/tasks/carry-over {"week"} – offene Aufgaben in die nächste Woche.
func (s *Server) handleTaskCarryOver(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Week string `json:"week"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	week, ok := s.mondayOf(req.Week)
	if !ok {
		writeError(w, http.StatusBadRequest, "Woche ungültig")
		return
	}
	t, _ := time.Parse(db.DateLayout, week)
	n, err := s.repo.CarryOver(r.Context(), week, t.AddDate(0, 0, 7).Format(db.DateLayout))
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"moved": n})
}
