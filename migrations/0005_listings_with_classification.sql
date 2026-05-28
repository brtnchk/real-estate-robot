-- +goose Up
-- +goose StatementBegin

-- listings_with_classification is the join Grafana queries: every active
-- listing alongside the score of its seller. One row per listing means the
-- table-style view shows clickable URLs directly, sorted by how likely the
-- seller is a real private owner.
--
-- This is a regular view (not materialized) so it's always fresh; the only
-- caching layer is seller_stats underneath. Run `make refresh-stats` after
-- a fresh batch of parsed listings to refresh that.
CREATE VIEW listings_with_classification AS
SELECT
    l.id                       AS listing_id,
    l.olx_listing_id,
    l.url,
    l.title,
    l.price,
    l.currency,
    l.city,
    l.district,
    l.posted_at,
    l.last_seen_at             AS listing_last_seen,

    sc.seller_id,
    sc.olx_user_id,
    sc.display_name            AS seller_name,
    sc.is_business,
    sc.registered_at           AS seller_registered_at,
    sc.listings_active         AS seller_listings_active,
    sc.districts_count         AS seller_districts_count,
    sc.real_seller_score
FROM listings l
JOIN seller_classifications sc ON sc.seller_id = l.seller_id
WHERE l.status = 'active';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS listings_with_classification;
-- +goose StatementEnd