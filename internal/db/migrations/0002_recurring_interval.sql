-- Abos und Verträge auch vierteljährlich, halbjährlich und jährlich:
-- gebucht wird alle interval_months Monate ab dem Monat von starts_on.
ALTER TABLE recurring ADD COLUMN interval_months TINYINT UNSIGNED NOT NULL DEFAULT 1 AFTER day;
