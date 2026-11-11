package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestForwardedIP(t *testing.T) {
	cases := []struct {
		name, remote, realIP, cfIP, want string
		unix                             bool
	}{
		{"direkt ohne Proxy", "203.0.113.5:4000", "1.2.3.4", "5.6.7.8", "203.0.113.5", false},
		// Socket-Aktivierung unter NexoraPanel: NGINX kommt über den Unix-Socket, Absender "@"
		{"NGINX über Unix-Socket", "@", "198.51.100.7", "", "198.51.100.7", true},
		{"NGINX ohne Cloudflare", "127.0.0.1:4000", "198.51.100.7", "", "198.51.100.7", false},
		{"NGINX hinter Cloudflare", "127.0.0.1:4000", "172.69.109.222", "198.51.100.7", "198.51.100.7", false},
		{"Cloudflare IPv6", "[::1]:4000", "2606:4700::1", "2001:db8::42", "2001:db8::42", false},
		{"gefälschter CF-Header an Cloudflare vorbei", "127.0.0.1:4000", "198.51.100.9", "1.1.1.1", "198.51.100.9", false},
		{"Cloudflare ohne CF-Header", "127.0.0.1:4000", "172.69.109.222", "", "172.69.109.222", false},
		{"Unsinn im CF-Header", "127.0.0.1:4000", "172.69.109.222", "kaputt", "172.69.109.222", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		if c.unix {
			r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.UnixAddr{Name: "/run/nexora-apps/nexoraplan/x.sock", Net: "unix"}))
		}
		r.RemoteAddr = c.remote
		r.Header.Set("X-Real-IP", c.realIP)
		if c.cfIP != "" {
			r.Header.Set("CF-Connecting-IP", c.cfIP)
		}
		var got string
		realIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = clientIP(r) })).ServeHTTP(httptest.NewRecorder(), r)
		if got != c.want {
			t.Errorf("%s: %s, erwartet %s", c.name, got, c.want)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(w, r)
	for k, v := range map[string]string{
		"X-Frame-Options":           "DENY",
		"Content-Security-Policy":   "frame-ancestors 'none'",
		"Referrer-Policy":           "same-origin",
		"Strict-Transport-Security": "max-age=31536000",
	} {
		if got := w.Header().Get(k); got != v {
			t.Errorf("%s = %q, erwartet %q", k, got, v)
		}
	}
}
