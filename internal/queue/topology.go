// Package queue owns the RabbitMQ topology used by every worker in the
// pipeline. It is intentionally tiny: a few name constants and a single
// Declare() that materializes the exchanges, queues, and bindings.
//
// Three exchanges, all of type "direct":
//
//   - olx.work   — primary work distribution. Publishers send messages
//                  here with a routing key that equals the target work
//                  queue name (e.g. "listings.fetch").
//   - olx.retry  — transient parking. Each {queue}.retry sibling sits
//                  on this exchange, holds messages for RetryTTL, then
//                  dead-letters them back to olx.work for re-delivery.
//   - olx.dead   — terminal. Messages that exhausted their retry budget
//                  end up in {queue}.dead. Nothing consumes from here
//                  automatically — these queues are inspected by hand.
//
// Per work queue (e.g. "listings.fetch") we get three queues:
//
//	listings.fetch        ← consumers read from this
//	listings.fetch.retry  ← TTL parking lot, no consumers
//	listings.fetch.dead   ← terminal, manual inspection
//
// The retry loop is "DLX + TTL", a stock RabbitMQ pattern that does not
// require the delayed-message plugin: a rejected message is routed by the
// broker to the retry queue, the TTL on that queue makes the broker
// dead-letter it again after RetryTTL, this time back to the work queue.
// Worker code is responsible for inspecting the x-death header and
// publishing to olx.dead once the retry count exceeds the budget — the
// topology itself has no built-in max.
package queue

import (
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Exchange names. Keep these stable: changing them after the broker has
// declared the old ones is a manual operation (delete + redeclare).
const (
	ExchangeWork  = "olx.work"
	ExchangeRetry = "olx.retry"
	ExchangeDead  = "olx.dead"
)

// Work queue names. The routing key used to publish to each queue is the
// queue name itself — that is the convention this topology relies on.
const (
	QueueListingsDiscover = "listings.discover" // search pages → produce listing URLs
	QueueListingsFetch    = "listings.fetch"    // listing URL → fetch HTML
	QueueListingsParse    = "listings.parse"    // raw HTML → parse → store
	QueueSellersEnrich    = "sellers.enrich"    // seller URL → fetch profile + their listings
)

// RetryTTL is how long a rejected message sits in its .retry queue before
// the broker dead-letters it back to the work queue. 60s is a sane default
// for "OLX hiccuped, try again" — tune per-stage if you need to.
const RetryTTL = int32(60_000)

// workQueues lists every primary queue in the pipeline. Retry and dead
// siblings are derived by appending ".retry" / ".dead", so we never have
// to keep three lists in sync.
var workQueues = []string{
	QueueListingsDiscover,
	QueueListingsFetch,
	QueueListingsParse,
	QueueSellersEnrich,
}

// Declare creates every exchange, queue, and binding the pipeline needs.
// It is idempotent on the broker side: repeated calls with matching
// arguments succeed silently. If you change a queue's arguments (e.g.
// RetryTTL), you must delete the old queue manually first — RabbitMQ
// refuses to redeclare with mismatched args.
func Declare(ch *amqp.Channel) error {
	// 1. Exchanges. Durable = survives broker restart; auto-delete = false
	// because we want them to stick around even when no queues are bound.
	for _, ex := range []string{ExchangeWork, ExchangeRetry, ExchangeDead} {
		if err := ch.ExchangeDeclare(
			ex,
			"direct", // routing by exact key match
			true,     // durable
			false,    // auto-delete
			false,    // internal
			false,    // no-wait
			nil,      // args
		); err != nil {
			return fmt.Errorf("declare exchange %q: %w", ex, err)
		}
	}

	// 2. For each pipeline stage, declare the work/retry/dead triple.
	for _, q := range workQueues {
		retryQ := q + ".retry"
		deadQ := q + ".dead"

		// --- dead queue: terminal, no DLX, no TTL.
		if _, err := ch.QueueDeclare(deadQ, true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare queue %q: %w", deadQ, err)
		}
		if err := ch.QueueBind(deadQ, deadQ, ExchangeDead, false, nil); err != nil {
			return fmt.Errorf("bind queue %q: %w", deadQ, err)
		}

		// --- retry queue: holds messages for RetryTTL, then dead-letters
		// them to the work exchange with the original work queue's routing
		// key. No consumers read from here; the broker is the only mover.
		if _, err := ch.QueueDeclare(retryQ, true, false, false, false, amqp.Table{
			"x-message-ttl":             RetryTTL,
			"x-dead-letter-exchange":    ExchangeWork,
			"x-dead-letter-routing-key": q,
		}); err != nil {
			return fmt.Errorf("declare queue %q: %w", retryQ, err)
		}
		if err := ch.QueueBind(retryQ, retryQ, ExchangeRetry, false, nil); err != nil {
			return fmt.Errorf("bind queue %q: %w", retryQ, err)
		}

		// --- work queue: where consumers actually read from. On nack the
		// broker dead-letters to the retry exchange under the retry routing
		// key, dropping the message in {q}.retry for delayed re-delivery.
		if _, err := ch.QueueDeclare(q, true, false, false, false, amqp.Table{
			"x-dead-letter-exchange":    ExchangeRetry,
			"x-dead-letter-routing-key": retryQ,
		}); err != nil {
			return fmt.Errorf("declare queue %q: %w", q, err)
		}
		if err := ch.QueueBind(q, q, ExchangeWork, false, nil); err != nil {
			return fmt.Errorf("bind queue %q: %w", q, err)
		}
	}

	return nil
}
