-- Stand eines Pockets für die Vorschau: balance_cents am Tag balance_on
-- (NULL = nicht eingetragen). deposit_day: an diesem Tag kommt der monatliche Betrag.
ALTER TABLE pockets
    ADD COLUMN balance_cents BIGINT           NULL AFTER monthly_cents,
    ADD COLUMN balance_on    DATE             NULL AFTER balance_cents,
    ADD COLUMN deposit_day   TINYINT UNSIGNED NOT NULL DEFAULT 1 AFTER balance_on;
