// Package ical liest und schreibt iCalendar (RFC 5545) so, dass beim
// Zurückschreiben alles erhalten bleibt, was die App nicht anfasst
// (Teilnehmer, Erinnerungen, eigene X-Felder anderer Programme).
package ical

import (
	"errors"
	"strings"
)

// Param ist ein Parameter einer Eigenschaft, z. B. TZID=Europe/Berlin.
type Param struct {
	Name  string
	Value string
}

// Prop ist eine Zeile wie DTSTART;TZID=Europe/Berlin:20261111T090000.
// Value ist der Rohwert (bei Text noch mit Escapes).
type Prop struct {
	Name   string
	Params []Param
	Value  string
}

func (p *Prop) Param(name string) string {
	for _, x := range p.Params {
		if strings.EqualFold(x.Name, name) {
			return x.Value
		}
	}
	return ""
}

// Component ist ein BEGIN:…/END:…-Block.
type Component struct {
	Name     string
	Props    []*Prop
	Children []*Component
}

// Prop liefert die erste Eigenschaft mit diesem Namen oder nil.
func (c *Component) Prop(name string) *Prop {
	for _, p := range c.Props {
		if strings.EqualFold(p.Name, name) {
			return p
		}
	}
	return nil
}

// PropsNamed liefert alle Eigenschaften mit diesem Namen (EXDATE kann mehrfach stehen).
func (c *Component) PropsNamed(name string) []*Prop {
	var out []*Prop
	for _, p := range c.Props {
		if strings.EqualFold(p.Name, name) {
			out = append(out, p)
		}
	}
	return out
}

// Text liefert den entschlüsselten Textwert einer Eigenschaft ("" = fehlt).
func (c *Component) Text(name string) string {
	if p := c.Prop(name); p != nil {
		return UnescapeText(p.Value)
	}
	return ""
}

// Set ersetzt die erste Eigenschaft name (oder hängt sie an) und entfernt weitere.
func (c *Component) Set(p *Prop) {
	replaced := false
	kept := c.Props[:0]
	for _, x := range c.Props {
		if strings.EqualFold(x.Name, p.Name) {
			if !replaced {
				kept = append(kept, p)
				replaced = true
			}
			continue
		}
		kept = append(kept, x)
	}
	c.Props = kept
	if !replaced {
		c.Props = append(c.Props, p)
	}
}

// SetText setzt einen Textwert; leer entfernt die Eigenschaft.
func (c *Component) SetText(name, value string) {
	if value == "" {
		c.Remove(name)
		return
	}
	c.Set(&Prop{Name: name, Value: EscapeText(value)})
}

func (c *Component) Remove(name string) {
	kept := c.Props[:0]
	for _, x := range c.Props {
		if !strings.EqualFold(x.Name, name) {
			kept = append(kept, x)
		}
	}
	c.Props = kept
}

// ChildrenNamed liefert alle Unterkomponenten mit diesem Namen.
func (c *Component) ChildrenNamed(name string) []*Component {
	var out []*Component
	for _, ch := range c.Children {
		if strings.EqualFold(ch.Name, name) {
			out = append(out, ch)
		}
	}
	return out
}

// Parse liest einen iCalendar-Text (einen VCALENDAR-Block).
func Parse(data string) (*Component, error) {
	lines := unfold(data)
	var stack []*Component
	var root *Component
	for _, line := range lines {
		if line == "" {
			continue
		}
		p, err := parseLine(line)
		if err != nil {
			return nil, err
		}
		switch strings.ToUpper(p.Name) {
		case "BEGIN":
			c := &Component{Name: strings.ToUpper(p.Value)}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, c)
			} else if root == nil {
				root = c
			} else {
				return nil, errors.New("iCalendar: mehr als ein Wurzelblock")
			}
			stack = append(stack, c)
		case "END":
			if len(stack) == 0 || !strings.EqualFold(stack[len(stack)-1].Name, p.Value) {
				return nil, errors.New("iCalendar: END ohne passendes BEGIN")
			}
			stack = stack[:len(stack)-1]
		default:
			if len(stack) == 0 {
				return nil, errors.New("iCalendar: Eigenschaft außerhalb eines Blocks")
			}
			cur := stack[len(stack)-1]
			cur.Props = append(cur.Props, p)
		}
	}
	if root == nil || len(stack) != 0 {
		return nil, errors.New("iCalendar: unvollständig")
	}
	return root, nil
}

// unfold fügt umbrochene Zeilen (Fortsetzung beginnt mit Leerzeichen/Tab) zusammen.
func unfold(data string) []string {
	data = strings.ReplaceAll(data, "\r\n", "\n")
	var out []string
	for _, l := range strings.Split(data, "\n") {
		if (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) && len(out) > 0 {
			out[len(out)-1] += l[1:]
			continue
		}
		out = append(out, strings.TrimRight(l, "\r"))
	}
	return out
}

// parseLine zerlegt NAME;PARAM=WERT;PARAM="WERT:MIT:DOPPELPUNKT":WERT.
func parseLine(line string) (*Prop, error) {
	p := &Prop{}
	i := 0
	// Name
	for i < len(line) && line[i] != ';' && line[i] != ':' {
		i++
	}
	p.Name = strings.ToUpper(line[:i])
	if p.Name == "" {
		return nil, errors.New("iCalendar: Zeile ohne Namen")
	}
	for i < len(line) && line[i] == ';' {
		i++
		start := i
		for i < len(line) && line[i] != '=' {
			i++
		}
		if i >= len(line) {
			return nil, errors.New("iCalendar: Parameter ohne Wert")
		}
		name := strings.ToUpper(line[start:i])
		i++ // '='
		var val strings.Builder
		for i < len(line) && line[i] != ';' && line[i] != ':' {
			if line[i] == '"' {
				end := strings.IndexByte(line[i+1:], '"')
				if end < 0 {
					return nil, errors.New("iCalendar: offenes Anführungszeichen")
				}
				val.WriteString(line[i+1 : i+1+end])
				i += end + 2
				continue
			}
			val.WriteByte(line[i])
			i++
		}
		p.Params = append(p.Params, Param{Name: name, Value: val.String()})
	}
	if i >= len(line) || line[i] != ':' {
		return nil, errors.New("iCalendar: Zeile ohne Wert: " + p.Name)
	}
	p.Value = line[i+1:]
	return p, nil
}

// Encode schreibt die Komponente als iCalendar-Text (CRLF, Zeilen ≤ 75 Byte).
func (c *Component) Encode() string {
	var b strings.Builder
	c.encode(&b)
	return b.String()
}

func (c *Component) encode(b *strings.Builder) {
	writeFolded(b, "BEGIN:"+c.Name)
	for _, p := range c.Props {
		var line strings.Builder
		line.WriteString(p.Name)
		for _, x := range p.Params {
			line.WriteString(";" + x.Name + "=")
			if strings.ContainsAny(x.Value, ":;,") {
				line.WriteString(`"` + x.Value + `"`)
			} else {
				line.WriteString(x.Value)
			}
		}
		line.WriteString(":" + p.Value)
		writeFolded(b, line.String())
	}
	for _, ch := range c.Children {
		ch.encode(b)
	}
	writeFolded(b, "END:"+c.Name)
}

// writeFolded bricht nach 75 Byte um, ohne UTF-8-Zeichen zu zerteilen.
func writeFolded(b *strings.Builder, line string) {
	limit := 75
	for len(line) > limit {
		cut := limit
		for cut > 0 && line[cut]&0xC0 == 0x80 { // Folgebyte eines UTF-8-Zeichens
			cut--
		}
		b.WriteString(line[:cut] + "\r\n ")
		line = line[cut:]
		limit = 74 // Fortsetzungszeilen beginnen mit einem Leerzeichen
	}
	b.WriteString(line + "\r\n")
}

// UnescapeText entschlüsselt \n, \, \; und \\ in Textwerten.
func UnescapeText(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n', 'N':
				b.WriteByte('\n')
			default:
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func EscapeText(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\r\n", `\n`, "\n", `\n`, ",", `\,`, ";", `\;`).Replace(s)
}
