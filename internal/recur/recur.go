// Package recur rechnet Serienregeln nach RFC 5545 (RRULE) in einzelne
// Termine um.
//
// Unterstützt wird, was Kalenderprogramme (SOGo, Thunderbird, iOS, Android)
// tatsächlich schreiben: FREQ=DAILY/WEEKLY/MONTHLY/YEARLY mit INTERVAL, COUNT,
// UNTIL, BYDAY (auch „2MO“, „-1FR“), BYMONTHDAY, BYMONTH, BYSETPOS und WKST.
// Gerechnet wird in der Wanduhrzeit des Starttermins: Ein Termin um 9:00
// bleibt nach der Zeitumstellung um 9:00.
package recur

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Freq int

const (
	Daily Freq = iota + 1
	Weekly
	Monthly
	Yearly
)

var freqNames = map[string]Freq{"DAILY": Daily, "WEEKLY": Weekly, "MONTHLY": Monthly, "YEARLY": Yearly}

func (f Freq) String() string {
	for k, v := range freqNames {
		if v == f {
			return k
		}
	}
	return ""
}

// WeekdayNum ist ein BYDAY-Eintrag: N = 0 jeder, 2 = zweiter, -1 = letzter.
type WeekdayNum struct {
	N   int
	Day time.Weekday
}

type Rule struct {
	Freq       Freq
	Interval   int       // ≥ 1
	Count      int       // 0 = unbegrenzt
	Until      time.Time // Null = unbegrenzt; letzter möglicher Beginn (einschließlich)
	UntilDate  bool      // UNTIL war ein reines Datum (ganztägige Termine)
	ByDay      []WeekdayNum
	ByMonthDay []int
	ByMonth    []time.Month
	BySetPos   []int
	WeekStart  time.Weekday
}

var dayCodes = []string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}

func parseDay(s string) (time.Weekday, bool) {
	for i, c := range dayCodes {
		if c == s {
			return time.Weekday(i), true
		}
	}
	return 0, false
}

// Parse liest eine RRULE (ohne „RRULE:“). UNTIL ohne Zeitzone gilt in loc.
func Parse(s string, loc *time.Location) (Rule, error) {
	r := Rule{Interval: 1, WeekStart: time.Monday}
	s = strings.TrimPrefix(strings.TrimSpace(s), "RRULE:")
	for part := range strings.SplitSeq(s, ";") {
		if part == "" {
			continue
		}
		key, val, ok := strings.Cut(part, "=")
		if !ok {
			return r, fmt.Errorf("RRULE: %q ohne Wert", part)
		}
		var err error
		switch strings.ToUpper(key) {
		case "FREQ":
			f, ok := freqNames[strings.ToUpper(val)]
			if !ok {
				return r, fmt.Errorf("RRULE: FREQ=%s wird nicht unterstützt", val)
			}
			r.Freq = f
		case "INTERVAL":
			r.Interval, err = strconv.Atoi(val)
			if err != nil || r.Interval < 1 || r.Interval > 1000 {
				return r, errors.New("RRULE: INTERVAL ungültig")
			}
		case "COUNT":
			r.Count, err = strconv.Atoi(val)
			if err != nil || r.Count < 1 {
				return r, errors.New("RRULE: COUNT ungültig")
			}
		case "UNTIL":
			r.Until, r.UntilDate, err = parseUntil(val, loc)
			if err != nil {
				return r, err
			}
		case "BYDAY":
			for d := range strings.SplitSeq(val, ",") {
				d = strings.ToUpper(strings.TrimSpace(d))
				if len(d) < 2 {
					return r, fmt.Errorf("RRULE: BYDAY=%s ungültig", d)
				}
				wd, ok := parseDay(d[len(d)-2:])
				if !ok {
					return r, fmt.Errorf("RRULE: BYDAY=%s ungültig", d)
				}
				n := 0
				if num := strings.TrimPrefix(d[:len(d)-2], "+"); num != "" {
					if n, err = strconv.Atoi(num); err != nil || n == 0 || n < -53 || n > 53 {
						return r, fmt.Errorf("RRULE: BYDAY=%s ungültig", d)
					}
				}
				r.ByDay = append(r.ByDay, WeekdayNum{N: n, Day: wd})
			}
		case "BYMONTHDAY":
			if r.ByMonthDay, err = intList(val, -31, 31); err != nil {
				return r, fmt.Errorf("RRULE: BYMONTHDAY %w", err)
			}
		case "BYMONTH":
			months, err := intList(val, 1, 12)
			if err != nil {
				return r, fmt.Errorf("RRULE: BYMONTH %w", err)
			}
			for _, m := range months {
				r.ByMonth = append(r.ByMonth, time.Month(m))
			}
		case "BYSETPOS":
			if r.BySetPos, err = intList(val, -366, 366); err != nil {
				return r, fmt.Errorf("RRULE: BYSETPOS %w", err)
			}
		case "WKST":
			wd, ok := parseDay(strings.ToUpper(val))
			if !ok {
				return r, errors.New("RRULE: WKST ungültig")
			}
			r.WeekStart = wd
		default:
			// BYHOUR, BYMINUTE, BYWEEKNO, BYYEARDAY … schreibt kaum ein Programm.
			// Lieber den Termin ohne diese Einschränkung zeigen als gar nicht.
		}
	}
	if r.Freq == 0 {
		return r, errors.New("RRULE: FREQ fehlt")
	}
	return r, nil
}

func parseUntil(v string, loc *time.Location) (time.Time, bool, error) {
	switch {
	case len(v) == 8:
		t, err := time.ParseInLocation("20060102", v, loc)
		// Ein Datum schließt den ganzen Tag ein.
		return t.Add(24*time.Hour - time.Second), true, err
	case strings.HasSuffix(v, "Z"):
		t, err := time.Parse("20060102T150405Z", v)
		return t, false, err
	default:
		t, err := time.ParseInLocation("20060102T150405", v, loc)
		return t, false, err
	}
}

func intList(v string, lo, hi int) ([]int, error) {
	var out []int
	for p := range strings.SplitSeq(v, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(p, "+")))
		if err != nil || n < lo || n > hi || n == 0 {
			return nil, fmt.Errorf("%q ungültig", p)
		}
		out = append(out, n)
	}
	return out, nil
}

// String schreibt die Regel wieder als RRULE (ohne „RRULE:“).
func (r Rule) String() string {
	parts := []string{"FREQ=" + r.Freq.String()}
	if r.Interval > 1 {
		parts = append(parts, "INTERVAL="+strconv.Itoa(r.Interval))
	}
	if r.Count > 0 {
		parts = append(parts, "COUNT="+strconv.Itoa(r.Count))
	}
	if !r.Until.IsZero() {
		if r.UntilDate {
			parts = append(parts, "UNTIL="+r.Until.Format("20060102"))
		} else {
			parts = append(parts, "UNTIL="+r.Until.UTC().Format("20060102T150405Z"))
		}
	}
	if len(r.ByDay) > 0 {
		var days []string
		for _, d := range r.ByDay {
			s := dayCodes[d.Day]
			if d.N != 0 {
				s = strconv.Itoa(d.N) + s
			}
			days = append(days, s)
		}
		parts = append(parts, "BYDAY="+strings.Join(days, ","))
	}
	if len(r.ByMonthDay) > 0 {
		parts = append(parts, "BYMONTHDAY="+joinInts(r.ByMonthDay))
	}
	if len(r.ByMonth) > 0 {
		var ms []int
		for _, m := range r.ByMonth {
			ms = append(ms, int(m))
		}
		parts = append(parts, "BYMONTH="+joinInts(ms))
	}
	if len(r.BySetPos) > 0 {
		parts = append(parts, "BYSETPOS="+joinInts(r.BySetPos))
	}
	if r.WeekStart != time.Monday {
		parts = append(parts, "WKST="+dayCodes[r.WeekStart])
	}
	return strings.Join(parts, ";")
}

func joinInts(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ",")
}

// maxPeriods begrenzt die Rechnung (täglich über 50 Jahre ≈ 18 000).
const maxPeriods = 50000

// Between liefert die Beginnzeiten aller Termine der Serie mit
// from <= Beginn < to. dtstart ist der erste Termin (in seiner Zeitzone).
// Wie in RFC 5545 zählt dtstart immer als erster Termin mit.
func (r Rule) Between(dtstart, from, to time.Time) []time.Time {
	var out []time.Time
	r.each(dtstart, func(t time.Time) bool {
		if !t.Before(to) {
			return false
		}
		if !t.Before(from) {
			out = append(out, t)
		}
		return true
	})
	return out
}

// Last liefert den letzten Termin einer begrenzten Serie (COUNT/UNTIL),
// sonst ok = false.
func (r Rule) Last(dtstart time.Time) (last time.Time, ok bool) {
	if r.Count == 0 && r.Until.IsZero() {
		return time.Time{}, false
	}
	r.each(dtstart, func(t time.Time) bool { last = t; return true })
	return last, !last.IsZero()
}

// each ruft fn für jeden Termin in zeitlicher Reihenfolge auf, bis fn false
// liefert oder die Serie endet.
func (r Rule) each(dtstart time.Time, fn func(time.Time) bool) {
	interval := max(r.Interval, 1)
	count := 0
	emit := func(t time.Time) bool {
		if !r.Until.IsZero() && t.After(r.Until) {
			return false
		}
		count++
		if r.Count > 0 && count > r.Count {
			return false
		}
		return fn(t)
	}
	// dtstart zählt immer, auch wenn es nicht zur Regel passt (RFC 5545 3.8.5.3).
	if !emit(dtstart) {
		return
	}
	for k := 0; k < maxPeriods; k++ {
		for _, t := range r.period(dtstart, k*interval) {
			if !t.After(dtstart) {
				continue
			}
			if !emit(t) {
				return
			}
		}
	}
}

// period liefert die sortierten Kandidaten der n-ten Periode nach dtstart.
func (r Rule) period(dtstart time.Time, n int) []time.Time {
	loc := dtstart.Location()
	h, mi, s := dtstart.Clock()
	at := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, h, mi, s, 0, loc) }
	y, m, d := dtstart.Date()

	var days []time.Time
	switch r.Freq {
	case Daily:
		t := at(y, m, d+n)
		if r.matchDay(t) {
			days = append(days, t)
		}
	case Weekly:
		// Wochenbeginn (WKST) der Woche von dtstart, dann n Wochen weiter
		offset := (int(dtstart.Weekday()) - int(r.WeekStart) + 7) % 7
		first := at(y, m, d-offset+7*n)
		wanted := r.ByDay
		if len(wanted) == 0 {
			wanted = []WeekdayNum{{Day: dtstart.Weekday()}}
		}
		for i := range 7 {
			t := at(first.Year(), first.Month(), first.Day()+i)
			if slices.ContainsFunc(wanted, func(w WeekdayNum) bool { return w.Day == t.Weekday() }) && r.matchMonth(t) {
				days = append(days, t)
			}
		}
	case Monthly:
		first := at(y, m+time.Month(n), 1)
		if r.matchMonth(first) {
			days = r.daysInMonth(first, d, at)
		}
	case Yearly:
		months := r.ByMonth
		if len(months) == 0 {
			months = []time.Month{m}
		}
		for _, mm := range months {
			days = append(days, r.daysInMonth(at(y+n, mm, 1), d, at)...)
		}
	}
	slices.SortFunc(days, func(a, b time.Time) int { return a.Compare(b) })
	days = slices.CompactFunc(days, func(a, b time.Time) bool { return a.Equal(b) })
	return r.setPos(days)
}

// daysInMonth: Termine im Monat von first nach BYMONTHDAY/BYDAY, sonst der
// Tag von dtstart (fällt weg, wenn es ihn nicht gibt, z. B. 31. im April).
func (r Rule) daysInMonth(first time.Time, startDay int, at func(int, time.Month, int) time.Time) []time.Time {
	y, m := first.Year(), first.Month()
	last := at(y, m+1, 0).Day()
	var out []time.Time
	if len(r.ByMonthDay) == 0 && len(r.ByDay) == 0 {
		if startDay <= last {
			out = append(out, at(y, m, startDay))
		}
		return out
	}
	for day := 1; day <= last; day++ {
		t := at(y, m, day)
		if len(r.ByMonthDay) > 0 && !slices.ContainsFunc(r.ByMonthDay, func(md int) bool {
			return md == day || (md < 0 && last+md+1 == day)
		}) {
			continue
		}
		if len(r.ByDay) > 0 && !slices.ContainsFunc(r.ByDay, func(w WeekdayNum) bool {
			if w.Day != t.Weekday() {
				return false
			}
			switch {
			case w.N > 0:
				return (day-1)/7+1 == w.N
			case w.N < 0:
				return (last-day)/7+1 == -w.N
			}
			return true
		}) {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (r Rule) matchMonth(t time.Time) bool {
	return len(r.ByMonth) == 0 || slices.Contains(r.ByMonth, t.Month())
}

// matchDay filtert tägliche Serien nach BYMONTH, BYMONTHDAY und BYDAY.
func (r Rule) matchDay(t time.Time) bool {
	if !r.matchMonth(t) {
		return false
	}
	if len(r.ByMonthDay) > 0 {
		last := time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
		if !slices.ContainsFunc(r.ByMonthDay, func(md int) bool { return md == t.Day() || (md < 0 && last+md+1 == t.Day()) }) {
			return false
		}
	}
	return len(r.ByDay) == 0 || slices.ContainsFunc(r.ByDay, func(w WeekdayNum) bool { return w.Day == t.Weekday() })
}

func (r Rule) setPos(days []time.Time) []time.Time {
	if len(r.BySetPos) == 0 || len(days) == 0 {
		return days
	}
	var out []time.Time
	for _, p := range r.BySetPos {
		i := p - 1
		if p < 0 {
			i = len(days) + p
		}
		if i >= 0 && i < len(days) {
			out = append(out, days[i])
		}
	}
	slices.SortFunc(out, func(a, b time.Time) int { return a.Compare(b) })
	return slices.CompactFunc(out, func(a, b time.Time) bool { return a.Equal(b) })
}
