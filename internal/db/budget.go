package db

import (
	"context"
	"database/sql"
	"time"
)

type Category struct {
	ID       uint64  `json:"id"`
	Name     string  `json:"name"`
	Color    string  `json:"color"`
	PocketID *uint64 `json:"pocket_id"` // Ausgaben dieser Kategorie gehen von diesem Pocket
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
	// IncomeID: befüllt vom Einkommensverteiler mit dieser festen Einnahme (Gehalt) –
	// dann zählt DepositDay nicht, die Auffüllung folgt der Gehaltsbuchung
	IncomeID *uint64 `json:"income_id"`
}

type Transaction struct {
	ID uint64 `json:"id"`
	// Kind: expense | income | topup (Auffüllung eines Pockets – Einnahme des
	// Pockets, für den Monat eine Umbuchung: weder Einnahme noch Ausgabe)
	Kind        string  `json:"kind"`
	AmountCents int64   `json:"amount_cents"`
	Day         string  `json:"day"`
	CategoryID  *uint64 `json:"category_id"`
	PocketID    *uint64 `json:"pocket_id"` // nur bei Auffüllungen
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
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, color, pocket_id FROM categories ORDER BY position, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Category{}
	for rows.Next() {
		var c Category
		var pocket sql.NullInt64
		if err := rows.Scan(&c.ID, &c.Name, &c.Color, &pocket); err != nil {
			return nil, err
		}
		c.PocketID = nullID(pocket)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repo) CreateCategory(ctx context.Context, c Category) (Category, error) {
	res, err := r.db.ExecContext(ctx, `INSERT INTO categories (name, color, pocket_id, position)
		SELECT ?, ?, ?, COALESCE(MAX(position), 0) + 1 FROM categories`, c.Name, c.Color, c.PocketID)
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
	res, err := r.db.ExecContext(ctx, `UPDATE categories SET name = ?, color = ?, pocket_id = ? WHERE id = ?`,
		c.Name, c.Color, c.PocketID, c.ID)
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

// TopupDays: Tage der Auffüllung (am Tag depositDay, sonst am Monatsletzten)
// mit after < Tag <= until, frühestens im Monat first.
func TopupDays(depositDay int, first, after, until time.Time) []time.Time {
	depositDay = min(max(depositDay, 1), 31)
	var out []time.Time
	for m := time.Date(first.Year(), first.Month(), 1, 0, 0, 0, 0, time.UTC); !m.After(until); m = m.AddDate(0, 1, 0) {
		day := time.Date(m.Year(), m.Month(), min(depositDay, m.AddDate(0, 1, -1).Day()), 0, 0, 0, 0, time.UTC)
		if day.After(after) && !day.After(until) {
			out = append(out, day)
		}
	}
	return out
}

// BookTopups bucht die monatlichen Auffüllungen der Pockets bis einschließlich
// today – frühestens im Monat, in dem das Pocket angelegt wurde. Wie bei festen
// Buchungen merkt sich jedes Pocket, bis wann gebucht ist.
//
// Ist ein Pocket an eine Einnahme gekoppelt (Einkommensverteiler), folgt je
// Gehaltsbuchung eine Auffüllung am selben Tag – höchstens eine je Monat.
// Sonst am festen Tag (Dauerauftrag).
func (r *Repo) BookTopups(ctx context.Context, today time.Time) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id, monthly_cents, deposit_day, income_id, booked_until, created_at FROM pockets
		WHERE booked_until IS NULL OR booked_until < ? FOR UPDATE`, dateString(today))
	if err != nil {
		return 0, err
	}
	type due struct {
		id      uint64
		monthly int64
		day     int
		income  sql.NullInt64
		after   time.Time
		first   time.Time
	}
	var list []due
	for rows.Next() {
		var d due
		var booked sql.NullTime
		var created time.Time
		if err := rows.Scan(&d.id, &d.monthly, &d.day, &d.income, &booked, &created); err != nil {
			rows.Close()
			return 0, err
		}
		d.first = time.Date(created.Year(), created.Month(), 1, 0, 0, 0, 0, time.UTC)
		d.after = d.first.AddDate(0, 0, -1)
		if booked.Valid && booked.Time.After(d.after) {
			d.after = booked.Time
		}
		list = append(list, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, d := range list {
		if d.monthly > 0 && d.income.Valid {
			k, err := bookIncomeTopups(ctx, tx, d.id, d.monthly, uint64(d.income.Int64), d.after, today)
			if err != nil {
				return 0, err
			}
			n += k
		} else if d.monthly > 0 {
			for _, day := range TopupDays(d.day, d.first, d.after, today) {
				if _, err := tx.ExecContext(ctx, `INSERT INTO transactions (kind, amount_cents, day, pocket_id, note)
					VALUES ('topup', ?, ?, ?, '')`, d.monthly, dateString(day), d.id); err != nil {
					return 0, err
				}
				n++
			}
		}
		// Auch ohne Betrag weiterschieben – sonst würde ein später eingetragener
		// Betrag rückwirkend bis zum Anlegen gebucht.
		if _, err := tx.ExecContext(ctx, `UPDATE pockets SET booked_until = ? WHERE id = ?`, dateString(today), d.id); err != nil {
			return 0, err
		}
	}
	return n, tx.Commit()
}

// bookIncomeTopups: je Gehaltsbuchung (after < Tag <= until) eine Auffüllung am
// selben Tag, wenn das Pocket in diesem Monat noch keine hat.
func bookIncomeTopups(ctx context.Context, tx *sql.Tx, pocket uint64, monthly int64, income uint64, after, until time.Time) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT s.id, s.day FROM transactions s
		WHERE s.recurring_id = ? AND s.kind = 'income' AND s.day > ? AND s.day <= ?
		AND NOT EXISTS (SELECT 1 FROM transactions t WHERE t.kind = 'topup' AND t.pocket_id = ?
			AND YEAR(t.day) = YEAR(s.day) AND MONTH(t.day) = MONTH(s.day))
		ORDER BY s.day`, income, dateString(after), dateString(until), pocket)
	if err != nil {
		return 0, err
	}
	type salary struct {
		id  uint64
		day time.Time
	}
	var list []salary
	for rows.Next() {
		var s salary
		if err := rows.Scan(&s.id, &s.day); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	month := ""
	n := 0
	for _, s := range list {
		if m := s.day.Format("2006-01"); m == month {
			continue // zwei Gehaltsbuchungen im Monat: nur eine Auffüllung
		} else {
			month = m
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO transactions (kind, amount_cents, day, pocket_id, note, source_id)
			VALUES ('topup', ?, ?, ?, '', ?)`, monthly, dateString(s.day), pocket, s.id); err != nil {
			return 0, err
		}
		n++
	}
	return n, nil
}

func (r *Repo) Pockets(ctx context.Context) ([]Pocket, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, color, monthly_cents, balance_cents, balance_on, deposit_day, income_id
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
		var income sql.NullInt64
		if err := rows.Scan(&p.ID, &p.Name, &p.Color, &p.MonthlyCents, &balance, &on, &p.DepositDay, &income); err != nil {
			return nil, err
		}
		p.IncomeID = nullID(income)
		if balance.Valid {
			p.BalanceCents = &balance.Int64
		}
		p.BalanceOn = nullDate(on)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repo) CreatePocket(ctx context.Context, p Pocket) (Pocket, error) {
	res, err := r.db.ExecContext(ctx, `INSERT INTO pockets (name, color, monthly_cents, balance_cents, balance_on, deposit_day, income_id, position)
		SELECT ?, ?, ?, ?, ?, ?, ?, COALESCE(MAX(position), 0) + 1 FROM pockets`,
		p.Name, p.Color, p.MonthlyCents, p.BalanceCents, p.BalanceOn, p.DepositDay, p.IncomeID)
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

// today: heutiges Datum der App (für das Neu-Ansehen bei geänderter Kopplung).
func (r *Repo) UpdatePocket(ctx context.Context, p Pocket, today time.Time) error {
	// Neu an eine Einnahme gekoppelt: die Gehaltsbuchungen dieses Monats noch
	// einmal ansehen (booked_until vor der Kopplung neu setzen – MySQL wertet
	// SET von links nach rechts aus); doppelt gebucht wird nicht, je Monat höchstens eine.
	res, err := r.db.ExecContext(ctx, `UPDATE pockets SET
		booked_until = IF(? IS NOT NULL AND NOT (income_id <=> ?), LAST_DAY(? - INTERVAL 1 MONTH), booked_until),
		name = ?, color = ?, monthly_cents = ?, balance_cents = ?, balance_on = ?, deposit_day = ?, income_id = ? WHERE id = ?`,
		p.IncomeID, p.IncomeID, dateString(today),
		p.Name, p.Color, p.MonthlyCents, p.BalanceCents, p.BalanceOn, p.DepositDay, p.IncomeID, p.ID)
	if isDuplicate(err) {
		return ErrExists
	}
	return affected(res, err)
}

// DeletePocket: Abos/Verträge und Kategorien bleiben, nur ohne Pocket.
func (r *Repo) DeletePocket(ctx context.Context, id uint64) error {
	return affected(r.db.ExecContext(ctx, `DELETE FROM pockets WHERE id = ?`, id))
}

// IsIncome: Gibt es eine feste Einnahme mit dieser ID?
func (r *Repo) IsIncome(ctx context.Context, id uint64) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM recurring WHERE id = ? AND kind = 'income'`, id).Scan(&n)
	return n > 0, err
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

const txColumns = `id, kind, amount_cents, day, category_id, pocket_id, note, recurring_id`

func scanTx(row scanner) (Transaction, error) {
	var t Transaction
	var day time.Time
	var cat, pocket, rec sql.NullInt64
	if err := row.Scan(&t.ID, &t.Kind, &t.AmountCents, &day, &cat, &pocket, &t.Note, &rec); err != nil {
		return t, notFound(err)
	}
	t.Day = dateString(day)
	t.CategoryID = nullID(cat)
	t.PocketID = nullID(pocket)
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
	res, err := r.db.ExecContext(ctx, `INSERT INTO transactions (kind, amount_cents, day, category_id, pocket_id, note, recurring_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, t.Kind, t.AmountCents, t.Day, t.CategoryID, t.PocketID, t.Note, t.RecurringID)
	if err != nil {
		return t, err
	}
	id, _ := res.LastInsertId()
	return r.Transaction(ctx, uint64(id))
}

func (r *Repo) UpdateTransaction(ctx context.Context, t Transaction) error {
	if err := affected(r.db.ExecContext(ctx, `UPDATE transactions SET kind = ?, amount_cents = ?, day = ?, category_id = ?, note = ?
		WHERE id = ?`, t.Kind, t.AmountCents, t.Day, t.CategoryID, t.Note, t.ID)); err != nil {
		return err
	}
	// Gehalt verschoben: die Auffüllungen des Einkommensverteilers ziehen mit
	_, err := r.db.ExecContext(ctx, `UPDATE transactions SET day = ? WHERE source_id = ?`, t.Day, t.ID)
	return err
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
// UpdateRecurring ändert die feste Buchung. Bekommt sie eine andere Kategorie,
// ziehen die schon gebuchten Buchungen mit – außer denen, die von Hand einer
// anderen Kategorie zugeordnet wurden.
func (r *Repo) UpdateRecurring(ctx context.Context, x Recurring) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var old sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT category_id FROM recurring WHERE id = ? FOR UPDATE`, x.ID).Scan(&old); err != nil {
		return notFound(err)
	}
	if err := affected(tx.ExecContext(ctx, `UPDATE recurring SET title = ?, kind = ?, amount_cents = ?, category_id = ?, pocket_id = ?, day = ?,
		interval_months = ?, starts_on = ?, ends_on = ?, payment_method = ?, payment_note = ? WHERE id = ?`,
		x.Title, x.Kind, x.AmountCents, x.CategoryID, x.PocketID, x.Day, x.IntervalMonths, x.StartsOn, x.EndsOn, x.PaymentMethod, x.PaymentNote, x.ID)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE transactions SET category_id = ? WHERE recurring_id = ? AND category_id <=> ?`,
		x.CategoryID, x.ID, old); err != nil {
		return err
	}
	return tx.Commit()
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
