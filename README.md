# OLX Real Estate Robot

A Go service that scrapes real-estate listings from OLX and identifies which
sellers are real private owners — as opposed to agencies, resellers and
spam accounts.

> **Status:** work in progress. Built as a portfolio project to explore Go,
> message queues, and applied data work on a real, messy dataset.

## Why

OLX is full of "private seller" listings that, once you look at the user
profile behind them, turn out to be agency accounts: 30+ active ads across
8 districts, the same set of photos reused across listings, suspiciously
fresh registration dates, and so on.

The goal here is to **automate that look-behind**: fetch a listing, walk
back to the seller's profile, fetch all their other listings, compute
signals, and store everything in a queryable shape so the question
"is this a real private seller?" can be answered with data instead of gut feel.

## Tech stack

- **Go 1.22+** — single repo, one binary per worker in `cmd/`
- **PostgreSQL 16** — primary store (relational core + `JSONB` for raw payloads)
- **RabbitMQ 3.13** — work queues between scraper stages
- **pgx/v5** — Postgres driver
- **amqp091-go** — RabbitMQ client
- **goose** — SQL migrations

## Architecture

```
┌───────────┐  listing_urls   ┌─────────┐   raw_html   ┌────────┐
│ discovery ├────────────────►│ fetcher ├─────────────►│ parser ├──► Postgres
└───────────┘                 └─────────┘              └────┬───┘
                                   ▲                       │ new_sellers
                                   │                       ▼
                                   │                ┌────────────┐
                                   └────────────────┤ enrich     │
                                    listing_urls    │ seller     │
                                                    └────────────┘
```

- Each stage is an independent worker process consuming from one queue and
  producing to the next.
- Failed messages flow into a DLX (dead-letter exchange) with TTL-based
  retry — no `time.Sleep` in worker code.
- Rate-limiting against OLX is done via consumer `prefetch`, not by sleeping.
- Workers are idempotent: a redelivered message must not corrupt the database.

## Data model

- `sellers` — the OLX user account. The thing we ultimately classify.
- `listings` — current state of an ad.
- `listing_snapshots` — append-only history of price / status changes,
  deduped by content hash.
- `listing_photos` — photos with perceptual hashes for cross-listing
  duplicate detection (a strong "this is an agency" signal).
- `seller_stats` — materialized view aggregating per-seller signals
  (active listings, district spread, first-seen date, etc.).

Full schema lives in [`migrations/0001_init.sql`](migrations/0001_init.sql).
Schema changes are applied as new migrations, never by editing old ones.

## Run locally

```bash
cp .env.example .env
make up                                                # postgres + rabbitmq
go install github.com/pressly/goose/v3/cmd/goose@latest
make migrate                                           # apply schema
make psql                                              # SQL shell
# RabbitMQ management UI: http://localhost:15672  (olx / olx)
```

## Roadmap

- [x] Database schema + local infra (`docker-compose`)
- [ ] Discovery worker — crawls OLX search pages, emits listing URLs
- [ ] Fetcher worker — HTTP with retry/DLX, respects rate limits
- [ ] HTML parser — `colly` first, `chromedp` if OLX requires JS
- [ ] Seller enrichment worker
- [ ] Classification: heuristic-based "real seller" score
- [ ] CLI for ad-hoc queries against the dataset

## Notes

- This is an exploratory / portfolio project. It does not attempt to defeat
  captchas or seriously evade rate limits.
- Phone numbers are stored as `sha256` hashes of the normalized form,
  never in plain text.