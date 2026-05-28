// Command scheduler is the "wake the pipeline up" bot. Every --interval it
// publishes a fresh discovery task for each configured search URL, which
// kicks the whole pipeline (discovery → fetcher → parser → enricher) into
// motion. New listings on OLX get picked up on the next tick after they
// appear.
//
// The scheduler itself has no state — losing it is harmless, the next
// startup just resumes the cadence. Idempotency comes from downstream:
// fetcher's --min-refetch window means repeated discovery tasks for the
// same URL don't burn HTTP requests on already-known listings.
//
//	AMQP_URL=amqp://olx:olx@localhost:5672/ \
//	scheduler --interval 5m \
//	          --searches https://www.olx.ua/uk/nedvizhimost/prodazha-kvartir/kiev/
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/brtnchk/real-estate-robot/internal/queue"
)

// DiscoveryTask matches discovery.Task — duplicated here so this binary
// doesn't import internal/discovery just for the struct shape.
type DiscoveryTask struct {
	SearchURL string `json:"search_url"`
	Page      int    `json:"page"`
}

func main() {
	interval := flag.Duration("interval", 5*time.Minute, "tick interval between discovery passes")
	searches := flag.String("searches", "",
		"comma-separated list of OLX search URLs to kick off discovery for")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	urls := parseSearchURLs(*searches)
	if len(urls) == 0 {
		log.Error("--searches is required (comma-separated)")
		os.Exit(2)
	}

	amqpURL := os.Getenv("AMQP_URL")
	if amqpURL == "" {
		log.Error("AMQP_URL is required")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pub, err := queue.NewPublisher(amqpURL, log)
	if err != nil {
		log.Error("connect amqp", "err", err)
		os.Exit(1)
	}
	defer func() { _ = pub.Close() }()

	log.Info("scheduler starting",
		"interval", *interval,
		"search_url_count", len(urls),
	)

	// Tick immediately on startup, then on the interval. Without the
	// immediate tick, restarts would pause discovery for up to a full
	// interval — annoying when developing.
	tick(ctx, log, pub, urls)

	t := time.NewTicker(*interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("scheduler stopped")
			return
		case <-t.C:
			tick(ctx, log, pub, urls)
		}
	}
}

// tick publishes one page=1 discovery task per search URL. Failures are
// logged but do not stop the loop — next tick will retry.
func tick(ctx context.Context, log *slog.Logger, pub *queue.Publisher, urls []string) {
	for _, u := range urls {
		body, _ := json.Marshal(DiscoveryTask{SearchURL: u, Page: 1})
		if err := pub.Publish(ctx, queue.QueueListingsDiscover, body); err != nil {
			log.Error("publish discovery task", "search_url", u, "err", err)
			continue
		}
		log.Info("queued discovery", "search_url", u)
	}
}

// parseSearchURLs splits the comma-separated flag into trimmed, non-empty
// URLs. Trailing/leading whitespace is tolerated so config files can wrap.
func parseSearchURLs(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}