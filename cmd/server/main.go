// Einstiegspunkt von nexora/plan: lädt die Konfiguration, verbindet die
// Datenbank und startet den HTTP-Server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // Zeitzonendaten einbetten – unabhängig vom Betriebssystem

	"nexoraplan/internal/config"
	"nexoraplan/internal/db"
	"nexoraplan/internal/secret"
	"nexoraplan/internal/server"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "genkey":
			// Schlüssel für PLAN_ENCRYPTION_KEY
			fmt.Println(secret.GenerateKey())
			return
		case "reset-password":
			if err := resetPassword(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "Fehler:", err)
				os.Exit(1)
			}
			return
		case "version":
			fmt.Println(server.Version)
			return
		}
	}
	if err := run(); err != nil {
		slog.Error("nexora/plan konnte nicht starten", "fehler", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.DatabaseDSN == "" {
		return errors.New("PLAN_DATABASE_DSN fehlt – siehe .env.example")
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return fmt.Errorf("PLAN_TIMEZONE: %w", err)
	}
	var box *secret.Box
	if cfg.EncryptionKey != "" {
		if box, err = secret.New(cfg.EncryptionKey); err != nil {
			return fmt.Errorf("PLAN_ENCRYPTION_KEY: %w", err)
		}
	} else {
		slog.Warn("PLAN_ENCRYPTION_KEY ist nicht gesetzt – ein Kalenderserver (SOGo) lässt sich nicht verbinden")
	}

	database, err := db.Open(ctx, cfg.DatabaseDSN)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := db.Migrate(ctx, database); err != nil {
		return err
	}

	app, err := server.New(db.NewRepo(database), server.Options{
		PublicURL: cfg.PublicURL,
		Location:  loc,
		SetupCode: cfg.SetupCode,
		Box:       box,
		CalDAVURL: cfg.CalDAVURL,
	})
	if err != nil {
		return err
	}
	go app.RunJanitor(ctx)

	ln, addr, err := listen(cfg.Addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: app, ReadHeaderTimeout: 10 * time.Second}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("nexora/plan läuft", "version", server.Version, "adresse", addr, "zeitzone", loc.String())
		errCh <- srv.Serve(ln)
	}()
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		slog.Info("Fahre herunter …")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
