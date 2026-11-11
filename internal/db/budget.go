package db

import (
	"context"
	"database/sql"
	"time"
)

type Category struct {
	ID          uint64  `json:"id"`
	Name        string  `json:"name"`
	Color       string  `json:"color"`
	BudgetCents int64   `json:"budget_cents"` // Monatsbudget, 0 = keins
	PocketID    *uint64 `json:"pocket_id"`    // Ausgaben dieser Kategorie gehen von diesem Pocket
}

// Pocket ist ein Unterkonto, auf das monatlich ein fester Betrag geht.
type Pocket struct {
	ID           uint64 `json:"id"`
	Name         string `json:"name"`
	Color        string `json:"color"`
	MonthlyCents int64  `json:"monthly_cents"`
	// Stand für die Vorschau: BalanceCents am Tag BalanceOn (beide nil = keiner eingetragen)
	BalanceCents *int64  `json:"balance_cents"`
	BalanceOn    *string `json:"balance_on"`
	DepositDay   int     `json:"deposit_day"` // Tag, an dem der monatliche Betrag kommt (1–31)
}

type Transaction struct {
	ID          uint64  `json:"id"`
	Kind        string  `json:"kind"` // expense | income
	AmountCents int64   `json:"amount_cents"`
	Day         string  `json:"day"`
	CategoryID  *uint64 `json:"category_id"`
	Note        string  `json:"note"`
	RecurringID *uint64 `json:"recurring_id"`
}

type Recurring struct {
	ID          uint64  `json:"id"`
	Title       string  `json:"title"`
	Kind        string  `json:"kind"`
	AmountCents int64   `json:"amount_cents"`
	CategoryID  *uint64 `json:"category_id"`
	PocketID    *uint64 `json:"pocket_id"` // abgebucht von diesem Pocket
	Day         int     `json:"day"`       // 1–31; fehlt der Tag im Monat, am letzten
	// IntervalMonths: 1 monatlich, 3 vierteljährlich, 6 halbjährlich, 12 jährlich –
	// gebucht ab dem Monat von StartsOn
	IntervalMonths int     `json:"interval_months"`
	StartsOn       string  `json:"starts_on"`
	EndsOn         *string `json:"ends_on"`
	BookedUntil    *string `json:"booked_until"`
	// PaymentMethod: sepa, standing_order, transfer, card, paypal, cash, other oder ""
	PaymentMethod string `json:"payment_method"`
	PaymentNote   string `json:"payment_note"` // z. B. „Visa …1234“
	// LogoVersion: 0 = kein Logo; ändert sich mit jedem neuen Logo (Cache im Browser)
	LogoVersion int `json:"logo_version"`
}

func nullID(n sql.NullInt64) *uint64 {
	if !n.Valid {
		return nil
	}
	v := uint64(n.Int64)
	return &v
}

// --- Kategorien -------------------------------------------------------------

func (r *Repo) Categories(ctx context.Context) ([]Category, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, color, budget_cents, pocket_id FROM categories ORDER BY position, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Category{}
	for rows.Next() {
		var c Category
		var pocket sql.NullInt64
		if err := rows.Scan(&c.ID, &c.Name, &c.Color, &c.BudgetCents, &pocket); err != nil {
			return nil, err
		}
		c.PocketID = nullID(pocket)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repo) CreateCategory(ctx context.Context, c Category) (Category, error) {
	res, err := r.db.ExecContext(ctx, `INSERT INTO categories (name, color, budget_cents, pocket_id, position)
		SELECT ?, ?, ?, ?, COALESCE(MAX(position), 0) + 1 FROM categories`, c.Name, c.Color, c.BudgetCents, c.PocketID)
	if isDuplicate(err) {
		return c, ErrExists
	}
	if err != nil {
		return c, err
	}
	id, _ := res.LastInsertId()
	c.ID = uint64(id)
	return c, nil
}

func (r *Repo) UpdateCategory(ctx context.Context, c Category) error {
	res, err := r.db.ExecContext(ctx, `UPDATE categories SET name = ?, color = ?, budget_cents = ?, pocket_id = ? WHERE id = ?`,
		c.Name, c.Color, c.BudgetCents, c.PocketID, c.ID)
	if isDuplicate(err) {
		return ErrExists
	}
	return affected(res, err) // ClientFoundRows: zählt auch Zeilen ohne Änderung
}

// DeleteCategory: Buchungen bleiben, nur ohne Kategorie.
func (r *Repo) DeleteCategory(ctx context.Context, id uint64) error {
	return affected(r.db.ExecContext(ctx, `DELETE FROM categories WHERE id = ?`, id))
}

// --- Pockets ----------------------------------------------------------------

func (r *Repo) Pockets(ctx context.Context) ([]Pocket, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, color, monthly_cents, balance_cents, balance_on, deposit_day
		FROM pockets ORDER BY position, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Pocket{}
	for rows.Next() {
		var p Pocket
		var balance sql.NullInt64
		var on sql.NullTime
		if err := rows.Scan(&p.ID, &p.Name, &p.Color, &p.MonthlyCents, &balance, &on, &p.DepositDay); err != nil {
			return nil, err
		}
		if balance.Valid {
			p.BalanceCents = &balance.Int64
		}
		p.BalanceOn = nullDate(on)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repo) CreatePocket(ctx context.Context, p Pocket) (Pocket, error) {
	res, err := r.db.ExecContext(ctx, `INSERT INTO pockets (name, color, monthly_cents, balance_cents, balance_on, deposit_day, position)
		SELECT ?, ?, ?, ?, ?, ?, COALESCE(MAX(position), 0) + 1 FROM pockets`,
		p.Name, p.Color, p.MonthlyCents, p.BalanceCents, p.BalanceOn, p.DepositDay)
	if isDuplicate(err) {
		return p, ErrExists
	}
	if err != nil {
		return p, err
	}
	id, _ := res.LastInsertId()
	p.ID = uint64(id)
	return p, nil
}

func (r *Repo) UpdatePocket(ctx context.Context, p Pocket) error {
	res, err := r.db.ExecContext(ctx, `UPDATE pockets SET name = ?, color = ?, monthly_cents = ?, balance_cents = ?, balance_on = ?,
		deposit_day = ? WHERE id = ?`, p.Name, p.Color, p.MonthlyCents, p.BalanceCents, p.BalanceOn, p.DepositDay, p.ID)
	if isDuplicate(err) {
		return ErrExists
	}
	return affected(res, err)
}

// DeletePocket: Abos/Verträge und Kategorien bleiben, nur ohne Pocket.
func (r *Repo) DeletePocket(ctx context.Context, id uint64) error {
	return affected(r.db.ExecContext(ctx, `DELETE FROM pockets WHERE id = ?`, id))
}

func (r *Repo) PocketExists(ctx context.Context, id uint64) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pockets WHERE id = ?`, id).Scan(&n)
	return n > 0, err
}

func (r *Repo) CategoryExists(ctx context.Context, id uint64) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM categories WHERE id = ?`, id).Scan(&n)
	return n > 0, err
}

// --- Buchungen --------------------------------------------------------------

const txColumns = `id, kind, amount_cents, day, category_id, note, recurring_id`

func scanTx(row scanner) (Transaction, error) {
	var t Transaction
	var day time.Time
	var cat, rec sql.NullInt64
	if err := row.Scan(&t.ID, &t.Kind, &t.AmountCents, &day, &cat, &t.Note, &rec); err != nil {
		return t, notFound(err)
	}
	t.Day = dateString(day)
	t.CategoryID = nullID(cat)
	t.RecurringID = nullID(rec)
	return t, nil
}

// Transactions liefert die Buchungen mit from <= Tag < to (Daten als 2006-01-02).
func (r *Repo) Transactions(ctx context.Context, from, to string) ([]Transaction, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+txColumns+` FROM transactions WHERE day >= ? AND day < ?
		ORDER BY day DESC, id DESC`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Transaction{}
	for rows.Next() {
		t, err := scanTx(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repo) Transaction(ctx context.Context, id uint64) (Transaction, error) {
	return scanTx(r.db.QueryRowContext(ctx, `SELECT `+txColumns+` FROM transactions WHERE id = ?`, id))
}

func (r *Repo) CreateTransaction(ctx context.Context, t Transaction) (Transaction, error) {
	res, err := r.db.ExecContext(ctx, `INSERT INTO transactions (kind, amount_cents, day, category_id, note, recurring_id)
		VALUES (?, ?, ?, ?, ?, ?)`, t.Kind, t.AmountCents, t.Day, t.CategoryID, t.Note, t.RecurringID)
	if err != nil {
		return t, err
	}
	id, _ := res.LastInsertId()
	return r.Transaction(ctx, uint64(id))
}

func (r *Repo) UpdateTransaction(ctx context.Context, t Transaction) error {
	return affected(r.db.ExecContext(ctx, `UPDATE transactions SET kind = ?, amount_cents = ?, day = ?, category_id = ?, note = ?
		WHERE id = ?`, t.Kind, t.AmountCents, t.Day, t.CategoryID, t.Note, t.ID))
}

func (r *Repo) DeleteTransaction(ctx context.Context, id uint64) error {
	return affected(r.db.ExecContext(ctx, `DELETE FROM transactions WHERE id = ?`, id))
}

// --- Feste Buchungen --------------------------------------------------------

// Ohne das Logo selbst – das lädt nur RecurringLogo.
const recColumns = `id, title, kind, amount_cents, category_id, pocket_id, day, interval_months, starts_on, ends_on, booked_until,
	payment_method, payment_note, IF(logo IS NULL, 0, logo_version)`

func scanRecurring(row scanner) (Recurring, error) {
	var x Recurring
	var cat, pocket sql.NullInt64
	var starts time.Time
	var ends, booked sql.NullTime
	if err := row.Scan(&x.ID, &x.Title, &x.Kind, &x.AmountCents, &cat, &pocket, &x.Day, &x.IntervalMonths, &starts, &ends, &booked,
		&x.PaymentMethod, &x.PaymentNote, &x.LogoVersion); err != nil {
		return x, notFound(err)
	}
	x.CategoryID = nullID(cat)
	x.PocketID = nullID(pocket)
	x.StartsOn = dateString(starts)
	x.EndsOn = nullDate(ends)
	x.BookedUntil = nullDate(booked)
	return x, nil
}

func (r *Repo) RecurringList(ctx context.Context) ([]Recurring, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+recColumns+` FROM recurring ORDER BY kind DESC, day, title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Recurring{}
	for rows.Next() {
		x, err := scanRecurring(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (r *Repo) RecurringByID(ctx context.Context, id uint64) (Recurring, error) {
	return scanRecurring(r.db.QueryRowContext(ctx, `SELECT `+recColumns+` FROM recurring WHERE id = ?`, id))
}

func (r *Repo) CreateRecurring(ctx context.Context, x Recurring) (Recurring, error) {
	res, err := r.db.ExecContext(ctx, `INSERT INTO recurring (title, kind, amount_cents, category_id, pocket_id, day, interval_months, starts_on, ends_on,
		payment_method, payment_note) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		x.Title, x.Kind, x.AmountCents, x.CategoryID, x.PocketID, x.Day, x.IntervalMonths, x.StartsOn, x.EndsOn, x.PaymentMethod, x.PaymentNote)
	if err != nil {
		return x, err
	}
	id, _ := res.LastInsertId()
	return r.RecurringByID(ctx, uint64(id))
}

// UpdateRecurring ändert die Vorlage; bereits gebuchte Monate bleiben, wie sie sind.
func (r *Repo) UpdateRecurring(ctx context.Context, x Recurring) error {
	return affected(r.db.ExecContext(ctx, `UPDATE recurring SET title = ?, kind = ?, amount_cents = ?, category_id = ?, pocket_id = ?, day = ?,
		interval_months = ?, starts_on = ?, ends_on = ?, payment_method = ?, payment_note = ? WHERE id = ?`,
		x.Title, x.Kind, x.AmountCents, x.CategoryID, x.PocketID, x.Day, x.IntervalMonths, x.StartsOn, x.EndsOn, x.PaymentMethod, x.PaymentNote, x.ID))
}

// SetRecurringLogo speichert ein Logo (PNG); nil entfernt es.
func (r *Repo) SetRecurringLogo(ctx context.Context, id uint64, png []byte) error {
	return affected(r.db.ExecContext(ctx, `UPDATE recurring SET logo = ?, logo_version = logo_version + 1 WHERE id = ?`, png, id))
}

// RecurringLogo liefert das Logo; ErrNotFound, wenn es keins gibt.
func (r *Repo) RecurringLogo(ctx context.Context, id uint64) ([]byte, error) {
	var png []byte
	err := r.db.QueryRowContext(ctx, `SELECT logo FROM recurring WHERE id = ? AND logo IS NOT NULL`, id).Scan(&png)
	return png, notFound(err)
}

// MonthlyShare: Anteil pro Monat (240 € jährlich = 20 €), auf Cent gerundet.
func (x Recurring) MonthlyShare() int64 {
	n := int64(max(x.IntervalMonths, 1))
	return (x.AmountCents + n/2) / n
}

// ActiveIn: Läuft die feste Buchung im Monat month (erster Tag des Monats)?
func (x Recurring) ActiveIn(month time.Time) bool {
	if x.StartsOn >= month.AddDate(0, 1, 0).Format(DateLayout) {
		return false
	}
	return x.EndsOn == nil || *x.EndsOn >= month.Format(DateLayout)
}

func (r *Repo) DeleteRecurring(ctx context.Context, id uint64) error {
	return affected(r.db.ExecContext(ctx, `DELETE FROM recurring WHERE id = ?`, id))
}

// DueDays liefert die Buchungstage einer festen Buchung mit after < Tag <= until:
// alle IntervalMonths Monate ab dem Monat von StartsOn. Fehlt der Tag im Monat
// (31. im April), wird am letzten Tag gebucht.
func DueDays(x Recurring, after, until time.Time) []time.Time {
	start, err := time.Parse(DateLayout, x.StartsOn)
	if err != nil {
		return nil
	}
	var end time.Time
	if x.EndsOn != nil {
		end, _ = time.Parse(DateLayout, *x.EndsOn)
	}
	var out []time.Time
	step := max(x.IntervalMonths, 1)
	for m := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC); !m.After(until); m = m.AddDate(0, step, 0) {
		last := m.AddDate(0, 1, -1).Day()
		day := time.Date(m.Year(), m.Month(), min(x.Day, last), 0, 0, 0, 0, time.UTC)
		if day.Before(start) || !day.After(after) || day.After(until) || (!end.IsZero() && day.After(end)) {
			continue
		}
		out = append(out, day)
	}
	return out
}

// BookRecurring bucht alle fälligen festen Buchungen bis einschließlich today.
// Jede Vorlage merkt sich, bis wann gebucht ist: Löscht man eine Buchung von
// Hand, kommt sie nicht wieder.
func (r *Repo) BookRecurring(ctx context.Context, today time.Time) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT `+recColumns+` FROM recurring
		WHERE starts_on <= ? AND (booked_until IS NULL OR booked_until < ?) FOR UPDATE`, dateString(today), dateString(today))
	if err != nil {
		return 0, err
	}
	var list []Recurring
	for rows.Next() {
		x, err := scanRecurring(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	booked := 0
	for _, x := range list {
		after := time.Time{}
		if x.BookedUntil != nil {
			after, _ = time.Parse(DateLayout, *x.BookedUntil)
		}
		for _, day := range DueDays(x, after, today) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO transactions (kind, amount_cents, day, category_id, note, recurring_id)
				VALUES (?, ?, ?, ?, ?, ?)`, x.Kind, x.AmountCents, dateString(day), x.CategoryID, x.Title, x.ID); err != nil {
				return 0, err
			}
			booked++
		}
		if _, err := tx.ExecContext(ctx, `UPDATE recurring SET booked_until = ? WHERE id = ?`, dateString(today), x.ID); err != nil {
			return 0, err
		}
	}
	return booked, tx.Commit()
}
