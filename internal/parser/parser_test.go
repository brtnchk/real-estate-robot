package parser

import (
	"strings"
	"testing"
)

func TestParse_ExtractsTitle(t *testing.T) {
	cases := []struct {
		name      string
		html      string
		url       string
		wantTitle string
	}{
		{
			name:      "simple title",
			html:      `<html><head><title>Hello</title></head><body></body></html>`,
			url:       "https://example.com/x",
			wantTitle: "Hello",
		},
		{
			name:      "title with whitespace is trimmed",
			html:      "<html><head><title>\n   Trimmed   \n</title></head></html>",
			url:       "https://example.com/y",
			wantTitle: "Trimmed",
		},
		{
			name:      "missing title falls back to placeholder",
			html:      `<html><head></head><body>no title here</body></html>`,
			url:       "https://example.com/z",
			wantTitle: "(no title)",
		},
		{
			name:      "garbage html does not crash",
			html:      `<<<>>><><><{{}}{`,
			url:       "https://example.com/garbage",
			wantTitle: "(no title)",
		},
		{
			name:      "ukrainian title is preserved",
			html:      `<html><head><title>Двокімнатна на Подолі</title></head></html>`,
			url:       "https://www.olx.ua/d/foo",
			wantTitle: "Двокімнатна на Подолі",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.url, []byte(tc.html))
			if err != nil {
				t.Fatalf("Parse returned error: %v", err)
			}
			if got.Listing.Title != tc.wantTitle {
				t.Errorf("title: got %q, want %q", got.Listing.Title, tc.wantTitle)
			}
		})
	}
}

func TestParse_SynthesizesStableIDsForSameURL(t *testing.T) {
	url := "https://www.olx.ua/d/listing-foo"
	r1, _ := Parse(url, []byte(`<title>A</title>`))
	r2, _ := Parse(url, []byte(`<title>B</title>`)) // body changed, URL same

	if r1.Seller.OlxUserID != r2.Seller.OlxUserID {
		t.Errorf("seller id changed for same URL: %q vs %q",
			r1.Seller.OlxUserID, r2.Seller.OlxUserID)
	}
	if r1.Listing.OlxListingID != r2.Listing.OlxListingID {
		t.Errorf("listing id changed for same URL: %q vs %q",
			r1.Listing.OlxListingID, r2.Listing.OlxListingID)
	}
}

func TestParse_DifferentURLsGiveDifferentIDs(t *testing.T) {
	r1, _ := Parse("https://example.com/a", []byte(`<title>X</title>`))
	r2, _ := Parse("https://example.com/b", []byte(`<title>X</title>`))

	if r1.Listing.OlxListingID == r2.Listing.OlxListingID {
		t.Error("different URLs produced the same listing id")
	}
	if !strings.HasPrefix(r1.Seller.OlxUserID, "stub-user-") {
		t.Errorf("expected stub-user- prefix, got %q", r1.Seller.OlxUserID)
	}
	if !strings.HasPrefix(r1.Listing.OlxListingID, "stub-listing-") {
		t.Errorf("expected stub-listing- prefix, got %q", r1.Listing.OlxListingID)
	}
}