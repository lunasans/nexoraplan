package sso

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// fakeIssuer: Mini-nexora/auth mit Discovery, JWKS und Token-Endpunkt (prüft PKCE).
type fakeIssuer struct {
	srv       *httptest.Server
	key       *rsa.PrivateKey
	challenge string
	nonce     string
	claims    map[string]any
}

func newFake(t *testing.T) *fakeIssuer {
	f := &fakeIssuer{}
	f.key, _ = rsa.GenerateKey(rand.Reader, 2048)
	b64 := base64.RawURLEncoding
	mux := http.NewServeMux()
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/oauth/authorize",
			"token_endpoint": f.srv.URL + "/oauth/token", "jwks_uri": f.srv.URL + "/oauth/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/oauth/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "k1",
			"n": b64.EncodeToString(f.key.N.Bytes()), "e": b64.EncodeToString(big.NewInt(int64(f.key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		r.ParseForm()
		id, secret, _ := r.BasicAuth()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if id != "plan" || secret != "geheim" || r.PostForm.Get("code") != "c0de" || b64.EncodeToString(sum[:]) != f.challenge {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		claims := map[string]any{"iss": f.srv.URL, "aud": "plan", "sub": "7", "nonce": f.nonce,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
			"email": "Ada@Example.de", "email_verified": true, "preferred_username": "ada", "amr": []string{"pwd", "otp", "mfa"}}
		for k, v := range f.claims {
			claims[k] = v
		}
		h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
		p, _ := json.Marshal(claims)
		in := b64.EncodeToString(h) + "." + b64.EncodeToString(p)
		d := sha256.Sum256([]byte(in))
		sig, _ := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, d[:])
		json.NewEncoder(w).Encode(map[string]any{"access_token": "a", "token_type": "Bearer", "expires_in": 3600, "id_token": in + "." + b64.EncodeToString(sig)})
	})
	return f
}

// login: Start → (nexora/auth) → Finish. Liefert Identität oder Fehler.
func (f *fakeIssuer) login(t *testing.T, c *Client, tamper func(url.Values, *http.Cookie)) (Identity, error) {
	t.Helper()
	w := httptest.NewRecorder()
	if err := c.Start(w, httptest.NewRequest("GET", "/api/oidc/login", nil)); err != nil {
		t.Fatal(err)
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	q := loc.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("redirect_uri") != "https://plan.example.de/api/oidc/callback" {
		t.Fatalf("authorize: %s", loc)
	}
	f.challenge, f.nonce = q.Get("code_challenge"), q.Get("nonce")
	cookie := w.Result().Cookies()[0]
	back := url.Values{"code": {"c0de"}, "state": {q.Get("state")}, "iss": {f.srv.URL}}
	if tamper != nil {
		tamper(back, cookie)
	}
	req := httptest.NewRequest("GET", CallbackPath+"?"+back.Encode(), nil)
	req.AddCookie(cookie)
	return c.Finish(httptest.NewRecorder(), req)
}

func TestLoginRoundTrip(t *testing.T) {
	f := newFake(t)
	c := New(f.srv.URL, "plan", "geheim", "https://plan.example.de/")
	id, err := f.login(t, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if id.Email != "ada@example.de" || !id.EmailVerified || id.Username != "ada" || !id.MFA || id.Subject != "7" {
		t.Fatalf("Identität: %+v", id)
	}
	if New("", "plan", "geheim", "x") != nil {
		t.Fatal("ohne Issuer eingerichtet")
	}
}

func TestLoginRejects(t *testing.T) {
	f := newFake(t)
	c := New(f.srv.URL, "plan", "geheim", "https://plan.example.de")
	cases := map[string]func(url.Values, *http.Cookie){
		"fremder state":       func(q url.Values, _ *http.Cookie) { q.Set("state", "x") },
		"Cookie fehlt/anders": func(_ url.Values, c *http.Cookie) { c.Value = "anders" },
		"fremder Issuer":      func(q url.Values, _ *http.Cookie) { q.Set("iss", "https://evil.example") },
		"falscher Code":       func(q url.Values, _ *http.Cookie) { q.Set("code", "falsch") },
		"Fehler von auth":     func(q url.Values, _ *http.Cookie) { q.Set("error", "access_denied") },
	}
	for name, tamper := range cases {
		if _, err := f.login(t, c, tamper); err == nil {
			t.Errorf("%s: angenommen", name)
		}
	}
	for name, claims := range map[string]map[string]any{
		"fremde Nonce": {"nonce": "x"},
		"fremde App":   {"aud": "andere"},
		"abgelaufen":   {"exp": time.Now().Add(-time.Hour).Unix()},
	} {
		f.claims = claims
		if _, err := f.login(t, c, nil); err == nil {
			t.Errorf("%s: angenommen", name)
		}
	}
}
