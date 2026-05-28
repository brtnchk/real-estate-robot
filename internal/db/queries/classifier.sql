-- name: TopRealSellers :many
-- Returns the top-N sellers by real_seller_score, used by `cmd/classify`.
-- Ties broken by listings_active DESC so the ranking is deterministic
-- (otherwise PostgreSQL's row order on equal scores is implementation-defined).
SELECT * FROM seller_classifications
 WHERE listings_active >= sqlc.arg('min_active_listings')::int
 ORDER BY real_seller_score DESC, listings_active DESC, seller_id ASC
 LIMIT sqlc.arg('limit')::int;

-- name: GetSellerClassification :one
-- Single seller's score and component breakdown.
SELECT * FROM seller_classifications
 WHERE olx_user_id = $1;

-- name: RefreshSellerStats :exec
-- Refresh the materialized view that the classifier reads from. Run after
-- a batch of new listings has been parsed and before re-running classify.
-- CONCURRENTLY needs the UNIQUE index that 0001_init.sql declares.
REFRESH MATERIALIZED VIEW CONCURRENTLY seller_stats;

-- name: ListListingsForAPI :many
-- The HTTP API's main query: filtered listings with their seller score.
-- max_age_days = 99999 effectively disables the recency filter (NOW() -
-- 273 years catches everything in practice).
SELECT * FROM listings_with_classification
 WHERE posted_at >= NOW() - (interval '1 day' * sqlc.arg('max_age_days')::int)
   AND real_seller_score >= sqlc.arg('min_score')::numeric
 ORDER BY real_seller_score DESC, posted_at DESC
 LIMIT sqlc.arg('limit_n')::int;

-- name: GetSellerCounts :many
-- Two rows: one for is_business=false, one for true. Used by the API's
-- stats endpoint to render the private/business split.
SELECT
    is_business,
    COUNT(*)::int                 AS sellers,
    AVG(real_seller_score)::float AS avg_score
FROM seller_classifications
GROUP BY is_business;