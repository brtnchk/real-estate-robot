-- +goose Up
-- +goose StatementBegin

-- listing_html_fetches stores the raw HTML body for every successful fetch
-- (and the status code for 4xx fetches we want to remember as "dead URL").
-- Keyed by URL + fetched_at, not by listings.id — at fetch time we have
-- not parsed the listing yet, so we don't know which listings row it
-- belongs to. The parser stage will look up the latest fetch by URL.
CREATE TABLE listing_html_fetches (
    id          BIGSERIAL PRIMARY KEY,
    url         TEXT        NOT NULL,
    status_code SMALLINT    NOT NULL,
    html        BYTEA,                                  -- NULL for 4xx (we keep the row, not the body)
    headers     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    error       TEXT,                                   -- for documentation only; transient errors aren't stored
    fetched_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Parser will query "latest fetch for this URL" — index supports it.
CREATE INDEX listing_html_fetches_url_recent_idx
    ON listing_html_fetches (url, fetched_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS listing_html_fetches;
-- +goose StatementEnd