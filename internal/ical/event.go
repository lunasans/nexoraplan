package ical

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"nexoraplan/internal/recur"
)

// Occurrence ist ein einzelner Termin – bei Serien ein Vorkommen.
type Occurrence struct {
	UID         string
	Summary     string
	Location    string
	Description string
	Start, End  time.Time
	AllDay      bool
	Recurring   bool      // gehört zu einer Serie
	Original    time.Time // ursprünglicher Beginn (RECURRENCE-ID) bei Serien
	RRule       string    // Regel der Serie (nur am Hauptteil)
}

// LoadLocation versteht auch TZIDs wie „/mozilla.org/20050126_1/Europe/Berlin“.
func LoadLocation(tzid string, fallback *time.Location) *time.Location {
	if tzid == "" {
		return fallback
	}
	if loc, err := time.LoadLocation(tzid); err == nil {
		return loc
	}
	parts := strings.Split(strings.Trim(tzid, "/"), "/")
	for i := range parts {
		if loc, err := time.LoadLocation(strings.Join(parts[i:], "/")); err == nil {
			return loc
		}
	}
	return fallback
}

// ParseTime liest DTSTART/DTEND/RECURRENCE-ID/EXDATE (einen Wert).
func ParseTime(p *Prop, value string, loc *time.Location) (t time.Time, allDay bool, err error) {
	if strings.EqualFold(p.Param("VALUE"), "DATE") || len(value) == 8 {
		t, err = time.ParseInLocation("20060102", value, loc)
		return t, true, err
	}
	if strings.HasSuffix(value, "Z") {
		t, err = time.Parse("20060102T150405Z", value)
		return t, false, err
	}
	t, err = time.ParseInLocation("20060102T150405", value, LoadLocation(p.Param("TZID"), loc))
	return t, false, err
}

// ParseDuration liest Dauern wie P1D, PT1H30M, P1W, -PT15M.
func ParseDuration(s string) (time.Duration, error) {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimLeft(s, "+-")
	if !strings.HasPrefix(s, "P") {
		return 0, fmt.Errorf("DURATION %q ungültig", s)
	}
	var d time.Duration
	inTime := false
	num := ""
	for _, c := range s[1:] {
		switch {
		case c == 'T':
			inTime = true
		case c >= '0' && c <= '9':
			num += string(c)
		default:
			n, err := strconv.Atoi(num)
			if err != nil {
				return 0, fmt.Errorf("DURATION %q ungültig", s)
			}
			num = ""
			unit := map[rune]time.Duration{'W': 7 * 24 * time.Hour, 'D': 24 * time.Hour}[c]
			if inTime {
				unit = map[rune]time.Duration{'H': time.Hour, 'M': time.Minute, 'S': time.Second}[c]
			}
			if unit == 0 {
				return 0, fmt.Errorf("DURATION %q ungültig", s)
			}
			d += time.Duration(n) * unit
		}
	}
	if neg {
		d = -d
	}
	return d, nil
}

type vevent struct {
	c        *Component
	start    time.Time
	end      time.Time
	allDay   bool
	recurID  time.Time
	hasRecur bool
}

func readEvent(c *Component, loc *time.Location) (vevent, error) {
	ev := vevent{c: c}
	ds := c.Prop("DTSTART")
	if ds == nil {
		return ev, errors.New("VEVENT ohne DTSTART")
	}
	var err error
	if ev.start, ev.allDay, err = ParseTime(ds, ds.Value, loc); err != nil {
		return ev, err
	}
	switch {
	case c.Prop("DTEND") != nil:
		de := c.Prop("DTEND")
		if ev.end, _, err = ParseTime(de, de.Value, loc); err != nil {
			return ev, err
		}
	case c.Prop("DURATION") != nil:
		dur, err := ParseDuration(c.Prop("DURATION").Value)
		if err != nil {
			return ev, err
		}
		ev.end = addDuration(ev.start, dur, ev.allDay)
	case ev.allDay:
		ev.end = ev.start.AddDate(0, 0, 1)
	default:
		ev.end = ev.start
	}
	if ev.end.Before(ev.start) {
		ev.end = ev.start
	}
	if rid := c.Prop("RECURRENCE-ID"); rid != nil {
		if ev.recurID, _, err = ParseTime(rid, rid.Value, loc); err != nil {
			return ev, err
		}
		ev.hasRecur = true
	}
	return ev, nil
}

// addDuration: ganze Tage zählen als Kalendertage (bleibt über die Zeitumstellung Mitternacht).
func addDuration(t time.Time, d time.Duration, allDay bool) time.Time {
	if allDay && d%(24*time.Hour) == 0 {
		return t.AddDate(0, 0, int(d/(24*time.Hour)))
	}
	return t.Add(d)
}

func (ev vevent) occurrence(start, end time.Time, recurring bool, original time.Time) Occurrence {
	return Occurrence{
		UID:         ev.c.Text("UID"),
		Summary:     ev.c.Text("SUMMARY"),
		Location:    ev.c.Text("LOCATION"),
		Description: ev.c.Text("DESCRIPTION"),
		Start:       start,
		End:         end,
		AllDay:      ev.allDay,
		Recurring:   recurring,
		Original:    original,
	}
}

func cancelled(c *Component) bool {
	return strings.EqualFold(c.Text("STATUS"), "CANCELLED")
}

// overlaps: liegt der Termin im Zeitraum [from, to)? Termine ohne Dauer zählen an ihrem Beginn.
func overlaps(start, end, from, to time.Time) bool {
	if !start.Before(to) {
		return false
	}
	if end.Equal(start) {
		return !start.Before(from)
	}
	return end.After(from)
}

// Expand liefert alle Termine eines Kalenderobjekts im Zeitraum [from, to).
// Serien werden aufgelöst, gestrichene Termine (EXDATE) fehlen, abweichende
// Einzeltermine (RECURRENCE-ID) ersetzen ihr Vorkommen – auch wenn sie in
// eine andere Woche verschoben wurden.
func Expand(cal *Component, from, to time.Time, loc *time.Location) ([]Occurrence, error) {
	var masters []vevent
	overrides := map[string][]vevent{}
	for _, c := range cal.ChildrenNamed("VEVENT") {
		ev, err := readEvent(c, loc)
		if err != nil {
			return nil, err
		}
		uid := c.Text("UID")
		if ev.hasRecur {
			overrides[uid] = append(overrides[uid], ev)
		} else {
			masters = append(masters, ev)
		}
	}

	var out []Occurrence
	seen := map[string]bool{}
	for _, m := range masters {
		uid := m.c.Text("UID")
		seen[uid] = true
		rr := m.c.Prop("RRULE")
		if rr == nil {
			if !cancelled(m.c) && overlaps(m.start, m.end, from, to) {
				out = append(out, m.occurrence(m.start, m.end, false, time.Time{}))
			}
			continue
		}
		if cancelled(m.c) {
			continue
		}
		rule, err := recur.Parse(rr.Value, m.start.Location())
		if err != nil {
			// Unbekannte Regel: wenigstens den ersten Termin zeigen
			if overlaps(m.start, m.end, from, to) {
				out = append(out, m.occurrence(m.start, m.end, false, time.Time{}))
			}
			continue
		}
		skip := exdates(m.c, loc)
		for _, o := range overrides[uid] {
			skip = append(skip, o.recurID)
		}
		dur := m.end.Sub(m.start)
		days := 0
		if m.allDay {
			days = int(dur.Round(24*time.Hour) / (24 * time.Hour))
		}
		lookFrom := from.Add(-dur - 24*time.Hour)
		for _, st := range rule.Between(m.start, lookFrom, to) {
			if slices.ContainsFunc(skip, func(x time.Time) bool { return sameInstant(x, st, m.allDay) }) {
				continue
			}
			end := st.Add(dur)
			if m.allDay {
				end = st.AddDate(0, 0, days)
			}
			if overlaps(st, end, from, to) {
				occ := m.occurrence(st, end, true, st)
				occ.RRule = rr.Value
				out = append(out, occ)
			}
		}
		for _, o := range overrides[uid] {
			if !cancelled(o.c) && overlaps(o.start, o.end, from, to) {
				occ := o.occurrence(o.start, o.end, true, o.recurID)
				occ.RRule = rr.Value
				out = append(out, occ)
			}
		}
	}
	// Einzeltermine ohne Hauptteil (Einladung zu nur einem Termin einer Serie)
	for uid, list := range overrides {
		if seen[uid] {
			continue
		}
		for _, o := range list {
			if !cancelled(o.c) && overlaps(o.start, o.end, from, to) {
				out = append(out, o.occurrence(o.start, o.end, true, o.recurID))
			}
		}
	}
	slices.SortFunc(out, func(a, b Occurrence) int { return a.Start.Compare(b.Start) })
	return out, nil
}

func exdates(c *Component, loc *time.Location) []time.Time {
	var out []time.Time
	for _, p := range c.PropsNamed("EXDATE") {
		for v := range strings.SplitSeq(p.Value, ",") {
			if t, _, err := ParseTime(p, strings.TrimSpace(v), loc); err == nil {
				out = append(out, t)
			}
		}
	}
	return out
}

// sameInstant vergleicht ganztägige Termine nach Datum, sonst nach Zeitpunkt.
func sameInstant(a, b time.Time, allDay bool) bool {
	if allDay {
		ay, am, ad := a.Date()
		by, bm, bd := b.Date()
		return ay == by && am == bm && ad == bd
	}
	return a.Equal(b)
}
