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
--
-- property_type and deal_type are "empty string = no filter" — if the
-- caller passes '', the OR-branch short-circuits and all rows pass; if
-- they pass a concrete value (e.g. 'квартири'), only matching rows do.
-- NULL property_type / deal_type rows are excluded when a filter is set,
-- which is what we want: filtering on a category implies "give me data
-- that actually carries that category".
SELECT * FROM listings_with_classification
 WHERE posted_at >= NOW() - (interval '1 day' * sqlc.arg('max_age_days')::int)
   AND real_seller_score >= sqlc.arg('min_score')::numeric
   AND (sqlc.arg('property_type')::text = '' OR property_type = sqlc.arg('property_type')::text)
   AND (sqlc.arg('deal_type')::text     = '' OR deal_type     = sqlc.arg('deal_type')::text)
   AND (sqlc.arg('city')::text          = '' OR city          = sqlc.arg('city')::text)
 ORDER BY real_seller_score DESC, posted_at DESC
 LIMIT sqlc.arg('limit_n')::int;

-- name: GetDistinctCities :many
-- Cities present in the dataset with their listing counts, drives the
-- frontend's city dropdown. City names are whatever OLX wrote
-- (Ukrainian: "Київ", "Львів", "Одеса", etc.) — we don't normalize.
SELECT city, COUNT(*)::int AS n
  FROM listings_with_classification
 WHERE city IS NOT NULL AND city <> ''
 GROUP BY city
 ORDER BY n DESC, city;

-- name: GetDistinctCategories :many
-- Distinct (property_type, deal_type) pairs present in the dataset, used
-- to populate the frontend dropdowns dynamically (no hardcoded list).
SELECT property_type, deal_type, COUNT(*)::int AS n
  FROM listings_with_classification
 WHERE property_type IS NOT NULL AND property_type <> ''
 GROUP BY property_type, deal_type
 ORDER BY n DESC, property_type;

-- name: GetLastParsedAt :one
-- When was the last time the parser committed a listing.
-- Used to show "data as of X" in the UI.
SELECT MAX(last_scraped_at) AS last_parsed_at FROM listings;

-- name: GetSellerCounts :many
-- Two rows: one for is_business=false, one for true. Used by the API's
-- stats endpoint to render the private/business split.
SELECT
    is_business,
    COUNT(*)::int                 AS sellers,
    AVG(real_seller_score)::float AS avg_score
FROM seller_classifications
GROUP BY is_business;