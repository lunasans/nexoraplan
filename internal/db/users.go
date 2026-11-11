package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type User struct {
	ID           uint64    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

func (r *Repo) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (r *Repo) CreateUser(ctx context.Context, email, hash string) (User, error) {
	res, err := r.db.ExecContext(ctx, `INSERT INTO users (email, password_hash) VALUES (?, ?)`, email, hash)
	if isDuplicate(err) {
		return User{}, ErrExists
	}
	if err != nil {
		return User{}, err
	}
	id, _ := res.LastInsertId()
	return r.UserByID(ctx, uint64(id))
}

const userColumns = `u.id, u.email, u.password_hash, u.created_at`

func scanUser(row scanner) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	return u, notFound(err)
}

func (r *Repo) UserByID(ctx context.Context, id uint64) (User, error) {
	return scanUser(r.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users u WHERE u.id = ?`, id))
}

func (r *Repo) UserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(r.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users u WHERE u.email = ?`, email))
}

func (r *Repo) SetPasswordHash(ctx context.Context, id uint64, hash string) error {
	return affected(r.db.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, id))
}

// --- Sitzungen --------------------------------------------------------------

func (r *Repo) CreateSession(ctx context.Context, id string, userID uint64, expires time.Time) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)`, id, userID, expires)
	return err
}

func (r *Repo) UserBySession(ctx context.Context, id string, now time.Time) (User, error) {
	return scanUser(r.db.QueryRowContext(ctx, `
		SELECT `+userColumns+` FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.id = ? AND s.expires_at > ?`, id, now))
}

func (r *Repo) DeleteSession(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// DeleteOtherSessions meldet überall ab außer in keep ("" = überall).
func (r *Repo) DeleteOtherSessions(ctx context.Context, userID uint64, keep string) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND id <> ?`, userID, keep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *Repo) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now)
	return err
}

// --- Einstellungen ----------------------------------------------------------

const (
	SettingCalDAVURL      = "caldav_url"
	SettingCalDAVUser     = "caldav_user"
	SettingCalDAVPassword = "caldav_password" // verschlüsselt (secret.Box)
	SettingHiddenCals     = "caldav_hidden"   // JSON-Liste der ausgeblendeten Kalender
	SettingDefaultCal     = "default_calendar"
	SettingBudgetTotal    = "budget_total_cents" // Gesamtbudget pro Monat, "" = keins
)

// Setting liefert den Wert einer Einstellung; "" = nicht gesetzt.
func (r *Repo) Setting(ctx context.Context, name string) (string, error) {
	var v string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE name = ?`, name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSettings schreibt mehrere Einstellungen; ein leerer Wert löscht.
func (r *Repo) SetSettings(ctx context.Context, values map[string]string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for name, v := range values {
		if v == "" {
			_, err = tx.ExecContext(ctx, `DELETE FROM settings WHERE name = ?`, name)
		} else {
			_, err = tx.ExecContext(ctx,
				`INSERT INTO settings (name, value) VALUES (?, ?) ON DUPLICATE KEY UPDATE value = VALUES(value)`, name, v)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
