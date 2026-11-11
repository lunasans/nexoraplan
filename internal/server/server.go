// Package server enthält den HTTP-Router mit API und Web-Oberfläche.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"nexoraplan/internal/db"
	"nexoraplan/internal/secret"
	"nexoraplan/web"
)

// Version wird beim Bauen gesetzt (release.ps1: Build-Datum, z. B. 2026.11.11).
var Version = "dev"

type Server struct {
	router    http.Handler
	repo      *db.Repo
	box       *secret.Box // nil = kein Schlüssel, CalDAV-Passwort lässt sich nicht speichern
	loc       *time.Location
	limiter   *loginLimiter
	setupCode string
	setupMu   sync.Mutex
	bookMu    sync.Mutex // feste Buchungen nicht doppelt buchen
	calendar  *calendarService
	publicURL string
	now       func() time.Time
}

type Options struct {
	PublicURL string
	Location  *time.Location
	SetupCode string
	Box       *secret.Box
	// CalDAVURL: Vorschlag für die Kalender-Adresse (SOGo des Panels).
	CalDAVURL string
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

// New baut den Server mit allen Routen.
func New(repo *db.Repo, opts Options) (*Server, error) {
	u, err := url.Parse(opts.PublicURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("PLAN_PUBLIC_URL ungültig: %q", opts.PublicURL)
	}
	s := &Server{
		repo:      repo,
		box:       opts.Box,
		loc:       opts.Location,
		limiter:   newLoginLimiter(),
		setupCode: opts.SetupCode,
		publicURL: strings.TrimRight(opts.PublicURL, "/"),
		now:       time.Now,
	}
	if s.loc == nil {
		s.loc = time.Local
	}
	if s.setupCode == "" {
		s.setupCode = newSetupCode()
	}
	s.calendar = newCalendarService(repo, opts.Box, opts.CalDAVURL, s.loc)
	if n, err := repo.CountUsers(context.Background()); err == nil && n == 0 {
		slog.Warn("Noch kein Konto eingerichtet – zum Anlegen im Browser diesen Einrichtungscode eingeben",
			"einrichtungscode", s.setupCode)
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(realIP)
	r.Use(securityHeaders)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Route("/api", func(r chi.Router) {
		r.Get("/health", s.handleHealth)
		r.Get("/setup", s.handleSetupStatus)
		r.Post("/setup", s.handleSetup)
		r.Post("/login", s.handleLogin)
		r.Post("/logout", s.handleLogout)

		r.Group(func(r chi.Router) {
			r.Use(s.requireUser)
			r.Get("/me", s.handleMe)
			r.Post("/password", s.handlePasswordChange)
			r.Post("/sessions/logout-others", s.handleLogoutOthers)

			r.Get("/week", s.handleWeek)

			r.Post("/tasks", s.handleTaskCreate)
			r.Patch("/tasks/{id}", s.handleTaskUpdate)
			r.Delete("/tasks/{id}", s.handleTaskDelete)
			r.Post("/tasks/carry-over", s.handleTaskCarryOver)

			r.Get("/events", s.handleEvents)
			r.Post("/events", s.handleEventCreate)
			r.Put("/events", s.handleEventUpdate)
			r.Post("/events/delete", s.handleEventDelete)

			r.Get("/calendars", s.handleCalendars)
			r.Put("/calendars/visibility", s.handleCalendarVisibility)
			r.Put("/calendars/default", s.handleCalendarDefault)
			r.Get("/caldav", s.handleCalDAVGet)
			r.Put("/caldav", s.handleCalDAVSet)
			r.Delete("/caldav", s.handleCalDAVDelete)

			r.Get("/budget", s.handleBudget)
			r.Put("/budget/total", s.handleBudgetTotal)
			r.Get("/pockets", s.handlePockets)
			r.Post("/pockets", s.handlePocketCreate)
			r.Put("/pockets/{id}", s.handlePocketUpdate)
			r.Delete("/pockets/{id}", s.handlePocketDelete)
			r.Get("/pockets/{id}/forecast", s.handlePocketForecast)
			r.Get("/categories", s.handleCategories)
			r.Post("/categories", s.handleCategoryCreate)
			r.Put("/categories/{id}", s.handleCategoryUpdate)
			r.Delete("/categories/{id}", s.handleCategoryDelete)
			r.Post("/transactions", s.handleTransactionCreate)
			r.Put("/transactions/{id}", s.handleTransactionUpdate)
			r.Delete("/transactions/{id}", s.handleTransactionDelete)
			r.Get("/recurring", s.handleRecurringList)
			r.Post("/recurring", s.handleRecurringCreate)
			r.Put("/recurring/{id}", s.handleRecurringUpdate)
			r.Delete("/recurring/{id}", s.handleRecurringDelete)
			r.Put("/recurring/{id}/logo", s.handleRecurringLogoPut)
			r.Delete("/recurring/{id}/logo", s.handleRecurringLogoDelete)
			r.Get("/recurring/{id}/logo", s.handleRecurringLogoGet)
		})
		r.NotFound(func(w http.ResponseWriter, r *http.Request) { writeError(w, http.StatusNotFound, "Unbekannter Pfad") })
	})

	r.Handle("/*", canonicalHost(s.publicURL, spaHandler(web.Dist())))
	s.router = r
	return s, nil
}

// RunJanitor räumt stündlich abgelaufene Sitzungen auf und bucht fällige
// feste Buchungen (auch wenn niemand die App öffnet).
func (s *Server) RunJanitor(ctx context.Context) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		if err := s.repo.DeleteExpiredSessions(ctx, s.now()); err != nil && ctx.Err() == nil {
			slog.Warn("Abgelaufene Sitzungen nicht gelöscht", "fehler", err)
		}
		if err := s.bookRecurring(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("Feste Buchungen nicht gebucht", "fehler", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Server) bookRecurring(ctx context.Context) error {
	s.bookMu.Lock()
	defer s.bookMu.Unlock()
	n, err := s.repo.BookRecurring(ctx, s.today())
	if n > 0 {
		slog.Info("Feste Buchungen gebucht", "anzahl", n)
	}
	return err
}

// today: heutiges Datum in der Zeitzone der App, als UTC-Mitternacht.
func (s *Server) today() time.Time {
	y, m, d := s.now().In(s.loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	dbStatus := "ok"
	if err := s.repo.Ping(ctx); err != nil {
		dbStatus = "fehler"
	}
	n, err := s.repo.CountUsers(ctx)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "ok",
		"datenbank":    dbStatus,
		"version":      Version,
		"setup_needed": err == nil && n == 0,
	})
}

// --- JSON ------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"fehler": msg})
}

func internalError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("Interner Fehler", "pfad", r.URL.Path, "fehler", err)
	writeError(w, http.StatusInternalServerError, "Interner Fehler")
}

// dbError: nicht gefunden → 404, sonst 500.
func dbError(w http.ResponseWriter, r *http.Request, err error, what string) {
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, what+" nicht gefunden")
		return
	}
	internalError(w, r, err)
}

// decodeJSON liest den Body als JSON. Nur echte JSON-Anfragen: Fremde Seiten
// können per Formular kein application/json senden – zusammen mit dem
// SameSite-Cookie schützt das vor CSRF.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "Erwarte JSON")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "Ungültige Anfrage")
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (uint64, bool) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id == 0 {
		writeError(w, http.StatusBadRequest, "Ungültige ID")
		return 0, false
	}
	return id, true
}

// --- Client-IP --------------------------------------------------------------

func realIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip := forwardedIP(r); ip != nil {
			r.RemoteAddr = net.JoinHostPort(ip.String(), "0")
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopback(addr string) bool {
	ip := net.ParseIP(clientIPFromAddr(addr))
	return ip != nil && ip.IsLoopback()
}

func clientIPFromAddr(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

func clientIP(r *http.Request) string {
	return clientIPFromAddr(r.RemoteAddr)
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// --- Web-Oberfläche ---------------------------------------------------------

// canonicalHost leitet Aufrufe unter einer anderen Adresse auf die
// eingestellte um – das Sitzungscookie gilt nur für genau diese.
func canonicalHost(publicURL string, next http.Handler) http.Handler {
	target, _ := url.Parse(publicURL)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Host, target.Host) {
			http.Redirect(w, r, target.Scheme+"://"+target.Host+r.URL.RequestURI(), http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// spaHandler liefert vorhandene Dateien aus, für alle anderen Pfade
// index.html – das Routing übernimmt die Oberfläche.
func spaHandler(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if st, err := fs.Stat(fsys, p); err != nil || st.IsDir() || p == "index.html" {
			if _, err := fs.Stat(fsys, "index.html"); err != nil {
				http.Error(w, "Oberfläche nicht gebaut (npm run build im Ordner frontend)", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, fsys, "index.html")
			return
		}
		if strings.HasPrefix(p, "_app/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}
