-- nexora/plan: ein Konto, Aufgaben je Kalenderwoche, eigene Termine mit
-- Serien, Budget mit Kategorien, Buchungen und festen Buchungen.
-- Zeitpunkte stehen in UTC, Tage (DATE) in der Zeitzone der App.

CREATE TABLE users (
    id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    email         VARCHAR(255)    NOT NULL UNIQUE,
    password_hash VARCHAR(255)    NOT NULL,
    created_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- id = SHA-256 des Tokens aus dem Cookie
CREATE TABLE sessions (
    id         CHAR(64)        NOT NULL PRIMARY KEY,
    user_id    BIGINT UNSIGNED NOT NULL,
    expires_at DATETIME        NOT NULL,
    created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_sessions_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    KEY idx_sessions_expires (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE settings (
    name  VARCHAR(64) NOT NULL PRIMARY KEY,
    value TEXT        NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- week = Montag der Kalenderwoche; day = NULL heißt „irgendwann in der Woche“
CREATE TABLE tasks (
    id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    week       DATE            NOT NULL,
    day        DATE            NULL,
    title      VARCHAR(500)    NOT NULL,
    done       BOOLEAN         NOT NULL DEFAULT FALSE,
    position   INT             NOT NULL DEFAULT 0,
    created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    KEY idx_tasks_week (week)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Eigene Termine. rrule wie in iCalendar (FREQ=WEEKLY;BYDAY=MO), NULL = einmalig.
CREATE TABLE events (
    id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    title      VARCHAR(500)    NOT NULL,
    location   VARCHAR(500)    NOT NULL DEFAULT '',
    note       TEXT            NOT NULL,
    all_day    BOOLEAN         NOT NULL DEFAULT FALSE,
    starts_at  DATETIME        NOT NULL,
    ends_at    DATETIME        NOT NULL,
    rrule      VARCHAR(500)    NULL,
    created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    KEY idx_events_start (starts_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- gestrichene Einzeltermine einer Serie (Beginn des Vorkommens)
CREATE TABLE event_exdates (
    event_id   BIGINT UNSIGNED NOT NULL,
    occurrence DATETIME        NOT NULL,
    PRIMARY KEY (event_id, occurrence),
    CONSTRAINT fk_exdates_event FOREIGN KEY (event_id) REFERENCES events (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Beträge immer in Cent, positiv; kind entscheidet über das Vorzeichen.
CREATE TABLE categories (
    id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    name         VARCHAR(100)    NOT NULL UNIQUE,
    color        CHAR(7)         NOT NULL DEFAULT '#76736e',
    budget_cents BIGINT UNSIGNED NOT NULL DEFAULT 0,
    position     INT             NOT NULL DEFAULT 0,
    created_at   DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Feste Buchungen (Miete, Gehalt …), jeden Monat am Tag day.
-- booked_until: bis zu diesem Tag ist gebucht – eine gelöschte Buchung kommt nicht wieder.
CREATE TABLE recurring (
    id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    title        VARCHAR(200)    NOT NULL,
    kind         ENUM('expense', 'income') NOT NULL,
    amount_cents BIGINT UNSIGNED NOT NULL,
    category_id  BIGINT UNSIGNED NULL,
    day          TINYINT UNSIGNED NOT NULL,
    starts_on    DATE            NOT NULL,
    ends_on      DATE            NULL,
    booked_until DATE            NULL,
    created_at   DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_recurring_category FOREIGN KEY (category_id) REFERENCES categories (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE transactions (
    id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    kind         ENUM('expense', 'income') NOT NULL,
    amount_cents BIGINT UNSIGNED NOT NULL,
    day          DATE            NOT NULL,
    category_id  BIGINT UNSIGNED NULL,
    note         VARCHAR(500)    NOT NULL DEFAULT '',
    recurring_id BIGINT UNSIGNED NULL,
    created_at   DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_tx_category  FOREIGN KEY (category_id)  REFERENCES categories (id) ON DELETE SET NULL,
    CONSTRAINT fk_tx_recurring FOREIGN KEY (recurring_id) REFERENCES recurring (id) ON DELETE SET NULL,
    KEY idx_tx_day (day)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
