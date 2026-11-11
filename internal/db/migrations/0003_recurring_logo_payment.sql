-- Logo und Zahlungsart für Abos und Verträge. Das Logo ist ein vom Server neu
-- berechnetes PNG (128×128); logo_version ändert sich mit jedem neuen Logo
-- (für den Browser-Cache).
ALTER TABLE recurring
    ADD COLUMN payment_method VARCHAR(20)  NOT NULL DEFAULT '' AFTER ends_on,
    ADD COLUMN payment_note   VARCHAR(100) NOT NULL DEFAULT '' AFTER payment_method,
    ADD COLUMN logo           MEDIUMBLOB   NULL AFTER payment_note,
    ADD COLUMN logo_version   INT UNSIGNED NOT NULL DEFAULT 0 AFTER logo;
