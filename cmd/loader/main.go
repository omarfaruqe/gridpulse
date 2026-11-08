// Command loader loads raw bronze files into the Postgres staging schema.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"github.com/omarfaruqe/gridpulse/internal/bronze"
	"github.com/omarfaruqe/gridpulse/internal/config"
	"github.com/omarfaruqe/gridpulse/internal/db"
	"github.com/omarfaruqe/gridpulse/internal/loader"
)

func main() {
	source := flag.String("source", "", "source to load, or \"all\" (see the list in the error message for an unknown name)")
	migrateOnly := flag.Bool("migrate", false, "apply database migrations and exit")
	force := flag.Bool("force", false, "reload files that were already loaded")
	timeout := flag.Duration("timeout", 5*time.Minute, "overall run timeout")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := config.LoadDotEnv(".env"); err != nil {
		log.Warn("could not read .env", "error", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	if err := run(ctx, log, *source, *migrateOnly, *force); err != nil {
		log.Error("load failed", "source", *source, "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger, source string, migrateOnly, force bool) error {
	cfg := config.Load()

	pool, err := db.Connect(ctx, cfg.PostgresDSN)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	if migrateOnly {
		log.Info("migrations applied")
		return nil
	}

	store, err := bronze.New(ctx, cfg)
	if err != nil {
		return err
	}
	l := &loader.Loader{Pool: pool, Bronze: store}

	names := []string{source}
	if source == "all" {
		names = loader.SourceNames()
	} else if !slices.Contains(loader.SourceNames(), source) {
		return fmt.Errorf("unknown or missing -source %q (available: all, %v)", source, loader.SourceNames())
	}

	var errs []error
	for _, name := range names {
		results, err := l.LoadAll(ctx, name, force)
		var loaded, skipped, rows int
		for _, r := range results {
			if r.Skipped {
				skipped++
				continue
			}
			loaded++
			rows += r.Rows
		}
		log.Info("load finished", "source", name, "files_loaded", loaded, "files_skipped", skipped, "rows", rows)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}
