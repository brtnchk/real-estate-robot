-- name: UpsertSeller :one
-- Insert a seller, or update the mutable fields if olx_user_id already
-- exists. last_seen_at and last_scraped_at are bumped to NOW() on both
-- insert and update; first_seen_at / created_at stay frozen (the schema
-- defaults handle the insert case, ON CONFLICT leaves them untouched).
-- updated_at is bumped automatically by the sellers_set_updated_at trigger.
--
-- registered_at uses COALESCE on conflict: once we have observed when a
-- seller joined OLX, we keep that value and don't let a later scrape
-- (which might come back NULL on a partial page) erase it.
INSERT INTO sellers (
    olx_user_id,
    display_name,
    profile_url,
    is_business,
    phone_hash,
    avatar_url,
    location,
    registered_at,
    raw,
    last_scraped_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, NOW()
)
ON CONFLICT (olx_user_id) DO UPDATE SET
    display_name    = EXCLUDED.display_name,
    profile_url     = EXCLUDED.profile_url,
    is_business     = EXCLUDED.is_business,
    phone_hash      = EXCLUDED.phone_hash,
    avatar_url      = EXCLUDED.avatar_url,
    location        = EXCLUDED.location,
    registered_at   = COALESCE(sellers.registered_at, EXCLUDED.registered_at),
    raw             = EXCLUDED.raw,
    last_seen_at    = NOW(),
    last_scraped_at = NOW()
RETURNING *;

-- name: GetSellerByOlxID :one
SELECT * FROM sellers WHERE olx_user_id = $1;

-- name: CountSellers :one
SELECT COUNT(*) FROM sellers;

-- name: MarkSellerEnriched :exec
-- Stamp the freshness gate. Called by the enrich worker after it has
-- finished (successfully or not — even a 404'd profile counts, otherwise
-- we'd retry-storm a dead URL forever).
UPDATE sellers SET last_enriched_at = NOW() WHERE id = $1;