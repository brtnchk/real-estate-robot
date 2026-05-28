package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Handler processes a single delivery. It must be idempotent: RabbitMQ
// can redeliver the same message after a consumer crash, after a TTL'd
// retry hop, or after a manual nack-with-requeue. The body is whatever
// the publisher sent — typically JSON.
//
//	return nil   → consumer Acks the delivery
//	return error → consumer Nacks (no-requeue) → broker DLX'es to .retry
type Handler func(ctx context.Context, d amqp.Delivery) error

// ConsumerConfig describes one subscription.
type ConsumerConfig struct {
	// Queue is the work queue to consume from (e.g. "listings.fetch").
	Queue string

	// Prefetch caps how many unacked messages this consumer will hold
	// at once. It is the primary rate-limit knob: with Prefetch=2, the
	// broker hands us at most 2 messages until we ack/nack at least one.
	// This naturally caps concurrency at Prefetch with no time.Sleep.
	Prefetch int

	// MaxRetries is the x-death budget. After this many failures, the
	// consumer promotes the message to {Queue}.dead instead of nacking
	// it back into the retry loop.
	MaxRetries int

	// Handler is the business logic.
	Handler Handler
}

// Consumer subscribes to one work queue and dispatches messages to a
// Handler, applying the retry/dead policy on failure.
type Consumer struct {
	conn *amqp.Connection
	ch   *amqp.Channel
	pub  *Publisher
	cfg  ConsumerConfig
	log  *slog.Logger
}

// NewConsumer dials AMQP, opens a channel, and applies QoS. It does NOT
// start consuming — call Run for that. Caller must Close() when done.
func NewConsumer(url string, pub *Publisher, cfg ConsumerConfig, log *slog.Logger) (*Consumer, error) {
	if cfg.Handler == nil {
		return nil, errors.New("queue: ConsumerConfig.Handler is required")
	}
	if cfg.Queue == "" {
		return nil, errors.New("queue: ConsumerConfig.Queue is required")
	}
	if cfg.Prefetch <= 0 {
		cfg.Prefetch = 1
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 5
	}

	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("dial amqp: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open channel: %w", err)
	}

	// Qos applies prefetch to this channel.
	//   prefetchCount = cfg.Prefetch — max in-flight unacked messages
	//   prefetchSize  = 0            — no byte-size limit
	//   global        = false        — per-consumer bucket (per-channel on
	//                                   AMQP 0-9-1; the wording matters less
	//                                   than the effect we want)
	if err := ch.Qos(cfg.Prefetch, 0, false); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("set qos: %w", err)
	}

	return &Consumer{
		conn: conn,
		ch:   ch,
		pub:  pub,
		cfg:  cfg,
		log:  log.With("queue", cfg.Queue),
	}, nil
}

// Run subscribes to the queue and dispatches messages until ctx is cancelled.
// On cancellation it stops pulling new deliveries, waits for in-flight
// handler goroutines to finish, and returns nil. A non-nil return means the
// broker refused the consume call or the channel died unexpectedly.
func (c *Consumer) Run(ctx context.Context) error {
	tag := fmt.Sprintf("consumer-%s", c.cfg.Queue)

	msgs, err := c.ch.ConsumeWithContext(
		ctx,
		c.cfg.Queue,
		tag,
		false, // autoAck=false: we ack manually after the handler succeeds
		false, // exclusive
		false, // noLocal (unused in RabbitMQ)
		false, // noWait
		nil,
	)
	if err != nil {
		return fmt.Errorf("start consume: %w", err)
	}

	c.log.Info("consumer started",
		"prefetch", c.cfg.Prefetch,
		"max_retries", c.cfg.MaxRetries,
	)

	var wg sync.WaitGroup
	for d := range msgs {
		wg.Add(1)
		// Run each handler in its own goroutine so prefetch > 1 actually
		// produces parallel handling. With prefetch=1 this still works —
		// the range loop only pulls the next delivery after we ack/nack.
		go func(d amqp.Delivery) {
			defer wg.Done()
			c.dispatch(ctx, d)
		}(d)
	}

	c.log.Info("consume stream closed, waiting for in-flight handlers")
	wg.Wait()
	c.log.Info("consumer stopped cleanly")
	return nil
}

// Close releases the channel and connection. Call after Run returns.
func (c *Consumer) Close() error {
	return errors.Join(c.ch.Close(), c.conn.Close())
}

// dispatch runs one delivery through the retry/dead policy.
func (c *Consumer) dispatch(ctx context.Context, d amqp.Delivery) {
	deathCount := xDeathCount(d.Headers)
	log := c.log.With("message_id", d.MessageId, "attempt", deathCount+1)

	// Panic insurance: a handler that crashes mid-flight must not silently
	// hold an unacked message forever. Convert the panic into a nack so
	// the message enters the retry loop just like a returned error would.
	defer func() {
		if r := recover(); r != nil {
			log.Error("handler panicked", "panic", r)
			_ = d.Nack(false, false)
		}
	}()

	// Budget exhausted → promote to dead queue and ack so the broker
	// forgets about this message. Publish FIRST, then ack — if we acked
	// first and the publish failed, the message would be lost.
	if deathCount >= c.cfg.MaxRetries {
		if err := c.pub.PublishDead(ctx, c.cfg.Queue, d.Body, "retries exhausted"); err != nil {
			log.Error("publish to dead exchange", "err", err)
			// Don't ack — let the broker redeliver to us or another consumer.
			// requeue=true puts it back at the HEAD of the queue, not tail.
			_ = d.Nack(false, true)
			return
		}
		log.Warn("promoted to dead queue", "reason", "retries exhausted")
		_ = d.Ack(false)
		return
	}

	if err := c.cfg.Handler(ctx, d); err != nil {
		log.Warn("handler failed, sending to retry", "err", err)
		// Nack with requeue=false → broker dead-letters to the retry
		// exchange per the queue's x-dead-letter-* args. multiple=false
		// because we are nacking exactly THIS delivery, not "up to and
		// including this delivery tag".
		_ = d.Nack(false, false)
		return
	}

	if err := d.Ack(false); err != nil {
		log.Error("ack failed", "err", err)
		return
	}
	log.Debug("acked")
}

// xDeathCount tells us how many times this message has been dead-lettered
// already. RabbitMQ maintains the x-death header as an array of entries,
// one per (queue, reason) pair the message has gone through:
//
//	x-death: [
//	  { queue: "listings.fetch",       reason: "rejected", count: 2, ... },
//	  { queue: "listings.fetch.retry", reason: "expired",  count: 2, ... },
//	]
//
// "rejected" entries come from our nacks (real failures); "expired" entries
// come from the TTL hop on the retry queue (not a real attempt, just the
// broker moving the message back). We count only "rejected" — that is the
// meaningful "this handler has failed N times" number.
func xDeathCount(headers amqp.Table) int {
	raw, ok := headers["x-death"]
	if !ok {
		return 0
	}
	arr, ok := raw.([]interface{})
	if !ok {
		return 0
	}
	total := 0
	for _, e := range arr {
		entry, ok := e.(amqp.Table)
		if !ok {
			continue
		}
		if reason, _ := entry["reason"].(string); reason != "rejected" {
			continue
		}
		switch n := entry["count"].(type) {
		case int64:
			total += int(n)
		case int32:
			total += int(n)
		case int:
			total += n
		}
	}
	return total
}