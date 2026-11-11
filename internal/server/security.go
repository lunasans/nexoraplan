package server

import (
	"net"
	"net/http"
	"net/netip"
)

// cloudflareRanges sind die Adressbereiche von Cloudflare
// (https://www.cloudflare.com/ips-v4 und ips-v6, Stand Oktober 2026).
var cloudflareRanges = func() []netip.Prefix {
	var list []netip.Prefix
	for _, s := range []string{
		"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
		"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
		"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
		"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
		"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32",
		"2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32",
	} {
		list = append(list, netip.MustParsePrefix(s))
	}
	return list
}()

func isCloudflare(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	for _, p := range cloudflareRanges {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// forwardedIP ermittelt die echte Client-IP hinter NGINX und Cloudflare.
//
//   - X-Real-IP gilt nur, wenn die Anfrage von NGINX auf demselben Rechner kommt:
//     über Loopback oder über den Unix-Socket (Socket-Aktivierung unter NexoraPanel,
//     nur für NGINX beschreibbar).
//   - Ist das ein Cloudflare-Server, steht der eigentliche Besucher in
//     CF-Connecting-IP. Dem Header wird nur dann geglaubt – wer an Cloudflare
//     vorbei direkt anfragt, kann ihn sonst beliebig fälschen.
func forwardedIP(r *http.Request) net.IP {
	if !isLoopback(r.RemoteAddr) && !viaUnixSocket(r) {
		return nil
	}
	ip := net.ParseIP(r.Header.Get("X-Real-IP"))
	if ip == nil {
		return nil
	}
	if isCloudflare(ip) {
		if visitor := net.ParseIP(r.Header.Get("CF-Connecting-IP")); visitor != nil {
			return visitor
		}
	}
	return ip
}

// viaUnixSocket: kam die Anfrage über einen Unix-Socket? Der Absender heißt dort
// nicht 127.0.0.1, sondern "@" – erkannt wird es am lokalen Listener.
func viaUnixSocket(r *http.Request) bool {
	addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	return ok && addr.Network() == "unix"
}

// securityHeaders setzt Schutz-Header für alle Antworten.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// Nie in andere Seiten einbetten (Klick-Fallen).
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if isHTTPS(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}
