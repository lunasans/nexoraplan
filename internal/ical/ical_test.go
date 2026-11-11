package ical

import (
	"strings"
	"testing"
	"time"
)

var berlin, _ = time.LoadLocation("Europe/Berlin")

// Aufbau wie von SOGo 5 geschrieben: VTIMEZONE, Serie mit EXDATE und ein
// verschobener Einzeltermin, dazu Felder, die erhalten bleiben müssen.
const sogoSeries = "BEGIN:VCALENDAR\r\n" +
	"VERSION:2.0\r\n" +
	"PRODID:-//Inverse inc./SOGo 5.11.0//EN\r\n" +
	"BEGIN:VTIMEZONE\r\n" +
	"TZID:Europe/Berlin\r\n" +
	"X-LIC-LOCATION:Europe/Berlin\r\n" +
	"BEGIN:DAYLIGHT\r\n" +
	"TZOFFSETFROM:+0100\r\nTZOFFSETTO:+0200\r\nTZNAME:CEST\r\nDTSTART:19700329T020000\r\n" +
	"RRULE:FREQ=YEARLY;BYMONTH=3;BYDAY=-1SU\r\n" +
	"END:DAYLIGHT\r\n" +
	"BEGIN:STANDARD\r\n" +
	"TZOFFSETFROM:+0200\r\nTZOFFSETTO:+0100\r\nTZNAME:CET\r\nDTSTART:19701025T030000\r\n" +
	"RRULE:FREQ=YEARLY;BYMONTH=10;BYDAY=-1SU\r\n" +
	"END:STANDARD\r\n" +
	"END:VTIMEZONE\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:1A2B-5F3C0000-1-6A1B2C00\r\n" +
	"SUMMARY:Teamrunde\\, Raum 2\r\n" +
	"LOCATION:Büro\r\n" +
	"CLASS:PUBLIC\r\n" +
	"X-SOGO-SEND-APPOINTMENT-NOTIFICATIONS:NO\r\n" +
	"ATTENDEE;PARTSTAT=ACCEPTED;CN=\"Müller, Eva\":mailto:eva@example.de\r\n" +
	"DTSTART;TZID=Europe/Berlin:20261102T090000\r\n" +
	"DTEND;TZID=Europe/Berlin:20261102T093000\r\n" +
	"RRULE:FREQ=WEEKLY;BYDAY=MO\r\n" +
	"EXDATE;TZID=Europe/Berlin:20261109T090000\r\n" +
	"BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT15M\r\nEND:VALARM\r\n" +
	"END:VEVENT\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:1A2B-5F3C0000-1-6A1B2C00\r\n" +
	"SUMMARY:Teamrunde (verschoben)\r\n" +
	"RECURRENCE-ID;TZID=Europe/Berlin:20261116T090000\r\n" +
	"DTSTART;TZID=Europe/Berlin:20261124T140000\r\n" +
	"DTEND;TZID=Europe/Berlin:20261124T143000\r\n" +
	"END:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func week(y int, m time.Month, d int) (time.Time, time.Time) {
	from := time.Date(y, m, d, 0, 0, 0, 0, berlin)
	return from, from.AddDate(0, 0, 7)
}

func summaries(occ []Occurrence) []string {
	var out []string
	for _, o := range occ {
		out = append(out, o.Start.In(berlin).Format("02.01. 15:04")+" "+o.Summary)
	}
	return out
}

func TestExpandSeries(t *testing.T) {
	cal, err := Parse(sogoSeries)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		from int
		want []string
	}{
		{2, []string{"02.11. 09:00 Teamrunde, Raum 2"}},
		{9, nil},  // gestrichen
		{16, nil}, // verschoben in die Woche danach
		{23, []string{"23.11. 09:00 Teamrunde, Raum 2", "24.11. 14:00 Teamrunde (verschoben)"}},
	}
	for _, c := range cases {
		from, to := week(2026, 11, c.from)
		occ, err := Expand(cal, from, to, berlin)
		if err != nil {
			t.Fatal(err)
		}
		got := summaries(occ)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("Woche ab %d.11.: %v, erwartet %v", c.from, got, c.want)
		}
	}
}

func TestExpandAllDayAndDuration(t *testing.T) {
	cal, err := Parse("BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:a\nSUMMARY:Urlaub\nDTSTART;VALUE=DATE:20261110\nDTEND;VALUE=DATE:20261113\nEND:VEVENT\n" +
		"BEGIN:VEVENT\nUID:b\nSUMMARY:Kurz\nDTSTART:20261111T120000Z\nDURATION:PT45M\nEND:VEVENT\nEND:VCALENDAR\n")
	if err != nil {
		t.Fatal(err)
	}
	from, to := week(2026, 11, 12) // beginnt mitten im Urlaub
	occ, _ := Expand(cal, from, to, berlin)
	if len(occ) != 1 || occ[0].Summary != "Urlaub" || !occ[0].AllDay {
		t.Fatalf("erwartet nur den Urlaub, bekommen %+v", occ)
	}
	from, to = week(2026, 11, 9)
	occ, _ = Expand(cal, from, to, berlin)
	if len(occ) != 2 || occ[1].End.Sub(occ[1].Start) != 45*time.Minute {
		t.Fatalf("Dauer nicht übernommen: %+v", occ)
	}
}

// Beim Ändern bleiben Teilnehmer, Erinnerung und X-Felder erhalten.
func TestUpdateKeepsForeignFields(t *testing.T) {
	cal, _ := Parse(sogoSeries)
	m, _ := Master(cal)
	start := time.Date(2026, 11, 2, 9, 0, 0, 0, berlin)
	err := Update(cal, EventData{Summary: "Teamrunde neu", Start: start, End: start.Add(time.Hour), RRule: m.Prop("RRULE").Value}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	out := cal.Encode()
	for _, keep := range []string{"ATTENDEE;PARTSTAT=ACCEPTED;CN=\"Müller, Eva\":mailto:eva@example.de", "TRIGGER:-PT15M",
		"X-SOGO-SEND-APPOINTMENT-NOTIFICATIONS:NO", "EXDATE;TZID=Europe/Berlin:20261109T090000", "RECURRENCE-ID", "SEQUENCE:1",
		"SUMMARY:Teamrunde neu", "DTEND;TZID=Europe/Berlin:20261102T100000"} {
		if !strings.Contains(out, keep) {
			t.Errorf("%q fehlt nach Update:\n%s", keep, out)
		}
	}
	if strings.Count(out, "BEGIN:VTIMEZONE") != 1 {
		t.Error("VTIMEZONE doppelt")
	}
	if strings.Contains(out, "\r\nLOCATION") {
		t.Error("leerer Ort muss die Eigenschaft entfernen")
	}

	// Beginn verschoben: Streichungen und Einzeltermine passen nicht mehr
	later := start.Add(time.Hour)
	Update(cal, EventData{Summary: "x", Start: later, End: later.Add(time.Hour), RRule: "FREQ=WEEKLY;BYDAY=MO"}, time.Now())
	out = cal.Encode()
	if strings.Contains(out, "EXDATE") || strings.Contains(out, "RECURRENCE-ID") {
		t.Errorf("alte Ausnahmen müssen entfallen:\n%s", out)
	}
}

func TestExclude(t *testing.T) {
	cal, _ := Parse(sogoSeries)
	// verschobenes Vorkommen streichen: Einzeltermin muss mit weg
	if err := Exclude(cal, time.Date(2026, 11, 16, 9, 0, 0, 0, berlin), time.Now()); err != nil {
		t.Fatal(err)
	}
	out := cal.Encode()
	if strings.Contains(out, "RECURRENCE-ID") || !strings.Contains(out, "EXDATE;TZID=Europe/Berlin:20261116T090000") {
		t.Fatalf("Streichung falsch:\n%s", out)
	}
	re, _ := Parse(out)
	from, to := week(2026, 11, 23)
	occ, _ := Expand(re, from, to, berlin)
	if len(occ) != 1 {
		t.Fatalf("nach dem Streichen: %v", summaries(occ))
	}
}

func TestNewCalendarRoundTrip(t *testing.T) {
	start := time.Date(2026, 11, 11, 18, 0, 0, 0, berlin)
	long := strings.Repeat("Ä", 60) // UTF-8 über die Zeilengrenze
	cal := NewCalendar("x@y", EventData{Summary: "Sport; mit Komma, und\nZeile", Description: long,
		Start: start, End: start.Add(90 * time.Minute), RRule: "FREQ=WEEKLY;BYDAY=WE"}, time.Now())
	out := cal.Encode()
	for _, line := range strings.Split(out, "\r\n") {
		if len(line) > 75 {
			t.Errorf("Zeile länger als 75 Byte: %q", line)
		}
	}
	if !strings.Contains(out, "BEGIN:VTIMEZONE") || !strings.Contains(out, "RRULE:FREQ=YEARLY;BYMONTH=3;BYDAY=-1SU") {
		t.Errorf("VTIMEZONE fehlt/falsch:\n%s", out)
	}
	re, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := Master(re)
	if m.Text("SUMMARY") != "Sport; mit Komma, und\nZeile" || m.Text("DESCRIPTION") != long {
		t.Errorf("Text nicht erhalten: %q / %q", m.Text("SUMMARY"), m.Text("DESCRIPTION"))
	}
	from, to := week(2026, 11, 16)
	occ, _ := Expand(re, from, to, berlin)
	if len(occ) != 1 || occ[0].Start.Format("2006-01-02 15:04") != "2026-11-18 18:00" {
		t.Fatalf("Serie nach Rundreise: %v", summaries(occ))
	}
}

func TestVTimezoneBerlin(t *testing.T) {
	tz := VTimezone(berlin, 2026).Encode()
	for _, want := range []string{"BEGIN:DAYLIGHT", "DTSTART:19700329T020000", "TZOFFSETFROM:+0100", "TZOFFSETTO:+0200",
		"BEGIN:STANDARD", "DTSTART:19701025T030000", "RRULE:FREQ=YEARLY;BYMONTH=10;BYDAY=-1SU"} {
		if !strings.Contains(tz, want) {
			t.Errorf("%q fehlt:\n%s", want, tz)
		}
	}
}

func TestLoadLocationMozilla(t *testing.T) {
	if got := LoadLocation("/mozilla.org/20050126_1/Europe/Berlin", time.UTC); got.String() != "Europe/Berlin" {
		t.Errorf("bekommen %s", got)
	}
	if got := LoadLocation("Westeuropäische Zeit", berlin); got != berlin {
		t.Errorf("unbekannte TZID muss auf den Standard fallen, bekommen %s", got)
	}
}

func TestParseDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{"PT1H30M": 90 * time.Minute, "P1D": 24 * time.Hour, "P1W": 7 * 24 * time.Hour, "-PT15M": -15 * time.Minute, "P1DT2H": 26 * time.Hour} {
		if got, err := ParseDuration(in); err != nil || got != want {
			t.Errorf("%s = %v %v", in, got, err)
		}
	}
}
