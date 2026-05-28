// Package parser turns raw OLX HTML into structured Listing / Seller data.
//
// The current Parse() is a PLACEHOLDER. It extracts the <title> tag from
// the HTML and synthesizes deterministic stub IDs from the URL so the
// downstream DB pipeline can be exercised end-to-end. The interface
// (Parse(url, html) -> Result) is what stays; the body is replaceable
// when we have real OLX pages to look at.
package parser

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Result is the structured data extracted from one listing page.
type Result struct {
	Seller  Seller
	Listing Listing
}

type Seller struct {
	OlxUserID   string
	DisplayName string
}

type Listing struct {
	OlxListingID string
	URL          string
	Title        string
	Description  string
}

// Parse extracts a Result from a raw HTML document. It is a pure function:
// same (url, html) input → same Result output. No I/O, no globals.
func Parse(url string, html []byte) (Result, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return Result{}, fmt.Errorf("parse html: %w", err)
	}

	title := strings.TrimSpace(doc.Find("title").First().Text())
	if title == "" {
		title = "(no title)"
	}

	// Stable synthetic IDs derived from the URL — keeps demo runs deterministic
	// and gives ON CONFLICT something concrete to match against.
	sum := sha256.Sum256([]byte(url))
	listingID := "stub-listing-" + hex.EncodeToString(sum[:6])
	userID := "stub-user-" + hex.EncodeToString(sum[6:12])

	return Result{
		Seller: Seller{
			OlxUserID:   userID,
			DisplayName: "Stub Seller " + userID[len(userID)-4:],
		},
		Listing: Listing{
			OlxListingID: listingID,
			URL:          url,
			Title:        title,
			Description:  "(stub description for " + url + ")",
		},
	}, nil
}