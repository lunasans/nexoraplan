package db

import (
	"context"
	"database/sql"
	"time"
)

type Task struct {
	ID    uint64  `json:"id"`
	Week  string  `json:"week"` // Montag der KW
	Day   *string `json:"day"`  // nil = irgendwann in der Woche
	Title string  `json:"title"`
	Done  bool    `json:"done"`
}

func scanTask(row scanner) (Task, error) {
	var t Task
	var week time.Time
	var day sql.NullTime
	if err := row.Scan(&t.ID, &week, &day, &t.Title, &t.Done); err != nil {
		return t, notFound(err)
	}
	t.Week = dateString(week)
	t.Day = nullDate(day)
	return t, nil
}

const taskColumns = `id, week, day, title, done`

// Tasks liefert die Aufgaben einer Woche: erst ohne Tag, dann nach Tag, offene vor erledigten.
func (r *Repo) Tasks(ctx context.Context, week string) ([]Task, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE week = ?
		ORDER BY day IS NOT NULL, day, done, position, id`, week)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repo) Task(ctx context.Context, id uint64) (Task, error) {
	return scanTask(r.db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id))
}

func (r *Repo) CreateTask(ctx context.Context, week string, day *string, title string) (Task, error) {
	res, err := r.db.ExecContext(ctx, `INSERT INTO tasks (week, day, title, position)
		SELECT ?, ?, ?, COALESCE(MAX(position), 0) + 1 FROM tasks WHERE week = ?`, week, day, title, week)
	if err != nil {
		return Task{}, err
	}
	id, _ := res.LastInsertId()
	return r.Task(ctx, uint64(id))
}

// UpdateTask schreibt Titel, Tag und Erledigt-Status (week folgt dem Tag).
func (r *Repo) UpdateTask(ctx context.Context, t Task) error {
	return affected(r.db.ExecContext(ctx, `UPDATE tasks SET week = ?, day = ?, title = ?, done = ? WHERE id = ?`,
		t.Week, t.Day, t.Title, t.Done, t.ID))
}

func (r *Repo) DeleteTask(ctx context.Context, id uint64) error {
	return affected(r.db.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, id))
}

// CarryOver verschiebt die offenen Aufgaben einer Woche in die nächste.
// Ein Tag wandert mit (Montag bleibt Montag).
func (r *Repo) CarryOver(ctx context.Context, week, next string) (int64, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE tasks
		SET week = ?, day = IF(day IS NULL, NULL, DATE_ADD(day, INTERVAL 7 DAY)), position = position + 100000
		WHERE week = ? AND NOT done`, next, week)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
