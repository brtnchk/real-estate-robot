-- name: UpsertListing :one
-- Insert or update by olx_listing_id. Same timestamp semantics as sellers:
-- created_at / first_seen_at frozen on insert, last_seen_at / last_scraped_at
-- bumped on every call, updated_at handled by the trigger.
INSERT INTO listings (
    olx_listing_id,
    seller_id,
    url,
    title,
    description,
    raw,
    last_scraped_at
) VALUES (
    $1, $2, $3, $4, $5, $6, NOW()
)
ON CONFLICT (olx_listing_id) DO UPDATE SET
    seller_id       = EXCLUDED.seller_id,
    title           = EXCLUDED.title,
    description     = EXCLUDED.description,
    raw             = EXCLUDED.raw,
    last_seen_at    = NOW(),
    last_scraped_at = NOW()
RETURNING *;

-- name: GetListingByOlxID :one
SELECT * FROM listings WHERE olx_listing_id = $1;

-- name: CountListings :one
SELECT COUNT(*) FROM listings;

-- name: InsertListingSnapshot :execrows
-- Append-only history of (price, status, title) changes per listing.
-- The unique constraint on (listing_id, raw_hash) means re-parsing an
-- unchanged listing is a no-op; only real changes create new rows.
-- :execrows returns the affected count so the worker can log whether
-- a new snapshot was actually persisted (1) or deduped (0).
INSERT INTO listing_snapshots (
    listing_id, price, currency, status, title, raw_hash
) VALUES (
    $1, $2, $3, $4, $5, $6
)
ON CONFLICT (listing_id, raw_hash) DO NOTHING;