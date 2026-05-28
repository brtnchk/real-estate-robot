package enrich

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/brtnchk/real-estate-robot/internal/httpc"
)

func TestIsListingURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.olx.ua/d/listing-123/", true},
		{"https://www.olx.ua/uk/d/listing-123/", true},
		{"https://www.olx.ua/uk/user/123/", false},      // user profile, not a listing
		{"https://www.olx.ua/", false},                  // root
		{"https://example.com/foo", false},              // wrong host
		{"/d/listing-123", false},                       // relative URLs aren't promoted
		{"", false},
	}
	for _, tc := range cases {
		if got := isListingURL(tc.url); got != tc.want {
			t.Errorf("isListingURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

func newTestWorker() *Worker {
	return &Worker{
		HTTP:    httpc.New(5 * time.Second),
		Limiter: rate.NewLimiter(rate.Inf, 1), // disable rate limiting in tests
		Log:     slog.New(slog.DiscardHandler),
	}
}

func TestFetchProfile_ExtractsListingURLsAndDedups(t *testing.T) {
	html := `
<html><body>
  <a href="https://www.olx.ua/d/listing-1/">listing 1</a>
  <a href="https://www.olx.ua/uk/d/listing-2/">listing 2</a>
  <a href="https://www.olx.ua/uk/user/12345/">profile link (ignored: not /d/)</a>
  <a href="https://example.com/external">external link (ignored)</a>
  <a href="https://www.olx.ua/d/listing-1/">duplicate (deduped)</a>
</body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(html))
	}))
	defer srv.Close()

	w := newTestWorker()
	urls, err := w.fetchProfile(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetchProfile: %v", err)
	}

	sort.Strings(urls)
	want := []string{
		"https://www.olx.ua/d/listing-1/",
		"https://www.olx.ua/uk/d/listing-2/",
	}
	if len(urls) != len(want) {
		t.Fatalf("urls count: got %d (%v), want %d", len(urls), urls, len(want))
	}
	for i := range urls {
		if urls[i] != want[i] {
			t.Errorf("urls[%d]: got %q, want %q", i, urls[i], want[i])
		}
	}
}

func TestFetchProfile_4xxReturnsEmptyNoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	w := newTestWorker()
	urls, err := w.fetchProfile(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("expected nil error for 404 (permanent), got %v", err)
	}
	if len(urls) != 0 {
		t.Errorf("expected zero urls for 404, got %v", urls)
	}
}

func TestFetchProfile_5xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	w := newTestWorker()
	if _, err := w.fetchProfile(context.Background(), srv.URL); err == nil {
		t.Fatal("expected non-nil error for 500 (transient), got nil")
	}
}

func TestFetchProfile_429IsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	w := newTestWorker()
	if _, err := w.fetchProfile(context.Background(), srv.URL); err == nil {
		t.Fatal("expected non-nil error for 429 (treated as transient), got nil")
	}
}