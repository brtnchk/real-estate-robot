// Package discovery is the worker that opens the pipeline: it consumes
// queue.QueueListingsDiscover ({search_url, page}), fetches that page of
// OLX search results, fans the listing URLs out to queue.QueueListingsFetch,
// and self-paginates by queuing up page+1 when the current page had results.
//
// Bootstrapping is external: publish ONE task to listings.discover with
// page=1 and the worker takes it from there. Stop conditions:
//
//   - page > MaxPage           — hard cap to bound discovery cost
//   - zero listings on the page — search has been fully walked
package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	amqp "github.com/rabbitmq/amqp091-go"
	"golang.org/x/time/rate"

	"github.com/brtnchk/real-estate-robot/internal/httpc"
	"github.com/brtnchk/real-estate-robot/internal/queue"
)

// Task is what we expect on queue.QueueListingsDiscover.
type Task struct {
	SearchURL string `json:"search_url"`
	Page      int    `json:"page"`
}

// FetchTask is what we publish onward to queue.QueueListingsFetch for each
// discovered listing URL.
type FetchTask struct {
	URL string `json:"url"`
}

// Worker is the queue.Handler implementation. All fields required;
// use New() for the easy path.
type Worker struct {
	HTTP      *http.Client
	Limiter   *rate.Limiter
	UserAgent string
	Publisher *queue.Publisher
	Log       *slog.Logger
	MaxPage   int
}

type Config struct {
	RPS         float64
	Burst       int
	HTTPTimeout time.Duration
	UserAgent   string
	MaxPage     int
	Publisher   *queue.Publisher
	Log         *slog.Logger
}

func New(cfg Config) *Worker {
	if cfg.MaxPage <= 0 {
		cfg.MaxPage = 10
	}
	return &Worker{
		HTTP:      httpc.New(cfg.HTTPTimeout),
		Limiter:   rate.NewLimiter(rate.Limit(cfg.RPS), cfg.Burst),
		UserAgent: cfg.UserAgent,
		Publisher: cfg.Publisher,
		Log:       cfg.Log,
		MaxPage:   cfg.MaxPage,
	}
}

// Handle implements queue.Handler.
func (w *Worker) Handle(ctx context.Context, d amqp.Delivery) error {
	var task Task
	if err := json.Unmarshal(d.Body, &task); err != nil {
		return fmt.Errorf("decode task: %w", err)
	}
	if task.SearchURL == "" {
		return errors.New("task has empty search_url")
	}
	if task.Page <= 0 {
		task.Page = 1 // first-page default for hand-crafted bootstraps
	}
	if task.Page > w.MaxPage {
		w.Log.Info("max page reached, stopping pagination",
			"search_url", task.SearchURL,
			"page", task.Page,
			"max_page", w.MaxPage,
		)
		return nil
	}

	pageURL, err := buildSearchPageURL(task.SearchURL, task.Page)
	if err != nil {
		return fmt.Errorf("build page url: %w", err)
	}

	log := w.Log.With("search_url", task.SearchURL, "page", task.Page)

	if err := w.Limiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait: %w", err)
	}

	urls, err := w.fetchListingURLs(ctx, pageURL)
	if err != nil {
		// Transient → retry via DLX. Permanent 4xx returns (nil, nil) and
		// we treat that as "page empty, stop paginating".
		return fmt.Errorf("fetch search page %s: %w", pageURL, err)
	}

	log.Info("page discovered", "listings_found", len(urls))

	// Fan-out to listings.fetch.
	for _, u := range urls {
		body, _ := json.Marshal(FetchTask{URL: u})
		if err := w.Publisher.Publish(ctx, queue.QueueListingsFetch, body); err != nil {
			return fmt.Errorf("publish fetch %q: %w", u, err)
		}
	}

	// Self-pagination: if there was anything on this page, look at the next
	// one. This is what makes discovery a "one publish → many pages" worker.
	// MaxPage check is the hard cap; zero-results on a page is the natural
	// terminator OLX gives us.
	if len(urls) > 0 && task.Page < w.MaxPage {
		nextBody, _ := json.Marshal(Task{
			SearchURL: task.SearchURL,
			Page:      task.Page + 1,
		})
		if err := w.Publisher.Publish(ctx, queue.QueueListingsDiscover, nextBody); err != nil {
			return fmt.Errorf("publish next page: %w", err)
		}
		log.Debug("next page enqueued", "next_page", task.Page+1)
	}

	return nil
}

// fetchListingURLs does the HTTP GET and extracts listing URLs from the
// resulting HTML. Same fail classification as the other workers.
func (w *Worker) fetchListingURLs(ctx context.Context, pageURL string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
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

	switch {
	case resp.StatusCode >= 500, resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("transient status %d", resp.StatusCode)
	case resp.StatusCode >= 400:
		// 4xx on a search page = "this page doesn't exist". Treat as empty.
		return nil, nil
	}

	doc, err := goquery.NewDocumentFromReader(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}

	base, err := url.Parse(pageURL)
	if err != nil {
		return nil, fmt.Errorf("parse page url: %w", err)
	}

	seen := make(map[string]struct{})
	var urls []string
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, ok := s.Attr("href")
		if !ok {
			return
		}
		abs := resolveAndClean(base, href)
		if abs == "" || !isListingURL(abs) {
			return
		}
		if _, dup := seen[abs]; dup {
			return
		}
		seen[abs] = struct{}{}
		urls = append(urls, abs)
	})
	return urls, nil
}

// isListingURL matches absolute OLX listing links. URLs are normalized to
// the absolute form by resolveAndClean before this is called, so we never
// see relative ones here.
func isListingURL(href string) bool {
	return strings.HasPrefix(href, "https://www.olx.ua/d/") ||
		strings.HasPrefix(href, "https://www.olx.ua/uk/d/")
}

// resolveAndClean turns a possibly-relative href into an absolute, canonical
// URL: relative paths are resolved against base, query string is stripped
// (OLX appends ?search_reason=... which would create duplicate fetch rows
// for the same listing), and fragments are dropped. Returns "" on parse
// failure so the caller can skip silently.
func resolveAndClean(base *url.URL, href string) string {
	if href == "" {
		return ""
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if !u.IsAbs() {
		u = base.ResolveReference(u)
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// buildSearchPageURL produces the per-page URL of a search. OLX paginates
// via the ?page=N query parameter; page 1 is the default and omits the
// parameter entirely (so bookmarks of "/uk/nedvizhimost/" keep working).
func buildSearchPageURL(searchURL string, page int) (string, error) {
	if page <= 1 {
		return searchURL, nil
	}
	u, err := url.Parse(searchURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	q := u.Query()
	q.Set("page", strconv.Itoa(page))
	u.RawQuery = q.Encode()
	return u.String(), nil
}