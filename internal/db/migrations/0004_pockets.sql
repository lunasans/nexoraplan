-- Unterkonten (z. B. „Pockets“ bei C24): auf jedes geht monatlich ein fester
-- Betrag. Abos/Verträge werden von einem Pocket abgebucht, Kategorien gehören
-- zu einem Pocket. Gibt es Pockets, ist ihre Summe das Gesamtbudget.
CREATE TABLE pockets (
    id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    name          VARCHAR(100)    NOT NULL UNIQUE,
    color         CHAR(7)         NOT NULL DEFAULT '#76736e',
    monthly_cents BIGINT UNSIGNED NOT NULL DEFAULT 0,
    position      INT             NOT NULL DEFAULT 0,
    created_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE recurring
    ADD COLUMN pocket_id BIGINT UNSIGNED NULL AFTER category_id,
    ADD CONSTRAINT fk_recurring_pocket FOREIGN KEY (pocket_id) REFERENCES pockets (id) ON DELETE SET NULL;

ALTER TABLE categories
    ADD COLUMN pocket_id BIGINT UNSIGNED NULL AFTER budget_cents,
    ADD CONSTRAINT fk_categories_pocket FOREIGN KEY (pocket_id) REFERENCES pockets (id) ON DELETE SET NULL;
