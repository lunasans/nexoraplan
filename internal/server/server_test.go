package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"nexoraplan/internal/db"
	"nexoraplan/internal/secret"
)

// Die Tests mit Datenbank laufen nur mit PLAN_TEST_DSN, z. B.
//
//	PLAN_TEST_DSN='plan:plan@unix(/run/mysqld/mysqld.sock)/nexoraplan_test' go test ./...
//
// Die Datenbank wird dabei geleert.
func testServer(t *testing.T) (*Server, *client) {
	t.Helper()
	dsn := os.Getenv("PLAN_TEST_DSN")
	if dsn == "" {
		t.Skip("PLAN_TEST_DSN nicht gesetzt")
	}
	ctx := context.Background()
	database, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	_, err = database.ExecContext(ctx, `SET FOREIGN_KEY_CHECKS = 0;
		DROP TABLE IF EXISTS transactions, recurring, categories, event_exdates, events, tasks, settings, sessions, users, schema_migrations;
		SET FOREIGN_KEY_CHECKS = 1;`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	box, _ := secret.New(secret.GenerateKey())
	berlin, _ := time.LoadLocation("Europe/Berlin")
	s, err := New(db.NewRepo(database), Options{PublicURL: "http://localhost", Location: berlin, SetupCode: "TEST-CODE-1234-ABCD", Box: box})
	if err != nil {
		t.Fatal(err)
	}
	// feste Uhr: Mittwoch, 11.11.2026, 10:00
	s.now = func() time.Time { return time.Date(2026, 11, 11, 10, 0, 0, 0, berlin) }
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return s, &client{t: t, base: srv.URL, http: &http.Client{Jar: jar}}
}

type client struct {
	t    *testing.T
	base string
	http *http.Client
}

// do sendet JSON und prüft den Status; out (falls nicht nil) bekommt die Antwort.
func (c *client) do(method, path string, body any, want int, out any) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		c.t.Fatalf("%s %s: %d, erwartet %d: %s", method, path, res.StatusCode, want, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			c.t.Fatalf("%s %s: %v: %s", method, path, err, data)
		}
	}
}

func setup(c *client) {
	c.do("POST", "/api/setup", map[string]string{"email": "Ich@Example.de", "password": "geheim-genug-123", "setup_code": "test-code-1234-abcd"}, 201, nil)
}

func TestSetupAndLogin(t *testing.T) {
	_, c := testServer(t)
	c.do("GET", "/api/me", nil, 401, nil)
	var st map[string]bool
	c.do("GET", "/api/setup", nil, 200, &st)
	if !st["needed"] {
		t.Fatal("Einrichtung muss nötig sein")
	}
	c.do("POST", "/api/setup", map[string]string{"email": "a@b.de", "password": "geheim-genug-123", "setup_code": "falsch"}, 403, nil)
	setup(c)
	c.do("POST", "/api/setup", map[string]string{"email": "x@b.de", "password": "geheim-genug-123", "setup_code": "TEST-CODE-1234-ABCD"}, 409, nil)
	var me db.User
	c.do("GET", "/api/me", nil, 200, &me)
	if me.Email != "ich@example.de" {
		t.Fatalf("E-Mail nicht normalisiert: %q", me.Email)
	}
	c.do("POST", "/api/logout", nil, 204, nil)
	c.do("GET", "/api/me", nil, 401, nil)
	c.do("POST", "/api/login", map[string]string{"email": "ich@example.de", "password": "falsch-falsch"}, 401, nil)
	c.do("POST", "/api/login", map[string]string{"email": "ich@example.de", "password": "geheim-genug-123"}, 200, nil)
	c.do("POST", "/api/password", map[string]string{"current_password": "geheim-genug-123", "new_password": "kurz"}, 400, nil)
	c.do("POST", "/api/password", map[string]string{"current_password": "geheim-genug-123", "new_password": "noch-geheimer-456"}, 200, nil)
	c.do("GET", "/api/me", nil, 200, nil)

	// Ohne JSON-Content-Type keine Änderung (Schutz vor CSRF über Formulare)
	req, _ := http.NewRequest("POST", c.base+"/api/tasks", strings.NewReader(`{"week":"2026-11-09","title":"x"}`))
	req.Header.Set("Content-Type", "text/plain")
	res, _ := c.http.Do(req)
	if res.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain angenommen: %d", res.StatusCode)
	}
}

type weekResp struct {
	Start     string `json:"start"`
	ISOWeek   int    `json:"iso_week"`
	Days      []weekDay
	WeekTasks []db.Task `json:"week_tasks"`
	Budget    struct {
		Planned int64 `json:"planned_cents"`
		Spent   int64 `json:"spent_cents"`
	} `json:"budget"`
	CalendarError string `json:"calendar_error"`
}

func TestTasks(t *testing.T) {
	_, c := testServer(t)
	setup(c)
	var a, b db.Task
	c.do("POST", "/api/tasks", map[string]any{"week": "2026-11-12", "title": "  Steuer  "}, 201, &a)
	if a.Week != "2026-11-09" || a.Day != nil || a.Title != "Steuer" {
		t.Fatalf("Aufgabe: %+v", a)
	}
	c.do("POST", "/api/tasks", map[string]any{"day": "2026-11-13", "title": "Einkaufen"}, 201, &b)
	c.do("POST", "/api/tasks", map[string]any{"day": "2026-11-13", "title": ""}, 400, nil)
	c.do("PATCH", fmt.Sprintf("/api/tasks/%d", b.ID), map[string]any{"done": true}, 200, nil)

	var w weekResp
	c.do("GET", "/api/week", nil, 200, &w)
	if w.Start != "2026-11-09" || w.ISOWeek != 46 || len(w.WeekTasks) != 1 || len(w.Days[4].Tasks) != 1 || !w.Days[4].Tasks[0].Done {
		t.Fatalf("Woche: %+v", w)
	}

	// Offene Aufgaben wandern, erledigte bleiben; der Tag rückt um 7 Tage
	c.do("PATCH", fmt.Sprintf("/api/tasks/%d", b.ID), map[string]any{"done": false}, 200, nil)
	var moved map[string]int
	c.do("POST", "/api/tasks/carry-over", map[string]string{"week": "2026-11-09"}, 200, &moved)
	if moved["moved"] != 2 {
		t.Fatalf("verschoben: %v", moved)
	}
	c.do("GET", "/api/week?date=2026-11-16", nil, 200, &w)
	if len(w.WeekTasks) != 1 || len(w.Days[4].Tasks) != 1 || *w.Days[4].Tasks[0].Day != "2026-11-20" {
		t.Fatalf("nächste Woche: %+v", w)
	}
	// Tag entfernen, dann löschen
	var x db.Task
	c.do("PATCH", fmt.Sprintf("/api/tasks/%d", b.ID), map[string]any{"clear_day": true}, 200, &x)
	if x.Day != nil {
		t.Fatal("Tag nicht entfernt")
	}
	c.do("DELETE", fmt.Sprintf("/api/tasks/%d", b.ID), nil, 204, nil)
	c.do("DELETE", fmt.Sprintf("/api/tasks/%d", b.ID), nil, 404, nil)
}

type eventsResp struct {
	Events []EventOut `json:"events"`
}

func TestLocalEvents(t *testing.T) {
	_, c := testServer(t)
	setup(c)
	var created map[string]string
	// wöchentlich montags 9:00–9:30, 4-mal
	c.do("POST", "/api/events", map[string]any{"calendar": "local", "title": "Sport", "start": "2026-11-02T09:00", "end": "2026-11-02T09:30",
		"rrule": "freq=weekly;count=4"}, 201, &created)
	c.do("POST", "/api/events", map[string]any{"calendar": "local", "title": "Urlaub", "all_day": true, "start": "2026-11-12", "end": "2026-11-13"}, 201, nil)
	c.do("POST", "/api/events", map[string]any{"title": "Falsch", "start": "2026-11-12T10:00", "end": "2026-11-12T09:00"}, 400, nil)
	c.do("POST", "/api/events", map[string]any{"title": "Falsch", "start": "2026-11-12T10:00", "end": "2026-11-12T11:00", "rrule": "FREQ=HOURLY"}, 400, nil)

	var ev eventsResp
	c.do("GET", "/api/events?from=2026-11-01&to=2026-12-01", nil, 200, &ev)
	var titles []string
	for _, e := range ev.Events {
		titles = append(titles, e.Start.Format("02.01. 15:04")+" "+e.Title)
	}
	want := "02.11. 09:00 Sport|09.11. 09:00 Sport|12.11. 00:00 Urlaub|16.11. 09:00 Sport|23.11. 09:00 Sport"
	if strings.Join(titles, "|") != want {
		t.Fatalf("Termine: %v", titles)
	}

	// ein Vorkommen streichen
	var sport EventOut
	for _, e := range ev.Events {
		if e.Title == "Sport" && e.Start.Day() == 9 {
			sport = e
		}
	}
	c.do("POST", "/api/events/delete", map[string]any{"key": sport.Key, "calendar": "local", "occurrence": sport.Occurrence}, 204, nil)

	var w weekResp
	c.do("GET", "/api/week?date=2026-11-11", nil, 200, &w)
	count := 0
	for _, d := range w.Days {
		count += len(d.Events)
	}
	// Urlaub 12.+13.11. (an beiden Tagen), Sport am 9. gestrichen
	if count != 2 || len(w.Days[3].Events) != 1 || len(w.Days[4].Events) != 1 {
		t.Fatalf("Woche nach Streichen: %+v", w.Days)
	}

	// Serie ändern: Detail liefert den ersten Termin
	var detail EventOut
	c.do("GET", "/api/events?key="+sport.Key+"&calendar=local", nil, 200, &detail)
	if detail.Start.Day() != 2 || detail.RRule != "FREQ=WEEKLY;COUNT=4" {
		t.Fatalf("Detail: %+v", detail)
	}
	c.do("PUT", "/api/events", map[string]any{"key": sport.Key, "calendar": "local", "title": "Sport neu", "start": "2026-11-02T18:00",
		"end": "2026-11-02T19:00", "rrule": "FREQ=WEEKLY;COUNT=4"}, 200, nil)
	ev = eventsResp{}
	c.do("GET", "/api/events?from=2026-11-09&to=2026-11-16", nil, 200, &ev)
	// Beginn verschoben: die Streichung passt nicht mehr und entfällt
	if len(ev.Events) != 2 || ev.Events[0].Title != "Sport neu" || ev.Events[0].Start.Hour() != 18 || ev.Events[1].Occurrence != nil {
		t.Fatalf("nach Änderung: %+v", ev.Events)
	}
	c.do("POST", "/api/events/delete", map[string]any{"key": sport.Key, "calendar": "local"}, 204, nil)
	c.do("GET", "/api/events?key="+sport.Key, nil, 404, nil)
}

type budgetResp struct {
	Income        int64 `json:"income_cents"`
	Expense       int64 `json:"expense_cents"`
	Balance       int64 `json:"balance_cents"`
	Budget        int64 `json:"budget_cents"`
	Uncategorized int64 `json:"uncategorized_cents"`
	Categories    []categorySummary
	Transactions  []db.Transaction
}

func TestBudget(t *testing.T) {
	s, c := testServer(t)
	setup(c)
	var food db.Category
	c.do("POST", "/api/categories", map[string]any{"name": "Lebensmittel", "color": "#15803D", "budget_cents": 30000}, 201, &food)
	c.do("POST", "/api/categories", map[string]any{"name": "Lebensmittel"}, 409, nil)
	c.do("POST", "/api/categories", map[string]any{"name": "X", "color": "rot"}, 400, nil)

	c.do("POST", "/api/transactions", map[string]any{"kind": "expense", "amount_cents": 4250, "day": "2026-11-10", "category_id": food.ID, "note": "Markt"}, 201, nil)
	c.do("POST", "/api/transactions", map[string]any{"kind": "expense", "amount_cents": 0, "day": "2026-11-10"}, 400, nil)
	c.do("POST", "/api/transactions", map[string]any{"kind": "expense", "amount_cents": 100, "day": "2026-11-10", "category_id": 999}, 400, nil)

	// Gehalt am 1., Miete am 31. (im November am 30.) – seit Oktober
	var rent db.Recurring
	c.do("POST", "/api/recurring", map[string]any{"title": "Gehalt", "kind": "income", "amount_cents": 250000, "day": 1, "starts_on": "2026-10-01"}, 201, nil)
	c.do("POST", "/api/recurring", map[string]any{"title": "Miete", "kind": "expense", "amount_cents": 90000, "day": 31, "starts_on": "2026-10-01"}, 201, &rent)

	var b budgetResp
	c.do("GET", "/api/budget?month=2026-11", nil, 200, &b)
	// heute = 11.11.: Gehalt 1.11. gebucht, Miete 30.11. noch nicht
	if b.Income != 250000 || b.Expense != 4250 || b.Balance != 245750 || b.Budget != 30000 || b.Categories[0].SpentCents != 4250 {
		t.Fatalf("November: %+v", b)
	}
	c.do("GET", "/api/budget?month=2026-10", nil, 200, &b)
	if b.Income != 250000 || b.Expense != 90000 || b.Uncategorized != 90000 {
		t.Fatalf("Oktober (Miete am 31.10.): %+v", b)
	}

	// Gelöschte Buchung kommt nicht wieder
	var gehalt db.Transaction
	c.do("GET", "/api/budget?month=2026-11", nil, 200, &b)
	for _, tx := range b.Transactions {
		if tx.Note == "Gehalt" {
			gehalt = tx
		}
	}
	c.do("DELETE", fmt.Sprintf("/api/transactions/%d", gehalt.ID), nil, 204, nil)
	c.do("GET", "/api/budget?month=2026-11", nil, 200, &b)
	if b.Income != 0 {
		t.Fatalf("gelöschte Buchung wieder da: %+v", b)
	}

	// Monatsende: Miete wird gebucht, nur einmal
	berlin := s.loc
	s.now = func() time.Time { return time.Date(2026, 12, 2, 8, 0, 0, 0, berlin) }
	c.do("GET", "/api/budget?month=2026-11", nil, 200, &b)
	c.do("GET", "/api/budget?month=2026-11", nil, 200, &b)
	if b.Expense != 4250+90000 {
		t.Fatalf("Miete November: %+v", b)
	}

	// Woche mit Monatswechsel: Budget anteilig aus November und Dezember
	var w weekResp
	c.do("GET", "/api/week?date=2026-11-30", nil, 200, &w)
	want := int64(30000/30 + 6*(30000/31))
	if w.Budget.Planned != want || w.Budget.Spent != 90000 || w.Days[0].SpentCents != 90000 {
		t.Fatalf("Woche: geplant %d (erwartet %d), ausgegeben %d", w.Budget.Planned, want, w.Budget.Spent)
	}

	// Kategorie löschen: Buchungen bleiben ohne Kategorie
	c.do("DELETE", fmt.Sprintf("/api/categories/%d", food.ID), nil, 204, nil)
	c.do("GET", "/api/budget?month=2026-11", nil, 200, &b)
	if b.Expense != 94250 || b.Uncategorized != 94250 {
		t.Fatalf("nach Löschen der Kategorie: %+v", b)
	}
	c.do("PUT", fmt.Sprintf("/api/recurring/%d", rent.ID), map[string]any{"title": "Miete", "kind": "expense", "amount_cents": 95000, "day": 31,
		"starts_on": "2026-10-01", "ends_on": "2026-09-01"}, 400, nil)
}

func TestCalDAVNeedsKey(t *testing.T) {
	s, c := testServer(t)
	setup(c)
	s.box = nil
	c.do("PUT", "/api/caldav", map[string]string{"url": "https://mx.example.de/SOGo/dav/", "username": "a", "password": "b"}, 409, nil)
	var cfg map[string]any
	c.do("GET", "/api/caldav", nil, 200, &cfg)
	if cfg["configured"] != false {
		t.Fatalf("%v", cfg)
	}
}
