// Package db verbindet sich mit MySQL/MariaDB und spielt Migrationen ein.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"time"

	"github.com/go-sql-driver/mysql"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Open baut die Verbindung auf. dsn hat das Format
// benutzer:passwort@tcp(127.0.0.1:3306)/datenbank
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("PLAN_DATABASE_DSN ungültig: %w", err)
	}
	cfg.ParseTime = true       // DATETIME-Spalten als time.Time lesen
	cfg.MultiStatements = true // Migrationen dürfen mehrere Befehle enthalten
	cfg.ClientFoundRows = true // UPDATE zählt auch Zeilen ohne echte Änderung
	cfg.Loc = time.UTC
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["charset"] = "utf8mb4"
	// Datenbank-Session auf UTC stellen, damit CURRENT_TIMESTAMP und die
	// Zeiten aus Go dieselbe Zeitzone haben.
	cfg.Params["time_zone"] = "'+00:00'"

	database, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(20)
	database.SetConnMaxLifetime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := database.PingContext(pingCtx); err != nil {
		database.Close()
		return nil, fmt.Errorf("datenbank nicht erreichbar: %w", err)
	}
	return database, nil
}

// Migrate führt alle noch nicht angewendeten Dateien aus migrations/ aus,
// sortiert nach Dateiname (0001_…, 0002_…).
func Migrate(ctx context.Context, database *sql.DB) error {
	_, err := database.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    VARCHAR(255) NOT NULL PRIMARY KEY,
			applied_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		return err
	}

	names, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)

	for _, name := range names {
		var exists int
		err := database.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, name).Scan(&exists)
		if err != nil {
			return err
		}
		if exists > 0 {
			continue
		}

		script, err := migrationFiles.ReadFile(name)
		if err != nil {
			return err
		}
		// Hinweis: MySQL kann CREATE TABLE nicht in Transaktionen
		// zurückrollen. Schlägt eine Migration fehl, muss man sie von Hand
		// bereinigen – deshalb Migrationen klein halten.
		if _, err := database.ExecContext(ctx, string(script)); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := database.ExecContext(ctx,
			`INSERT INTO schema_migrations (version) VALUES (?)`, name); err != nil {
			return err
		}
		slog.Info("Migration angewendet", "datei", name)
	}
	return nil
}
