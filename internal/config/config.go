// Package config liest die Einstellungen aus Umgebungsvariablen.
//
// Unter NexoraPanel setzt systemd die Variablen aus der Env-Datei der App
// (/etc/nexora/apps/nexoraplan-<domain>.env). Lokal liest Load die .env im
// Arbeitsordner selbst ein.
package config

import (
	"bufio"
	"os"
	"strings"
)

type Config struct {
	Addr        string // Adresse, auf der der Server ohne systemd-Socket lauscht
	DatabaseDSN string // MariaDB-Verbindung
	PublicURL   string // Adresse im Browser, z. B. https://plan.example.de
	// EncryptionKey verschlüsselt das gespeicherte CalDAV-Passwort (Base64, 32 Byte).
	EncryptionKey string
	// Timezone, in der Woche, Termine und Buchungen gerechnet werden.
	Timezone string
	// SetupCode für die Ersteinrichtung. Leer = zufällig, steht dann im Log.
	SetupCode string
	// CalDAVURL ist der Vorschlag für die Kalender-Adresse (SOGo des Panels).
	CalDAVURL string
	// TelegramAPI: nur für Tests (Telegram-Nachbau), sonst https://api.telegram.org
	TelegramAPI string
	// Anmeldung über nexora/auth (das Panel trägt die drei Werte beim Verbinden ein)
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
}

func Load() (Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return Config{}, err
	}
	return Config{
		Addr:          getenv("PLAN_ADDR", "127.0.0.1:8090"),
		DatabaseDSN:   os.Getenv("PLAN_DATABASE_DSN"),
		PublicURL:     getenv("PLAN_PUBLIC_URL", "http://localhost:8090"),
		EncryptionKey: os.Getenv("PLAN_ENCRYPTION_KEY"),
		Timezone:      getenv("PLAN_TIMEZONE", "Europe/Berlin"),
		SetupCode:     os.Getenv("PLAN_SETUP_CODE"),
		CalDAVURL:     os.Getenv("PLAN_CALDAV_URL"),
		TelegramAPI:   os.Getenv("PLAN_TELEGRAM_API"),

		OIDCIssuer:       os.Getenv("PLAN_OIDC_ISSUER"),
		OIDCClientID:     os.Getenv("PLAN_OIDC_CLIENT_ID"),
		OIDCClientSecret: os.Getenv("PLAN_OIDC_CLIENT_SECRET"),
	}, nil
}

// LoadEnvFile liest Einstellungen aus einer bestimmten Datei (vor Load aufrufen).
// Anders als die .env im Arbeitsordner muss sie existieren.
func LoadEnvFile(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	return loadDotEnv(path)
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// loadDotEnv liest KEY=VALUE-Zeilen. Bereits gesetzte Umgebungsvariablen
// werden nicht überschrieben. Fehlt die Datei, ist das kein Fehler.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
	return scanner.Err()
}
