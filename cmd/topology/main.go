// Command topology declares every exchange, queue, and binding the pipeline
// needs and then exits. Run it once after `make up`, or any time you change
// internal/queue/topology.go.
//
//	AMQP_URL=amqp://olx:olx@localhost:5672/ go run ./cmd/topology
package main

import (
	"log/slog"
	"os"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/brtnchk/real-estate-robot/internal/queue"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	url := os.Getenv("AMQP_URL")
	if url == "" {
		log.Error("AMQP_URL is not set; copy .env.example to .env and source it")
		os.Exit(1)
	}

	conn, err := amqp.Dial(url)
	if err != nil {
		log.Error("connect to rabbitmq", "err", err)
		os.Exit(1)
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		log.Error("open channel", "err", err)
		os.Exit(1)
	}
	defer ch.Close()

	if err := queue.Declare(ch); err != nil {
		log.Error("declare topology", "err", err)
		os.Exit(1)
	}

	log.Info("topology declared", "exchanges", 3, "queues", 4*3)
}
