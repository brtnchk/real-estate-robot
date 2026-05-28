// Command classify ranks sellers by real_seller_score and prints a table.
// It is the closing-the-loop CLI: discovery + fetcher + parser + enricher
// fill the DB; classify turns that data into the actual product question
// "which sellers are real private owners?".
//
//	classify [--refresh] [--limit N] [--min-listings N]
//
// Use --refresh after a fresh batch of parsed listings to update the
// seller_stats materialized view before scoring.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/brtnchk/real-estate-robot/internal/db"
	"github.com/brtnchk/real-estate-robot/internal/db/sqlc"
)

func main() {
	limit := flag.Int("limit", 20, "max rows in the ranking")
	minListings := flag.Int("min-listings", 1, "only consider sellers with at least N active listings")
	refresh := flag.Bool("refresh", false, "REFRESH MATERIALIZED VIEW seller_stats before scoring")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Error("DATABASE_URL is not set")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.Connect(ctx, url)
	if err != nil {
		log.Error("connect", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	q := sqlc.New(pool)

	if *refresh {
		if err := q.RefreshSellerStats(ctx); err != nil {
			log.Error("refresh seller_stats", "err", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "  ✓ seller_stats refreshed")
	}

	rows, err := q.TopRealSellers(ctx, sqlc.TopRealSellersParams{
		MinActiveListings: int32(*minListings),
		Limit:             int32(*limit),
	})
	if err != nil {
		log.Error("query", "err", err)
		os.Exit(1)
	}

	if len(rows) == 0 {
		fmt.Println("(no sellers found — did parser run? try `make publish q=listings.fetch m='{\"url\":\"...\"}'`)")
		return
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "rank\tscore\tname\tbiz\tlistings\tdistricts\tage\tpersonhood\tcount\tgeo\tage_score")
	fmt.Fprintln(tw, "----\t-----\t----\t---\t--------\t---------\t---\t----------\t-----\t---\t---------")
	for i, r := range rows {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d\t%d\t%s\t%s\t%s\t%s\t%s\n",
			i+1,
			numericPretty(r.RealSellerScore, 2),
			truncate(textOr(r.DisplayName, "?"), 28),
			boolPretty(r.IsBusiness),
			r.ListingsActive,
			r.DistrictsCount,
			ageDays(r.RegisteredAt),
			numericPretty(r.ScorePersonhood, 2),
			numericPretty(r.ScoreListingsCount, 2),
			numericPretty(r.ScoreGeography, 2),
			numericPretty(r.ScoreAccountAge, 2),
		)
	}
	_ = tw.Flush()
}

// --- formatting helpers ----------------------------------------------------

func textOr(t pgtype.Text, fallback string) string {
	if !t.Valid {
		return fallback
	}
	return t.String
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func boolPretty(b bool) string {
	if b {
		return "✓"
	}
	return "·"
}

// numericPretty formats a pgtype.Numeric with N decimal places.
func numericPretty(n pgtype.Numeric, decimals int) string {
	if !n.Valid {
		return "·"
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return "?"
	}
	return strconv.FormatFloat(f.Float64, 'f', decimals, 64)
}

// ageDays renders a TIMESTAMPTZ as "Nd" / "Nm" / "Ny" — relative humans-friendly.
func ageDays(t pgtype.Timestamptz) string {
	if !t.Valid {
		return "?"
	}
	d := time.Since(t.Time)
	days := int(d.Hours() / 24)
	switch {
	case days < 30:
		return fmt.Sprintf("%dd", days)
	case days < 365:
		return fmt.Sprintf("%dmo", days/30)
	default:
		return fmt.Sprintf("%dy", days/365)
	}
}