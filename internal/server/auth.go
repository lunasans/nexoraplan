package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"nexoraplan/internal/db"
	"nexoraplan/internal/password"
)

const (
	sessionCookie   = "nexora_plan_session"
	sessionDuration = 30 * 24 * time.Hour
	msgTooMany      = "Zu viele Fehlversuche. Bitte in 15 Minuten erneut versuchen."
)

// dummyHash wird bei unbekannter E-Mail geprüft – die Antwort dauert gleich lang.
var dummyHash = password.Hash("nexora-plan-dummy-ohne-konto")

type ctxKey struct{}

func userFrom(r *http.Request) db.User {
	return r.Context().Value(ctxKey{}).(db.User)
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (c *credentials) normalize() { c.Email = strings.ToLower(strings.TrimSpace(c.Email)) }

func validatePassword(pw string) string {
	switch {
	case utf8.RuneCountInString(pw) < 10:
		return "Das Passwort muss mindestens 10 Zeichen lang sein"
	case len(pw) > 1024:
		return "Das Passwort ist zu lang"
	}
	return ""
}

func (c credentials) validate() string {
	if len(c.Email) > 255 || !strings.Contains(c.Email, "@") {
		return "Bitte eine gültige E-Mail-Adresse angeben"
	}
	return validatePassword(c.Password)
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.repo.CountUsers(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"needed": n == 0})
}

// newSetupCode erzeugt z. B. „K7QF-2MXA-PL4D-9TRE“.
func newSetupCode() string {
	raw := make([]byte, 10)
	rand.Read(raw)
	code := base32.StdEncoding.EncodeToString(raw)
	return code[0:4] + "-" + code[4:8] + "-" + code[8:12] + "-" + code[12:16]
}

func normalizeSetupCode(code string) string {
	return strings.NewReplacer("-", "", " ", "").Replace(strings.ToUpper(strings.TrimSpace(code)))
}

// POST /api/setup {"email", "password", "setup_code"} – legt das einzige Konto an.
// Der Code steht beim Start im Log: Wer die Adresse vor dem Besitzer aufruft,
// kann die Installation so nicht übernehmen.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.limiter.allow(ip) {
		writeError(w, http.StatusTooManyRequests, msgTooMany)
		return
	}
	var req struct {
		credentials
		SetupCode string `json:"setup_code"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	c := req.credentials
	c.normalize()
	if msg := c.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if subtle.ConstantTimeCompare([]byte(normalizeSetupCode(req.SetupCode)), []byte(normalizeSetupCode(s.setupCode))) != 1 {
		s.limiter.fail(ip)
		slog.Warn("Anmeldung fehlgeschlagen: falscher Einrichtungscode", "ip", ip)
		writeError(w, http.StatusForbidden, "Der Einrichtungscode stimmt nicht. Das Panel zeigt ihn unter Domain → Apps → nexora/plan („Ersteinrichtung“).")
		return
	}
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	n, err := s.repo.CountUsers(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	if n > 0 {
		writeError(w, http.StatusConflict, "nexora/plan ist bereits eingerichtet")
		return
	}
	u, err := s.repo.CreateUser(r.Context(), c.Email, password.Hash(c.Password))
	if err != nil {
		internalError(w, r, err)
		return
	}
	if err := s.startSession(w, r, u); err != nil {
		internalError(w, r, err)
		return
	}
	slog.Info("Konto angelegt", "email", u.Email)
	writeJSON(w, http.StatusCreated, u)
}

// POST /api/login
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.limiter.allow(ip) {
		writeError(w, http.StatusTooManyRequests, msgTooMany)
		return
	}
	var c credentials
	if !decodeJSON(w, r, &c) {
		return
	}
	c.normalize()
	u, err := s.repo.UserByEmail(r.Context(), c.Email)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		internalError(w, r, err)
		return
	}
	hash := dummyHash
	if err == nil {
		hash = u.PasswordHash
	}
	if !password.Verify(c.Password, hash) || err != nil {
		s.limiter.fail(ip)
		// Diese Zeile liest Fail2Ban (Filter nexora-nexoracloud im Panel).
		slog.Warn("Anmeldung fehlgeschlagen", "ip", ip)
		writeError(w, http.StatusUnauthorized, "E-Mail oder Passwort falsch")
		return
	}
	s.limiter.reset(ip)
	if password.NeedsRehash(u.PasswordHash) {
		if err := s.repo.SetPasswordHash(r.Context(), u.ID, password.Hash(c.Password)); err != nil {
			slog.Warn("Passwort-Hash nicht erneuert", "fehler", err)
		}
	}
	if err := s.startSession(w, r, u); err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.repo.DeleteSession(r.Context(), hashToken(c.Value)); err != nil {
			internalError(w, r, err)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, userFrom(r))
}

// POST /api/password {"current_password", "new_password"} – meldet alle anderen Sitzungen ab.
func (s *Server) handlePasswordChange(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.limiter.allow(ip) {
		writeError(w, http.StatusTooManyRequests, msgTooMany)
		return
	}
	u := userFrom(r)
	var req struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !password.Verify(req.Current, u.PasswordHash) {
		s.limiter.fail(ip)
		writeError(w, http.StatusForbidden, "Das bisherige Passwort stimmt nicht")
		return
	}
	if msg := validatePassword(req.New); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.repo.SetPasswordHash(r.Context(), u.ID, password.Hash(req.New)); err != nil {
		internalError(w, r, err)
		return
	}
	n, err := s.repo.DeleteOtherSessions(r.Context(), u.ID, currentSession(r))
	if err != nil {
		internalError(w, r, err)
		return
	}
	slog.Info("Passwort geändert", "abgemeldet", n)
	writeJSON(w, http.StatusOK, map[string]int64{"signed_out": n})
}

func (s *Server) handleLogoutOthers(w http.ResponseWriter, r *http.Request) {
	n, err := s.repo.DeleteOtherSessions(r.Context(), userFrom(r).ID, currentSession(r))
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"signed_out": n})
}

func currentSession(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return hashToken(c.Value)
}

// requireUser lässt nur Anfragen mit gültiger Sitzung durch.
func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := currentSession(r)
		if id == "" {
			writeError(w, http.StatusUnauthorized, "Nicht angemeldet")
			return
		}
		u, err := s.repo.UserBySession(r.Context(), id, s.now())
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "Nicht angemeldet")
			return
		}
		if err != nil {
			internalError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	})
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u db.User) error {
	raw := make([]byte, 32)
	rand.Read(raw)
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := s.now().Add(sessionDuration)
	if err := s.repo.CreateSession(r.Context(), hashToken(token), u.ID, expires); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteStrictMode,
	})
	return nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
