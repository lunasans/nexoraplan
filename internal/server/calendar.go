package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"nexoraplan/internal/caldav"
	"nexoraplan/internal/db"
	"nexoraplan/internal/ical"
	"nexoraplan/internal/recur"
	"nexoraplan/internal/secret"
)

// LocalCalendar ist der Schlüssel der eigenen Termine (in der Datenbank der App).
const LocalCalendar = "local"

const localColor = "#2563eb"

// calendarService verbindet eigene Termine und die Kalender vom CalDAV-Server.
// Kalenderliste und Abfragen werden kurz zwischengespeichert, damit das
// Blättern in der Woche nicht jedes Mal auf SOGo wartet.
type calendarService struct {
	repo      *db.Repo
	box       *secret.Box
	suggested string
	loc       *time.Location

	mu      sync.Mutex
	loaded  bool
	client  *caldav.Client
	cals    []caldav.Calendar
	calsAt  time.Time
	queries map[string]cachedQuery
}

type cachedQuery struct {
	objs []caldav.Object
	at   time.Time
}

const (
	calendarListTTL = 15 * time.Minute
	queryTTL        = 2 * time.Minute
)

func newCalendarService(repo *db.Repo, box *secret.Box, suggested string, loc *time.Location) *calendarService {
	return &calendarService{repo: repo, box: box, suggested: suggested, loc: loc, queries: map[string]cachedQuery{}}
}

// Calendar ist ein Kalender, wie ihn die Oberfläche sieht.
type Calendar struct {
	Href     string `json:"href"`
	Name     string `json:"name"`
	Color    string `json:"color"`
	ReadOnly bool   `json:"read_only"`
	Visible  bool   `json:"visible"`
	Remote   bool   `json:"remote"`
}

// reset verwirft Verbindung und Zwischenspeicher (nach geänderten Zugangsdaten).
func (c *calendarService) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loaded, c.client, c.cals, c.calsAt = false, nil, nil, time.Time{}
	c.queries = map[string]cachedQuery{}
}

// invalidate verwirft gespeicherte Abfragen (nach einer Änderung an einem Termin).
func (c *calendarService) invalidate() {
	c.mu.Lock()
	c.queries = map[string]cachedQuery{}
	c.mu.Unlock()
}

// connection liefert den CalDAV-Client oder nil, wenn keiner eingerichtet ist.
func (c *calendarService) connection(ctx context.Context) (*caldav.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaded {
		return c.client, nil
	}
	u, err := c.repo.Setting(ctx, db.SettingCalDAVURL)
	if err != nil {
		return nil, err
	}
	user, err := c.repo.Setting(ctx, db.SettingCalDAVUser)
	if err != nil {
		return nil, err
	}
	sealed, err := c.repo.Setting(ctx, db.SettingCalDAVPassword)
	if err != nil {
		return nil, err
	}
	c.loaded = true
	if u == "" || user == "" || sealed == "" || c.box == nil {
		return nil, nil
	}
	pass, err := c.box.Open(sealed, db.SettingCalDAVPassword)
	if err != nil {
		return nil, fmt.Errorf("CalDAV-Passwort nicht lesbar (PLAN_ENCRYPTION_KEY geändert?) – bitte neu eingeben")
	}
	c.client, err = caldav.New(u, user, pass)
	if err != nil {
		c.client = nil
		return nil, err
	}
	return c.client, nil
}

// remoteCalendars liefert die Kalender vom Server (zwischengespeichert).
func (c *calendarService) remoteCalendars(ctx context.Context, fresh bool) ([]caldav.Calendar, error) {
	cl, err := c.connection(ctx)
	if err != nil || cl == nil {
		return nil, err
	}
	c.mu.Lock()
	if !fresh && c.cals != nil && time.Since(c.calsAt) < calendarListTTL {
		cals := c.cals
		c.mu.Unlock()
		return cals, nil
	}
	c.mu.Unlock()
	cals, err := cl.Discover(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.cals, c.calsAt = cals, time.Now()
	c.mu.Unlock()
	return cals, nil
}

func (c *calendarService) hidden(ctx context.Context) (map[string]bool, error) {
	raw, err := c.repo.Setting(ctx, db.SettingHiddenCals)
	if err != nil || raw == "" {
		return map[string]bool{}, err
	}
	var list []string
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return map[string]bool{}, nil
	}
	out := map[string]bool{}
	for _, h := range list {
		out[h] = true
	}
	return out, nil
}

// Calendars: eigene Termine zuerst, dann die vom Server. Ein Fehler beim
// Server lässt die eigenen trotzdem durch.
func (c *calendarService) Calendars(ctx context.Context, fresh bool) ([]Calendar, error) {
	hidden, err := c.hidden(ctx)
	if err != nil {
		return nil, err
	}
	out := []Calendar{{Href: LocalCalendar, Name: "Eigene Termine", Color: localColor, Visible: !hidden[LocalCalendar]}}
	remote, err := c.remoteCalendars(ctx, fresh)
	for _, r := range remote {
		color := r.Color
		if color == "" {
			color = "#76736e"
		}
		out = append(out, Calendar{Href: r.Href, Name: r.Name, Color: color, ReadOnly: r.ReadOnly, Visible: !hidden[r.Href], Remote: true})
	}
	return out, err
}

// remoteCalendar sucht einen Kalender des Servers nach href.
func (c *calendarService) remoteCalendar(ctx context.Context, href string) (*caldav.Client, caldav.Calendar, error) {
	cl, err := c.connection(ctx)
	if err != nil {
		return nil, caldav.Calendar{}, err
	}
	if cl == nil {
		return nil, caldav.Calendar{}, errors.New("Kein Kalenderserver eingerichtet")
	}
	cals, err := c.remoteCalendars(ctx, false)
	if err != nil {
		return nil, caldav.Calendar{}, err
	}
	for _, x := range cals {
		if x.Href == href {
			return cl, x, nil
		}
	}
	return nil, caldav.Calendar{}, errors.New("Kalender nicht gefunden")
}

func (c *calendarService) query(ctx context.Context, cl *caldav.Client, href string, from, to time.Time) ([]caldav.Object, error) {
	key := href + "|" + from.UTC().Format(time.RFC3339) + "|" + to.UTC().Format(time.RFC3339)
	c.mu.Lock()
	if q, ok := c.queries[key]; ok && time.Since(q.at) < queryTTL {
		c.mu.Unlock()
		return q.objs, nil
	}
	c.mu.Unlock()
	objs, err := cl.Query(ctx, href, from, to)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if len(c.queries) > 200 {
		c.queries = map[string]cachedQuery{}
	}
	c.queries[key] = cachedQuery{objs: objs, at: time.Now()}
	c.mu.Unlock()
	return objs, nil
}

// EventOut ist ein Termin (bei Serien ein Vorkommen) für die Oberfläche.
type EventOut struct {
	Key        string     `json:"key"`      // „local:<id>“ oder href des Kalenderobjekts
	Calendar   string     `json:"calendar"` // „local“ oder href des Kalenders
	Title      string     `json:"title"`
	Location   string     `json:"location"`
	Note       string     `json:"note"`
	AllDay     bool       `json:"all_day"`
	Start      time.Time  `json:"start"`
	End        time.Time  `json:"end"` // bei ganztägigen Terminen ausschließlich (Folgetag 0:00)
	Recurring  bool       `json:"recurring"`
	Occurrence *time.Time `json:"occurrence,omitempty"` // ursprünglicher Beginn bei Serien
	RRule      string     `json:"rrule"`
	Color      string     `json:"color"`
	ReadOnly   bool       `json:"read_only"`
}

// Occurrences liefert alle sichtbaren Termine im Zeitraum. remoteErr meldet
// Probleme mit dem Kalenderserver, ohne die eigenen Termine zu verlieren.
func (c *calendarService) Occurrences(ctx context.Context, from, to time.Time) (out []EventOut, remoteErr error, err error) {
	cals, remoteErr := c.Calendars(ctx, false)
	if cals == nil {
		return nil, nil, remoteErr
	}
	out = []EventOut{}
	if cals[0].Visible {
		local, err := c.localOccurrences(ctx, from, to)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, local...)
	}
	cl, _ := c.connection(ctx)
	type result struct {
		events []EventOut
		err    error
	}
	var wg sync.WaitGroup
	results := make([]result, len(cals))
	for i, cal := range cals[1:] {
		if !cal.Visible {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ev, err := c.remoteOccurrences(ctx, cl, cal, from, to)
			results[i+1] = result{ev, err}
		}()
	}
	wg.Wait()
	for i, r := range results {
		if r.err != nil {
			slog.Warn("Kalender nicht geladen", "kalender", cals[i].Name, "fehler", r.err)
			if remoteErr == nil {
				remoteErr = fmt.Errorf("%s: %w", cals[i].Name, r.err)
			}
		}
		out = append(out, r.events...)
	}
	// Nach Tag, innerhalb eines Tages ganztägige zuerst, dann nach Uhrzeit
	slices.SortStableFunc(out, func(a, b EventOut) int {
		if d := strings.Compare(a.Start.Format(time.DateOnly), b.Start.Format(time.DateOnly)); d != 0 {
			return d
		}
		if a.AllDay != b.AllDay {
			if a.AllDay {
				return -1
			}
			return 1
		}
		return a.Start.Compare(b.Start)
	})
	return out, remoteErr, nil
}

func (c *calendarService) remoteOccurrences(ctx context.Context, cl *caldav.Client, cal Calendar, from, to time.Time) ([]EventOut, error) {
	objs, err := c.query(ctx, cl, cal.Href, from, to)
	if err != nil {
		return nil, err
	}
	var out []EventOut
	for _, o := range objs {
		parsed, err := ical.Parse(o.Data)
		if err != nil {
			slog.Warn("Termin nicht lesbar", "href", o.Href, "fehler", err)
			continue
		}
		occ, err := ical.Expand(parsed, from, to, c.loc)
		if err != nil {
			slog.Warn("Termin nicht lesbar", "href", o.Href, "fehler", err)
			continue
		}
		for _, x := range occ {
			e := EventOut{
				Key: o.Href, Calendar: cal.Href, Title: x.Summary, Location: x.Location, Note: x.Description,
				AllDay: x.AllDay, Start: x.Start.In(c.loc), End: x.End.In(c.loc), Recurring: x.Recurring,
				RRule: x.RRule, Color: cal.Color, ReadOnly: cal.ReadOnly,
			}
			if x.Recurring {
				orig := x.Original.In(c.loc)
				e.Occurrence = &orig
			}
			out = append(out, e)
		}
	}
	return out, nil
}

func (c *calendarService) localOccurrences(ctx context.Context, from, to time.Time) ([]EventOut, error) {
	events, err := c.repo.EventsBetween(ctx, from, to)
	if err != nil {
		return nil, err
	}
	var out []EventOut
	for _, e := range events {
		out = append(out, expandLocal(e, from, to, c.loc)...)
	}
	return out, nil
}

func localKey(id uint64) string { return fmt.Sprintf("local:%d", id) }

// expandLocal löst einen eigenen Termin im Zeitraum auf.
func expandLocal(e db.Event, from, to time.Time, loc *time.Location) []EventOut {
	start, end := e.Start.In(loc), e.End.In(loc)
	base := EventOut{
		Key: localKey(e.ID), Calendar: LocalCalendar, Title: e.Title, Location: e.Location, Note: e.Note,
		AllDay: e.AllDay, Start: start, End: end, RRule: e.RRule, Color: localColor,
	}
	if e.RRule == "" {
		if overlaps(start, end, from, to) {
			return []EventOut{base}
		}
		return nil
	}
	rule, err := recur.Parse(e.RRule, loc)
	if err != nil {
		return nil
	}
	dur := end.Sub(start)
	days := 0
	if e.AllDay {
		days = int(dur.Round(24*time.Hour) / (24 * time.Hour))
	}
	var out []EventOut
	for _, st := range rule.Between(start, from.Add(-dur-24*time.Hour), to) {
		if slices.ContainsFunc(e.ExDates, func(x time.Time) bool { return x.Equal(st) }) {
			continue
		}
		en := st.Add(dur)
		if e.AllDay {
			en = st.AddDate(0, 0, days)
		}
		if !overlaps(st, en, from, to) {
			continue
		}
		o := base
		o.Start, o.End, o.Recurring = st, en, true
		orig := st
		o.Occurrence = &orig
		out = append(out, o)
	}
	return out
}

func overlaps(start, end, from, to time.Time) bool {
	if !start.Before(to) {
		return false
	}
	if end.Equal(start) {
		return !start.Before(from)
	}
	return end.After(from)
}

// isLocalKey zerlegt „local:<id>“.
func isLocalKey(key string) (uint64, bool) {
	rest, ok := strings.CutPrefix(key, "local:")
	if !ok {
		return 0, false
	}
	var id uint64
	if _, err := fmt.Sscanf(rest, "%d", &id); err != nil || id == 0 || fmt.Sprint(id) != rest {
		return 0, false
	}
	return id, true
}
