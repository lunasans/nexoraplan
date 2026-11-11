package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/go-sql-driver/mysql"
)

var (
	ErrNotFound = errors.New("nicht gefunden")
	ErrExists   = errors.New("existiert bereits")
)

// Repo bündelt alle Datenbankabfragen. Die HTTP-Handler sprechen nur mit
// dem Repo, nie direkt mit SQL.
type Repo struct {
	db *sql.DB
}

func NewRepo(database *sql.DB) *Repo {
	return &Repo{db: database}
}

func (r *Repo) Ping(ctx context.Context) error {
	return r.db.PingContext(ctx)
}

type scanner interface {
	Scan(dest ...any) error
}

func isDuplicate(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// affected: keine betroffene Zeile = nicht gefunden.
func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Tage stehen als DATE in der Datenbank und als „2006-01-02“ in Go –
// unabhängig von jeder Zeitzone.
const DateLayout = "2006-01-02"

func dateString(t time.Time) string { return t.Format(DateLayout) }

func nullDate(t sql.NullTime) *string {
	if !t.Valid {
		return nil
	}
	s := dateString(t.Time)
	return &s
}
