-- Auffüllungen der Pockets als eigene Buchungsart „topup“: Einnahme des
-- Pockets, für den Monat aber eine Umbuchung (weder Einnahme noch Ausgabe).
-- booked_until wie bei festen Buchungen: eine gelöschte Auffüllung kommt nicht wieder.
ALTER TABLE transactions
    MODIFY COLUMN kind ENUM('expense', 'income', 'topup') NOT NULL,
    ADD COLUMN pocket_id BIGINT UNSIGNED NULL AFTER category_id,
    ADD CONSTRAINT fk_tx_pocket FOREIGN KEY (pocket_id) REFERENCES pockets (id) ON DELETE SET NULL;

ALTER TABLE pockets
    ADD COLUMN booked_until DATE NULL AFTER deposit_day;
