// Package fetcher is the first real worker. It consumes URLs from
// queue.QueueListingsFetch, downloads the HTML, stores it in
// listing_html_fetches, and emits a parse-stage task on queue.QueueListingsParse.
//
// Failure semantics:
//
//   - Network / 5xx / 429 → transient → return error → consumer Nacks
//     → message rides the retry queue → tried again after RetryTTL
//   - 4xx (not 429)       → permanent → store a row with NULL html so
//     we remember this URL is dead, ack, do NOT publish a parse task
//   - 2xx                 → store row with body, publish next stage, ack
package fetcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	amqp "github.com/rabbitmq/amqp091-go"
	"golang.org/x/time/rate"

	"github.com/brtnchk/real-estate-robot/internal/db/sqlc"
	"github.com/brtnchk/real-estate-robot/internal/httpc"
	"github.com/brtnchk/real-estate-robot/internal/queue"
)

// Task is the JSON payload we expect on queue.QueueListingsFetch.
type Task struct {
	URL string `json:"url"`
}

// NextTask is what we publish onward to queue.QueueListingsParse.
type NextTask struct {
	FetchID int64  `json:"fetch_id"`
	URL     string `json:"url"`
}

// Fetcher is the queue.Handler that drives the fetch → store → publish chain.
// All fields are required; the easiest way to construct one is via New().
type Fetcher struct {
	HTTP               *http.Client
	Limiter            *rate.Limiter
	UserAgent          string
	DB                 *sqlc.Queries
	Publisher          *queue.Publisher
	Log                *slog.Logger
	MinRefetchInterval time.Duration // skip URL if fetched within this window (0 = always fetch)
}

// Config bundles construction parameters for New.
type Config struct {
	RPS                float64       // requests per second (token bucket rate)
	Burst              int           // burst size (max tokens in the bucket)
	HTTPTimeout        time.Duration // per-request deadline
	UserAgent          string
	MinRefetchInterval time.Duration // see Fetcher.MinRefetchInterval
	DB                 *sqlc.Queries
	Publisher          *queue.Publisher
	Log                *slog.Logger
}

// New constructs a Fetcher with sensible HTTP client defaults.
func New(cfg Config) *Fetcher {
	return &Fetcher{
		HTTP:               httpc.New(cfg.HTTPTimeout),
		Limiter:            rate.NewLimiter(rate.Limit(cfg.RPS), cfg.Burst),
		UserAgent:          cfg.UserAgent,
		DB:                 cfg.DB,
		Publisher:          cfg.Publisher,
		Log:                cfg.Log,
		MinRefetchInterval: cfg.MinRefetchInterval,
	}
}

// Handle is the queue.Handler implementation. See the package doc for the
// fail/retry semantics.
func (f *Fetcher) Handle(ctx context.Context, d amqp.Delivery) error {
	var task Task
	if err := json.Unmarshal(d.Body, &task); err != nil {
		// Malformed JSON is permanent — retrying won't fix the body.
		// Return error to nack and let it eventually land in dead.
		return fmt.Errorf("decode task: %w", err)
	}
	if task.URL == "" {
		return errors.New("task has empty url")
	}

	log := f.Log.With("url", task.URL)

	// Dedup gate: if we already fetched this URL recently, ack and skip.
	// This is what makes "scheduler republishes discovery every 30s" safe —
	// the same listing URL might surface every cycle, but we only spend
	// an HTTP round-trip on it once per MinRefetchInterval.
	if f.MinRefetchInterval > 0 {
		latest, err := f.DB.GetLatestFetchByURL(ctx, task.URL)
		switch {
		case err == nil:
			if recentlyFetched(latest.FetchedAt.Time, time.Now(), f.MinRefetchInterval) {
				// Info-level: this is a meaningful event for capacity/cost
				// monitoring — every skipped fetch is a saved OLX hit.
				log.Info("skip: fetched recently",
					"age", time.Since(latest.FetchedAt.Time).Round(time.Second),
					"window", f.MinRefetchInterval,
				)
				return nil
			}
		case errors.Is(err, pgx.ErrNoRows):
			// First time we see this URL — fall through to the fetch.
		default:
			// Real DB error — return it, message goes to retry.
			return fmt.Errorf("dedup lookup: %w", err)
		}
	}

	// Wait for a rate-limit token. This is what keeps us polite to OLX
	// regardless of how many consumers are running — the limiter is local
	// to this Fetcher, but combined with prefetch=N it caps concurrency.
	// rate.Limiter.Wait blocks until a token is available or ctx expires.
	if err := f.Limiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait: %w", err)
	}

	body, status, headers, fetchErr := f.fetch(ctx, task.URL)

	// --- classify ---------------------------------------------------------
	switch {
	case fetchErr != nil, status >= 500, status == http.StatusTooManyRequests:
		log.Warn("transient fetch failure, will retry",
			"status", status, "err", fetchErr,
		)
		// Return an error → consumer Nacks → DLX → retry queue → ...
		if fetchErr != nil {
			return fmt.Errorf("fetch: %w", fetchErr)
		}
		return fmt.Errorf("fetch returned status %d", status)

	case status >= 400:
		// Permanent client error — record it so we know this URL is dead,
		// but do not enqueue a parse task. Ack to remove the message.
		log.Warn("permanent fetch failure", "status", status)
		_, err := f.DB.InsertFetch(ctx, sqlc.InsertFetchParams{
			Url:        task.URL,
			StatusCode: int16(status),
			Html:       nil,
			Headers:    mustMarshalHeaders(headers),
			Error:      pgtype.Text{},
		})
		if err != nil {
			// DB hiccup is itself transient — let the message retry.
			return fmt.Errorf("insert fetch row (4xx): %w", err)
		}
		return nil
	}

	// --- happy path -------------------------------------------------------
	row, err := f.DB.InsertFetch(ctx, sqlc.InsertFetchParams{
		Url:        task.URL,
		StatusCode: int16(status),
		Html:       body,
		Headers:    mustMarshalHeaders(headers),
		Error:      pgtype.Text{},
	})
	if err != nil {
		return fmt.Errorf("insert fetch row: %w", err)
	}

	next, _ := json.Marshal(NextTask{FetchID: row.ID, URL: task.URL})
	if err := f.Publisher.Publish(ctx, queue.QueueListingsParse, next); err != nil {
		// We've already stored the fetch row. A retry will create another
		// fetch row (duplicate audit) and re-publish. The parser must be
		// idempotent on URL — we already planned that.
		return fmt.Errorf("publish parse task: %w", err)
	}

	log.Info("fetched",
		"status", status,
		"fetch_id", row.ID,
		"bytes", len(body),
	)
	return nil
}

// fetch performs the HTTP GET. On a non-nil err the other return values
// are zero-valued.
func (f *Fetcher) fetch(ctx context.Context, url string) ([]byte, int, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("build request: %w", err)
	}
	if f.UserAgent != "" {
		req.Header.Set("User-Agent", f.UserAgent)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9")
	req.Header.Set("Accept-Language", "uk-UA,uk;q=0.9,ru;q=0.8,en;q=0.5")

	resp, err := f.HTTP.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer resp.Body.Close()

	// Cap the read so a runaway server cannot OOM us. 10 MiB is plenty
	// for any OLX listing page; revisit if real pages exceed this.
	const maxBytes = 10 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return nil, resp.StatusCode, resp.Header, fmt.Errorf("read body: %w", err)
	}

	return body, resp.StatusCode, resp.Header, nil
}

// recentlyFetched returns true when fetchedAt is within (now - interval).
// Extracted as a pure function so the dedup decision is unit-testable
// without bringing up a Postgres for a single inequality check.
func recentlyFetched(fetchedAt, now time.Time, interval time.Duration) bool {
	if interval <= 0 || fetchedAt.IsZero() {
		return false
	}
	return now.Sub(fetchedAt) < interval
}

// mustMarshalHeaders flattens the http.Header to JSON. We only keep a
// small whitelist — full headers can be megabytes if servers send cookies
// or vary chains. Add headers here as the parser needs them.
func mustMarshalHeaders(h http.Header) json.RawMessage {
	if h == nil {
		return json.RawMessage(`{}`)
	}
	keep := map[string]string{}
	for _, k := range []string{"Content-Type", "Content-Length", "Last-Modified", "Etag", "Server"} {
		if v := h.Get(k); v != "" {
			keep[k] = v
		}
	}
	b, _ := json.Marshal(keep)
	return b
}
