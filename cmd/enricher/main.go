// Command enricher is the seller-enrichment worker. Consumes from
// queue.QueueSellersEnrich, fetches the seller's profile, fans out
// listing URLs back to queue.QueueListingsFetch, stamps last_enriched_at.
//
//	DATABASE_URL=... AMQP_URL=... go run ./cmd/enricher
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/brtnchk/real-estate-robot/internal/db"
	"github.com/brtnchk/real-estate-robot/internal/db/sqlc"
	"github.com/brtnchk/real-estate-robot/internal/enrich"
	"github.com/brtnchk/real-estate-robot/internal/queue"
)

func main() {
	rps := flag.Float64("rps", 1.0, "requests per second (rate limit on profile fetches)")
	burst := flag.Int("burst", 1, "rate limiter burst size")
	prefetch := flag.Int("prefetch", 1, "consumer in-flight cap")
	maxRetries := flag.Int("max-retries", 3, "retry budget before promoting to dead queue")
	httpTimeout := flag.Duration("http-timeout", 30*time.Second, "per-request HTTP timeout")
	freshness := flag.Duration("freshness", 6*time.Hour, "skip if last_enriched_at is newer than this")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	dbURL := os.Getenv("DATABASE_URL")
	amqpURL := os.Getenv("AMQP_URL")
	if dbURL == "" || amqpURL == "" {
		log.Error("DATABASE_URL and AMQP_URL are required")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, dbURL)
	if err != nil {
		log.Error("connect pg", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	pub, err := queue.NewPublisher(amqpURL, log)
	if err != nil {
		log.Error("connect amqp", "err", err)
		os.Exit(1)
	}
	defer func() { _ = pub.Close() }()

	w := enrich.New(enrich.Config{
		RPS:         *rps,
		Burst:       *burst,
		HTTPTimeout: *httpTimeout,
		UserAgent:   "olx-real-estate-robot (https://github.com/brtnchk/real-estate-robot)",
		Freshness:   *freshness,
		Queries:     sqlc.New(pool),
		Publisher:   pub,
		Log:         log,
	})

	cons, err := queue.NewConsumer(amqpURL, pub, queue.ConsumerConfig{
		Queue:      queue.QueueSellersEnrich,
		Prefetch:   *prefetch,
		MaxRetries: *maxRetries,
		Handler:    w.Handle,
	}, log)
	if err != nil {
		log.Error("init consumer", "err", err)
		os.Exit(1)
	}
	defer func() { _ = cons.Close() }()

	log.Info("enricher starting",
		"rps", *rps,
		"prefetch", *prefetch,
		"max_retries", *maxRetries,
		"freshness", *freshness,
	)
	if err := cons.Run(ctx); err != nil {
		log.Error("consumer run", "err", err)
		os.Exit(1)
	}
}