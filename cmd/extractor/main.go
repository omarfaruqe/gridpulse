// Command extractor fetches one source and writes the raw data to bronze.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/omarfaruqe/gridpulse/internal/bronze"
	"github.com/omarfaruqe/gridpulse/internal/config"
	"github.com/omarfaruqe/gridpulse/internal/extractors/hydroquebec"
	"github.com/omarfaruqe/gridpulse/internal/extractors/ieso"
)

type options struct {
	source, report, date, from, to string
	year, fromYear, toYear         int
}

func main() {
	var o options
	flag.StringVar(&o.source, "source", "", "source to extract (hydroquebec, ieso)")
	flag.StringVar(&o.report, "report", "GenOutputCapability", "ieso: report name")
	flag.StringVar(&o.date, "date", "", "ieso: single data day YYYY-MM-DD (default: yesterday)")
	flag.StringVar(&o.from, "from", "", "ieso backfill: first day YYYY-MM-DD (needs -to)")
	flag.StringVar(&o.to, "to", "", "ieso backfill: last day YYYY-MM-DD (needs -from)")
	flag.IntVar(&o.year, "year", 0, "ieso yearly reports (Demand): year (default: current year)")
	flag.IntVar(&o.fromYear, "from-year", 0, "ieso yearly backfill: first year (needs -to-year)")
	flag.IntVar(&o.toYear, "to-year", 0, "ieso yearly backfill: last year (needs -from-year)")
	timeout := flag.Duration("timeout", 2*time.Minute, "overall run timeout")
	list := flag.String("list", "", "list bronze keys under this prefix (e.g. source=hydroquebec_demand/) instead of extracting")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	var err error
	if *list != "" {
		err = listKeys(ctx, *list)
	} else {
		err = run(ctx, log, o)
	}
	if err != nil {
		log.Error("extract failed", "source", o.source, "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger, o options) error {
	store, err := bronze.New(ctx, config.Load())
	if err != nil {
		return err
	}

	switch o.source {
	case "hydroquebec":
		res, err := hydroquebec.New(store).Run(ctx)
		if err != nil {
			return err
		}
		log.Info("extracted", "source", o.source, "key", res.Key,
			"slots", res.Records, "filled", res.Filled, "snapshot", res.Snapshot)
		return nil
	case "ieso":
		return runIESO(ctx, log, store, o)
	default:
		return fmt.Errorf("unknown or missing -source %q (available: hydroquebec, ieso)", o.source)
	}
}

func runIESO(ctx context.Context, log *slog.Logger, store *bronze.Store, o options) error {
	ex := ieso.New(store)

	if ieso.IsYearly(o.report) {
		return runIESOYearly(ctx, log, ex, o)
	}

	if o.from != "" || o.to != "" {
		from, err := parseDay(o.from)
		if err != nil {
			return fmt.Errorf("-from: %w", err)
		}
		to, err := parseDay(o.to)
		if err != nil {
			return fmt.Errorf("-to: %w", err)
		}
		stored, missing, err := ex.Backfill(ctx, o.report, from, to)
		log.Info("backfill finished", "report", o.report, "stored", len(stored), "missing", missing)
		return err
	}

	day := time.Now().In(ieso.EST).AddDate(0, 0, -1) // IESO data days are EST
	if o.date != "" {
		var err error
		if day, err = parseDay(o.date); err != nil {
			return fmt.Errorf("-date: %w", err)
		}
	}
	res, err := ex.FetchDay(ctx, o.report, day)
	if errors.Is(err, ieso.ErrNotPublished) {
		log.Warn("report not published yet", "report", o.report, "error", err)
		return nil
	}
	if err != nil {
		return err
	}
	log.Info("extracted", "source", ieso.Source(o.report), "key", res.Key, "period", res.Period, "rows", res.Rows)
	return nil
}

func runIESOYearly(ctx context.Context, log *slog.Logger, ex *ieso.Extractor, o options) error {
	if o.fromYear != 0 || o.toYear != 0 {
		stored, missing, err := ex.BackfillYears(ctx, o.report, o.fromYear, o.toYear)
		log.Info("backfill finished", "report", o.report, "stored", len(stored), "missing", missing)
		return err
	}
	year := o.year
	if year == 0 {
		year = time.Now().In(ieso.EST).Year()
	}
	res, err := ex.FetchYear(ctx, o.report, year)
	if errors.Is(err, ieso.ErrNotPublished) {
		log.Warn("report not published", "report", o.report, "year", year, "error", err)
		return nil
	}
	if err != nil {
		return err
	}
	log.Info("extracted", "source", ieso.Source(o.report), "key", res.Key, "period", res.Period, "rows", res.Rows)
	return nil
}

func parseDay(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, errors.New("a date is required (YYYY-MM-DD)")
	}
	return time.Parse("2006-01-02", s)
}

func listKeys(ctx context.Context, prefix string) error {
	store, err := bronze.New(ctx, config.Load())
	if err != nil {
		return err
	}
	keys, err := store.List(ctx, prefix)
	if err != nil {
		return err
	}
	for _, k := range keys {
		fmt.Println(k)
	}
	fmt.Printf("%d object(s)\n", len(keys))
	return nil
}
