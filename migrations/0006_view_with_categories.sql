-- +goose Up
-- +goose StatementBegin

-- Extend listings_with_classification with the new property_type and
-- deal_type columns the parser now fills. CREATE OR REPLACE allows
-- appending columns at the END of the SELECT list — anywhere else and
-- Postgres rejects.
CREATE OR REPLACE VIEW listings_with_classification AS
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
    sc.real_seller_score,
    l.property_type,
    l.deal_type
FROM listings l
JOIN seller_classifications sc ON sc.seller_id = l.seller_id
WHERE l.status = 'active';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Restore the pre-categories shape. Same CREATE OR REPLACE trick in reverse.
CREATE OR REPLACE VIEW listings_with_classification AS
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