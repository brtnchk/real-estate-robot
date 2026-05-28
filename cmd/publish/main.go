// Command publish is a tiny CLI that drops one message into a work queue.
// Useful for sandbox exploration and end-to-end smoke tests before there
// is a real producer.
//
//	go run ./cmd/publish <queue> <body>
//
// Example:
//
//	go run ./cmd/publish listings.fetch '{"url":"https://www.olx.ua/..."}'
//
// Environment:
//
//	AMQP_URL — required (e.g. amqp://olx:olx@localhost:5672/)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/brtnchk/real-estate-robot/internal/queue"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: publish <queue> <body>")
		os.Exit(2)
	}
	q, body := os.Args[1], os.Args[2]

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
	defer func() {
		if err := pub.Close(); err != nil {
			log.Warn("close publisher", "err", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := pub.Publish(ctx, q, []byte(body)); err != nil {
		log.Error("publish", "queue", q, "err", err)
		os.Exit(1)
	}

	// Give the broker a beat to fire a basic.return if the routing key
	// was bogus — otherwise the process exits before watchReturns logs it.
	time.Sleep(200 * time.Millisecond)

	log.Info("ok", "queue", q, "bytes", len(body))
}