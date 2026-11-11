package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nexoraplan/internal/telegram"
)

const fakeToken = "123456789:AAEhBP0av28yXfJpQdZXp9ADy-THISISFAKE"

// fakeTelegram bildet getMe, getUpdates und sendMessage nach und merkt sich die Nachrichten.
type fakeTelegram struct {
	mu       sync.Mutex
	sent     []string
	started  bool // hat der Nutzer /start geschickt?
	failSend bool
}

func (f *fakeTelegram) texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

func startFakeTelegram(t *testing.T) *fakeTelegram {
	f := &fakeTelegram{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/bot"+fakeToken+"/") {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch strings.TrimPrefix(r.URL.Path, "/bot"+fakeToken+"/") {
		case "getMe":
			w.Write([]byte(`{"ok":true,"result":{"username":"mein_plan_bot"}}`))
		case "getUpdates":
			if f.started {
				w.Write([]byte(`{"ok":true,"result":[{"update_id":1,"message":{"chat":{"id":4242,"type":"private","first_name":"Eva","username":"eva"},"text":"/start"}}]}`))
			} else {
				w.Write([]byte(`{"ok":true,"result":[]}`))
			}
		case "sendMessage":
			var m struct {
				ChatID string `json:"chat_id"`
				Text   string `json:"text"`
			}
			json.NewDecoder(r.Body).Decode(&m)
			if f.failSend || m.ChatID != "4242" {
				w.Write([]byte(`{"ok":false,"description":"Bad Request: chat not found"}`))
				return
			}
			f.sent = append(f.sent, m.Text)
			w.Write([]byte(`{"ok":true,"result":{}}`))
		}
	}))
	old := telegram.API
	telegram.API = srv.URL
	t.Cleanup(func() { telegram.API = old; srv.Close() })
	return f
}

func TestTelegramNotifications(t *testing.T) {
	s, c := testServer(t) // heute: Mittwoch, 11.11.2026, 10:00
	setup(c)
	tg := startFakeTelegram(t)
	ctx := t.Context()

	// Ohne Verbindung passiert nichts
	if err := s.notifyTick(ctx); err != nil || len(tg.texts()) != 0 {
		t.Fatalf("ohne Telegram: %v %v", err, tg.texts())
	}

	c.do("PUT", "/api/notifications/telegram", map[string]string{"token": "kein-token"}, 400, nil)
	c.do("PUT", "/api/notifications/telegram", map[string]string{"token": "123456789:AAEhBP0av28yXfJpQdZXp9ADy-WRONGTOKEN0"}, 502, nil)
	var me map[string]string
	c.do("PUT", "/api/notifications/telegram", map[string]string{"token": fakeToken}, 200, &me)
	if me["bot"] != "mein_plan_bot" {
		t.Fatalf("Bot: %v", me)
	}
	c.do("POST", "/api/notifications/telegram/link", nil, 415, nil)
	c.do("POST", "/api/notifications/telegram/link", map[string]any{}, 409, nil) // noch kein /start
	tg.started = true
	var linked map[string]string
	c.do("POST", "/api/notifications/telegram/link", map[string]any{}, 200, &linked)
	if linked["chat_name"] != "Eva (@eva)" || len(tg.texts()) != 1 || !strings.Contains(tg.texts()[0], "verbunden") {
		t.Fatalf("Verknüpfen: %v %v", linked, tg.texts())
	}
	var info map[string]any
	c.do("GET", "/api/notifications", nil, 200, &info)
	if info["linked"] != true || info["has_token"] != true || strings.Contains(mustJSON(info), fakeToken) {
		t.Fatalf("Info (Token darf nie zurückkommen): %v", info)
	}

	// Daten: Termin heute, Aufgabe heute, Abbuchung morgen, Kategorie überzogen
	var food struct{ ID uint64 }
	c.do("POST", "/api/categories", map[string]any{"name": "Lebensmittel", "budget_cents": 1000}, 201, &food)
	c.do("POST", "/api/transactions", map[string]any{"kind": "expense", "amount_cents": 1500, "day": "2026-11-10", "category_id": food.ID}, 201, nil)
	c.do("POST", "/api/recurring", map[string]any{"title": "Haftpflicht", "kind": "expense", "amount_cents": 24000, "day": 12,
		"interval_months": 12, "starts_on": "2026-11-12", "payment_method": "sepa"}, 201, nil)
	c.do("POST", "/api/events", map[string]any{"title": "Zahnarzt", "location": "Praxis", "start": "2026-11-11T15:00", "end": "2026-11-11T16:00"}, 201, nil)
	c.do("POST", "/api/tasks", map[string]any{"day": "2026-11-11", "title": "Paket abholen"}, 201, nil)

	// Alles aus: nichts
	tg.mu.Lock()
	tg.sent = nil
	tg.mu.Unlock()
	if err := s.notifyTick(ctx); err != nil || len(tg.texts()) != 0 {
		t.Fatalf("alles aus: %v %v", err, tg.texts())
	}
	c.do("PUT", "/api/notifications", map[string]any{"payments": true, "payment_days": 2, "alerts": true, "digest": true, "time": "07:00"}, 400, nil)
	c.do("PUT", "/api/notifications", map[string]any{"payments": true, "payment_days": 1, "alerts": true, "digest": true, "time": "7 Uhr"}, 400, nil)
	c.do("PUT", "/api/notifications", map[string]any{"payments": true, "payment_days": 1, "alerts": true, "digest": true, "time": "11:30"}, 200, nil)

	// 10:00 – vor der eingestellten Zeit
	if err := s.notifyTick(ctx); err != nil || len(tg.texts()) != 0 {
		t.Fatalf("vor 11:30: %v %v", err, tg.texts())
	}
	c.do("PUT", "/api/notifications", map[string]any{"payments": true, "payment_days": 1, "alerts": true, "digest": true, "time": "07:00"}, 200, nil)
	if err := s.notifyTick(ctx); err != nil {
		t.Fatal(err)
	}
	got := tg.texts()
	if len(got) != 3 {
		t.Fatalf("erwartet 3 Nachrichten, bekommen %d: %q", len(got), got)
	}
	for _, want := range []string{"Mittwoch, 11.11.2026 (KW 46)", "15:00–16:00 Zahnarzt (Praxis)", "• Paket abholen"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("Tagesübersicht ohne %q:\n%s", want, got[0])
		}
	}
	if !strings.Contains(got[1], "Morgen (Donnerstag, 12.11.2026) wird abgebucht") || !strings.Contains(got[1], "Haftpflicht 240,00 € · SEPA-Lastschrift") {
		t.Errorf("Abbuchung:\n%s", got[1])
	}
	if !strings.Contains(got[2], "Kategorie Lebensmittel: 15,00 € von 10,00 € ausgegeben (5,00 € drüber)") {
		t.Errorf("Warnung:\n%s", got[2])
	}

	// Gleicher Tag: nichts mehr
	s.notifyTick(ctx)
	if len(tg.texts()) != 3 {
		t.Fatalf("doppelt gesendet: %q", tg.texts())
	}
	// Nächster Tag: Übersicht ja, dieselbe Warnung nicht noch einmal
	berlin := s.loc
	s.now = func() time.Time { return time.Date(2026, 11, 12, 7, 5, 0, 0, berlin) }
	s.notifyTick(ctx)
	got = tg.texts()[3:]
	if len(got) != 1 || !strings.Contains(got[0], "Heute abgebucht:\n• Haftpflicht") {
		t.Fatalf("nächster Tag: %q", got)
	}

	// Warnung kommt nach fehlgeschlagenem Senden später doch noch
	if !strings.Contains(tg.texts()[2], "Budget November überschritten") {
		t.Errorf("Budget-Warnung fehlt am 11.11.:\n%s", tg.texts()[2])
	}
	var fun struct{ ID uint64 }
	c.do("POST", "/api/categories", map[string]any{"name": "Freizeit", "budget_cents": 2000}, 201, &fun)
	c.do("POST", "/api/transactions", map[string]any{"kind": "expense", "amount_cents": 3000, "day": "2026-11-12", "category_id": fun.ID}, 201, nil)
	tg.failSend = true
	s.now = func() time.Time { return time.Date(2026, 11, 13, 8, 0, 0, 0, berlin) }
	if err := s.notifyTick(ctx); err == nil {
		t.Fatal("Fehler beim Senden muss gemeldet werden")
	}
	tg.failSend = false
	s.now = func() time.Time { return time.Date(2026, 11, 14, 8, 0, 0, 0, berlin) }
	s.notifyTick(ctx)
	last := tg.texts()[len(tg.texts())-1]
	if !strings.Contains(last, "Kategorie Freizeit") || strings.Contains(last, "Lebensmittel") {
		t.Fatalf("Warnung nicht nachgeholt: %q", last)
	}

	// Jetzt senden (Test) und trennen
	var sent map[string]string
	c.do("POST", "/api/notifications/send", map[string]string{"kind": "payments"}, 200, &sent)
	c.do("DELETE", "/api/notifications/telegram", nil, 204, nil)
	c.do("POST", "/api/notifications/send", map[string]string{"kind": "digest"}, 409, nil)
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestEuro(t *testing.T) {
	for in, want := range map[int64]string{0: "0,00 €", 5: "0,05 €", 123456: "1.234,56 €", 100000000: "1.000.000,00 €", -1850: "−18,50 €"} {
		if got := euro(in); got != want {
			t.Errorf("euro(%d) = %q, erwartet %q", in, got, want)
		}
	}
}
