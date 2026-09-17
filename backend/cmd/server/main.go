// Command server is the entry point of the backend. Starting it: reads
// configuration from environment variables, connects to Postgres, brings
// the database schema up to date, seeds demo data if the database is
// empty, and then starts listening for HTTP requests until it's told to
// shut down.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"eva2/backend/internal/config"
	"eva2/backend/internal/db"
	"eva2/backend/internal/httpapi"
	"eva2/backend/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(log); err != nil {
		log.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// A context that is cancelled the moment the process receives an
	// interrupt or termination signal (Ctrl+C locally, or the signal a
	// hosting platform sends before killing the container). Everything
	// below uses this so shutdown is graceful rather than abrupt.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool, log); err != nil {
		return err
	}

	if cfg.SeedOnStart {
		if err := db.SeedIfEmpty(ctx, pool, log); err != nil {
			return err
		}
	}

	server := &httpapi.Server{
		Store:  store.New(pool),
		Pool:   pool,
		Config: cfg,
		Log:    log,
	}
	handler := httpapi.NewRouter(server)

	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Info("listening", "port", cfg.Port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received, draining connections")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return err
	}
	log.Info("shutdown complete")
	return nil
}
