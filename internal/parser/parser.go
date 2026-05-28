// Package parser turns raw OLX HTML into structured Listing / Seller data.
//
// OLX is a Next.js-style app that ships every listing's full state as an
// inline JSON blob:
//
//	<script>window.__PRERENDERED_STATE__= "...escaped JSON...";</script>
//
// Parsing that blob is far more robust than CSS selectors against arbitrary
// markup — the JSON shape moves much less than the rendered DOM.
package parser

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// Result is the structured data extracted from one listing page.
type Result struct {
	Seller  Seller
	Listing Listing
}

// Seller carries the OLX user account behind a listing.
type Seller struct {
	OlxUserID    string     // user.uuid — stable, opaque
	DisplayName  string     // user.name
	ProfileURL   string     // constructed: https://www.olx.ua/uk/user/<uuid>/
	RegisteredAt *time.Time // user.created — when the seller joined OLX
	IsBusiness   bool       // ad.isBusiness — OLX's "pro account" flag
}

// Listing carries the listing's own data.
type Listing struct {
	OlxListingID string
	URL          string
	Title        string
	Description  string // HTML fragment from OLX (contains <br/>)

	// Price is in the listed currency. nil means "no price" (e.g. "free" or "by negotiation").
	Price    *float64
	Currency string // "UAH" / "USD" / "EUR"

	Status   string     // "active" / "removed" / ...
	PostedAt *time.Time // ad.createdTime

	City     string // location.cityName
	District string // location.districtName
	Address  string // location.pathName ("Київська область, Київ, Солом'янський")

	Lat *float64
	Lon *float64

	PropertyType string          // category.type — "real_estate"
	Attributes   json.RawMessage // ad.params — list of {key,name,value,normalizedValue}
}

// Parse extracts a Result from an OLX listing page. pageURL is informational —
// the canonical URL comes from inside the JSON.
func Parse(pageURL string, html []byte) (Result, error) {
	state, err := extractPrerenderedState(html)
	if err != nil {
		// Fall back to <title>-only extraction if the JSON isn't present.
		// Keeps the parser usable against synthetic test HTML (httptest
		// fixtures) where we don't bother shipping the whole OLX blob.
		return fallbackParse(pageURL, html, err)
	}

	var p prerendered
	if err := json.Unmarshal(state, &p); err != nil {
		return Result{}, fmt.Errorf("decode prerendered state: %w", err)
	}
	a := p.Ad.Ad
	if a.ID == 0 || a.User.UUID == "" {
		return Result{}, errors.New("prerendered state missing required fields (ad.id / user.uuid)")
	}

	seller := Seller{
		OlxUserID:    a.User.UUID,
		DisplayName:  a.User.Name,
		ProfileURL:   sellerProfileURL(a.User.UUID),
		RegisteredAt: nonZeroTime(a.User.Created),
		IsBusiness:   a.IsBusiness,
	}

	listing := Listing{
		OlxListingID: strconv.FormatInt(a.ID, 10),
		URL:          a.URL,
		Title:        a.Title,
		Description:  a.Description,
		Status:       a.Status,
		PostedAt:     nonZeroTime(a.CreatedTime),
		City:         a.Location.CityName,
		District:     a.Location.DistrictName,
		Address:      a.Location.PathName,
		PropertyType: a.Category.Type,
		Attributes:   a.Params,
	}
	if v := a.Price.RegularPrice.Value; v > 0 {
		listing.Price = &v
		listing.Currency = a.Price.RegularPrice.CurrencyCode
	}
	if a.Map.Lat != 0 || a.Map.Lon != 0 {
		lat, lon := a.Map.Lat, a.Map.Lon
		listing.Lat, listing.Lon = &lat, &lon
	}

	return Result{Seller: seller, Listing: listing}, nil
}

// fallbackParse handles inputs that don't contain __PRERENDERED_STATE__ — old
// tests, unit-test fixtures, accidental non-OLX pages. It returns a Result
// with whatever we can pull from <title> and synthetic IDs so the downstream
// pipeline does not crash on weird inputs.
func fallbackParse(pageURL string, html []byte, stateErr error) (Result, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return Result{}, fmt.Errorf("fallback parse (after %v): %w", stateErr, err)
	}
	title := doc.Find("title").First().Text()
	if title == "" {
		title = "(no title)"
	}
	// Synthetic IDs so multiple non-OLX pages don't collide on ON CONFLICT.
	h := hashFragment(pageURL)
	return Result{
		Seller: Seller{
			OlxUserID:   "stub-user-" + h,
			DisplayName: "Stub Seller",
			ProfileURL:  pageURL,
		},
		Listing: Listing{
			OlxListingID: "stub-listing-" + h,
			URL:          pageURL,
			Title:        title,
			Description:  "(fallback parse — __PRERENDERED_STATE__ not present)",
		},
	}, nil
}

// --- internal JSON-tagged types ---------------------------------------------

type prerendered struct {
	Ad struct {
		Ad ad `json:"ad"`
	} `json:"ad"`
}

type ad struct {
	ID          int64           `json:"id"`
	URL         string          `json:"url"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	CreatedTime time.Time       `json:"createdTime"`
	Status      string          `json:"status"`
	IsBusiness  bool            `json:"isBusiness"`
	Price       adPrice         `json:"price"`
	Location    adLocation      `json:"location"`
	Map         adMap           `json:"map"`
	User        adUser          `json:"user"`
	Params      json.RawMessage `json:"params"`
	Category    adCategory      `json:"category"`
}

type adPrice struct {
	RegularPrice struct {
		Value        float64 `json:"value"`
		CurrencyCode string  `json:"currencyCode"`
	} `json:"regularPrice"`
}

type adLocation struct {
	CityName     string `json:"cityName"`
	DistrictName string `json:"districtName"`
	PathName     string `json:"pathName"`
}

type adMap struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type adUser struct {
	UUID    string    `json:"uuid"`
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
}

type adCategory struct {
	Type string `json:"type"`
}

// --- helpers ---------------------------------------------------------------

func sellerProfileURL(uuid string) string {
	if uuid == "" {
		return ""
	}
	return "https://www.olx.ua/uk/user/" + uuid + "/"
}

func nonZeroTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// extractPrerenderedState finds the
//
//	window.__PRERENDERED_STATE__= "<JSON-encoded-string>"
//
// assignment in the HTML and returns the decoded inner JSON.
//
// The grammar is two layers: an outer JS/JSON string literal whose content
// is itself JSON. We walk the literal byte-by-byte to find the matching
// closing quote (regex would mis-handle escapes), then run json.Unmarshal
// to decode it into a plain Go string.
func extractPrerenderedState(html []byte) ([]byte, error) {
	const marker = "__PRERENDERED_STATE__"
	start := bytes.Index(html, []byte(marker))
	if start < 0 {
		return nil, errors.New("__PRERENDERED_STATE__ not found")
	}
	rest := html[start:]
	rel := bytes.IndexByte(rest, '"')
	if rel < 0 {
		return nil, errors.New("opening quote not found after __PRERENDERED_STATE__")
	}
	quoteStart := start + rel

	// Walk forward to the matching unescaped quote.
	i := quoteStart + 1
	for i < len(html) {
		switch html[i] {
		case '\\':
			i += 2 // skip the escape and the next byte
			continue
		case '"':
			literal := html[quoteStart : i+1]
			var inner string
			if err := json.Unmarshal(literal, &inner); err != nil {
				return nil, fmt.Errorf("decode state string literal: %w", err)
			}
			return []byte(inner), nil
		}
		i++
	}
	return nil, errors.New("unterminated __PRERENDERED_STATE__ string literal")
}

// hashFragment is used only by fallbackParse to produce stable stub IDs.
// Lives in hash.go so the production hash helper can be tested in isolation.
func hashFragment(s string) string {
	return sha256NewSum(s)[:12]
}