package parser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/brtnchk/real-estate-robot/internal/db/sqlc"
	"github.com/brtnchk/real-estate-robot/internal/queue"
)

// Task is what we expect on queue.QueueListingsParse — what fetcher published.
type Task struct {
	FetchID int64  `json:"fetch_id"`
	URL     string `json:"url"`
}

// NextTask is what we publish to queue.QueueSellersEnrich after a successful
// parse. The seller-enrich worker will fetch the seller's profile and all
// their other listings, feeding new URLs back into queue.QueueListingsFetch.
type NextTask struct {
	OlxUserID string `json:"olx_user_id"`
}

// Worker is the queue.Handler implementation.
type Worker struct {
	Pool      *pgxpool.Pool
	Queries   *sqlc.Queries
	Publisher *queue.Publisher
	Log       *slog.Logger
}

// Handle implements queue.Handler.
func (w *Worker) Handle(ctx context.Context, d amqp.Delivery) error {
	var task Task
	if err := json.Unmarshal(d.Body, &task); err != nil {
		return fmt.Errorf("decode task: %w", err)
	}
	if task.FetchID == 0 {
		return errors.New("task has zero fetch_id")
	}

	log := w.Log.With("fetch_id", task.FetchID, "url", task.URL)

	fetch, err := w.Queries.GetFetchByID(ctx, task.FetchID)
	if err != nil {
		return fmt.Errorf("load fetch row: %w", err)
	}
	if fetch.Html == nil {
		log.Warn("fetch row has no html, skipping")
		return nil
	}

	res, err := Parse(task.URL, fetch.Html)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}

	snapHash := snapshotHash(
		res.Listing.Title,
		res.Listing.Status,
		formatFloatPtr(res.Listing.Price),
		res.Listing.Currency,
	)

	// One transaction: seller + listing + snapshot all commit together
	// or not at all. WithTx re-binds the sqlc Queries to this tx so every
	// query goes through the same session.
	var sellerID, listingID int64
	var snapAffected int64

	err = pgx.BeginFunc(ctx, w.Pool, func(tx pgx.Tx) error {
		qtx := w.Queries.WithTx(tx)

		seller, err := qtx.UpsertSeller(ctx, sqlc.UpsertSellerParams{
			OlxUserID:    res.Seller.OlxUserID,
			DisplayName:  text(res.Seller.DisplayName),
			ProfileUrl:   text(res.Seller.ProfileURL),
			IsBusiness:   res.Seller.IsBusiness,
			RegisteredAt: timestamptzPtr(res.Seller.RegisteredAt),
			Raw:          json.RawMessage(`{"source":"parser"}`),
		})
		if err != nil {
			return fmt.Errorf("upsert seller: %w", err)
		}
		sellerID = seller.ID

		listing, err := qtx.UpsertListing(ctx, sqlc.UpsertListingParams{
			OlxListingID: res.Listing.OlxListingID,
			SellerID:     pgtype.Int8{Int64: seller.ID, Valid: true},
			Url:          res.Listing.URL,
			Title:        text(res.Listing.Title),
			Description:  text(res.Listing.Description),
			Price:        numericFromFloatPtr(res.Listing.Price),
			Currency:     text(res.Listing.Currency),
			Status:       defaultIfEmpty(res.Listing.Status, "active"),
			PropertyType: text(res.Listing.PropertyType),
			DealType:     text(res.Listing.DealType),
			City:         text(res.Listing.City),
			District:     text(res.Listing.District),
			Address:      text(res.Listing.Address),
			Lat:          float8Ptr(res.Listing.Lat),
			Lon:          float8Ptr(res.Listing.Lon),
			PostedAt:     timestamptzPtr(res.Listing.PostedAt),
			Attributes:   nonNilJSON(res.Listing.Attributes),
			Raw:          json.RawMessage(`{"source":"parser"}`),
		})
		if err != nil {
			return fmt.Errorf("upsert listing: %w", err)
		}
		listingID = listing.ID

		affected, err := qtx.InsertListingSnapshot(ctx, sqlc.InsertListingSnapshotParams{
			ListingID: listing.ID,
			Price:     numericFromFloatPtr(res.Listing.Price),
			Currency:  text(res.Listing.Currency),
			Status:    text(res.Listing.Status),
			Title:     text(res.Listing.Title),
			RawHash:   snapHash,
		})
		if err != nil {
			return fmt.Errorf("insert snapshot: %w", err)
		}
		snapAffected = affected

		return nil
	})
	if err != nil {
		return err
	}

	log.Info("parsed and stored",
		"olx_listing_id", res.Listing.OlxListingID,
		"seller_id", sellerID,
		"listing_id", listingID,
		"snapshot_inserted", snapAffected > 0,
		"title", res.Listing.Title,
		"is_business", res.Seller.IsBusiness,
	)

	// After commit, fan out a seller-enrich task. The retry of a failed
	// publish hits the same idempotent upserts → safe.
	next, _ := json.Marshal(NextTask{OlxUserID: res.Seller.OlxUserID})
	if err := w.Publisher.Publish(ctx, queue.QueueSellersEnrich, next); err != nil {
		return fmt.Errorf("publish enrich task: %w", err)
	}

	return nil
}

// snapshotHash builds the dedup key for listing_snapshots. We hash the
// joined "state" fields we care about; if any of them changes, a new
// snapshot row is created. Adding new fields here is a deliberate act —
// it invalidates existing dedup decisions for the next pass.
func snapshotHash(parts ...string) string {
	return sha256NewSum(parts...)
}

// --- pgtype helpers --------------------------------------------------------

// text wraps a string as a non-null pgtype.Text. Empty string becomes a
// NULL — we don't store distinguish between "" and unset in this schema.
func text(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func timestamptzPtr(t *time.Time) pgtype.Timestamptz {
	if t == nil || t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func float8Ptr(f *float64) pgtype.Float8 {
	if f == nil {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: *f, Valid: true}
}

// numericFromFloatPtr converts a *float64 to a pgtype.Numeric by routing
// through the textual representation. Float→big.Int conversion would lose
// precision for non-integer prices; the string round-trip is exact for
// values that fit in NUMERIC(14,2).
func numericFromFloatPtr(f *float64) pgtype.Numeric {
	if f == nil {
		return pgtype.Numeric{}
	}
	var n pgtype.Numeric
	if err := n.Scan(strconv.FormatFloat(*f, 'f', 2, 64)); err != nil {
		return pgtype.Numeric{}
	}
	return n
}

func defaultIfEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func nonNilJSON(j json.RawMessage) json.RawMessage {
	if len(j) == 0 {
		return json.RawMessage(`{}`)
	}
	return j
}

// formatFloatPtr is a small helper used only by snapshotHash so the hash is
// stable across reparses regardless of whether the float was parsed as 43000.0
// or as 43000.00.
func formatFloatPtr(f *float64) string {
	if f == nil {
		return ""
	}
	return strconv.FormatFloat(*f, 'f', 2, 64)
}