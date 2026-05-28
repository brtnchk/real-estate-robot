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