# OLX Real Estate Robot

[![CI](https://github.com/brtnchk/real-estate-robot/actions/workflows/ci.yml/badge.svg)](https://github.com/brtnchk/real-estate-robot/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A Go service that crawls real-estate listings from OLX and figures out
which sellers are **real private owners** as opposed to agencies, resellers,
and spam accounts.

> **Status:** work in progress. Built as a portfolio project to explore
> Go, message queues, and applied data work on a real, messy dataset.

## Why

OLX is full of listings tagged "private seller" that, once you look at
the user profile behind them, turn out to be agency accounts: 30+ active
ads across 8 districts, the same photos reused across listings,
suspiciously fresh registration dates.

The goal is to **automate that look-behind**: discover listings via
search pages, fetch each one, walk back to the seller's profile, pull
all their other listings, compute per-seller signals, and store
everything in a queryable shape so the question "is this a real private
seller?" can be answered with data instead of gut feel.

## Architecture

Four worker processes, each consuming from one RabbitMQ queue and
producing to the next:

```
   publish search task        ← bootstrap (one-off)
          │
          ▼
   ┌──────────────────┐
   │ listings.discover│ ◄────────────────────────┐
   └────────┬─────────┘    self-paginate         │
            │              (page+1)              │
       ┌────▼─────┐ ──────────────────────────── ┘
       │discovery │
       └────┬─────┘
            │ fan-out N listing URLs
            ▼
   ┌──────────────────┐
   │  listings.fetch  │ ◄──── fan-out from enricher (cycle gated)
   └────────┬─────────┘
            │
       ┌────▼─────┐
       │ fetcher  │  HTTP GET → store HTML → next-stage publish
       └────┬─────┘
            │ {fetch_id, url}
            ▼
   ┌──────────────────┐
   │  listings.parse  │
   └────────┬─────────┘
            │
       ┌────▼─────┐
       │  parser  │  transaction:
       └────┬─────┘     UpsertSeller + UpsertListing + InsertSnapshot
            │ {olx_user_id}
            ▼
   ┌──────────────────┐
   │  sellers.enrich  │
   └────────┬─────────┘
            │
       ┌────▼─────┐
       │ enricher │  freshness gate prevents the
       └────┬─────┘  parser → enrich → fetch → parser loop
            │
            └─── fan-out N URLs back to listings.fetch
```

Every stage has a three-queue triple — `{stage}` (work), `{stage}.retry`
(TTL parking), `{stage}.dead` (terminal). Failed messages dead-letter
into the retry queue, sit there for 60 s, then come back to the work
queue. Workers count the `x-death` header and promote to the dead queue
after a configurable retry budget.

## Design highlights

- **Explainable classifier** — every seller score is the sum of four
  named components (personhood / listings_count / geography / account_age),
  exposed as separate columns of the `seller_classifications` view. When
  a result looks wrong you can tell which signal dominated; no black box.
- **Parser reads OLX's `__PRERENDERED_STATE__` JSON** instead of scraping
  the DOM. OLX is Next.js-style and ships every listing's full state as
  an inline JSON blob — orders of magnitude more stable than CSS
  selectors against rendered HTML. Falls back to `<title>` extraction
  when the blob is absent (synthetic test HTML, non-OLX pages).
- **Three-exchange topology** (`olx.work` / `olx.retry` / `olx.dead`)
  with per-stage retry budgets driven by the `x-death` header. Failed
  HTTP fetches loop through the retry queue with a TTL hop; permanent
  4xx errors get promoted to dead by worker code.
- **Idempotent upserts everywhere** — every primary entity has
  `ON CONFLICT (external_id) DO UPDATE`. Combined with at-least-once
  delivery, redelivered messages can't corrupt state.
- **Transactional multi-table writes** — the parser writes seller +
  listing + snapshot in a single `pgx.BeginFunc`, so partial state
  never escapes.
- **Cycle prevention via `last_enriched_at`** — without the freshness
  gate, parser → enrich → fetch → parser would loop forever for every
  listing it sees. The gate also makes "30 enrichment tasks for the
  same seller in a minute" cheap (29 are acked-and-skipped).
- **Self-paginating discovery** — one publish to `listings.discover`
  starts the entire crawl; the worker enqueues `page+1` back into its
  own queue until a page returns zero listings or `MaxPage` is hit.
- **sqlc-generated queries** — typed Go functions from hand-written
  SQL. No ORM, no runtime query builder, query plans are obvious.
- **Publisher confirms + `basic.return` listener** — every Publish
  blocks until the broker acks, and unrouteable messages (no binding
  match) are logged instead of silently dropped.
- **`httptest.NewServer` everywhere** — HTTP-dependent code is unit-
  tested against an in-process server, no network, no mocks.

## Tech stack

Backend:
- **Go 1.22+** (developed on 1.26.1), one binary per worker in `cmd/`
- **PostgreSQL 16** — relational core + `JSONB` for raw OLX payloads
- **RabbitMQ 3.13** — work queues with management plugin
- **pgx/v5** — Postgres driver (native, supports COPY)
- **amqp091-go** — RabbitMQ client (the maintained fork of streadway/amqp)
- **sqlc** — SQL → typed Go (run as `go tool sqlc`, no PATH dance)
- **goose** — SQL migrations
- **goquery** — jQuery-like HTML traversal
- **golang.org/x/time/rate** — token-bucket rate limiting

Frontend:
- **React 18** + **TypeScript** + **Vite 6**
- HTTP/JSON API in `cmd/api/` (`net/http` stdlib only, Go 1.22 method routing)
- Same-origin during dev via Vite proxy; permissive CORS on the API as a backup

## Project structure

```
.
├── cmd/                       one binary per command
│   ├── discovery/             worker: search-results crawler
│   ├── fetcher/               worker: HTTP → listing_html_fetches
│   ├── parser/                worker: HTML → DB (transactional)
│   ├── enricher/              worker: seller profile → fan-out URLs
│   ├── scheduler/             ticks every --interval, kicks discovery
│   ├── classify/              CLI: print ranked sellers
│   ├── api/                   HTTP/JSON API for the React frontend
│   ├── topology/              one-off: declare exchanges + queues
│   ├── publish/               sandbox: hand-publish a message
│   ├── consume/               sandbox: hand-consume a queue
│   └── db-demo/               sandbox: upsert idempotency smoke test
├── internal/
│   ├── queue/                 topology + Publisher (confirms, returns)
│   │                          + Consumer (prefetch, manual ack, retry/dead)
│   ├── api/                   HTTP handlers, JSON marshalling, CORS
│   ├── db/
│   │   ├── queries/           hand-written SQL (input to sqlc)
│   │   └── sqlc/              generated typed Go (committed)
│   ├── httpc/                 shared http.Client builder
│   ├── discovery/             discovery worker package
│   ├── fetcher/               fetcher worker package
│   ├── parser/                parser package + worker
│   └── enrich/                seller-enrich worker package
├── frontend/                  React + TypeScript + Vite (src/App.tsx)
├── migrations/                goose-format SQL migrations
├── docker-compose.yml         Postgres + RabbitMQ
├── sqlc.yaml                  sqlc config + JSONB overrides
└── Makefile                   ~30 targets
```

## Run locally

```bash
cp .env.example .env

# 1. Infrastructure
make up                       # Postgres 16 + RabbitMQ 3.13 in Docker

# 2. Schema
go install github.com/pressly/goose/v3/cmd/goose@latest
make migrate

# 3. RabbitMQ topology (idempotent — safe to re-run)
make topology

# 4. Sanity-check the DB layer
make db-demo                  # runs UpsertSeller twice, proves idempotency
```

### Run a worker

Each worker is its own binary, intentional — you can run any subset in
isolation while developing.

```bash
make fetcher                  # consumes listings.fetch
make parser                   # consumes listings.parse
make enricher                 # consumes sellers.enrich
make discovery                # consumes listings.discover
```

### Drive the pipeline by hand

```bash
# Bootstrap discovery from a search URL
make publish q=listings.discover \
    m='{"search_url":"https://www.olx.ua/uk/nedvizhimost/prodazha/kiev/","page":1}'

# Or skip discovery and inject a listing URL directly
make publish q=listings.fetch \
    m='{"url":"https://www.olx.ua/d/some-listing/"}'

# Watch what is happening:
open http://localhost:15672   # RabbitMQ management UI (olx / olx)
make psql                     # SQL shell
```

### Re-generate typed SQL after editing `internal/db/queries/*.sql`

```bash
make sqlc                     # = go tool sqlc generate
```

### Run the frontend (React + Vite + Go API)

```bash
# In one terminal: the HTTP/JSON API
make api                      # http://localhost:8080/api/...

# In another terminal: the React dev server
make frontend                 # http://localhost:5173
```

Open `http://localhost:5173` — interactive UI with filter dropdowns
(posted within N days, min score slider, limit), score-colored rows,
and clickable titles that open on OLX.

## Tests

```bash
make test                     # all packages
make test-v                   # verbose
make test-cover               # coverage summary + path to HTML report
```

Pure helpers (`xDeathCount`, `snapshotHash`, `isListingURL`, search-page
URL building, HTML title extraction) are at 100 % coverage. Worker
`Handle` methods are not yet covered — they require a live Postgres /
RabbitMQ; integration tests via testcontainers-go are on the roadmap.

## Data model

- `sellers` — the OLX user account, the thing we ultimately classify
- `listings` — current state of one ad
- `listing_snapshots` — append-only history of (price, status, title)
  changes, deduped by content hash so re-parsing an unchanged listing
  is a no-op
- `listing_html_fetches` — raw HTML keyed by `url` + `fetched_at`; the
  parser stage looks up the latest fetch for its URL
- `listing_photos` — photos with perceptual hashes for cross-listing
  duplicate detection (a strong "this is an agency" signal)
- `seller_stats` — materialized view aggregating per-seller signals
  (active listings, district spread, first-seen date, etc.)

Full schema lives in [`migrations/`](migrations/). Schema changes are
applied as new migrations, never by editing old ones.

## Roadmap

- [x] Database schema + local infra (`docker-compose`)
- [x] RabbitMQ topology — work / retry / dead per stage
- [x] Publisher with confirms + return-listener
- [x] Consumer with prefetch, manual ack, retry budget
- [x] Postgres layer via sqlc-generated typed queries
- [x] Fetcher worker — HTTP, rate limit, fail classification
- [x] Parser worker — transactional upsert across seller + listing + snapshot
- [x] Seller-enrich worker — fan-out + `last_enriched_at` cycle gate
- [x] Discovery worker — self-paginating search-page crawler
- [x] Unit tests for pure helpers
- [x] Real OLX HTML parser via `__PRERENDERED_STATE__` JSON extraction
- [x] Classification: heuristic "real seller" score (`seller_classifications`
      view) + `cmd/classify` CLI with per-component breakdown
- [x] HTTP/JSON API + React frontend (filterable table of ranked listings,
      clickable URLs, live updating)
- [x] Scheduler with fetcher dedup for continuous (re-)discovery
- [ ] Relative-URL resolution in discovery (OLX search pages use `/d/...`,
      not absolute URLs)
- [ ] testcontainers-go integration tests for worker `Handle` methods
- [ ] HTTP/JSON API on top of the classifier

## Notes

- Phone numbers are stored as `sha256` hashes of the normalized form,
  never in plain text.
- This is an exploratory / portfolio project. It does not attempt to
  defeat captchas or seriously evade rate limits — the rate limiter is
  there to be polite, not stealthy.
- The parser still falls back to a `<title>`-only stub when the input
  is not a real OLX page (synthetic test HTML, accidental non-OLX URL).
  Synthetic IDs are derived from the URL so downstream upserts get a
  stable key. The real OLX path extracts everything (price, currency,
  city, district, lat/lon, seller UUID, registration date, business
  flag) from the `__PRERENDERED_STATE__` JSON blob.