// Command transform rebuilds the silver and gold layers and runs the data quality tests.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/omarfaruqe/gridpulse/internal/config"
	"github.com/omarfaruqe/gridpulse/internal/db"
	"github.com/omarfaruqe/gridpulse/internal/transform"
	sqlfiles "github.com/omarfaruqe/gridpulse/sql"
)

func main() {
	skipTests := flag.Bool("skip-tests", false, "build the layers but do not run the data quality tests")
	testsOnly := flag.Bool("tests-only", false, "run the data quality tests without rebuilding")
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

	if err := run(ctx, log, *skipTests, *testsOnly); err != nil {
		log.Error("transform failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger, skipTests, testsOnly bool) error {
	cfg := config.Load()
	pool, err := db.Connect(ctx, cfg.PostgresDSN)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	r := &transform.Runner{Pool: pool, Files: sqlfiles.FS}
	if !testsOnly {
		start := time.Now()
		if err := r.Build(ctx); err != nil {
			return err
		}
		log.Info("silver and gold rebuilt", "took", time.Since(start).Round(time.Millisecond))
	}
	if skipTests {
		return nil
	}

	results, err := r.Test(ctx)
	if err != nil {
		return err
	}
	failed := 0
	for _, t := range results {
		if t.Passed() {
			log.Info("test passed", "test", t.Name)
			continue
		}
		failed++
		log.Error("test failed", "test", t.Name, "violations", t.Violations, "sample", t.Sample)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d data quality tests failed", failed, len(results))
	}
	log.Info("all data quality tests passed", "tests", len(results))
	return nil
}
