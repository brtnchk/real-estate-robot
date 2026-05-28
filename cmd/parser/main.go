// Command parser is the second worker. Consumes from queue.QueueListingsParse,
// loads the raw HTML from listing_html_fetches, parses it, writes the
// resulting seller + listing + snapshot in one transaction, then fans out
// a seller-enrich task.
//
//	DATABASE_URL=postgres://olx:olx@localhost:5432/olx?sslmode=disable \
//	AMQP_URL=amqp://olx:olx@localhost:5672/                            \
//	go run ./cmd/parser
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/brtnchk/real-estate-robot/internal/db"
	"github.com/brtnchk/real-estate-robot/internal/db/sqlc"
	"github.com/brtnchk/real-estate-robot/internal/parser"
	"github.com/brtnchk/real-estate-robot/internal/queue"
)

func main() {
	prefetch := flag.Int("prefetch", 2, "consumer in-flight cap")
	maxRetries := flag.Int("max-retries", 3, "retry budget before promoting to dead queue")
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

	w := &parser.Worker{
		Pool:      pool,
		Queries:   sqlc.New(pool),
		Publisher: pub,
		Log:       log,
	}

	cons, err := queue.NewConsumer(amqpURL, pub, queue.ConsumerConfig{
		Queue:      queue.QueueListingsParse,
		Prefetch:   *prefetch,
		MaxRetries: *maxRetries,
		Handler:    w.Handle,
	}, log)
	if err != nil {
		log.Error("init consumer", "err", err)
		os.Exit(1)
	}
	defer func() { _ = cons.Close() }()

	log.Info("parser starting", "prefetch", *prefetch, "max_retries", *maxRetries)
	if err := cons.Run(ctx); err != nil {
		log.Error("consumer run", "err", err)
		os.Exit(1)
	}
}