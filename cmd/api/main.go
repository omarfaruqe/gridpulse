// Command api serves the warehouse over a read-only HTTP API.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/omarfaruqe/gridpulse/internal/api"
	"github.com/omarfaruqe/gridpulse/internal/config"
	"github.com/omarfaruqe/gridpulse/internal/db"
)

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	grace := flag.Duration("grace", 15*time.Second, "how long in-flight requests may finish after a shutdown signal")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := config.LoadDotEnv(".env"); err != nil {
		log.Warn("could not read .env", "error", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, log, *addr, *grace); err != nil {
		log.Error("api failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger, addr string, grace time.Duration) error {
	pool, err := db.ConnectReadOnly(ctx, config.Load().PostgresDSN)
	if err != nil {
		return err
	}
	defer pool.Close()

	srv := &http.Server{
		Addr:              addr,
		Handler:           api.New(pool, log).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("api listening", "addr", addr)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down", "grace", grace)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
