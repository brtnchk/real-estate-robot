package discovery

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

func TestBuildSearchPageURL(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		page    int
		want    string
		wantErr bool
	}{
		{
			name: "page 1 is identity",
			in:   "https://www.olx.ua/uk/nedvizhimost/",
			page: 1,
			want: "https://www.olx.ua/uk/nedvizhimost/",
		},
		{
			name: "page 0 normalized to identity (defensive)",
			in:   "https://www.olx.ua/uk/nedvizhimost/",
			page: 0,
			want: "https://www.olx.ua/uk/nedvizhimost/",
		},
		{
			name: "page 2 appends ?page=2",
			in:   "https://www.olx.ua/uk/nedvizhimost/",
			page: 2,
			want: "https://www.olx.ua/uk/nedvizhimost/?page=2",
		},
		{
			name: "page query merged into existing query string",
			in:   "https://www.olx.ua/uk/nedvizhimost/?currency=usd",
			page: 3,
			want: "https://www.olx.ua/uk/nedvizhimost/?currency=usd&page=3",
		},
		{
			name: "page=N replaces an existing page= query (no stacking)",
			in:   "https://www.olx.ua/uk/nedvizhimost/?page=1",
			page: 5,
			want: "https://www.olx.ua/uk/nedvizhimost/?page=5",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildSearchPageURL(tc.in, tc.page)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("buildSearchPageURL: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func newTestWorker() *Worker {
	return &Worker{
		HTTP:    httpc.New(5 * time.Second),
		Limiter: rate.NewLimiter(rate.Inf, 1),
		Log:     slog.New(slog.DiscardHandler),
		MaxPage: 10,
	}
}

func TestFetchListingURLs_DedupsAndFiltersToOLX(t *testing.T) {
	html := `<html><body>
        <a href="https://www.olx.ua/d/listing-1/">a</a>
        <a href="https://www.olx.ua/d/listing-1/">same listing again</a>
        <a href="https://www.olx.ua/uk/d/listing-2/">b</a>
        <a href="https://www.olx.ua/uk/user/12345/">profile (ignored)</a>
        <a href="https://example.com/external">external (ignored)</a>
    </body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(html))
	}))
	defer srv.Close()

	urls, err := newTestWorker().fetchListingURLs(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetchListingURLs: %v", err)
	}
	sort.Strings(urls)
	want := []string{
		"https://www.olx.ua/d/listing-1/",
		"https://www.olx.ua/uk/d/listing-2/",
	}
	if len(urls) != len(want) {
		t.Fatalf("got %d urls (%v), want %d", len(urls), urls, len(want))
	}
	for i := range urls {
		if urls[i] != want[i] {
			t.Errorf("urls[%d]: got %q, want %q", i, urls[i], want[i])
		}
	}
}

func TestFetchListingURLs_4xxReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	urls, err := newTestWorker().fetchListingURLs(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("expected nil error for 404 (page doesn't exist), got %v", err)
	}
	if urls != nil {
		t.Errorf("expected nil urls for 404, got %v", urls)
	}
}

func TestFetchListingURLs_5xxIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	if _, err := newTestWorker().fetchListingURLs(context.Background(), srv.URL); err == nil {
		t.Fatal("expected non-nil error for 502, got nil")
	}
}