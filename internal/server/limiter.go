package server

import (
	"net/netip"
	"sync"
	"time"
)

// loginLimiter sperrt eine IP nach zu vielen fehlgeschlagenen Anmeldungen.
// Die Zähler liegen nur im Speicher – nach einem Neustart beginnen sie neu,
// das reicht für eine private Cloud.
type loginLimiter struct {
	mu       sync.Mutex
	failures map[string][]time.Time
	max      int
	window   time.Duration
}

// maxLimiterEntries: Ab so vielen Einträgen werden abgelaufene sofort
// aufgeräumt – sonst wüchse die Map mit jeder neuen Angreifer-IP weiter.
const maxLimiterEntries = 10000

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{
		failures: map[string][]time.Time{},
		max:      10,
		window:   15 * time.Minute,
	}
}

// newAccountLimiter zählt Fehlversuche pro Konto statt pro IP – gegen Angriffe
// von vielen Adressen aus. Das Limit ist bewusst höher als pro IP: Wer es
// erreicht, sperrt auch den echten Nutzer aus (Passkeys gehen weiterhin).
func newAccountLimiter() *loginLimiter {
	l := newLoginLimiter()
	l.max = 30
	return l
}

// limitKey fasst IPv6-Adressen zu ihrem /64-Netz zusammen. Ein einzelner
// Anschluss bekommt meist ein ganzes /64 und könnte sonst mit jeder Anfrage
// eine neue Adresse nehmen.
func limitKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return addr.String()
	}
	prefix, _ := addr.Prefix(64)
	return prefix.String()
}

func (l *loginLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(limitKey(ip))) < l.max
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := limitKey(ip)
	if len(l.failures) >= maxLimiterEntries {
		l.sweep()
	}
	l.failures[key] = append(l.recent(key), time.Now())
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, limitKey(ip))
}

// sweep entfernt alle Einträge ohne Fehlversuch im Zeitfenster. Aufruf nur mit l.mu.
func (l *loginLimiter) sweep() {
	for key := range l.failures {
		l.recent(key)
	}
}

// recent entfernt Fehlversuche außerhalb des Zeitfensters. Aufruf nur mit l.mu.
func (l *loginLimiter) recent(key string) []time.Time {
	cutoff := time.Now().Add(-l.window)
	kept := l.failures[key][:0]
	for _, t := range l.failures[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.failures, key)
		return nil
	}
	l.failures[key] = kept
	return kept
}
