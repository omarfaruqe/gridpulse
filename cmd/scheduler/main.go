// Command scheduler runs the whole pipeline on a schedule: extract, load,
// transform, and freshness checks.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/omarfaruqe/gridpulse/internal/bronze"
	"github.com/omarfaruqe/gridpulse/internal/config"
	"github.com/omarfaruqe/gridpulse/internal/db"
	"github.com/omarfaruqe/gridpulse/internal/freshness"
	"github.com/omarfaruqe/gridpulse/internal/loader"
	"github.com/omarfaruqe/gridpulse/internal/pipeline"
	"github.com/omarfaruqe/gridpulse/internal/scheduler"
	"github.com/omarfaruqe/gridpulse/internal/transform"
	sqlfiles "github.com/omarfaruqe/gridpulse/sql"
)

func main() {
	list := flag.Bool("list", false, "list the jobs and their schedules, then exit")
	once := flag.String("once", "", "run one job now (recorded in ops.job_run), then exit")
	check := flag.Bool("freshness", false, "print data freshness and exit non-zero if any series is stale")
	runNow := flag.Bool("run-now", true, "run every job once at startup to catch up after downtime")
	grace := flag.Duration("grace", 30*time.Second, "how long running jobs may finish after a shutdown signal")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := config.LoadDotEnv(".env"); err != nil {
		log.Warn("could not read .env", "error", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, log, *list, *once, *check, *runNow, *grace); err != nil {
		log.Error("scheduler failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger, list bool, once string, check, runNow bool, grace time.Duration) error {
	cfg := config.Load()
	pool, err := db.Connect(ctx, cfg.PostgresDSN)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	if check {
		return printFreshness(ctx, pool)
	}

	store, err := bronze.New(ctx, cfg)
	if err != nil {
		return err
	}
	p := pipeline.New(pipeline.Config{
		Store:        store,
		Loader:       &loader.Loader{Pool: pool, Bronze: store},
		Transform:    &transform.Runner{Pool: pool, Files: sqlfiles.FS},
		Pool:         pool,
		AESOKey:      cfg.AESOAPIKey,
		DisabledJobs: cfg.DisabledJobs,
		Log:          log,
	})
	jobs, err := p.Jobs()
	if err != nil {
		return err
	}
	s, err := scheduler.New(pool, log, jobs)
	if err != nil {
		return err
	}

	switch {
	case list:
		for _, j := range jobs {
			fmt.Printf("%-16s %-22s timeout %s\n", j.Name, j.Spec, j.Timeout)
		}
		return nil
	case once != "":
		return s.RunOnce(ctx, once)
	}

	// Jobs run under their own context so a shutdown signal lets them finish
	// within the grace period instead of cutting them off mid-write.
	runCtx, cancelRuns := context.WithCancel(context.Background())
	defer cancelRuns()
	if err := s.Start(runCtx); err != nil {
		return err
	}
	log.Info("scheduler started", "jobs", s.Names())

	if runNow {
		go func() {
			for _, name := range s.Names() {
				if ctx.Err() != nil {
					return
				}
				_ = s.RunOnce(runCtx, name) // failures are logged and recorded by the scheduler
			}
			log.Info("startup catch-up finished")
		}()
	}

	<-ctx.Done()
	log.Info("shutting down", "grace", grace)
	stopCtx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	if err := s.Stop(stopCtx); err != nil {
		log.Warn("jobs still running at the end of the grace period; cancelling them")
	}
	cancelRuns()
	return nil
}

// printFreshness prints one line per series and fails when any is stale.
func printFreshness(ctx context.Context, pool *pgxpool.Pool) error {
	statuses, err := freshness.Check(ctx, pool, time.Now())
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SERIES\tNEWEST DATA (UTC)\tLAG\tLIMIT\tSTATUS")
	stale := 0
	for _, st := range statuses {
		newest, lag, status := "no data", "-", "ok"
		if st.LatestHour != nil {
			newest = st.LatestHour.UTC().Format("2006-01-02 15:04")
			lag = st.Lag.Round(time.Minute).String()
		}
		if st.Stale {
			status = "STALE"
			stale++
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", st.Series, newest, lag, st.MaxLag, status)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if stale > 0 {
		return fmt.Errorf("%d of %d series are stale", stale, len(statuses))
	}
	return nil
}
