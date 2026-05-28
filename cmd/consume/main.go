// Command consume runs a long-lived consumer on a single work queue. It is
// a sandbox tool, not a real worker — the "business logic" just logs the
// message and either acks or (with --fail) returns a synthetic error to
// exercise the retry/dead path.
//
//	go run ./cmd/consume listings.fetch
//	go run ./cmd/consume --fail listings.fetch
//	go run ./cmd/consume --fail --max-retries 2 listings.fetch
//
// Ctrl+C gracefully drains in-flight handlers before exiting.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/brtnchk/real-estate-robot/internal/queue"
)

func main() {
	fail := flag.Bool("fail", false, "handler always returns an error (exercises retry/dead path)")
	maxRetries := flag.Int("max-retries", 3, "x-death budget before promotion to .dead")
	prefetch := flag.Int("prefetch", 1, "per-consumer in-flight cap (QoS)")
	flag.Usage = func() {
		_, _ = os.Stderr.WriteString("usage: consume [flags] <queue>\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	q := flag.Arg(0)

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	url := os.Getenv("AMQP_URL")
	if url == "" {
		log.Error("AMQP_URL is not set")
		os.Exit(1)
	}

	pub, err := queue.NewPublisher(url, log)
	if err != nil {
		log.Error("init publisher", "err", err)
		os.Exit(1)
	}
	defer func() { _ = pub.Close() }()

	handler := func(_ context.Context, d amqp.Delivery) error {
		log.Info("handler got message",
			"body", string(d.Body),
			"message_id", d.MessageId,
		)
		if *fail {
			return errors.New("synthetic failure for testing")
		}
		return nil
	}

	cons, err := queue.NewConsumer(url, pub, queue.ConsumerConfig{
		Queue:      q,
		Prefetch:   *prefetch,
		MaxRetries: *maxRetries,
		Handler:    handler,
	}, log)
	if err != nil {
		log.Error("init consumer", "err", err)
		os.Exit(1)
	}
	defer func() { _ = cons.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("starting", "queue", q, "fail_mode", *fail, "prefetch", *prefetch, "max_retries", *maxRetries)
	if err := cons.Run(ctx); err != nil {
		log.Error("run", "err", err)
		os.Exit(1)
	}
}