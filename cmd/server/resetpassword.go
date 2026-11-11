package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"strings"

	"nexoraplan/internal/config"
	"nexoraplan/internal/db"
	"nexoraplan/internal/password"
)

// resetPassword setzt ein neues, zufälliges Passwort und meldet alle
// Sitzungen ab. Läuft auf dem Server als root (NexoraPanel: Apps → Passwort
// zurücksetzen):
//
//	nexoraplan reset-password -env /etc/nexora/apps/nexoraplan-<domain>.env <email>
func resetPassword(args []string) error {
	fs := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	envFile := fs.String("env", "", "Einstellungen aus dieser Datei lesen")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("Aufruf: nexoraplan reset-password [-env DATEI] <email>")
	}
	if *envFile != "" {
		if err := config.LoadEnvFile(*envFile); err != nil {
			return err
		}
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.DatabaseDSN == "" {
		return errors.New("PLAN_DATABASE_DSN fehlt – mit -env die Env-Datei der Installation angeben")
	}
	ctx := context.Background()
	database, err := db.Open(ctx, cfg.DatabaseDSN)
	if err != nil {
		return err
	}
	defer database.Close()
	repo := db.NewRepo(database)

	email := strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	u, err := repo.UserByEmail(ctx, email)
	if errors.Is(err, db.ErrNotFound) {
		return fmt.Errorf("kein Konto mit der Adresse %s", email)
	}
	if err != nil {
		return err
	}
	// 26 Zeichen Base32 (130 Bit), nicht auf der Kommandozeile – sonst stünde es in der Shell-History
	pw := rand.Text()
	if err := repo.SetPasswordHash(ctx, u.ID, password.Hash(pw)); err != nil {
		return err
	}
	n, err := repo.DeleteOtherSessions(ctx, u.ID, "")
	if err != nil {
		return err
	}
	fmt.Printf("Neues Passwort für %s: %s\n", u.Email, pw)
	fmt.Printf("Abgemeldet: %d Sitzung(en). Nach dem Anmelden unter „Einstellungen“ ein eigenes setzen.\n", n)
	return nil
}
