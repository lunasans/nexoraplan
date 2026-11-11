// Package sso meldet Benutzer über nexora/auth an (OpenID Connect,
// Authorization Code mit PKCE). Gleicher Code in nexora/plan, nexora/short
// und NexoraCloud – Änderungen bitte in allen dreien nachziehen.
//
// Ablauf: GET /api/oidc/login → nexora/auth → GET /api/oidc/callback. Die App
// ordnet die Identität selbst einem ihrer Konten zu (E-Mail bzw. Benutzername)
// und startet ihre normale Sitzung. Konten legt sie dabei nie neu an.
package sso

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	CallbackPath = "/api/oidc/callback"
	cookieName   = "nexora_sso"
	cookiePath   = "/api/oidc"
	flowTimeout  = 10 * time.Minute
	maxFlows     = 10000
)

// Identity: was nexora/auth über den Benutzer bestätigt.
type Identity struct {
	Subject       string
	Email         string
	EmailVerified bool
	Username      string
	Name          string
	MFA           bool // nexora/auth hat zwei Faktoren geprüft
}

type flow struct {
	nonce    string
	verifier string
	expires  time.Time
}

// Client spricht mit einer nexora/auth-Installation. nil = nicht eingerichtet.
type Client struct {
	issuer   string
	oauth    oauth2.Config
	secure   bool
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	mu       sync.Mutex
	flows    map[string]flow
	// HTTPClient für Discovery/Token (Tests); nil = http.DefaultClient
	HTTPClient *http.Client
}

// New liefert nil, wenn issuer, clientID oder secret fehlen.
func New(issuer, clientID, secret, publicURL string) *Client {
	if issuer == "" || clientID == "" || secret == "" {
		return nil
	}
	publicURL = strings.TrimRight(publicURL, "/")
	return &Client{
		issuer: strings.TrimRight(issuer, "/"),
		oauth: oauth2.Config{
			ClientID: clientID, ClientSecret: secret,
			RedirectURL: publicURL + CallbackPath,
			Scopes:      []string{oidc.ScopeOpenID, "profile", "email"},
		},
		secure: strings.HasPrefix(publicURL, "https://"),
		flows:  map[string]flow{},
	}
}

func (c *Client) Enabled() bool { return c != nil }

func (c *Client) ctx(ctx context.Context) context.Context {
	if c.HTTPClient != nil {
		return oidc.ClientContext(ctx, c.HTTPClient)
	}
	return ctx
}

// setup holt beim ersten Gebrauch die Discovery – nexora/auth muss beim Start
// der App noch nicht laufen. Schlägt es fehl, wird es beim nächsten Mal erneut versucht.
func (c *Client) setup(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.provider != nil {
		return nil
	}
	p, err := oidc.NewProvider(c.ctx(ctx), c.issuer)
	if err != nil {
		return fmt.Errorf("nexora/auth nicht erreichbar: %w", err)
	}
	c.provider = p
	c.oauth.Endpoint = p.Endpoint()
	c.verifier = p.Verifier(&oidc.Config{ClientID: c.oauth.ClientID})
	return nil
}

func random() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Start leitet zu nexora/auth weiter.
func (c *Client) Start(w http.ResponseWriter, r *http.Request) error {
	if err := c.setup(r.Context()); err != nil {
		return err
	}
	state, f := random(), flow{nonce: random(), verifier: oauth2.GenerateVerifier(), expires: time.Now().Add(flowTimeout)}
	c.mu.Lock()
	now := time.Now()
	for k, v := range c.flows {
		if now.After(v.expires) {
			delete(c.flows, k)
		}
	}
	if len(c.flows) >= maxFlows {
		c.mu.Unlock()
		return errors.New("zu viele offene Anmeldungen – bitte gleich noch einmal versuchen")
	}
	c.flows[state] = f
	c.mu.Unlock()

	// SameSite=Lax: Die Rückkehr von nexora/auth ist eine Navigation von
	// einer anderen Seite – dabei muss das Cookie mitkommen.
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: state, Path: cookiePath, MaxAge: int(flowTimeout.Seconds()),
		HttpOnly: true, Secure: c.secure, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, c.oauth.AuthCodeURL(state, oidc.Nonce(f.nonce), oauth2.S256ChallengeOption(f.verifier)), http.StatusFound)
	return nil
}

// Finish prüft die Rückkehr von nexora/auth und liefert die Identität.
func (c *Client) Finish(w http.ResponseWriter, r *http.Request) (Identity, error) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: cookiePath, MaxAge: -1,
		HttpOnly: true, Secure: c.secure, SameSite: http.SameSiteLaxMode})
	q := r.URL.Query()
	cookie, err := r.Cookie(cookieName)
	state := q.Get("state")
	if err != nil || state == "" || cookie.Value != state {
		return Identity{}, errors.New("Die Anmeldung ist abgelaufen. Bitte noch einmal versuchen.")
	}
	c.mu.Lock()
	f, ok := c.flows[state]
	delete(c.flows, state)
	c.mu.Unlock()
	if !ok || time.Now().After(f.expires) {
		return Identity{}, errors.New("Die Anmeldung ist abgelaufen. Bitte noch einmal versuchen.")
	}
	if e := q.Get("error"); e != "" {
		return Identity{}, fmt.Errorf("nexora/auth hat abgelehnt: %s", firstNonEmpty(q.Get("error_description"), e))
	}
	// RFC 9207: Antwort muss von genau diesem Issuer kommen (Mix-up-Schutz).
	if iss := q.Get("iss"); iss != "" && iss != c.issuer {
		return Identity{}, errors.New("Antwort von einem anderen Anmeldedienst")
	}
	if err := c.setup(r.Context()); err != nil {
		return Identity{}, err
	}
	ctx := c.ctx(r.Context())
	tok, err := c.oauth.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(f.verifier))
	if err != nil {
		return Identity{}, fmt.Errorf("nexora/auth hat die Anmeldung nicht bestätigt: %w", err)
	}
	raw, _ := tok.Extra("id_token").(string)
	idt, err := c.verifier.Verify(ctx, raw)
	if err != nil {
		return Identity{}, fmt.Errorf("ID-Token ungültig: %w", err)
	}
	if idt.Nonce != f.nonce {
		return Identity{}, errors.New("ID-Token gehört nicht zu dieser Anmeldung")
	}
	var claims struct {
		Email         string   `json:"email"`
		EmailVerified bool     `json:"email_verified"`
		Username      string   `json:"preferred_username"`
		Name          string   `json:"name"`
		AMR           []string `json:"amr"`
	}
	if err := idt.Claims(&claims); err != nil {
		return Identity{}, err
	}
	return Identity{
		Subject: idt.Subject, Email: strings.ToLower(strings.TrimSpace(claims.Email)), EmailVerified: claims.EmailVerified,
		Username: strings.ToLower(claims.Username), Name: claims.Name, MFA: slices.Contains(claims.AMR, "mfa"),
	}, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
