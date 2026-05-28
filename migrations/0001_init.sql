-- +goose Up
-- +goose StatementBegin

-- pg_trgm: fuzzy search on titles/addresses (useful for "find similar listings")
CREATE EXTENSION IF NOT EXISTS pg_trgm;
-- citext: case-insensitive text for things like phone hashes / external ids
CREATE EXTENSION IF NOT EXISTS citext;

-- Auto-bump updated_at on UPDATE.
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = NOW();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- =========================================================================
-- sellers: an OLX user account. The thing we ultimately classify
-- as "real person" vs "agency / reseller".
-- =========================================================================
CREATE TABLE sellers (
    id                BIGSERIAL PRIMARY KEY,
    olx_user_id       TEXT        NOT NULL UNIQUE,  -- stable id from OLX
    display_name      TEXT,
    profile_url       TEXT,
    registered_at     TIMESTAMPTZ,                  -- if OLX shows "on site since"
    is_business       BOOLEAN     NOT NULL DEFAULT FALSE,  -- OLX "pro" / business flag
    phone_hash        TEXT,                         -- sha256 of normalized phone
    avatar_url        TEXT,
    location          TEXT,                         -- whatever OLX prints on profile
    raw               JSONB       NOT NULL DEFAULT '{}'::jsonb,

    -- lifecycle timestamps
    first_seen_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_scraped_at   TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX sellers_phone_hash_idx     ON sellers (phone_hash) WHERE phone_hash IS NOT NULL;
CREATE INDEX sellers_last_scraped_idx   ON sellers (last_scraped_at NULLS FIRST);
CREATE INDEX sellers_is_business_idx    ON sellers (is_business);

CREATE TRIGGER sellers_set_updated_at
BEFORE UPDATE ON sellers
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- =========================================================================
-- listings: a single ad. Most recent state lives here; history goes in
-- listing_snapshots so we can see price drops, status flips, etc.
-- =========================================================================
CREATE TABLE listings (
    id                  BIGSERIAL PRIMARY KEY,
    olx_listing_id      TEXT        NOT NULL UNIQUE,
    seller_id           BIGINT      REFERENCES sellers(id) ON DELETE SET NULL,

    url                 TEXT        NOT NULL,
    title               TEXT,
    description         TEXT,

    -- price
    price               NUMERIC(14,2),
    currency            TEXT,                       -- 'UAH' / 'USD' / 'EUR'

    -- classification
    deal_type           TEXT,                       -- 'sale' / 'rent_long' / 'rent_daily'
    property_type       TEXT,                       -- 'apartment' / 'house' / 'commercial' / ...

    -- attributes worth promoting out of JSONB for indexing
    rooms               SMALLINT,
    area_total          NUMERIC(8,2),               -- m²
    area_living         NUMERIC(8,2),
    floor               SMALLINT,
    floors_total        SMALLINT,

    -- location
    city                TEXT,
    district            TEXT,
    address             TEXT,
    lat                 DOUBLE PRECISION,
    lon                 DOUBLE PRECISION,

    -- remote timestamps as reported by OLX
    posted_at           TIMESTAMPTZ,
    updated_at_remote   TIMESTAMPTZ,

    status              TEXT        NOT NULL DEFAULT 'active',  -- 'active' / 'archived' / 'removed'

    attributes          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    raw                 JSONB       NOT NULL DEFAULT '{}'::jsonb,

    -- lifecycle timestamps
    first_seen_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_scraped_at     TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX listings_seller_idx         ON listings (seller_id);
CREATE INDEX listings_posted_at_idx      ON listings (posted_at DESC NULLS LAST);
CREATE INDEX listings_city_district_idx  ON listings (city, district);
CREATE INDEX listings_active_idx         ON listings (status) WHERE status = 'active';
CREATE INDEX listings_title_trgm_idx     ON listings USING gin (title gin_trgm_ops);
CREATE INDEX listings_attributes_gin_idx ON listings USING gin (attributes jsonb_path_ops);

CREATE TRIGGER listings_set_updated_at
BEFORE UPDATE ON listings
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- =========================================================================
-- listing_snapshots: append-only history. One row per observed change
-- (or per re-scrape, depending on how strict you want to be).
-- =========================================================================
CREATE TABLE listing_snapshots (
    id            BIGSERIAL PRIMARY KEY,
    listing_id    BIGINT      NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    price         NUMERIC(14,2),
    currency      TEXT,
    status        TEXT,
    title         TEXT,
    raw_hash      TEXT        NOT NULL,             -- hash of normalized payload, used to dedupe
    captured_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (listing_id, raw_hash)                   -- don't store identical re-scrapes
);
CREATE INDEX listing_snapshots_listing_time_idx
    ON listing_snapshots (listing_id, captured_at DESC);

-- =========================================================================
-- listing_photos: separate so we can perceptual-hash and find listings
-- that reuse the same photos (strong signal for "this is an agency").
-- =========================================================================
CREATE TABLE listing_photos (
    id            BIGSERIAL PRIMARY KEY,
    listing_id    BIGINT      NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    url           TEXT        NOT NULL,
    position      SMALLINT    NOT NULL DEFAULT 0,
    phash         TEXT,                             -- perceptual hash, NULL until computed
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (listing_id, url)
);
CREATE INDEX listing_photos_phash_idx ON listing_photos (phash) WHERE phash IS NOT NULL;

-- =========================================================================
-- scrape_jobs: operational log. Optional, but pays for itself the first
-- time you ask "why didn't we re-fetch this listing?".
-- =========================================================================
CREATE TABLE scrape_jobs (
    id           BIGSERIAL PRIMARY KEY,
    kind         TEXT        NOT NULL,              -- 'listing' / 'seller' / 'search'
    target       TEXT        NOT NULL,              -- url or external id
    status       TEXT        NOT NULL DEFAULT 'queued', -- 'queued'/'running'/'done'/'failed'
    attempt      INT         NOT NULL DEFAULT 0,
    error        TEXT,
    started_at   TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX scrape_jobs_status_idx ON scrape_jobs (status, kind);
CREATE INDEX scrape_jobs_target_idx ON scrape_jobs (target);

-- =========================================================================
-- seller_stats: materialized view of "how many active listings per seller,
-- in how many districts, since when". Refresh periodically; this is your
-- main "is this a real person?" classifier input.
-- =========================================================================
CREATE MATERIALIZED VIEW seller_stats AS
SELECT
    s.id                                     AS seller_id,
    s.olx_user_id,
    s.is_business,
    COUNT(l.id)                              AS listings_total,
    COUNT(l.id) FILTER (WHERE l.status = 'active') AS listings_active,
    COUNT(DISTINCT l.district)               AS districts_count,
    COUNT(DISTINCT l.city)                   AS cities_count,
    MIN(l.posted_at)                         AS first_listing_at,
    MAX(l.posted_at)                         AS last_listing_at
FROM sellers s
LEFT JOIN listings l ON l.seller_id = s.id
GROUP BY s.id;

CREATE UNIQUE INDEX seller_stats_seller_id_idx ON seller_stats (seller_id);
CREATE INDEX seller_stats_active_idx           ON seller_stats (listings_active DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP MATERIALIZED VIEW IF EXISTS seller_stats;
DROP TABLE IF EXISTS scrape_jobs;
DROP TABLE IF EXISTS listing_photos;
DROP TABLE IF EXISTS listing_snapshots;
DROP TABLE IF EXISTS listings;
DROP TABLE IF EXISTS sellers;
DROP FUNCTION IF EXISTS set_updated_at();
-- +goose StatementEnd