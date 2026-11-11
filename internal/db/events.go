package db

import (
	"context"
	"database/sql"
	"time"
)

// Event ist ein eigener Termin (nicht aus SOGo). Zeiten in UTC.
type Event struct {
	ID       uint64
	Title    string
	Location string
	Note     string
	AllDay   bool
	Start    time.Time
	End      time.Time
	RRule    string // "" = einmalig
	ExDates  []time.Time
}

const eventColumns = `id, title, location, note, all_day, starts_at, ends_at, rrule`

func scanEvent(row scanner) (Event, error) {
	var e Event
	var rrule sql.NullString
	err := row.Scan(&e.ID, &e.Title, &e.Location, &e.Note, &e.AllDay, &e.Start, &e.End, &rrule)
	e.RRule = rrule.String
	return e, notFound(err)
}

// EventsBetween liefert einmalige Termine, die [from, to) berühren, und alle
// Serien, die vor to beginnen (aufgelöst wird im Server).
func (r *Repo) EventsBetween(ctx context.Context, from, to time.Time) ([]Event, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+eventColumns+` FROM events
		WHERE starts_at < ? AND (rrule IS NOT NULL OR ends_at > ? OR (ends_at = starts_at AND starts_at >= ?))
		ORDER BY starts_at`, to.UTC(), from.UTC(), from.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	byID := map[uint64]int{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		byID[e.ID] = len(out)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	ex, err := r.db.QueryContext(ctx, `SELECT x.event_id, x.occurrence FROM event_exdates x
		JOIN events e ON e.id = x.event_id WHERE e.rrule IS NOT NULL AND e.starts_at < ?`, to.UTC())
	if err != nil {
		return nil, err
	}
	defer ex.Close()
	for ex.Next() {
		var id uint64
		var t time.Time
		if err := ex.Scan(&id, &t); err != nil {
			return nil, err
		}
		if i, ok := byID[id]; ok {
			out[i].ExDates = append(out[i].ExDates, t)
		}
	}
	return out, ex.Err()
}

func (r *Repo) Event(ctx context.Context, id uint64) (Event, error) {
	return scanEvent(r.db.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM events WHERE id = ?`, id))
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func (r *Repo) CreateEvent(ctx context.Context, e Event) (uint64, error) {
	res, err := r.db.ExecContext(ctx, `INSERT INTO events (title, location, note, all_day, starts_at, ends_at, rrule)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, e.Title, e.Location, e.Note, e.AllDay, e.Start.UTC(), e.End.UTC(), nullString(e.RRule))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return uint64(id), err
}

// UpdateEvent ändert den Termin. Verschiebt sich der Beginn einer Serie oder
// endet sie als Serie, passen gestrichene Einzeltermine nicht mehr – sie entfallen.
func (r *Repo) UpdateEvent(ctx context.Context, e Event) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldStart time.Time
	if err := tx.QueryRowContext(ctx, `SELECT starts_at FROM events WHERE id = ? FOR UPDATE`, e.ID).Scan(&oldStart); err != nil {
		return notFound(err)
	}
	if !oldStart.Equal(e.Start.UTC()) || e.RRule == "" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM event_exdates WHERE event_id = ?`, e.ID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE events SET title = ?, location = ?, note = ?, all_day = ?,
		starts_at = ?, ends_at = ?, rrule = ? WHERE id = ?`,
		e.Title, e.Location, e.Note, e.AllDay, e.Start.UTC(), e.End.UTC(), nullString(e.RRule), e.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repo) DeleteEvent(ctx context.Context, id uint64) error {
	return affected(r.db.ExecContext(ctx, `DELETE FROM events WHERE id = ?`, id))
}

// ExcludeOccurrence streicht ein Vorkommen einer Serie.
func (r *Repo) ExcludeOccurrence(ctx context.Context, id uint64, occurrence time.Time) error {
	_, err := r.db.ExecContext(ctx, `INSERT IGNORE INTO event_exdates (event_id, occurrence) VALUES (?, ?)`, id, occurrence.UTC())
	return err
}
