-- Einkommensverteiler (z. B. C24): Ein Pocket kann an eine feste Einnahme
-- (Gehalt) gekoppelt sein. Wird diese gebucht, folgt am selben Tag die
-- Auffüllung (source_id = die Gehaltsbuchung). Wird die Gehaltsbuchung
-- verschoben, ziehen die Auffüllungen mit; wird sie gelöscht, fallen sie weg.
ALTER TABLE pockets
    ADD COLUMN income_id BIGINT UNSIGNED NULL AFTER deposit_day,
    ADD CONSTRAINT fk_pockets_income FOREIGN KEY (income_id) REFERENCES recurring (id) ON DELETE SET NULL;

ALTER TABLE transactions
    ADD COLUMN source_id BIGINT UNSIGNED NULL AFTER recurring_id,
    ADD CONSTRAINT fk_tx_source FOREIGN KEY (source_id) REFERENCES transactions (id) ON DELETE CASCADE;
