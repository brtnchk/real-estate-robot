// Command db-demo proves the Postgres layer works end-to-end:
//
//  1. Connects to the DB and pings.
//  2. Calls UpsertSeller for a synthetic seller.
//  3. Calls UpsertSeller AGAIN with the same olx_user_id but different
//     fields, proving the ON CONFLICT branch updates instead of inserting.
//  4. Asserts the row count stayed at +1 and the id of both calls matches.
//
//	DATABASE_URL=postgres://olx:olx@localhost:5432/olx?sslmode=disable \
//	go run ./cmd/db-demo
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/brtnchk/real-estate-robot/internal/db"
	"github.com/brtnchk/real-estate-robot/internal/db/sqlc"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Error("DATABASE_URL is not set")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := db.Connect(ctx, url)
	if err != nil {
		log.Error("connect", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	q := sqlc.New(pool)

	const olxID = "demo-user-42"

	before, err := q.CountSellers(ctx)
	if err != nil {
		log.Error("count before", "err", err)
		os.Exit(1)
	}
	log.Info("sellers before", "count", before)

	// --- First upsert: INSERT branch ----------------------------------------
	s1, err := q.UpsertSeller(ctx, sqlc.UpsertSellerParams{
		OlxUserID:   olxID,
		DisplayName: textPtr("Олег П."),
		ProfileUrl:  textPtr("https://www.olx.ua/uk/user/" + olxID),
		IsBusiness:  false,
		PhoneHash:   textPtr("c2hhMjU2OmFiYzEyMw=="),
		AvatarUrl:   pgtype.Text{}, // null
		Location:    textPtr("Київ, Подільський"),
		Raw:         json.RawMessage(`{"source":"db-demo","attempt":1}`),
	})
	if err != nil {
		log.Error("upsert 1", "err", err)
		os.Exit(1)
	}
	log.Info("upsert 1 (insert)",
		"id", s1.ID,
		"display_name", textVal(s1.DisplayName),
		"created_at", s1.CreatedAt.Time.Format(time.RFC3339Nano),
		"last_seen_at", s1.LastSeenAt.Time.Format(time.RFC3339Nano),
		"updated_at", s1.UpdatedAt.Time.Format(time.RFC3339Nano),
	)

	// Pause briefly so the timestamps differ visibly on the second upsert.
	time.Sleep(1500 * time.Millisecond)

	// --- Second upsert: UPDATE branch ---------------------------------------
	s2, err := q.UpsertSeller(ctx, sqlc.UpsertSellerParams{
		OlxUserID:   olxID,
		DisplayName: textPtr("Олег Партенич"),                    // changed
		ProfileUrl:  textPtr("https://www.olx.ua/uk/user/" + olxID),
		IsBusiness:  false,
		PhoneHash:   textPtr("c2hhMjU2OmFiYzEyMw=="),
		AvatarUrl:   textPtr("https://...avatar.jpg"),            // null → value
		Location:    textPtr("Київ, Подільський"),
		Raw:         json.RawMessage(`{"source":"db-demo","attempt":2}`), // changed
	})
	if err != nil {
		log.Error("upsert 2", "err", err)
		os.Exit(1)
	}
	log.Info("upsert 2 (update)",
		"id", s2.ID,
		"display_name", textVal(s2.DisplayName),
		"created_at", s2.CreatedAt.Time.Format(time.RFC3339Nano),
		"last_seen_at", s2.LastSeenAt.Time.Format(time.RFC3339Nano),
		"updated_at", s2.UpdatedAt.Time.Format(time.RFC3339Nano),
	)

	// --- Assertions ---------------------------------------------------------
	if s1.ID != s2.ID {
		log.Error("BUG: upsert created a new row instead of updating", "id1", s1.ID, "id2", s2.ID)
		os.Exit(1)
	}
	if !s2.LastSeenAt.Time.After(s1.LastSeenAt.Time) {
		log.Error("BUG: last_seen_at did not advance on update")
		os.Exit(1)
	}
	if !s2.UpdatedAt.Time.After(s1.UpdatedAt.Time) {
		log.Error("BUG: updated_at trigger did not fire on update")
		os.Exit(1)
	}
	if !s2.CreatedAt.Time.Equal(s1.CreatedAt.Time) {
		log.Error("BUG: created_at changed on update — should be immutable")
		os.Exit(1)
	}

	after, err := q.CountSellers(ctx)
	if err != nil {
		log.Error("count after", "err", err)
		os.Exit(1)
	}
	log.Info("sellers after", "count", after, "delta", after-before)

	// Delta is 1 on the very first run (insert + update) and 0 on every
	// subsequent run (update + update). Anything > 1 means our ON CONFLICT
	// clause failed to match — that would be a real bug.
	if after-before > 1 {
		log.Error("BUG: more than one new row created", "delta", after-before)
		os.Exit(1)
	}

	// Bonus: read it back via GetSellerByOlxID and show what's stored.
	got, err := q.GetSellerByOlxID(ctx, olxID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			log.Error("get back", "err", err)
		}
		log.Error("get back", "err", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "\n=== stored row ===\n%s\n", mustJSON(got))

	log.Info("✓ upsert is idempotent: same id on both calls, last_seen_at and updated_at advanced, created_at immutable")
}

// textPtr returns a non-null pgtype.Text for the given string.
func textPtr(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: true}
}

// textVal returns the underlying string or "<null>" — for logs.
func textVal(t pgtype.Text) string {
	if !t.Valid {
		return "<null>"
	}
	return t.String
}

// mustJSON pretty-prints anything for logging. Demo-grade.
func mustJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%+v", v)
	}
	return string(b)
}