// Package caldav spricht mit einem CalDAV-Server (RFC 4791), vor allem mit
// SOGo aus NexoraPanel: Kalender finden, Termine eines Zeitraums laden,
// Termine anlegen, ändern und löschen.
package caldav

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

// Client ist an genau einen Server gebunden: Zugangsdaten gehen nur an den
// Host der eingestellten Adresse, nie an einen anderen.
type Client struct {
	base *url.URL
	user string
	pass string
	http *http.Client
}

type Calendar struct {
	Href     string `json:"href"`
	Name     string `json:"name"`
	Color    string `json:"color"`
	ReadOnly bool   `json:"read_only"`
}

type Object struct {
	Href string
	ETag string
	Data string
}

var (
	ErrAuth     = errors.New("Anmeldung am Kalender fehlgeschlagen – Benutzer oder Passwort falsch")
	ErrNotFound = errors.New("Termin auf dem Kalenderserver nicht gefunden")
	ErrConflict = errors.New("Der Termin wurde inzwischen woanders geändert – bitte neu laden")
)

// New prüft die Adresse. Erlaubt ist nur https (http nur für localhost, zum Testen).
func New(rawURL, user, pass string) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("Kalender-Adresse ungültig")
	}
	local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return nil, errors.New("Kalender-Adresse muss mit https:// beginnen")
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	c := &Client{base: u, user: user, pass: pass}
	c.http = &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("zu viele Weiterleitungen")
			}
			if req.URL.Host != u.Host || req.URL.Scheme != u.Scheme {
				return fmt.Errorf("Kalenderserver leitet auf %s weiter – bitte diese Adresse eintragen", req.URL.Host)
			}
			return nil
		},
	}
	return c, nil
}

// resolve macht aus einem href eine Adresse auf dem eingestellten Server.
func (c *Client) resolve(href string) (*url.URL, error) {
	ref, err := url.Parse(href)
	if err != nil {
		return nil, err
	}
	u := c.base.ResolveReference(ref)
	if u.Host != c.base.Host || u.Scheme != c.base.Scheme {
		return nil, errors.New("Adresse auf fremdem Server")
	}
	return u, nil
}

func (c *Client) do(ctx context.Context, method, href string, body []byte, header map[string]string) (*http.Response, error) {
	u, err := c.resolve(href)
	if err != nil {
		return nil, err
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.user, c.pass)
	req.Header.Set("User-Agent", "nexora-plan")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Kalenderserver nicht erreichbar: %w", err)
	}
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		res.Body.Close()
		if res.StatusCode == http.StatusForbidden && method != "PROPFIND" {
			return nil, errors.New("Keine Berechtigung für diesen Kalender")
		}
		return nil, ErrAuth
	}
	return res, nil
}

const xmlHeader = `<?xml version="1.0" encoding="utf-8"?>` + "\n"

func (c *Client) propfind(ctx context.Context, href, depth, props string) (*multistatus, error) {
	body := xmlHeader + `<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav" xmlns:A="http://apple.com/ns/ical/"><D:prop>` +
		props + `</D:prop></D:propfind>`
	return c.multistatus(ctx, "PROPFIND", href, depth, body)
}

func (c *Client) multistatus(ctx context.Context, method, href, depth, body string) (*multistatus, error) {
	res, err := c.do(ctx, method, href, []byte(body), map[string]string{
		"Depth": depth, "Content-Type": "application/xml; charset=utf-8",
	})
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMultiStatus {
		return nil, fmt.Errorf("Kalenderserver antwortet auf %s mit %s", method, res.Status)
	}
	var ms multistatus
	if err := xml.NewDecoder(io.LimitReader(res.Body, 32<<20)).Decode(&ms); err != nil {
		return nil, fmt.Errorf("Antwort des Kalenderservers unlesbar: %w", err)
	}
	return &ms, nil
}

// Discover sucht die Kalender des Kontos: current-user-principal →
// calendar-home-set → Sammlungen darin (RFC 6764 / RFC 4791).
func (c *Client) Discover(ctx context.Context) ([]Calendar, error) {
	principal, err := c.principal(ctx, c.base.Path)
	if err != nil {
		return nil, err
	}
	if principal == "" {
		// Server-Wurzel ohne Antwort: /.well-known/caldav probieren
		if p, err := c.principal(ctx, "/.well-known/caldav"); err == nil && p != "" {
			principal = p
		}
	}
	home := c.base.Path
	if principal != "" {
		ms, err := c.propfind(ctx, principal, "0", `<C:calendar-home-set/>`)
		if err != nil {
			return nil, err
		}
		for _, r := range ms.Responses {
			if p := r.ok(); p != nil && p.CalendarHomeSet.Href != "" {
				home = strings.TrimSpace(p.CalendarHomeSet.Href)
			}
		}
	}
	ms, err := c.propfind(ctx, home, "1",
		`<D:resourcetype/><D:displayname/><A:calendar-color/><C:supported-calendar-component-set/><D:current-user-privilege-set/>`)
	if err != nil {
		return nil, err
	}
	var cals []Calendar
	for _, r := range ms.Responses {
		p := r.ok()
		if p == nil || p.ResourceType.Calendar == nil {
			continue
		}
		if !p.Components.has("VEVENT") {
			continue // reine Aufgabenlisten
		}
		name := strings.TrimSpace(p.DisplayName)
		if name == "" {
			name = path.Base(strings.TrimSuffix(r.Href, "/"))
		}
		cals = append(cals, Calendar{
			Href:     strings.TrimSpace(r.Href),
			Name:     name,
			Color:    normalizeColor(p.Color),
			ReadOnly: p.Privileges.present && !p.Privileges.canWrite(),
		})
	}
	if len(cals) == 0 {
		return nil, errors.New("Keine Kalender gefunden – stimmt die Adresse?")
	}
	return cals, nil
}

func (c *Client) principal(ctx context.Context, href string) (string, error) {
	ms, err := c.propfind(ctx, href, "0", `<D:current-user-principal/>`)
	if err != nil {
		return "", err
	}
	for _, r := range ms.Responses {
		if p := r.ok(); p != nil && p.CurrentUserPrincipal.Href != "" {
			return strings.TrimSpace(p.CurrentUserPrincipal.Href), nil
		}
	}
	return "", nil
}

// Query lädt alle Termine eines Kalenders, die den Zeitraum berühren
// (Serien ganz, mit allen Ausnahmen – aufgelöst wird in der App).
func (c *Client) Query(ctx context.Context, calendar string, from, to time.Time) ([]Object, error) {
	body := xmlHeader + `<C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">` +
		`<D:prop><D:getetag/><C:calendar-data/></D:prop>` +
		`<C:filter><C:comp-filter name="VCALENDAR"><C:comp-filter name="VEVENT">` +
		`<C:time-range start="` + from.UTC().Format("20060102T150405Z") + `" end="` + to.UTC().Format("20060102T150405Z") + `"/>` +
		`</C:comp-filter></C:comp-filter></C:filter></C:calendar-query>`
	ms, err := c.multistatus(ctx, "REPORT", calendar, "1", body)
	if err != nil {
		return nil, err
	}
	var out []Object
	for _, r := range ms.Responses {
		if p := r.ok(); p != nil && p.CalendarData != "" {
			out = append(out, Object{Href: strings.TrimSpace(r.Href), ETag: p.ETag, Data: p.CalendarData})
		}
	}
	return out, nil
}

// Get lädt ein Kalenderobjekt samt ETag.
func (c *Client) Get(ctx context.Context, href string) (Object, error) {
	res, err := c.do(ctx, "GET", href, nil, nil)
	if err != nil {
		return Object{}, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return Object{}, ErrNotFound
	}
	if res.StatusCode != http.StatusOK {
		return Object{}, fmt.Errorf("Kalenderserver antwortet mit %s", res.Status)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return Object{}, err
	}
	return Object{Href: href, ETag: res.Header.Get("ETag"), Data: string(data)}, nil
}

// Put speichert ein Kalenderobjekt. etag "" legt es neu an (darf noch nicht
// existieren), sonst nur, wenn es seit dem Laden unverändert ist.
func (c *Client) Put(ctx context.Context, href, data, etag string) error {
	h := map[string]string{"Content-Type": "text/calendar; charset=utf-8"}
	if etag == "" {
		h["If-None-Match"] = "*"
	} else {
		h["If-Match"] = etag
	}
	res, err := c.do(ctx, "PUT", href, []byte(data), h)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusPreconditionFailed:
		return ErrConflict
	case res.StatusCode >= 200 && res.StatusCode < 300:
		return nil
	}
	msg, _ := io.ReadAll(io.LimitReader(res.Body, 500))
	return fmt.Errorf("Kalenderserver lehnt den Termin ab: %s %s", res.Status, strings.TrimSpace(stripTags(string(msg))))
}

// Delete löscht ein Kalenderobjekt, wenn es seit dem Laden unverändert ist.
func (c *Client) Delete(ctx context.Context, href, etag string) error {
	h := map[string]string{}
	if etag != "" {
		h["If-Match"] = etag
	}
	res, err := c.do(ctx, "DELETE", href, nil, h)
	if err != nil {
		return err
	}
	res.Body.Close()
	switch {
	case res.StatusCode == http.StatusPreconditionFailed:
		return ErrConflict
	case res.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case res.StatusCode >= 200 && res.StatusCode < 300:
		return nil
	}
	return fmt.Errorf("Kalenderserver lehnt das Löschen ab: %s", res.Status)
}

// ObjectHref: Adresse für einen neuen Termin im Kalender.
func ObjectHref(calendar, uid string) string {
	name := regexp.MustCompile(`[^A-Za-z0-9._-]`).ReplaceAllString(uid, "-")
	return strings.TrimSuffix(calendar, "/") + "/" + name + ".ics"
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

func stripTags(s string) string { return tagRe.ReplaceAllString(s, " ") }

// normalizeColor macht aus SOGos „#AABBCCFF“ ein „#aabbcc“.
func normalizeColor(c string) string {
	c = strings.TrimSpace(c)
	if !regexp.MustCompile(`^#[0-9A-Fa-f]{6}([0-9A-Fa-f]{2})?$`).MatchString(c) {
		return ""
	}
	return strings.ToLower(c[:7])
}

// --- XML ---------------------------------------------------------------------

type multistatus struct {
	Responses []response `xml:"DAV: response"`
}

type response struct {
	Href      string     `xml:"DAV: href"`
	Propstats []propstat `xml:"DAV: propstat"`
}

// ok liefert die Eigenschaften mit Status 200 (fehlende kommen als 404).
func (r response) ok() *prop {
	for i := range r.Propstats {
		if strings.Contains(r.Propstats[i].Status, " 200") {
			return &r.Propstats[i].Prop
		}
	}
	return nil
}

type propstat struct {
	Status string `xml:"DAV: status"`
	Prop   prop   `xml:"DAV: prop"`
}

type hrefProp struct {
	Href string `xml:"DAV: href"`
}

type prop struct {
	CurrentUserPrincipal hrefProp `xml:"DAV: current-user-principal"`
	CalendarHomeSet      hrefProp `xml:"urn:ietf:params:xml:ns:caldav calendar-home-set"`
	ResourceType         struct {
		Calendar *struct{} `xml:"urn:ietf:params:xml:ns:caldav calendar"`
	} `xml:"DAV: resourcetype"`
	DisplayName  string       `xml:"DAV: displayname"`
	Color        string       `xml:"http://apple.com/ns/ical/ calendar-color"`
	Components   componentSet `xml:"urn:ietf:params:xml:ns:caldav supported-calendar-component-set"`
	Privileges   privilegeSet `xml:"DAV: current-user-privilege-set"`
	ETag         string       `xml:"DAV: getetag"`
	CalendarData string       `xml:"urn:ietf:params:xml:ns:caldav calendar-data"`
}

type componentSet struct {
	present bool
	Comp    []struct {
		Name string `xml:"name,attr"`
	} `xml:"urn:ietf:params:xml:ns:caldav comp"`
}

func (s *componentSet) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	type plain componentSet
	s.present = true
	return d.DecodeElement((*plain)(s), &start)
}

// has: fehlt die Angabe, kann der Kalender alles.
func (s componentSet) has(name string) bool {
	if !s.present || len(s.Comp) == 0 {
		return true
	}
	for _, c := range s.Comp {
		if strings.EqualFold(c.Name, name) {
			return true
		}
	}
	return false
}

type privilegeSet struct {
	present   bool
	Privilege []struct {
		All          *struct{} `xml:"DAV: all"`
		Write        *struct{} `xml:"DAV: write"`
		WriteContent *struct{} `xml:"DAV: write-content"`
		Bind         *struct{} `xml:"DAV: bind"`
	} `xml:"DAV: privilege"`
}

func (s *privilegeSet) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	type plain privilegeSet
	s.present = true
	return d.DecodeElement((*plain)(s), &start)
}

func (s privilegeSet) canWrite() bool {
	for _, p := range s.Privilege {
		if p.All != nil || p.Write != nil || p.WriteContent != nil || p.Bind != nil {
			return true
		}
	}
	return false
}
