package parser

import (
	"os"
	"strings"
	"testing"
)

// --- fallback path (no __PRERENDERED_STATE__) ------------------------------
// These cover the "we hit a non-OLX page" branch: title is pulled out of
// <title>, IDs are synthesized from the URL so the downstream upserts get
// a stable key.

func TestParse_Fallback_ExtractsTitle(t *testing.T) {
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
				t.Fatalf("Parse: %v", err)
			}
			// Title comes from <title>, trimmed.
			gotTitle := strings.TrimSpace(got.Listing.Title)
			if gotTitle != tc.wantTitle {
				t.Errorf("title: got %q, want %q", gotTitle, tc.wantTitle)
			}
		})
	}
}

func TestParse_Fallback_SynthesizesStableIDsForSameURL(t *testing.T) {
	url := "https://example.com/x"
	r1, _ := Parse(url, []byte(`<title>A</title>`))
	r2, _ := Parse(url, []byte(`<title>B</title>`))
	if r1.Listing.OlxListingID != r2.Listing.OlxListingID {
		t.Errorf("listing id changed for same URL: %q vs %q",
			r1.Listing.OlxListingID, r2.Listing.OlxListingID)
	}
	if r1.Seller.OlxUserID != r2.Seller.OlxUserID {
		t.Errorf("seller id changed for same URL: %q vs %q",
			r1.Seller.OlxUserID, r2.Seller.OlxUserID)
	}
}

func TestParse_Fallback_DifferentURLsGiveDifferentIDs(t *testing.T) {
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

// --- happy path: real OLX listing page -------------------------------------
// Uses a real (heavily-stripped) OLX listing HTML in testdata/. If OLX
// changes the __PRERENDERED_STATE__ shape, this test breaks loudly and
// tells us exactly what moved.

func TestParse_RealOLXListing(t *testing.T) {
	html, err := os.ReadFile("testdata/olx-listing-sample.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	const pageURL = "https://www.olx.ua/d/uk/obyavlenie/1-k-zhk-london-park-ID10lIP0.html"
	r, err := Parse(pageURL, html)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// ---- listing fields ----
	if got, want := r.Listing.OlxListingID, "921310018"; got != want {
		t.Errorf("OlxListingID: got %q, want %q", got, want)
	}
	if got, want := r.Listing.Title, "1-к ЖК Лондон парк"; got != want {
		t.Errorf("Title: got %q, want %q", got, want)
	}
	if got, want := r.Listing.Status, "active"; got != want {
		t.Errorf("Status: got %q, want %q", got, want)
	}
	if r.Listing.Price == nil || *r.Listing.Price != 43000 {
		t.Errorf("Price: got %v, want 43000", r.Listing.Price)
	}
	if got, want := r.Listing.Currency, "USD"; got != want {
		t.Errorf("Currency: got %q, want %q", got, want)
	}
	if got, want := r.Listing.City, "Київ"; got != want {
		t.Errorf("City: got %q, want %q", got, want)
	}
	if got, want := r.Listing.District, "Солом'янський"; got != want {
		t.Errorf("District: got %q, want %q", got, want)
	}
	if r.Listing.Lat == nil || *r.Listing.Lat < 50.0 || *r.Listing.Lat > 51.0 {
		t.Errorf("Lat: got %v, want ~50.4", r.Listing.Lat)
	}
	if r.Listing.Lon == nil || *r.Listing.Lon < 30.0 || *r.Listing.Lon > 31.0 {
		t.Errorf("Lon: got %v, want ~30.4", r.Listing.Lon)
	}
	if r.Listing.PostedAt == nil {
		t.Error("PostedAt: got nil, want non-nil")
	}
	if got, want := r.Listing.PropertyType, "real_estate"; got != want {
		t.Errorf("PropertyType: got %q, want %q", got, want)
	}
	if len(r.Listing.Attributes) == 0 {
		t.Error("Attributes: got empty, want non-empty (10 params in fixture)")
	}

	// ---- seller fields ----
	if got, want := r.Seller.OlxUserID, "4ba50790-b57f-4d2d-875c-5563e6698590"; got != want {
		t.Errorf("OlxUserID: got %q, want %q", got, want)
	}
	if got, want := r.Seller.DisplayName, "Руслан"; got != want {
		t.Errorf("DisplayName: got %q, want %q", got, want)
	}
	if !r.Seller.IsBusiness {
		t.Error("IsBusiness: got false, want true (fixture is a business listing)")
	}
	if r.Seller.RegisteredAt == nil {
		t.Error("RegisteredAt: got nil, want non-nil")
	}
	const wantURL = "https://www.olx.ua/uk/user/4ba50790-b57f-4d2d-875c-5563e6698590/"
	if r.Seller.ProfileURL != wantURL {
		t.Errorf("ProfileURL: got %q, want %q", r.Seller.ProfileURL, wantURL)
	}
}

func TestParse_PrerenderedStateMissingFields(t *testing.T) {
	// Has the marker but the JSON inside doesn't have ad.id / user.uuid.
	html := []byte(`<html><script>window.__PRERENDERED_STATE__= "{\"ad\":{\"ad\":{}}}";</script></html>`)
	_, err := Parse("https://example.com/", html)
	if err == nil {
		t.Fatal("expected error for missing ad.id, got nil")
	}
	if !strings.Contains(err.Error(), "missing required") {
		t.Errorf("error message should mention 'missing required': %v", err)
	}
}

func TestExtractPrerenderedState_UnterminatedStringLiteral(t *testing.T) {
	html := []byte(`window.__PRERENDERED_STATE__= "no closing quote here`)
	_, err := extractPrerenderedState(html)
	if err == nil {
		t.Fatal("expected error for unterminated literal, got nil")
	}
}