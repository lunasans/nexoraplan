package ical

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// EventData sind die Felder, die die App an einem Termin bearbeitet.
type EventData struct {
	Summary     string
	Location    string
	Description string
	Start, End  time.Time // in der Zeitzone der App
	AllDay      bool      // Start/End sind dann Mitternacht, End ausschließlich
	RRule       string    // "" = keine Serie
}

const prodID = "-//NexoraPanel//nexora-plan//DE"

// NewUID erzeugt eine weltweit eindeutige UID.
func NewUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b) + "@nexora-plan"
}

// NewCalendar baut ein neues Kalenderobjekt mit einem Termin.
func NewCalendar(uid string, ev EventData, now time.Time) *Component {
	cal := &Component{Name: "VCALENDAR", Props: []*Prop{
		{Name: "VERSION", Value: "2.0"},
		{Name: "PRODID", Value: prodID},
		{Name: "CALSCALE", Value: "GREGORIAN"},
	}}
	ve := &Component{Name: "VEVENT", Props: []*Prop{
		{Name: "UID", Value: uid},
		{Name: "DTSTAMP", Value: now.UTC().Format("20060102T150405Z")},
		{Name: "CREATED", Value: now.UTC().Format("20060102T150405Z")},
	}}
	cal.Children = append(cal.Children, ve)
	apply(cal, ve, ev, now)
	ve.Remove("SEQUENCE") // neuer Termin beginnt ohne SEQUENCE (= 0)
	return cal
}

// Master liefert den Hauptteil (VEVENT ohne RECURRENCE-ID).
func Master(cal *Component) (*Component, error) {
	for _, c := range cal.ChildrenNamed("VEVENT") {
		if c.Prop("RECURRENCE-ID") == nil {
			return c, nil
		}
	}
	return nil, errors.New("kein Termin im Kalenderobjekt")
}

// Update ändert den Hauptteil eines vorhandenen Termins. Alles andere bleibt,
// wie es war (Teilnehmer, Erinnerungen, Felder anderer Programme).
//
// Verschiebt sich der Beginn einer Serie, passen gestrichene und abweichende
// Einzeltermine nicht mehr zu ihren Vorkommen – sie entfallen dann.
func Update(cal *Component, ev EventData, now time.Time) error {
	m, err := Master(cal)
	if err != nil {
		return err
	}
	old, err := readEvent(m, ev.Start.Location())
	if err != nil {
		return err
	}
	if !old.start.Equal(ev.Start) || old.allDay != ev.AllDay || ev.RRule == "" {
		m.Remove("EXDATE")
		kept := cal.Children[:0]
		for _, c := range cal.Children {
			if !(strings.EqualFold(c.Name, "VEVENT") && c.Prop("RECURRENCE-ID") != nil) {
				kept = append(kept, c)
			}
		}
		cal.Children = kept
	}
	apply(cal, m, ev, now)
	return nil
}

func apply(cal, ve *Component, ev EventData, now time.Time) {
	ve.SetText("SUMMARY", ev.Summary)
	ve.SetText("LOCATION", ev.Location)
	ve.SetText("DESCRIPTION", ev.Description)
	ve.Set(timeProp(cal, "DTSTART", ev.Start, ev.AllDay))
	ve.Remove("DURATION")
	ve.Set(timeProp(cal, "DTEND", ev.End, ev.AllDay))
	if ev.RRule != "" {
		ve.Set(&Prop{Name: "RRULE", Value: ev.RRule})
	} else {
		ve.Remove("RRULE")
	}
	stamp := now.UTC().Format("20060102T150405Z")
	ve.Set(&Prop{Name: "DTSTAMP", Value: stamp})
	ve.Set(&Prop{Name: "LAST-MODIFIED", Value: stamp})
	seq := 0
	if p := ve.Prop("SEQUENCE"); p != nil {
		seq, _ = strconv.Atoi(p.Value)
	}
	ve.Set(&Prop{Name: "SEQUENCE", Value: strconv.Itoa(seq + 1)})
}

// Exclude streicht ein einzelnes Vorkommen einer Serie (EXDATE) und
// entfernt einen abweichenden Einzeltermin dafür, falls es einen gibt.
func Exclude(cal *Component, original time.Time, now time.Time) error {
	m, err := Master(cal)
	if err != nil {
		return err
	}
	if m.Prop("RRULE") == nil {
		return errors.New("kein Serientermin")
	}
	ms, err := readEvent(m, original.Location())
	if err != nil {
		return err
	}
	// Im Format von DTSTART schreiben, sonst erkennen andere Programme die Streichung nicht.
	at := original
	if !ms.allDay {
		at = original.In(ms.start.Location())
	}
	m.Props = append(m.Props, timeProp(cal, "EXDATE", at, ms.allDay))
	kept := cal.Children[:0]
	for _, c := range cal.Children {
		if strings.EqualFold(c.Name, "VEVENT") && c.Prop("RECURRENCE-ID") != nil {
			if o, err := readEvent(c, original.Location()); err == nil && sameInstant(o.recurID, original, ms.allDay) {
				continue
			}
		}
		kept = append(kept, c)
	}
	cal.Children = kept
	stamp := now.UTC().Format("20060102T150405Z")
	m.Set(&Prop{Name: "DTSTAMP", Value: stamp})
	m.Set(&Prop{Name: "LAST-MODIFIED", Value: stamp})
	return nil
}

// timeProp schreibt eine Zeit als Datum, in UTC oder mit TZID (dann kommt
// die passende VTIMEZONE in den Kalender).
func timeProp(cal *Component, name string, t time.Time, allDay bool) *Prop {
	if allDay {
		return &Prop{Name: name, Params: []Param{{"VALUE", "DATE"}}, Value: t.Format("20060102")}
	}
	loc := t.Location()
	if loc == time.UTC || loc.String() == "UTC" || loc.String() == "Local" {
		return &Prop{Name: name, Value: t.UTC().Format("20060102T150405Z")}
	}
	ensureTimezone(cal, loc, t.Year())
	return &Prop{Name: name, Params: []Param{{"TZID", loc.String()}}, Value: t.Format("20060102T150405")}
}

func ensureTimezone(cal *Component, loc *time.Location, year int) {
	for _, tz := range cal.ChildrenNamed("VTIMEZONE") {
		if tz.Text("TZID") == loc.String() {
			return
		}
	}
	tz := VTimezone(loc, year)
	// VTIMEZONE vor den Terminen, wie es die meisten Programme schreiben
	cal.Children = append([]*Component{tz}, cal.Children...)
}

// VTimezone beschreibt die Zeitzone mit den Umstellungsregeln des Jahres
// (z. B. „letzter Sonntag im März“). Gilt die Regel so seit Jahren, wie in
// Europa, ist das für alle Jahre richtig.
func VTimezone(loc *time.Location, year int) *Component {
	tz := &Component{Name: "VTIMEZONE", Props: []*Prop{{Name: "TZID", Value: loc.String()}}}
	transitions := yearTransitions(loc, year)
	if len(transitions) == 0 {
		name, off := time.Date(year, 1, 1, 0, 0, 0, 0, loc).Zone()
		tz.Children = append(tz.Children, &Component{Name: "STANDARD", Props: []*Prop{
			{Name: "DTSTART", Value: "19700101T000000"},
			{Name: "TZOFFSETFROM", Value: offset(off)},
			{Name: "TZOFFSETTO", Value: offset(off)},
			{Name: "TZNAME", Value: name},
		}})
		return tz
	}
	for _, tr := range transitions {
		kind := "STANDARD"
		if tr.isDST {
			kind = "DAYLIGHT"
		}
		// Ortszeit vor der Umstellung, wie RFC 5545 es verlangt
		local := tr.at.In(time.FixedZone("", tr.from))
		n := (local.Day()-1)/7 + 1
		if local.AddDate(0, 0, 7).Month() != local.Month() {
			n = -1
		}
		tz.Children = append(tz.Children, &Component{Name: kind, Props: []*Prop{
			{Name: "DTSTART", Value: "1970" + local.Format("0102T150405")},
			{Name: "TZOFFSETFROM", Value: offset(tr.from)},
			{Name: "TZOFFSETTO", Value: offset(tr.to)},
			{Name: "TZNAME", Value: tr.name},
			{Name: "RRULE", Value: fmt.Sprintf("FREQ=YEARLY;BYMONTH=%d;BYDAY=%d%s", local.Month(), n, dayCodes[local.Weekday()])},
		}})
	}
	return tz
}

var dayCodes = []string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}

type transition struct {
	at       time.Time
	from, to int
	name     string
	isDST    bool
}

func yearTransitions(loc *time.Location, year int) []transition {
	var out []transition
	t := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	_, prev := t.In(loc).Zone()
	for h := 0; h < 366*24; h++ {
		next := t.Add(time.Hour)
		name, off := next.In(loc).Zone()
		if off != prev {
			out = append(out, transition{at: next, from: prev, to: off, name: name, isDST: next.In(loc).IsDST()})
			prev = off
		}
		t = next
	}
	return out
}

func offset(sec int) string {
	sign := "+"
	if sec < 0 {
		sign = "-"
		sec = -sec
	}
	return fmt.Sprintf("%s%02d%02d", sign, sec/3600, sec%3600/60)
}

// MasterData liest die bearbeitbaren Felder des Hauptteils (bei Serien der erste Termin).
func MasterData(cal *Component, loc *time.Location) (EventData, error) {
	m, err := Master(cal)
	if err != nil {
		return EventData{}, err
	}
	ev, err := readEvent(m, loc)
	if err != nil {
		return EventData{}, err
	}
	d := EventData{
		Summary: m.Text("SUMMARY"), Location: m.Text("LOCATION"), Description: m.Text("DESCRIPTION"),
		Start: ev.start, End: ev.end, AllDay: ev.allDay,
	}
	if rr := m.Prop("RRULE"); rr != nil {
		d.RRule = rr.Value
	}
	return d, nil
}
