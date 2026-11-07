// Command extractor fetches one source and writes the raw data to bronze.
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

	"github.com/omarfaruqe/gridpulse/internal/bronze"
	"github.com/omarfaruqe/gridpulse/internal/config"
	"github.com/omarfaruqe/gridpulse/internal/extractors/hydroquebec"
)

func main() {
	source := flag.String("source", "", "source to extract (hydroquebec)")
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
		err = run(ctx, log, *source)
	}
	if err != nil {
		log.Error("extract failed", "source", *source, "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger, source string) error {
	store, err := bronze.New(ctx, config.Load())
	if err != nil {
		return err
	}

	switch source {
	case "hydroquebec":
		res, err := hydroquebec.New(store).Run(ctx)
		if err != nil {
			return err
		}
		log.Info("extracted", "source", source, "key", res.Key,
			"slots", res.Records, "filled", res.Filled, "snapshot", res.Snapshot)
		return nil
	default:
		return fmt.Errorf("unknown or missing -source %q (available: hydroquebec)", source)
	}
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
