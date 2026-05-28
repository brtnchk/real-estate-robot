// Command api is the HTTP/JSON layer the React frontend talks to.
//
//	DATABASE_URL=postgres://olx:olx@localhost:5432/olx?sslmode=disable \
//	go run ./cmd/api
//
// Listens on :8080 by default. CORS is wide open for local dev; tighten
// before exposing publicly.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/brtnchk/real-estate-robot/internal/api"
	"github.com/brtnchk/real-estate-robot/internal/db"
	"github.com/brtnchk/real-estate-robot/internal/db/sqlc"
	"github.com/brtnchk/real-estate-robot/internal/queue"
)

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Error("DATABASE_URL is required")
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

	// AMQP is optional — read-only endpoints work without it.
	// Only POST /api/crawl needs a publisher.
	var pub *queue.Publisher
	if amqpURL := os.Getenv("AMQP_URL"); amqpURL != "" {
		pub, err = queue.NewPublisher(amqpURL, log)
		if err != nil {
			log.Warn("AMQP unavailable, /api/crawl disabled", "err", err)
		} else {
			defer func() { _ = pub.Close() }()
		}
	}

	rabbitMgmt := os.Getenv("RABBITMQ_MGMT_URL")
	if rabbitMgmt == "" {
		rabbitMgmt = "http://localhost:15672"
	}
	rabbitUser := os.Getenv("RABBITMQ_USER")
	if rabbitUser == "" {
		rabbitUser = "olx"
	}
	rabbitPass := os.Getenv("RABBITMQ_PASSWORD")
	if rabbitPass == "" {
		rabbitPass = "olx"
	}

	srv := &api.Server{
		Queries:    sqlc.New(pool),
		Publisher:  pub,
		Log:        log,
		RabbitMgmt: rabbitMgmt,
		RabbitUser: rabbitUser,
		RabbitPass: rabbitPass,
	}

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,  // mitigates slowloris
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Run server in a goroutine so the main thread can wait for ctx.
	serverErr := make(chan error, 1)
	go func() {
		log.Info("api listening", "addr", *addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Error("graceful shutdown", "err", err)
		}
	case err := <-serverErr:
		log.Error("listen", "err", err)
		os.Exit(1)
	}
}