// Package api is the HTTP/JSON layer that the React frontend talks to.
// Stays as thin as possible — every endpoint is just a SQL call + JSON
// marshal. No business logic lives here; if you find yourself writing
// any, put it in a domain package instead.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/brtnchk/real-estate-robot/internal/db/sqlc"
)

// Server holds dependencies and exposes Handler() for the HTTP server.
type Server struct {
	Queries *sqlc.Queries
	Log     *slog.Logger
}

// Handler wires the routes and middleware (CORS for local dev).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/listings", s.listings)
	mux.HandleFunc("GET /api/stats", s.stats)
	mux.HandleFunc("GET /api/categories", s.categories)
	return logRequest(s.Log, cors(mux))
}

// --- response payloads -----------------------------------------------------

// Listing is the JSON shape the frontend consumes. Defined here (not just
// reused from sqlc) so we control field names and don't accidentally
// expose internal-only columns.
type Listing struct {
	ListingID       int64    `json:"listing_id"`
	OlxListingID    string   `json:"olx_listing_id"`
	URL             string   `json:"url"`
	Title           string   `json:"title"`
	Price           *float64 `json:"price,omitempty"`
	Currency        string   `json:"currency,omitempty"`
	City            string   `json:"city,omitempty"`
	District        string   `json:"district,omitempty"`
	PropertyType    string   `json:"property_type,omitempty"`
	DealType        string   `json:"deal_type,omitempty"`
	PostedAt        *string  `json:"posted_at,omitempty"`
	SellerID        int64    `json:"seller_id"`
	SellerName      string   `json:"seller_name,omitempty"`
	IsBusiness      bool     `json:"is_business"`
	SellerListings  int64    `json:"seller_listings"`
	SellerDistricts int64    `json:"seller_districts"`
	RealSellerScore float64  `json:"real_seller_score"`
}

type Stats struct {
	PrivateSellers   int32   `json:"private_sellers"`
	BusinessSellers  int32   `json:"business_sellers"`
	PrivateAvgScore  float64 `json:"private_avg_score"`
	BusinessAvgScore float64 `json:"business_avg_score"`
}

// Category is one (property_type, deal_type) pair the frontend uses to
// populate its dropdowns. n is the listing count — handy for displaying
// "Houses (42)" instead of just "Houses".
type Category struct {
	PropertyType string `json:"property_type"`
	DealType     string `json:"deal_type,omitempty"`
	Count        int32  `json:"count"`
}

// --- handlers --------------------------------------------------------------

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	maxAgeDays := intParam(q, "max_age_days", 30)
	minScore := floatParam(q, "min_score", 0.0)
	limit := intParam(q, "limit", 200)
	propertyType := strings.TrimSpace(q.Get("property_type"))
	dealType := strings.TrimSpace(q.Get("deal_type"))

	if limit > 1000 {
		limit = 1000 // hard cap so a runaway frontend can't OOM us
	}

	rows, err := s.Queries.ListListingsForAPI(r.Context(), sqlc.ListListingsForAPIParams{
		MaxAgeDays:   int32(maxAgeDays),
		MinScore:     numericFromFloat(minScore),
		PropertyType: propertyType,
		DealType:     dealType,
		LimitN:       int32(limit),
	})
	if err != nil {
		s.Log.Error("list listings", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	out := make([]Listing, 0, len(rows))
	for _, r := range rows {
		out = append(out, toAPIListing(r))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) categories(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Queries.GetDistinctCategories(r.Context())
	if err != nil {
		s.Log.Error("get categories", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	out := make([]Category, 0, len(rows))
	for _, r := range rows {
		c := Category{Count: r.N}
		if r.PropertyType.Valid {
			c.PropertyType = r.PropertyType.String
		}
		if r.DealType.Valid {
			c.DealType = r.DealType.String
		}
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Queries.GetSellerCounts(r.Context())
	if err != nil {
		s.Log.Error("get stats", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	var st Stats
	for _, row := range rows {
		if row.IsBusiness {
			st.BusinessSellers = row.Sellers
			st.BusinessAvgScore = row.AvgScore
		} else {
			st.PrivateSellers = row.Sellers
			st.PrivateAvgScore = row.AvgScore
		}
	}
	writeJSON(w, http.StatusOK, st)
}

// --- helpers ---------------------------------------------------------------

func toAPIListing(r sqlc.ListingsWithClassification) Listing {
	out := Listing{
		ListingID:       r.ListingID,
		OlxListingID:    r.OlxListingID,
		URL:             r.Url,
		IsBusiness:      r.IsBusiness,
		SellerID:        r.SellerID,
		SellerListings:  r.SellerListingsActive,
		SellerDistricts: r.SellerDistrictsCount,
	}
	if r.Title.Valid {
		out.Title = r.Title.String
	}
	if r.Currency.Valid {
		out.Currency = r.Currency.String
	}
	if r.City.Valid {
		out.City = r.City.String
	}
	if r.District.Valid {
		out.District = r.District.String
	}
	if r.SellerName.Valid {
		out.SellerName = r.SellerName.String
	}
	if r.PropertyType.Valid {
		out.PropertyType = r.PropertyType.String
	}
	if r.DealType.Valid {
		out.DealType = r.DealType.String
	}
	if r.PostedAt.Valid {
		s := r.PostedAt.Time.Format(time.RFC3339)
		out.PostedAt = &s
	}
	if r.Price.Valid {
		if f, err := r.Price.Float64Value(); err == nil && f.Valid {
			out.Price = &f.Float64
		}
	}
	if r.RealSellerScore.Valid {
		if f, err := r.RealSellerScore.Float64Value(); err == nil && f.Valid {
			out.RealSellerScore = f.Float64
		}
	}
	return out
}

func intParam(q map[string][]string, name string, def int) int {
	v, ok := q[name]
	if !ok || len(v) == 0 {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v[0]))
	if err != nil {
		return def
	}
	return n
}

func floatParam(q map[string][]string, name string, def float64) float64 {
	v, ok := q[name]
	if !ok || len(v) == 0 {
		return def
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v[0]), 64)
	if err != nil {
		return def
	}
	return f
}

func numericFromFloat(f float64) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(strconv.FormatFloat(f, 'f', 4, 64))
	return n
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// --- middleware ------------------------------------------------------------

// cors is permissive on purpose: this is a local-dev API, the React app
// runs on a different port (Vite default 5173). For production, tighten
// Access-Control-Allow-Origin to specific hosts.
func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// logRequest is a tiny access-log middleware. Wraps ResponseWriter so we
// can capture the status code for the log line.
func logRequest(log *slog.Logger, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &statusWriter{ResponseWriter: w, status: 200}
		h.ServeHTTP(ww, r)
		log.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.status,
			"duration", time.Since(start).Round(time.Microsecond),
		)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

// Compile-time check: ensure context.Context is what we think.
var _ context.Context = context.Background()