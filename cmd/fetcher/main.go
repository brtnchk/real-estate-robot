// Command fetcher is the first real worker. Wires Postgres + RabbitMQ +
// HTTP and runs the fetcher.Handler against queue.QueueListingsFetch until
// signalled to stop.
//
//	DATABASE_URL=postgres://olx:olx@localhost:5432/olx?sslmode=disable \
//	AMQP_URL=amqp://olx:olx@localhost:5672/                            \
//	go run ./cmd/fetcher
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
	"github.com/brtnchk/real-estate-robot/internal/fetcher"
	"github.com/brtnchk/real-estate-robot/internal/queue"
)

func main() {
	rps := flag.Float64("rps", 2.0, "requests per second (rate limit)")
	burst := flag.Int("burst", 1, "rate limiter burst size")
	prefetch := flag.Int("prefetch", 1, "consumer in-flight cap")
	maxRetries := flag.Int("max-retries", 3, "retry budget before promoting to dead queue")
	httpTimeout := flag.Duration("http-timeout", 30*time.Second, "per-request HTTP timeout")
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
		log.Error("connect amqp publisher", "err", err)
		os.Exit(1)
	}
	defer func() { _ = pub.Close() }()

	f := fetcher.New(fetcher.Config{
		RPS:         *rps,
		Burst:       *burst,
		HTTPTimeout: *httpTimeout,
		UserAgent:   "olx-real-estate-robot (https://github.com/brtnchk/real-estate-robot)",
		DB:          sqlc.New(pool),
		Publisher:   pub,
		Log:         log,
	})

	cons, err := queue.NewConsumer(amqpURL, pub, queue.ConsumerConfig{
		Queue:      queue.QueueListingsFetch,
		Prefetch:   *prefetch,
		MaxRetries: *maxRetries,
		Handler:    f.Handle,
	}, log)
	if err != nil {
		log.Error("init consumer", "err", err)
		os.Exit(1)
	}
	defer func() { _ = cons.Close() }()

	log.Info("fetcher starting",
		"rps", *rps,
		"prefetch", *prefetch,
		"max_retries", *maxRetries,
		"http_timeout", *httpTimeout,
	)
	if err := cons.Run(ctx); err != nil {
		log.Error("consumer run", "err", err)
		os.Exit(1)
	}
}