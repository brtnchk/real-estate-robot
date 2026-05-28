// Command discovery is the discovery worker. Consumes queue.QueueListingsDiscover,
// fetches a search-results page, fans out listing URLs to queue.QueueListingsFetch,
// and self-paginates by enqueuing page+1 when listings were found.
//
// Bootstrap by publishing one task by hand:
//
//	make publish q=listings.discover \
//	    m='{"search_url":"https://www.olx.ua/uk/nedvizhimost/prodazha/kiev/","page":1}'
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/brtnchk/real-estate-robot/internal/discovery"
	"github.com/brtnchk/real-estate-robot/internal/queue"
)

func main() {
	rps := flag.Float64("rps", 1.0, "search-page requests per second")
	burst := flag.Int("burst", 1, "rate limiter burst size")
	prefetch := flag.Int("prefetch", 1, "consumer in-flight cap")
	maxRetries := flag.Int("max-retries", 3, "retry budget before promoting to dead queue")
	httpTimeout := flag.Duration("http-timeout", 30*time.Second, "per-request HTTP timeout")
	maxPage := flag.Int("max-page", 10, "hard cap on pagination depth")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

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

	w := discovery.New(discovery.Config{
		RPS:         *rps,
		Burst:       *burst,
		HTTPTimeout: *httpTimeout,
		UserAgent:   "olx-real-estate-robot (https://github.com/brtnchk/real-estate-robot)",
		MaxPage:     *maxPage,
		Publisher:   pub,
		Log:         log,
	})

	cons, err := queue.NewConsumer(amqpURL, pub, queue.ConsumerConfig{
		Queue:      queue.QueueListingsDiscover,
		Prefetch:   *prefetch,
		MaxRetries: *maxRetries,
		Handler:    w.Handle,
	}, log)
	if err != nil {
		log.Error("init consumer", "err", err)
		os.Exit(1)
	}
	defer func() { _ = cons.Close() }()

	log.Info("discovery starting",
		"rps", *rps,
		"prefetch", *prefetch,
		"max_retries", *maxRetries,
		"max_page", *maxPage,
	)
	if err := cons.Run(ctx); err != nil {
		log.Error("consumer run", "err", err)
		os.Exit(1)
	}
}