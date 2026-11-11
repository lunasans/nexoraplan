package db

import (
	"context"
	"time"
)

// Einstellungen der Benachrichtigungen (alle standardmäßig aus).
const (
	SettingTelegramToken    = "telegram_token" // verschlüsselt (secret.Box)
	SettingTelegramBot      = "telegram_bot"   // Benutzername des Bots
	SettingTelegramChat     = "telegram_chat_id"
	SettingTelegramChatName = "telegram_chat_name"
	SettingNotifyPayments   = "notify_payments"     // "1" = an
	SettingNotifyPayDays    = "notify_payment_days" // Tage vorher: 1, 3 oder 7
	SettingNotifyAlerts     = "notify_alerts"
	SettingNotifyDigest     = "notify_digest"
	SettingNotifyTime       = "notify_time" // „07:00“
)

// MarkSent merkt sich eine Meldung; false = war schon gesendet.
func (r *Repo) MarkSent(ctx context.Context, key string) (bool, error) {
	res, err := r.db.ExecContext(ctx, "INSERT IGNORE INTO notifications_sent (`key`) VALUES (?)", key)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ForgetSent nimmt eine Meldung zurück (Senden fehlgeschlagen).
func (r *Repo) ForgetSent(ctx context.Context, key string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM notifications_sent WHERE `key` = ?", key)
	return err
}

// CleanupSent löscht Einträge, die älter als before sind.
func (r *Repo) CleanupSent(ctx context.Context, before time.Time) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM notifications_sent WHERE sent_at < ?`, before.UTC())
	return err
}
