-- name: UpsertSeller :one
-- Insert a seller, or update the mutable fields if olx_user_id already
-- exists. last_seen_at and last_scraped_at are bumped to NOW() on both
-- insert and update; first_seen_at / created_at stay frozen (the schema
-- defaults handle the insert case, ON CONFLICT leaves them untouched).
-- updated_at is bumped automatically by the sellers_set_updated_at trigger.
INSERT INTO sellers (
    olx_user_id,
    display_name,
    profile_url,
    is_business,
    phone_hash,
    avatar_url,
    location,
    raw,
    last_scraped_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, NOW()
)
ON CONFLICT (olx_user_id) DO UPDATE SET
    display_name    = EXCLUDED.display_name,
    profile_url     = EXCLUDED.profile_url,
    is_business     = EXCLUDED.is_business,
    phone_hash      = EXCLUDED.phone_hash,
    avatar_url      = EXCLUDED.avatar_url,
    location        = EXCLUDED.location,
    raw             = EXCLUDED.raw,
    last_seen_at    = NOW(),
    last_scraped_at = NOW()
RETURNING *;

-- name: GetSellerByOlxID :one
SELECT * FROM sellers WHERE olx_user_id = $1;

-- name: CountSellers :one
SELECT COUNT(*) FROM sellers;