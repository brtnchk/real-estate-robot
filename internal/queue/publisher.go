package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

// Publisher owns a single AMQP connection + channel and publishes messages
// to the work exchange with publisher confirms enabled.
//
// Concurrency: amqp091.Channel is NOT safe for concurrent use. Publish
// serializes calls via mu, so a single Publisher is safe to share across
// goroutines. If throughput becomes the bottleneck, run multiple Publishers
// (each gets its own channel) instead of removing the mutex.
type Publisher struct {
	conn *amqp.Connection
	ch   *amqp.Channel
	log  *slog.Logger

	mu sync.Mutex // protects ch.Publish calls

	// returns receives messages that the broker could not route to any
	// queue (mandatory=true + no binding match). A background goroutine
	// drains it and logs — a return is always a routing-key bug.
	returns chan amqp.Return
	done    chan struct{}
}

// NewPublisher dials AMQP, opens a channel, switches it into confirm mode,
// and starts a background goroutine that watches for unrouteable returns.
// Caller must Close().
func NewPublisher(url string, log *slog.Logger) (*Publisher, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("dial amqp: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open channel: %w", err)
	}

	// Switch the channel into "confirm mode". Every subsequent publish
	// will produce a basic.ack (or basic.nack) from the broker that we
	// can wait on. Without this call, Publish returns nil instantly
	// regardless of whether the broker accepted the message.
	if err := ch.Confirm(false /* noWait */); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("put channel into confirm mode: %w", err)
	}

	p := &Publisher{
		conn:    conn,
		ch:      ch,
		log:     log,
		returns: ch.NotifyReturn(make(chan amqp.Return, 16)),
		done:    make(chan struct{}),
	}

	go p.watchReturns()

	return p, nil
}

// Close shuts the channel and connection in the right order. Safe to call
// more than once; subsequent calls are no-ops.
func (p *Publisher) Close() error {
	chErr := p.ch.Close()       // closing ch also closes the returns chan
	connErr := p.conn.Close()   // → watchReturns exits via the range loop
	<-p.done                    // wait for goroutine to actually finish
	return errors.Join(chErr, connErr)
}

// Publish sends body to the work queue named q. It blocks until the broker
// confirms (acks) the message or ctx expires, whichever happens first.
//
// Returns an error if:
//   - the channel is closed
//   - ctx expires before the confirm arrives
//   - the broker explicitly nacks (extremely rare; usually a broker bug
//     or a queue-mode mismatch)
//
// An unrouteable message (mandatory=true + no binding) is NOT an error
// here: the broker still acks it before returning it, so Publish returns
// nil and the return is logged separately by watchReturns. That asymmetry
// is real and worth knowing: ack ≠ delivered to a queue.
func (p *Publisher) Publish(ctx context.Context, q string, body []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	msgID := uuid.NewString()

	// PublishWithDeferredConfirmWithContext returns a handle we can Wait()
	// on for the broker's ack. This is the modern API; the older approach
	// used channel.NotifyPublish and manual delivery-tag accounting.
	confirm, err := p.ch.PublishWithDeferredConfirmWithContext(
		ctx,
		ExchangeWork, // exchange
		q,            // routing key = queue name (our convention)
		true,         // mandatory: bounce instead of silently dropping
		false,        // immediate: deprecated, must be false
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent, // write to disk → survives broker restart
			MessageId:    msgID,
			Timestamp:    time.Now().UTC(),
			Body:         body,
		},
	)
	if err != nil {
		return fmt.Errorf("publish to %s: %w", q, err)
	}

	ack, err := confirm.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("wait for confirm on %s: %w", q, err)
	}
	if !ack {
		return fmt.Errorf("broker nacked message %s for queue %s", msgID, q)
	}

	p.log.Debug("published", "queue", q, "message_id", msgID, "bytes", len(body))
	return nil
}

// PublishDead routes a give-up message to the dead exchange so it lands
// in {q}.dead for manual inspection. reason is stored as a header to help
// whoever opens the dead queue figure out why the message ended up there.
//
// This is called by Consumer when the retry budget is exhausted; it is
// intentionally a separate method (instead of an option on Publish) so
// the code path is obvious in stack traces and dashboards.
func (p *Publisher) PublishDead(ctx context.Context, q string, body []byte, reason string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	msgID := uuid.NewString()
	deadKey := q + ".dead"

	confirm, err := p.ch.PublishWithDeferredConfirmWithContext(
		ctx,
		ExchangeDead,
		deadKey,
		true, // mandatory: must hit {q}.dead, otherwise log + bug
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			MessageId:    msgID,
			Timestamp:    time.Now().UTC(),
			Body:         body,
			Headers: amqp.Table{
				"x-dead-reason":    reason,
				"x-original-queue": q,
			},
		},
	)
	if err != nil {
		return fmt.Errorf("publish to dead exchange: %w", err)
	}

	ack, err := confirm.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("wait for confirm on dead publish: %w", err)
	}
	if !ack {
		return fmt.Errorf("broker nacked dead message %s", msgID)
	}
	return nil
}

// watchReturns drains the broker's "unrouteable" returns and logs them.
// Exits when the channel closes (which closes the returns chan).
func (p *Publisher) watchReturns() {
	defer close(p.done)
	for r := range p.returns {
		p.log.Warn("unrouteable message returned by broker",
			"exchange", r.Exchange,
			"routing_key", r.RoutingKey,
			"reply_code", r.ReplyCode,
			"reply_text", r.ReplyText,
			"message_id", r.MessageId,
		)
	}
}