package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"nexoraplan/internal/caldav"
	"nexoraplan/internal/db"
	"nexoraplan/internal/ical"
	"nexoraplan/internal/recur"
)

// GET /api/events?from=2026-11-09&to=2026-11-16 – Termine im Zeitraum (to ausschließlich).
// Mit ?key=… (und calendar=…) stattdessen ein einzelner Termin, bei Serien der Hauptteil.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if key := q.Get("key"); key != "" {
		s.handleEventDetail(w, r, key, q.Get("calendar"))
		return
	}
	from, err1 := time.ParseInLocation(db.DateLayout, q.Get("from"), s.loc)
	to, err2 := time.ParseInLocation(db.DateLayout, q.Get("to"), s.loc)
	if err1 != nil || err2 != nil || !to.After(from) || to.Sub(from) > 400*24*time.Hour {
		writeError(w, http.StatusBadRequest, "Zeitraum ungültig")
		return
	}
	events, remoteErr, err := s.calendar.Occurrences(r.Context(), from, to)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "calendar_error": errText(remoteErr)})
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Server) handleEventDetail(w http.ResponseWriter, r *http.Request, key, calendar string) {
	if id, ok := isLocalKey(key); ok {
		e, err := s.repo.Event(r.Context(), id)
		if err != nil {
			dbError(w, r, err, "Termin")
			return
		}
		out := EventOut{Key: key, Calendar: LocalCalendar, Title: e.Title, Location: e.Location, Note: e.Note,
			AllDay: e.AllDay, Start: e.Start.In(s.loc), End: e.End.In(s.loc), Recurring: e.RRule != "", RRule: e.RRule, Color: localColor}
		writeJSON(w, http.StatusOK, out)
		return
	}
	_, cal, obj, ok := s.remoteObject(w, r, key, calendar, false)
	if !ok {
		return
	}
	parsed, err := ical.Parse(obj.Data)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Termin vom Kalenderserver nicht lesbar")
		return
	}
	d, err := ical.MasterData(parsed, s.loc)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Termin vom Kalenderserver nicht lesbar")
		return
	}
	writeJSON(w, http.StatusOK, EventOut{Key: key, Calendar: cal.Href, Title: d.Summary, Location: d.Location, Note: d.Description,
		AllDay: d.AllDay, Start: d.Start.In(s.loc), End: d.End.In(s.loc), Recurring: d.RRule != "", RRule: d.RRule,
		Color: cal.Color, ReadOnly: cal.ReadOnly})
}

// remoteObject prüft Kalender und Termin und lädt ihn frisch vom Server.
func (s *Server) remoteObject(w http.ResponseWriter, r *http.Request, key, calendar string, write bool) (*caldav.Client, caldav.Calendar, caldav.Object, bool) {
	cl, cal, err := s.calendar.remoteCalendar(r.Context(), calendar)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, cal, caldav.Object{}, false
	}
	// Nur Termine innerhalb dieses Kalenders – sonst ließe sich mit einem
	// beschreibbaren Kalender ein fremder Pfad ansprechen.
	if !insideCalendar(key, cal.Href) {
		writeError(w, http.StatusBadRequest, "Termin gehört nicht zu diesem Kalender")
		return nil, cal, caldav.Object{}, false
	}
	if write && cal.ReadOnly {
		writeError(w, http.StatusForbidden, "Dieser Kalender ist schreibgeschützt")
		return nil, cal, caldav.Object{}, false
	}
	obj, err := cl.Get(r.Context(), key)
	if err != nil {
		writeCalDAVError(w, err)
		return nil, cal, caldav.Object{}, false
	}
	return cl, cal, obj, true
}

// insideCalendar: liegt das Kalenderobjekt key direkt im Kalender cal? Verglichen
// wird entschlüsselt – Server schreiben „@“ mal als %40, mal nicht.
func insideCalendar(key, cal string) bool {
	k, err1 := url.PathUnescape(key)
	c, err2 := url.PathUnescape(cal)
	if err1 != nil || err2 != nil || strings.Contains(k, "..") || strings.ContainsAny(k, "?#") {
		return false
	}
	rest, ok := strings.CutPrefix(k, strings.TrimSuffix(c, "/")+"/")
	return ok && rest != "" && !strings.Contains(rest, "/")
}

func writeCalDAVError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, caldav.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, caldav.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusBadGateway, err.Error())
	}
}

type eventRequest struct {
	Key      string `json:"key"` // nur beim Ändern
	Calendar string `json:"calendar"`
	Title    string `json:"title"`
	Location string `json:"location"`
	Note     string `json:"note"`
	AllDay   bool   `json:"all_day"`
	// Wanduhrzeit in der Zeitzone der App: „2026-11-11T09:00“, ganztägig
	// „2026-11-11“ (Ende = letzter Tag, einschließlich).
	Start string `json:"start"`
	End   string `json:"end"`
	RRule string `json:"rrule"`
}

// parse prüft die Eingabe; End ist danach bei ganztägigen Terminen ausschließlich.
func (s *Server) parseEvent(req *eventRequest) (start, end time.Time, msg string) {
	req.Title = strings.TrimSpace(req.Title)
	req.Location = strings.TrimSpace(req.Location)
	req.RRule = strings.ToUpper(strings.TrimSpace(req.RRule))
	switch {
	case req.Title == "":
		return start, end, "Bitte einen Titel angeben"
	case utf8.RuneCountInString(req.Title) > 500 || utf8.RuneCountInString(req.Location) > 500:
		return start, end, "Titel oder Ort ist zu lang"
	case len(req.Note) > 20000:
		return start, end, "Die Notiz ist zu lang"
	case len(req.RRule) > 500:
		return start, end, "Die Wiederholung ist zu lang"
	}
	layout := "2006-01-02T15:04"
	if req.AllDay {
		layout = db.DateLayout
	}
	var err error
	if start, err = time.ParseInLocation(layout, req.Start, s.loc); err != nil {
		return start, end, "Beginn ungültig"
	}
	if end, err = time.ParseInLocation(layout, req.End, s.loc); err != nil {
		return start, end, "Ende ungültig"
	}
	if req.AllDay {
		end = end.AddDate(0, 0, 1)
	}
	if end.Before(start) || (req.AllDay && !end.After(start)) {
		return start, end, "Das Ende liegt vor dem Beginn"
	}
	if end.Sub(start) > 366*24*time.Hour {
		return start, end, "Ein Termin darf höchstens ein Jahr dauern"
	}
	if req.RRule != "" {
		rule, err := recur.Parse(req.RRule, s.loc)
		if err != nil {
			return start, end, "Wiederholung ungültig: " + err.Error()
		}
		req.RRule = rule.String()
	}
	return start, end, ""
}

// POST /api/events – neuer Termin im gewählten Kalender.
func (s *Server) handleEventCreate(w http.ResponseWriter, r *http.Request) {
	var req eventRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	start, end, msg := s.parseEvent(&req)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if req.Calendar == "" || req.Calendar == LocalCalendar {
		id, err := s.repo.CreateEvent(r.Context(), db.Event{Title: req.Title, Location: req.Location, Note: req.Note,
			AllDay: req.AllDay, Start: start, End: end, RRule: req.RRule})
		if err != nil {
			internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"key": localKey(id)})
		return
	}
	cl, cal, err := s.calendar.remoteCalendar(r.Context(), req.Calendar)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if cal.ReadOnly {
		writeError(w, http.StatusForbidden, "Dieser Kalender ist schreibgeschützt")
		return
	}
	uid := ical.NewUID()
	href := caldav.ObjectHref(cal.Href, uid)
	data := ical.NewCalendar(uid, ical.EventData{Summary: req.Title, Location: req.Location, Description: req.Note,
		Start: start, End: end, AllDay: req.AllDay, RRule: req.RRule}, s.now()).Encode()
	if err := cl.Put(r.Context(), href, data, ""); err != nil {
		writeCalDAVError(w, err)
		return
	}
	s.calendar.invalidate()
	slog.Info("Termin im Kalenderserver angelegt", "kalender", cal.Name)
	writeJSON(w, http.StatusCreated, map[string]string{"key": href})
}

// PUT /api/events – Termin ändern (bei Serien die ganze Serie).
func (s *Server) handleEventUpdate(w http.ResponseWriter, r *http.Request) {
	var req eventRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	start, end, msg := s.parseEvent(&req)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if id, ok := isLocalKey(req.Key); ok {
		if req.Calendar != LocalCalendar {
			writeError(w, http.StatusBadRequest, "Termine lassen sich nicht in einen anderen Kalender verschieben")
			return
		}
		err := s.repo.UpdateEvent(r.Context(), db.Event{ID: id, Title: req.Title, Location: req.Location, Note: req.Note,
			AllDay: req.AllDay, Start: start, End: end, RRule: req.RRule})
		if err != nil {
			dbError(w, r, err, "Termin")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"key": req.Key})
		return
	}
	cl, _, obj, ok := s.remoteObject(w, r, req.Key, req.Calendar, true)
	if !ok {
		return
	}
	parsed, err := ical.Parse(obj.Data)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Termin vom Kalenderserver nicht lesbar")
		return
	}
	if err := ical.Update(parsed, ical.EventData{Summary: req.Title, Location: req.Location, Description: req.Note,
		Start: start, End: end, AllDay: req.AllDay, RRule: req.RRule}, s.now()); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := cl.Put(r.Context(), req.Key, parsed.Encode(), obj.ETag); err != nil {
		writeCalDAVError(w, err)
		return
	}
	s.calendar.invalidate()
	writeJSON(w, http.StatusOK, map[string]string{"key": req.Key})
}

// POST /api/events/delete {"key", "calendar", "occurrence"} – ohne occurrence
// den ganzen Termin (bzw. die Serie), mit occurrence nur dieses Vorkommen.
func (s *Server) handleEventDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key        string     `json:"key"`
		Calendar   string     `json:"calendar"`
		Occurrence *time.Time `json:"occurrence"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if id, ok := isLocalKey(req.Key); ok {
		var err error
		if req.Occurrence != nil {
			if _, err = s.repo.Event(r.Context(), id); err == nil {
				err = s.repo.ExcludeOccurrence(r.Context(), id, *req.Occurrence)
			}
		} else {
			err = s.repo.DeleteEvent(r.Context(), id)
		}
		if err != nil {
			dbError(w, r, err, "Termin")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	cl, _, obj, ok := s.remoteObject(w, r, req.Key, req.Calendar, true)
	if !ok {
		return
	}
	var err error
	if req.Occurrence != nil {
		parsed, perr := ical.Parse(obj.Data)
		if perr != nil {
			writeError(w, http.StatusBadGateway, "Termin vom Kalenderserver nicht lesbar")
			return
		}
		if err := ical.Exclude(parsed, req.Occurrence.In(s.loc), s.now()); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		err = cl.Put(r.Context(), req.Key, parsed.Encode(), obj.ETag)
	} else {
		err = cl.Delete(r.Context(), req.Key, obj.ETag)
	}
	if err != nil {
		writeCalDAVError(w, err)
		return
	}
	s.calendar.invalidate()
	w.WriteHeader(http.StatusNoContent)
}

// --- Kalender und Zugang zum Kalenderserver ---------------------------------

// GET /api/calendars?fresh=1 – alle Kalender mit Sichtbarkeit.
func (s *Server) handleCalendars(w http.ResponseWriter, r *http.Request) {
	cals, remoteErr := s.calendar.Calendars(r.Context(), r.URL.Query().Get("fresh") == "1")
	if cals == nil {
		internalError(w, r, remoteErr)
		return
	}
	def, err := s.repo.Setting(r.Context(), db.SettingDefaultCal)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if def == "" || !containsCal(cals, def) {
		def = LocalCalendar
	}
	writeJSON(w, http.StatusOK, map[string]any{"calendars": cals, "default": def, "calendar_error": errText(remoteErr)})
}

func containsCal(cals []Calendar, href string) bool {
	for _, c := range cals {
		if c.Href == href {
			return true
		}
	}
	return false
}

// PUT /api/calendars/visibility {"href", "visible"}
func (s *Server) handleCalendarVisibility(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Href    string `json:"href"`
		Visible bool   `json:"visible"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Href == "" || len(req.Href) > 1000 {
		writeError(w, http.StatusBadRequest, "Kalender ungültig")
		return
	}
	hidden, err := s.calendar.hidden(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	if req.Visible {
		delete(hidden, req.Href)
	} else {
		hidden[req.Href] = true
	}
	list := []string{}
	for h := range hidden {
		list = append(list, h)
	}
	raw, _ := json.Marshal(list)
	value := string(raw)
	if len(list) == 0 {
		value = ""
	}
	if err := s.repo.SetSettings(r.Context(), map[string]string{db.SettingHiddenCals: value}); err != nil {
		internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PUT /api/calendars/default {"href"} – Kalender für neue Termine.
func (s *Server) handleCalendarDefault(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Href string `json:"href"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Href != LocalCalendar {
		if _, cal, err := s.calendar.remoteCalendar(r.Context(), req.Href); err != nil || cal.ReadOnly {
			writeError(w, http.StatusBadRequest, "In diesem Kalender lassen sich keine Termine anlegen")
			return
		}
	}
	if err := s.repo.SetSettings(r.Context(), map[string]string{db.SettingDefaultCal: req.Href}); err != nil {
		internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/caldav – eingestellter Kalenderserver (ohne Passwort).
func (s *Server) handleCalDAVGet(w http.ResponseWriter, r *http.Request) {
	u, err := s.repo.Setting(r.Context(), db.SettingCalDAVURL)
	if err != nil {
		internalError(w, r, err)
		return
	}
	user, _ := s.repo.Setting(r.Context(), db.SettingCalDAVUser)
	pass, _ := s.repo.Setting(r.Context(), db.SettingCalDAVPassword)
	writeJSON(w, http.StatusOK, map[string]any{
		"url": u, "username": user, "has_password": pass != "", "configured": u != "" && user != "" && pass != "",
		"suggested_url": s.calendar.suggested, "can_store": s.box != nil,
	})
}

// PUT /api/caldav {"url", "username", "password"} – prüft die Verbindung und
// speichert erst dann. Leeres Passwort behält das bisherige.
func (s *Server) handleCalDAVSet(w http.ResponseWriter, r *http.Request) {
	if s.box == nil {
		writeError(w, http.StatusConflict, "PLAN_ENCRYPTION_KEY fehlt – ohne Schlüssel speichert die App kein Kalender-Passwort")
		return
	}
	var req struct {
		URL      string `json:"url"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.URL, req.Username = strings.TrimSpace(req.URL), strings.TrimSpace(req.Username)
	if req.Username == "" || len(req.Username) > 255 || len(req.URL) > 1000 || len(req.Password) > 1024 {
		writeError(w, http.StatusBadRequest, "Bitte Adresse, Benutzer und Passwort angeben")
		return
	}
	pass := req.Password
	if pass == "" {
		sealed, err := s.repo.Setting(r.Context(), db.SettingCalDAVPassword)
		if err != nil {
			internalError(w, r, err)
			return
		}
		if sealed != "" {
			pass, _ = s.box.Open(sealed, db.SettingCalDAVPassword)
		}
		if pass == "" {
			writeError(w, http.StatusBadRequest, "Bitte das Passwort angeben")
			return
		}
	}
	cl, err := caldav.New(req.URL, req.Username, pass)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cals, err := cl.Discover(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := s.repo.SetSettings(r.Context(), map[string]string{
		db.SettingCalDAVURL:      req.URL,
		db.SettingCalDAVUser:     req.Username,
		db.SettingCalDAVPassword: s.box.Seal(pass, db.SettingCalDAVPassword),
	}); err != nil {
		internalError(w, r, err)
		return
	}
	s.calendar.reset()
	slog.Info("Kalenderserver verbunden", "kalender", len(cals))
	writeJSON(w, http.StatusOK, map[string]int{"calendars": len(cals)})
}

// DELETE /api/caldav – Verbindung trennen (Termine bleiben auf dem Server).
func (s *Server) handleCalDAVDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.repo.SetSettings(r.Context(), map[string]string{
		db.SettingCalDAVURL: "", db.SettingCalDAVUser: "", db.SettingCalDAVPassword: "", db.SettingHiddenCals: "", db.SettingDefaultCal: "",
	}); err != nil {
		internalError(w, r, err)
		return
	}
	s.calendar.reset()
	w.WriteHeader(http.StatusNoContent)
}
