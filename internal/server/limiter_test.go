package server

import (
	"fmt"
	"testing"
)

func TestLimitKey(t *testing.T) {
	cases := map[string]string{
		"198.51.100.7":            "198.51.100.7",
		"::ffff:198.51.100.7":     "198.51.100.7",
		"2001:db8:1:2:aaaa::1":    "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff::9999": "2001:db8:1:2::/64",
		"kaputt":                  "kaputt",
	}
	for in, want := range cases {
		if got := limitKey(in); got != want {
			t.Errorf("limitKey(%q) = %q, erwartet %q", in, got, want)
		}
	}
}

// Wer innerhalb seines /64 die Adresse wechselt, bleibt gesperrt.
func TestLimiterIPv6Prefix(t *testing.T) {
	l := newLoginLimiter()
	for i := 0; i < l.max; i++ {
		l.fail(fmt.Sprintf("2001:db8:1:2::%x", i+1))
	}
	if l.allow("2001:db8:1:2::ffff") {
		t.Error("andere Adresse im selben /64 ist nicht gesperrt")
	}
	if !l.allow("2001:db8:1:3::1") {
		t.Error("anderes /64 ist fälschlich gesperrt")
	}
}

func TestInsideCalendar(t *testing.T) {
	cal := "/SOGo/dav/eva%40example.de/Calendar/personal/"
	for key, want := range map[string]bool{
		"/SOGo/dav/eva%40example.de/Calendar/personal/a.ics":      true,
		"/SOGo/dav/eva@example.de/Calendar/personal/a.ics":        true,
		"/SOGo/dav/eva@example.de/Calendar/personal/":             false,
		"/SOGo/dav/eva@example.de/Calendar/personal/sub/a.ics":    false,
		"/SOGo/dav/eva@example.de/Calendar/personal/../other.ics": false,
		"/SOGo/dav/eva@example.de/Calendar/personal%2F..%2Fx.ics": false,
		"/SOGo/dav/eva@example.de/Calendar/other/a.ics":           false,
	} {
		if got := insideCalendar(key, cal); got != want {
			t.Errorf("insideCalendar(%q) = %v, erwartet %v", key, got, want)
		}
	}
}
