-- name: InsertFetch :one
-- Append-only: every fetch attempt that lands here gets a new row.
-- The fetcher writes successes (status 2xx, body present) and permanent
-- failures (4xx, body NULL). Transient failures are NOT stored here —
-- they live in retry queues + application logs.
INSERT INTO listing_html_fetches (
    url, status_code, html, headers, error
) VALUES (
    $1, $2, $3, $4, $5
)
RETURNING *;

-- name: GetLatestFetchByURL :one
SELECT * FROM listing_html_fetches
WHERE url = $1
ORDER BY fetched_at DESC
LIMIT 1;

-- name: GetFetchByID :one
SELECT * FROM listing_html_fetches WHERE id = $1;