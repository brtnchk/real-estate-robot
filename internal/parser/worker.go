package parser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

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

// Worker is the queue.Handler implementation: load HTML by fetch_id, parse
// it, write seller + listing + snapshot in one transaction, then fan out
// a seller-enrich task.
//
// All fields are required.
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

	// Load the raw HTML by id. Note we read OUTSIDE the transaction —
	// reads don't need to be serialized with writes here, and keeping
	// the transaction small reduces lock contention.
	fetch, err := w.Queries.GetFetchByID(ctx, task.FetchID)
	if err != nil {
		return fmt.Errorf("load fetch row: %w", err)
	}
	if fetch.Html == nil {
		// fetcher should never publish parse tasks for 4xx fetches, but
		// guard anyway — defensive coding around message contracts.
		log.Warn("fetch row has no html, skipping")
		return nil
	}

	res, err := Parse(task.URL, fetch.Html)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}

	snapHash := snapshotHash(res.Listing.Title) // expand as we extract more fields

	// pgx.BeginFunc handles Begin / Commit / Rollback. The callback runs
	// inside the transaction; returning nil commits, returning err rolls
	// back. WithTx(tx) re-binds the sqlc Queries to the transaction's
	// session so every query inside goes through the same tx.
	var sellerID, listingID int64
	var snapAffected int64

	err = pgx.BeginFunc(ctx, w.Pool, func(tx pgx.Tx) error {
		qtx := w.Queries.WithTx(tx)

		seller, err := qtx.UpsertSeller(ctx, sqlc.UpsertSellerParams{
			OlxUserID:   res.Seller.OlxUserID,
			DisplayName: pgtype.Text{String: res.Seller.DisplayName, Valid: true},
			Raw:         json.RawMessage(`{"source":"parser-stub"}`),
		})
		if err != nil {
			return fmt.Errorf("upsert seller: %w", err)
		}
		sellerID = seller.ID

		listing, err := qtx.UpsertListing(ctx, sqlc.UpsertListingParams{
			OlxListingID: res.Listing.OlxListingID,
			SellerID:     pgtype.Int8{Int64: seller.ID, Valid: true},
			Url:          res.Listing.URL,
			Title:        pgtype.Text{String: res.Listing.Title, Valid: true},
			Description:  pgtype.Text{String: res.Listing.Description, Valid: true},
			Raw:          json.RawMessage(`{"source":"parser-stub"}`),
		})
		if err != nil {
			return fmt.Errorf("upsert listing: %w", err)
		}
		listingID = listing.ID

		affected, err := qtx.InsertListingSnapshot(ctx, sqlc.InsertListingSnapshotParams{
			ListingID: listing.ID,
			Price:     pgtype.Numeric{},                                       // unset for now
			Currency:  pgtype.Text{},                                          //
			Status:    pgtype.Text{String: "active", Valid: true},
			Title:     pgtype.Text{String: res.Listing.Title, Valid: true},
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
		"seller_id", sellerID,
		"listing_id", listingID,
		"snapshot_inserted", snapAffected > 0,
	)

	// After the tx commits, fan out a seller-enrich task. If this publish
	// fails, the message will be retried — the transaction's idempotent
	// upserts make re-runs safe.
	next, _ := json.Marshal(NextTask{OlxUserID: res.Seller.OlxUserID})
	if err := w.Publisher.Publish(ctx, queue.QueueSellersEnrich, next); err != nil {
		return fmt.Errorf("publish enrich task: %w", err)
	}

	return nil
}

// snapshotHash builds the dedup key for listing_snapshots. We hash the
// concatenation of every "state" field we care about — when the parser
// learns new fields (price, status, location), they get added here so
// snapshots are written on any meaningful change.
func snapshotHash(parts ...string) string {
	h := sha256NewSum(parts...)
	return h
}