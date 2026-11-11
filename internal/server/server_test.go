package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
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
		DROP TABLE IF EXISTS transactions, recurring, categories, pockets, event_exdates, events, tasks, settings, sessions, users, schema_migrations;
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
		Planned  int64 `json:"planned_cents"`
		Spent    int64 `json:"spent_cents"`
		Budgeted int64 `json:"budgeted_cents"`
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
	// UNTIL als Datum wird bei Terminen mit Uhrzeit zum UTC-Zeitpunkt am Tagesende
	var until map[string]string
	c.do("POST", "/api/events", map[string]any{"title": "Kurs", "start": "2026-12-01T18:00", "end": "2026-12-01T19:00", "rrule": "FREQ=WEEKLY;UNTIL=20261215"}, 201, &until)
	var kurs EventOut
	c.do("GET", "/api/events?key="+until["key"], nil, 200, &kurs)
	if kurs.RRule != "FREQ=WEEKLY;UNTIL=20261215T225959Z" {
		t.Fatalf("UNTIL: %q", kurs.RRule)
	}
	c.do("POST", "/api/events/delete", map[string]any{"key": until["key"], "calendar": "local"}, 204, nil)

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
	if w.Budget.Planned != want || w.Budget.Spent != 90000 || w.Budget.Budgeted != 0 || w.Days[0].SpentCents != 90000 {
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

// Gesamtbudget: Abos & Verträge gehen anteilig ab, der Rest ist frei.
func TestBudgetTotal(t *testing.T) {
	_, c := testServer(t)
	setup(c)
	var food db.Category
	c.do("POST", "/api/categories", map[string]any{"name": "Lebensmittel", "budget_cents": 40000}, 201, &food)
	c.do("PUT", "/api/budget/total", map[string]any{"total_cents": 200000}, 200, nil)
	c.do("PUT", "/api/budget/total", map[string]any{"total_cents": -1}, 400, nil)

	// monatlich 12,99 €, jährlich 240 € (im März), vierteljährlich 30 €, Gehalt zählt nicht
	c.do("POST", "/api/recurring", map[string]any{"title": "Streaming", "kind": "expense", "amount_cents": 1299, "day": 5, "starts_on": "2026-10-05"}, 201, nil)
	c.do("POST", "/api/recurring", map[string]any{"title": "Haftpflicht", "kind": "expense", "amount_cents": 24000, "day": 15, "interval_months": 12, "starts_on": "2026-03-15"}, 201, nil)
	c.do("POST", "/api/recurring", map[string]any{"title": "Wasser", "kind": "expense", "amount_cents": 3000, "day": 1, "interval_months": 3, "starts_on": "2026-01-01"}, 201, nil)
	c.do("POST", "/api/recurring", map[string]any{"title": "Gehalt", "kind": "income", "amount_cents": 300000, "day": 1, "starts_on": "2026-11-01"}, 201, nil)
	c.do("POST", "/api/recurring", map[string]any{"title": "Falsch", "kind": "expense", "amount_cents": 100, "day": 1, "interval_months": 2, "starts_on": "2026-11-01"}, 400, nil)
	c.do("POST", "/api/transactions", map[string]any{"kind": "expense", "amount_cents": 5000, "day": "2026-11-10", "category_id": food.ID}, 201, nil)

	var b struct {
		budgetResp
		Plan      budgetPlan      `json:"plan"`
		Variable  int64           `json:"variable_cents"`
		Remaining int64           `json:"remaining_cents"`
		Contracts []contractShare `json:"contracts"`
	}
	c.do("GET", "/api/budget?month=2026-11", nil, 200, &b)
	wantContracts := int64(1299 + 2000 + 1000)
	if b.Plan.TotalCents != 200000 || b.Plan.ContractsCents != wantContracts || b.Plan.FreeCents != 200000-wantContracts ||
		b.Plan.AllocatedCents != 40000 || len(b.Contracts) != 3 {
		t.Fatalf("Plan: %+v, Verträge %d", b.Plan, len(b.Contracts))
	}
	// Gebucht im November: Streaming (5.11.) – zählt nicht noch einmal gegen den freien Teil
	if b.Variable != 5000 || b.Remaining != 200000-wantContracts-5000 || b.Expense != 1299+5000 {
		t.Fatalf("Ausgaben: variabel %d, Rest %d, gesamt %d", b.Variable, b.Remaining, b.Expense)
	}
	if b.Categories[0].SpentCents != 5000 {
		t.Fatalf("Kategorie: %+v", b.Categories[0])
	}
	// März: die Haftpflicht wurde wirklich gebucht (240 €), im Plan bleibt es bei 20 €
	c.do("GET", "/api/budget?month=2026-03", nil, 200, &b)
	// (Wasser vierteljährlich ab 01.01. ist im Januar, April, Juli, Oktober fällig – nicht im März)
	if b.Expense != 24000 || b.Plan.ContractsCents != 2000+1000 || b.Variable != 0 {
		t.Fatalf("März: gebucht %d, Plan %+v", b.Expense, b.Plan)
	}

	// Woche: frei planbar anteilig (November hat 30 Tage)
	var w weekResp
	c.do("GET", "/api/week?date=2026-11-11", nil, 200, &w)
	if w.Budget.Planned != 7*((200000-wantContracts)/30) || w.Budget.Budgeted != 5000 {
		t.Fatalf("Woche: geplant %d, gezählt %d", w.Budget.Planned, w.Budget.Budgeted)
	}

	// Gesamtbudget entfernen: wieder die Summe der Kategoriebudgets
	c.do("PUT", "/api/budget/total", map[string]any{"total_cents": 0}, 200, nil)
	c.do("GET", "/api/budget?month=2026-11", nil, 200, &b)
	if b.Plan.TotalCents != 0 || b.Plan.FreeCents != 40000 {
		t.Fatalf("ohne Gesamtbudget: %+v", b.Plan)
	}
}

func TestContractLogoAndPayment(t *testing.T) {
	_, c := testServer(t)
	setup(c)
	var x db.Recurring
	c.do("POST", "/api/recurring", map[string]any{"title": "Streaming", "kind": "expense", "amount_cents": 1299, "day": 5,
		"starts_on": "2026-11-05", "payment_method": "card", "payment_note": " Visa …1234 "}, 201, &x)
	if x.PaymentMethod != "card" || x.PaymentNote != "Visa …1234" || x.LogoVersion != 0 {
		t.Fatalf("angelegt: %+v", x)
	}
	c.do("POST", "/api/recurring", map[string]any{"title": "X", "kind": "expense", "amount_cents": 100, "day": 1,
		"starts_on": "2026-11-01", "payment_method": "bitcoin"}, 400, nil)
	c.do("PUT", fmt.Sprintf("/api/recurring/%d", x.ID), map[string]any{"title": "Streaming", "kind": "expense", "amount_cents": 1299, "day": 5,
		"starts_on": "2026-11-05", "payment_method": "card", "payment_note": "4111 1111 1111 1111"}, 400, nil)

	upload := func(ct string, body []byte, want int) {
		t.Helper()
		req, _ := http.NewRequest("PUT", fmt.Sprintf("%s/api/recurring/%d/logo", c.base, x.ID), bytes.NewReader(body))
		req.Header.Set("Content-Type", ct)
		res, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("Upload %s: %d, erwartet %d", ct, res.StatusCode, want)
		}
	}
	var img bytes.Buffer
	png.Encode(&img, testImage(300, 300))
	upload("text/plain", img.Bytes(), 415)
	upload("image/png", []byte("kein Bild"), 400)
	upload("image/png", img.Bytes(), 200)
	upload("image/png", img.Bytes(), 200)

	var list []db.Recurring
	c.do("GET", "/api/recurring", nil, 200, &list)
	if list[0].LogoVersion != 2 {
		t.Fatalf("Logo-Version %d", list[0].LogoVersion)
	}
	res, err := c.http.Get(fmt.Sprintf("%s/api/recurring/%d/logo?v=2", c.base, x.ID))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := png.Decode(res.Body)
	res.Body.Close()
	if res.Header.Get("Content-Type") != "image/png" || got == nil || got.Bounds().Dx() != 128 {
		t.Fatalf("Logo: %s %v", res.Header.Get("Content-Type"), got)
	}

	// Ändern behält das Logo
	c.do("PUT", fmt.Sprintf("/api/recurring/%d", x.ID), map[string]any{"title": "Streaming", "kind": "expense", "amount_cents": 1399, "day": 5,
		"starts_on": "2026-11-05", "payment_method": "sepa"}, 200, &x)
	if x.LogoVersion != 2 || x.PaymentMethod != "sepa" || x.PaymentNote != "" {
		t.Fatalf("nach Ändern: %+v", x)
	}
	c.do("DELETE", fmt.Sprintf("/api/recurring/%d/logo", x.ID), nil, 204, nil)
	c.do("GET", fmt.Sprintf("/api/recurring/%d/logo", x.ID), nil, 404, nil)
	c.do("GET", "/api/recurring", nil, 200, &list)
	if list[0].LogoVersion != 0 {
		t.Fatal("Logo nicht entfernt")
	}
}

// Pockets: Summe = Gesamtbudget, Verträge und Kategorien je Pocket, Warnung bei zu wenig.
func TestPockets(t *testing.T) {
	_, c := testServer(t)
	setup(c)
	c.do("PUT", "/api/budget/total", map[string]any{"total_cents": 999999}, 200, nil) // wird von den Pockets abgelöst
	var vers, haus db.Pocket
	c.do("POST", "/api/pockets", map[string]any{"name": "Versicherungen", "color": "#2563eb", "monthly_cents": 4000}, 201, &vers)
	c.do("POST", "/api/pockets", map[string]any{"name": "Haushalt", "monthly_cents": 60000}, 201, &haus)
	c.do("POST", "/api/pockets", map[string]any{"name": "Haushalt"}, 409, nil)
	c.do("PUT", "/api/budget/total", map[string]any{"total_cents": 100}, 409, nil)

	var food db.Category
	c.do("POST", "/api/categories", map[string]any{"name": "Lebensmittel", "budget_cents": 40000, "pocket_id": haus.ID}, 201, &food)
	c.do("POST", "/api/categories", map[string]any{"name": "X", "pocket_id": 999}, 400, nil)
	// Versicherungen brauchen anteilig 20 € + 25 € = 45 €, drauf gehen nur 40 €
	c.do("POST", "/api/recurring", map[string]any{"title": "Haftpflicht", "kind": "expense", "amount_cents": 24000, "day": 15,
		"interval_months": 12, "starts_on": "2027-03-15", "pocket_id": vers.ID}, 201, nil)
	c.do("POST", "/api/recurring", map[string]any{"title": "Hausrat", "kind": "expense", "amount_cents": 7500, "day": 1,
		"interval_months": 3, "starts_on": "2026-10-01", "pocket_id": vers.ID}, 201, nil)
	c.do("POST", "/api/recurring", map[string]any{"title": "Streaming", "kind": "expense", "amount_cents": 1299, "day": 5, "starts_on": "2026-10-05"}, 201, nil)
	c.do("POST", "/api/recurring", map[string]any{"title": "X", "kind": "expense", "amount_cents": 1, "day": 1, "starts_on": "2026-10-05", "pocket_id": 999}, 400, nil)
	c.do("POST", "/api/transactions", map[string]any{"kind": "expense", "amount_cents": 5000, "day": "2026-11-10", "category_id": food.ID}, 201, nil)

	var b struct {
		Plan    budgetPlan      `json:"plan"`
		Pockets []pocketSummary `json:"pockets"`
	}
	// März 2027: Haftpflicht läuft, Hausrat auch
	c.do("GET", "/api/budget?month=2027-03", nil, 200, &b)
	if !b.Plan.FromPockets || b.Plan.TotalCents != 64000 || len(b.Pockets) != 3 {
		t.Fatalf("Plan: %+v, Pockets %d", b.Plan, len(b.Pockets))
	}
	v := b.Pockets[1] // Reihenfolge wie angelegt: Versicherungen, Haushalt, Ohne Pocket
	if v.Name != "Versicherungen" {
		v = b.Pockets[0]
	}
	if v.ContractsCents != 4500 || v.Contracts != 2 || v.ShortCents != 500 || v.FreeCents != -500 {
		t.Fatalf("Versicherungen: %+v", v)
	}
	// November 2026: Haushalt mit Lebensmittel-Ausgabe, Streaming ohne Pocket
	c.do("GET", "/api/budget?month=2026-11", nil, 200, &b)
	byName := map[string]pocketSummary{}
	for _, p := range b.Pockets {
		byName[p.Name] = p
	}
	h := byName["Haushalt"]
	if h.SpentCents != 5000 || h.RemainingCents != 55000 || h.AllocatedCents != 40000 || h.ShortCents != 0 {
		t.Fatalf("Haushalt: %+v", h)
	}
	if none := byName["Ohne Pocket"]; none.ContractsCents != 1299 || none.ShortCents != 0 {
		t.Fatalf("ohne Pocket: %+v", none)
	}
	if byName["Versicherungen"].ShortCents != 0 { // Haftpflicht beginnt erst 2027
		t.Fatalf("Versicherungen Nov.: %+v", byName["Versicherungen"])
	}

	// Pocket löschen: Vertrag und Kategorie bleiben, nur ohne Pocket
	c.do("DELETE", fmt.Sprintf("/api/pockets/%d", haus.ID), nil, 204, nil)
	var cats []db.Category
	c.do("GET", "/api/categories", nil, 200, &cats)
	if cats[0].PocketID != nil {
		t.Fatal("Kategorie hängt noch am gelöschten Pocket")
	}
	// Alle Pockets weg: wieder das von Hand eingetragene Gesamtbudget
	c.do("DELETE", fmt.Sprintf("/api/pockets/%d", vers.ID), nil, 204, nil)
	c.do("GET", "/api/budget?month=2026-11", nil, 200, &b)
	if b.Plan.FromPockets || b.Plan.TotalCents != 999999 {
		t.Fatalf("ohne Pockets: %+v", b.Plan)
	}
}

func TestPocketForecastAPI(t *testing.T) {
	_, c := testServer(t) // heute: 11.11.2026
	setup(c)
	c.do("POST", "/api/pockets", map[string]any{"name": "X", "balance_cents": 100}, 400, nil)
	c.do("POST", "/api/pockets", map[string]any{"name": "X", "balance_cents": 100, "balance_on": "2026-12-01"}, 400, nil)
	c.do("POST", "/api/pockets", map[string]any{"name": "X", "deposit_day": 32}, 400, nil)
	var p db.Pocket
	c.do("POST", "/api/pockets", map[string]any{"name": "Jährlich", "monthly_cents": 5000, "balance_cents": 3000, "balance_on": "2026-11-11"}, 201, &p)
	if p.DepositDay != 1 {
		t.Fatalf("Tag des Dauerauftrags: %d", p.DepositDay)
	}
	c.do("POST", "/api/recurring", map[string]any{"title": "Haftpflicht", "kind": "expense", "amount_cents": 24000, "day": 15,
		"interval_months": 12, "starts_on": "2027-01-15", "pocket_id": p.ID}, 201, nil)

	var b struct {
		Pockets []pocketSummary `json:"pockets"`
	}
	c.do("GET", "/api/budget", nil, 200, &b)
	f := b.Pockets[0].Forecast
	if f == nil || f.ShortOn == nil || *f.ShortOn != "2027-01-15" || f.ShortCents != 11000 || f.Events != nil {
		t.Fatalf("Vorschau in der Übersicht: %+v", f)
	}
	var full pocketForecast
	c.do("GET", fmt.Sprintf("/api/pockets/%d/forecast", p.ID), nil, 200, &full)
	if len(full.Events) != 13 { // 12 Eingänge + Haftpflicht
		t.Fatalf("Posten: %d", len(full.Events))
	}
	// Stand entfernen: keine Vorschau mehr
	c.do("PUT", fmt.Sprintf("/api/pockets/%d", p.ID), map[string]any{"name": "Jährlich", "monthly_cents": 5000}, 200, nil)
	c.do("GET", fmt.Sprintf("/api/pockets/%d/forecast", p.ID), nil, 409, nil)
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
