-- +goose Up
-- +goose StatementBegin

-- seller_classifications computes a "real private seller" score per seller,
-- in [0, 1], from explainable components. Higher = more likely a real
-- private owner; lower = agency / reseller / spam.
--
-- The view is a plain (non-materialized) view because it's cheap: it joins
-- sellers with seller_stats (already a MV) and applies CASE arithmetic.
-- For interactive queries this is fast enough; for batch jobs, run
-- `REFRESH MATERIALIZED VIEW seller_stats` first.
--
-- The four components are emitted as separate columns so the score is
-- ALWAYS explainable — when a seller looks misclassified, you can see
-- which signal dominated.
CREATE VIEW seller_classifications AS
WITH components AS (
    SELECT
        s.id,
        s.olx_user_id,
        s.display_name,
        s.is_business,
        s.registered_at,
        COALESCE(ss.listings_active, 0)              AS listings_active,
        COALESCE(ss.districts_count, 0)              AS districts_count,
        COALESCE(ss.cities_count, 0)                 AS cities_count,

        -- Component 1: "personhood". OLX's is_business flag is the
        -- strongest single signal — Pro accounts pay for that label and
        -- almost always belong to agencies / developers / shops.
        (CASE WHEN s.is_business THEN -0.5 ELSE 0.3 END)::numeric
            AS score_personhood,

        -- Component 2: listing count. A normal private owner has 1-2
        -- active listings (selling their apartment, renting a room).
        -- More than 5 = reseller / agency; we hard-zero past 10.
        (CASE
            WHEN COALESCE(ss.listings_active, 0) = 0 THEN 0
            WHEN ss.listings_active <= 2  THEN 0.3
            WHEN ss.listings_active <= 5  THEN 0.15
            WHEN ss.listings_active <= 10 THEN 0.05
            ELSE 0
        END)::numeric AS score_listings_count,

        -- Component 3: geography. A private owner usually sells in one
        -- district they live in. Listings across 3+ districts at once
        -- means "I have access to many properties" — almost always an
        -- agency signal.
        (CASE
            WHEN COALESCE(ss.districts_count, 0) <= 1 THEN 0.2
            WHEN ss.districts_count = 2 THEN 0.1
            ELSE 0
        END)::numeric AS score_geography,

        -- Component 4: account age. Fresh accounts (< 6 months) get no
        -- bonus because agencies churn through them. Older accounts
        -- accumulate trust — a 2-year-old account is unlikely to be a
        -- throwaway agency clone.
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
    -- Sum the components, clamp to [0, 1]. The clamp matters because
    -- score_personhood is negative for businesses — without GREATEST the
    -- score could go below zero, which would be technically fine but
    -- harder to reason about (we'd want monotonicity in display).
    LEAST(1.0, GREATEST(0.0,
        score_personhood + score_listings_count + score_geography + score_account_age
    ))::numeric AS real_seller_score
FROM components;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS seller_classifications;
-- +goose StatementEnd