-- Gesendete Benachrichtigungen (Telegram): jede Meldung nur einmal, z. B.
-- „daily:2026-11-11“ oder „short:7:2027-01-15“ (Engpass im Pocket 7).
CREATE TABLE notifications_sent (
    `key`   VARCHAR(100) NOT NULL PRIMARY KEY,
    sent_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    KEY idx_notifications_sent_at (sent_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
