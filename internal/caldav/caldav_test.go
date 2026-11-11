package caldav

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeSOGo bildet die Pfade von SOGo nach: /SOGo/dav/ → Principal
// /SOGo/dav/<user>/ → Kalender unter /SOGo/dav/<user>/Calendar/.
func fakeSOGo(t *testing.T) *httptest.Server {
	t.Helper()
	objects := map[string]string{
		"/SOGo/dav/eva@example.de/Calendar/personal/a.ics": "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:a\r\nDTSTART:20261111T090000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n",
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "eva@example.de" || pass != "app-passwort" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		ms := func(s string) {
			w.Header().Set("Content-Type", "text/xml; charset=utf-8")
			w.WriteHeader(http.StatusMultiStatus)
			io.WriteString(w, `<?xml version="1.0" encoding="utf-8"?><D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav" xmlns:x1="http://apple.com/ns/ical/">`+s+`</D:multistatus>`)
		}
		switch {
		case r.Method == "PROPFIND" && r.URL.Path == "/SOGo/dav/":
			ms(`<D:response><D:href>/SOGo/dav/</D:href><D:propstat><D:prop><D:current-user-principal><D:href>/SOGo/dav/eva@example.de/</D:href></D:current-user-principal></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>`)
		case r.Method == "PROPFIND" && r.URL.Path == "/SOGo/dav/eva@example.de/":
			ms(`<D:response><D:href>/SOGo/dav/eva@example.de/</D:href><D:propstat><D:prop><C:calendar-home-set><D:href>/SOGo/dav/eva@example.de/Calendar/</D:href></C:calendar-home-set></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>`)
		case r.Method == "PROPFIND" && r.URL.Path == "/SOGo/dav/eva@example.de/Calendar/":
			if r.Header.Get("Depth") != "1" {
				t.Errorf("Depth = %q", r.Header.Get("Depth"))
			}
			ms(`<D:response><D:href>/SOGo/dav/eva@example.de/Calendar/</D:href><D:propstat><D:prop><D:resourcetype><D:collection/></D:resourcetype></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>` +
				`<D:response><D:href>/SOGo/dav/eva@example.de/Calendar/personal/</D:href><D:propstat><D:prop><D:resourcetype><D:collection/><C:calendar/></D:resourcetype><D:displayname>Persönlicher Kalender</D:displayname><x1:calendar-color>#F0A23CFF</x1:calendar-color>` +
				`<C:supported-calendar-component-set><C:comp name="VEVENT"/><C:comp name="VTODO"/></C:supported-calendar-component-set><D:current-user-privilege-set><D:privilege><D:read/></D:privilege><D:privilege><D:write/></D:privilege></D:current-user-privilege-set></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>` +
				`<D:response><D:href>/SOGo/dav/eva@example.de/Calendar/feiertage/</D:href><D:propstat><D:prop><D:resourcetype><D:collection/><C:calendar/></D:resourcetype><D:displayname>Feiertage</D:displayname><D:current-user-privilege-set><D:privilege><D:read/></D:privilege></D:current-user-privilege-set></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat>` +
				`<D:propstat><D:prop><x1:calendar-color/></D:prop><D:status>HTTP/1.1 404 Not Found</D:status></D:propstat></D:response>` +
				`<D:response><D:href>/SOGo/dav/eva@example.de/Calendar/aufgaben/</D:href><D:propstat><D:prop><D:resourcetype><D:collection/><C:calendar/></D:resourcetype><D:displayname>Aufgaben</D:displayname><C:supported-calendar-component-set><C:comp name="VTODO"/></C:supported-calendar-component-set></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>`)
		case r.Method == "REPORT":
			if !strings.Contains(string(body), `start="20261109T000000Z"`) {
				t.Errorf("Zeitraum fehlt: %s", body)
			}
			ms(`<D:response><D:href>/SOGo/dav/eva@example.de/Calendar/personal/a.ics</D:href><D:propstat><D:prop><D:getetag>"gcs00000001"</D:getetag><C:calendar-data>` +
				strings.ReplaceAll(objects["/SOGo/dav/eva@example.de/Calendar/personal/a.ics"], "\r\n", "&#13;\n") + `</C:calendar-data></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>`)
		case r.Method == "PUT":
			if _, exists := objects[r.URL.Path]; exists && r.Header.Get("If-None-Match") == "*" {
				w.WriteHeader(http.StatusPreconditionFailed)
				return
			}
			if r.Header.Get("If-Match") != "" && r.Header.Get("If-Match") != `"gcs00000001"` {
				w.WriteHeader(http.StatusPreconditionFailed)
				return
			}
			objects[r.URL.Path] = string(body)
			w.WriteHeader(http.StatusCreated)
		case r.Method == "DELETE":
			delete(objects, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestDiscoverAndQuery(t *testing.T) {
	srv := fakeSOGo(t)
	defer srv.Close()
	c, err := New(strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)+"/SOGo/dav", "eva@example.de", "app-passwort")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cals, err := c.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(cals) != 2 {
		t.Fatalf("erwartet 2 Kalender (ohne reine Aufgabenliste), bekommen %+v", cals)
	}
	if cals[0].Name != "Persönlicher Kalender" || cals[0].Color != "#f0a23c" || cals[0].ReadOnly {
		t.Errorf("persönlich: %+v", cals[0])
	}
	if cals[1].Name != "Feiertage" || !cals[1].ReadOnly {
		t.Errorf("Feiertage müssen schreibgeschützt sein: %+v", cals[1])
	}

	from := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	objs, err := c.Query(ctx, cals[0].Href, from, from.AddDate(0, 0, 7))
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 1 || objs[0].ETag != `"gcs00000001"` || !strings.Contains(objs[0].Data, "UID:a") {
		t.Fatalf("Query: %+v", objs)
	}

	href := ObjectHref(cals[0].Href, "neu@nexora-plan")
	if err := c.Put(ctx, href, "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n", ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Put(ctx, href, "x", ""); err != ErrConflict {
		t.Fatalf("doppeltes Anlegen: %v", err)
	}
	if err := c.Put(ctx, objs[0].Href, "x", `"veraltet"`); err != ErrConflict {
		t.Fatalf("veralteter ETag: %v", err)
	}
	if err := c.Delete(ctx, href, ""); err != nil {
		t.Fatal(err)
	}
}

func TestWrongPassword(t *testing.T) {
	srv := fakeSOGo(t)
	defer srv.Close()
	c, _ := New(strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)+"/SOGo/dav/", "eva@example.de", "falsch")
	if _, err := c.Discover(context.Background()); err != ErrAuth {
		t.Fatalf("erwartet ErrAuth, bekommen %v", err)
	}
}

func TestNewRejects(t *testing.T) {
	for _, u := range []string{"", "ftp://x", "http://sogo.example.de/SOGo/dav/", "https://user:pw@x/", "https:///nohost"} {
		if _, err := New(u, "a", "b"); err == nil {
			t.Errorf("New(%q) ohne Fehler", u)
		}
	}
	if _, err := New("https://mx.example.de/SOGo/dav/", "a", "b"); err != nil {
		t.Error(err)
	}
}

// Zugangsdaten gehen nie an einen anderen Host – auch nicht über ein href in der Antwort.
func TestForeignHref(t *testing.T) {
	c, _ := New("https://mx.example.de/SOGo/dav/", "a", "b")
	for _, h := range []string{"https://evil.example/x", "//evil.example/x", "http://mx.example.de/x"} {
		if _, err := c.resolve(h); err == nil {
			t.Errorf("resolve(%q) erlaubt", h)
		}
	}
	if u, err := c.resolve("/SOGo/dav/a/Calendar/"); err != nil || u.Host != "mx.example.de" {
		t.Errorf("eigener Pfad: %v %v", u, err)
	}
}
