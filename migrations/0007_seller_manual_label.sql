-- +goose Up
-- +goose StatementBegin

ALTER TABLE sellers
    ADD COLUMN manual_label TEXT
        CHECK (manual_label IN ('owner', 'agency'));

-- Rebuild seller_classifications with manual_label OVERRIDE logic.
-- New column appended at the END (Postgres CREATE OR REPLACE constraint).
CREATE OR REPLACE VIEW seller_classifications AS
WITH components AS (
    SELECT
        s.id,
        s.olx_user_id,
        s.display_name,
        s.is_business,
        s.registered_at,
        s.manual_label,
        COALESCE(ss.listings_active, 0)  AS listings_active,
        COALESCE(ss.districts_count, 0)  AS districts_count,
        COALESCE(ss.cities_count, 0)     AS cities_count,
        (CASE WHEN s.is_business THEN -0.5 ELSE 0.3 END)::numeric AS score_personhood,
        (CASE
            WHEN COALESCE(ss.listings_active, 0) = 0 THEN 0
            WHEN ss.listings_active <= 2  THEN 0.3
            WHEN ss.listings_active <= 5  THEN 0.15
            WHEN ss.listings_active <= 10 THEN 0.05
            ELSE 0
        END)::numeric AS score_listings_count,
        (CASE
            WHEN COALESCE(ss.districts_count, 0) <= 1 THEN 0.2
            WHEN ss.districts_count = 2 THEN 0.1
            ELSE 0
        END)::numeric AS score_geography,
        (CASE
            WHEN s.registered_at IS NULL THEN 0
            WHEN s.registered_at < NOW() - INTERVAL '2 years'  THEN 0.2
            WHEN s.registered_at < NOW() - INTERVAL '6 months' THEN 0.1
            ELSE 0
        END)::numeric AS score_account_age
    FROM sellers s
    LEFT JOIN seller_stats ss ON ss.seller_id = s.id
)
SELECT
    id                    AS seller_id,
    olx_user_id,
    display_name,
    is_business,
    registered_at,
    listings_active,
    districts_count,
    cities_count,
    score_personhood,
    score_listings_count,
    score_geography,
    score_account_age,
    -- manual_label overrides formula: 'owner'→1.0, 'agency'→0.0, NULL→formula
    CASE
        WHEN manual_label = 'owner'  THEN 1.0::numeric
        WHEN manual_label = 'agency' THEN 0.0::numeric
        ELSE LEAST(1.0, GREATEST(0.0,
            score_personhood + score_listings_count + score_geography + score_account_age
        ))::numeric
    END AS real_seller_score,
    manual_label           -- new column, appended last
FROM components;

-- listings_with_classification: add manual_label after existing columns.
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
    l.deal_type,
    sc.manual_label            -- new, appended last
FROM listings l
JOIN seller_classifications sc ON sc.seller_id = l.seller_id
WHERE l.status = 'active';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE VIEW listings_with_classification AS
SELECT
    l.id AS listing_id, l.olx_listing_id, l.url, l.title, l.price, l.currency,
    l.city, l.district, l.posted_at, l.last_seen_at AS listing_last_seen,
    sc.seller_id, sc.olx_user_id, sc.display_name AS seller_name,
    sc.is_business, sc.registered_at AS seller_registered_at,
    sc.listings_active AS seller_listings_active,
    sc.districts_count AS seller_districts_count,
    sc.real_seller_score, l.property_type, l.deal_type
FROM listings l
JOIN seller_classifications sc ON sc.seller_id = l.seller_id
WHERE l.status = 'active';
ALTER TABLE sellers DROP COLUMN IF EXISTS manual_label;
-- +goose StatementEnd