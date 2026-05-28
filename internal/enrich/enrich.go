// Package enrich is the seller-enrichment worker. It consumes
// queue.QueueSellersEnrich, fetches the seller's OLX profile page, extracts
// every listing URL from it, and fans those URLs back out to
// queue.QueueListingsFetch. The seller's last_enriched_at column acts as
// the cycle-prevention gate — without it, parser → enrich → fetch → parser
// would loop indefinitely for every listing.
package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/jackc/pgx/v5"
	amqp "github.com/rabbitmq/amqp091-go"
	"golang.org/x/time/rate"

	"github.com/brtnchk/real-estate-robot/internal/db/sqlc"
	"github.com/brtnchk/real-estate-robot/internal/httpc"
	"github.com/brtnchk/real-estate-robot/internal/queue"
)

// Task is what we expect on queue.QueueSellersEnrich.
type Task struct {
	OlxUserID string `json:"olx_user_id"`
}

// FetchTask is the message we publish back to queue.QueueListingsFetch
// for each URL we discover on the seller's profile.
type FetchTask struct {
	URL string `json:"url"`
}

// Worker is the queue.Handler implementation. All fields required;
// use New() for the easy path.
type Worker struct {
	HTTP      *http.Client
	Limiter   *rate.Limiter
	UserAgent string
	Queries   *sqlc.Queries
	Publisher *queue.Publisher
	Log       *slog.Logger
	Freshness time.Duration // skip if last_enriched_at is newer than this
}

// Config bundles construction parameters.
type Config struct {
	RPS         float64
	Burst       int
	HTTPTimeout time.Duration
	UserAgent   string
	Freshness   time.Duration
	Queries     *sqlc.Queries
	Publisher   *queue.Publisher
	Log         *slog.Logger
}

func New(cfg Config) *Worker {
	if cfg.Freshness <= 0 {
		cfg.Freshness = 6 * time.Hour
	}
	return &Worker{
		HTTP:      httpc.New(cfg.HTTPTimeout),
		Limiter:   rate.NewLimiter(rate.Limit(cfg.RPS), cfg.Burst),
		UserAgent: cfg.UserAgent,
		Freshness: cfg.Freshness,
		Queries:   cfg.Queries,
		Publisher: cfg.Publisher,
		Log:       cfg.Log,
	}
}

// Handle implements queue.Handler. See package doc for the cycle story.
func (w *Worker) Handle(ctx context.Context, d amqp.Delivery) error {
	var task Task
	if err := json.Unmarshal(d.Body, &task); err != nil {
		return fmt.Errorf("decode task: %w", err)
	}
	if task.OlxUserID == "" {
		return errors.New("task has empty olx_user_id")
	}

	log := w.Log.With("olx_user_id", task.OlxUserID)

	seller, err := w.Queries.GetSellerByOlxID(ctx, task.OlxUserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// We were asked to enrich a user we've never seen. Could happen
			// if a parser ran after an enricher ack'd the parent task. Drop.
			log.Warn("unknown olx_user_id, dropping")
			return nil
		}
		return fmt.Errorf("lookup seller: %w", err)
	}

	// --- the cycle-prevention gate --------------------------------------
	if seller.LastEnrichedAt.Valid {
		age := time.Since(seller.LastEnrichedAt.Time)
		if age < w.Freshness {
			log.Info("skipping: freshly enriched",
				"age", age.Round(time.Second),
				"freshness_window", w.Freshness,
			)
			return nil
		}
	}

	if !seller.ProfileUrl.Valid || seller.ProfileUrl.String == "" {
		// Nothing to fetch. Still mark enriched, otherwise the freshness
		// gate never closes and we'd revisit this seller in a tight loop.
		log.Warn("no profile_url, marking enriched anyway")
		return w.Queries.MarkSellerEnriched(ctx, seller.ID)
	}

	// Polite rate limit before hitting OLX.
	if err := w.Limiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait: %w", err)
	}

	urls, err := w.fetchProfile(ctx, seller.ProfileUrl.String)
	if err != nil {
		// Transient → return error → nack → retry queue → eventually dead.
		return fmt.Errorf("fetch profile %s: %w", seller.ProfileUrl.String, err)
	}

	log.Info("profile parsed",
		"profile_url", seller.ProfileUrl.String,
		"listings_found", len(urls),
	)

	// Fan-out: one input → N outputs.
	for _, u := range urls {
		body, _ := json.Marshal(FetchTask{URL: u})
		if err := w.Publisher.Publish(ctx, queue.QueueListingsFetch, body); err != nil {
			// Partial fan-out is possible: some URLs already published, this
			// one failed. The retry will republish all of them — fetcher and
			// parser are both idempotent on URL, so a duplicate listings.fetch
			// is harmless. The cost is some extra HTTP, not data corruption.
			return fmt.Errorf("publish fetch %q: %w", u, err)
		}
	}

	// Mark enriched ONLY after publishing succeeded. If publish failed
	// above, we want the next retry to try the whole thing again.
	if err := w.Queries.MarkSellerEnriched(ctx, seller.ID); err != nil {
		return fmt.Errorf("mark enriched: %w", err)
	}

	return nil
}

// fetchProfile does the HTTP GET + extracts listing URLs from the resulting
// HTML. The selector is OLX-specific; example.com etc. will return zero URLs
// (which is what the demo relies on).
func (w *Worker) fetchProfile(ctx context.Context, url string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if w.UserAgent != "" {
		req.Header.Set("User-Agent", w.UserAgent)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9")
	req.Header.Set("Accept-Language", "uk-UA,uk;q=0.9,ru;q=0.8,en;q=0.5")

	resp, err := w.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Same fail classification as fetcher: 5xx/429 → transient, 4xx →
	// permanent (empty result), 2xx → parse.
	switch {
	case resp.StatusCode >= 500, resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("transient status %d", resp.StatusCode)
	case resp.StatusCode >= 400:
		return nil, nil
	}

	doc, err := goquery.NewDocumentFromReader(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}

	seen := make(map[string]struct{})
	var urls []string
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, ok := s.Attr("href")
		if !ok || !isListingURL(href) {
			return
		}
		if _, dup := seen[href]; dup {
			return
		}
		seen[href] = struct{}{}
		urls = append(urls, href)
	})
	return urls, nil
}

// isListingURL matches OLX listing links. Real selector will sharpen up
// once we have a real OLX profile page in hand.
func isListingURL(href string) bool {
	return strings.HasPrefix(href, "https://www.olx.ua/d/") ||
		strings.HasPrefix(href, "https://www.olx.ua/uk/d/")
}