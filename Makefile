.PHONY: up down logs ps psql rabbit-ui migrate migrate-down migrate-status topology build publish consume sqlc db-demo fetcher parser enricher discovery scheduler classify refresh-stats api frontend test test-v test-cover

# --- infra ------------------------------------------------------------------

up:
	docker compose up -d

down:
	docker compose down

logs:
	docker compose logs -f

ps:
	docker compose ps

# --- shells -----------------------------------------------------------------

psql:
	docker compose exec postgres psql -U $${POSTGRES_USER:-olx} -d $${POSTGRES_DB:-olx}

rabbit-ui:
	@echo "Open http://localhost:15672  (user: olx / pass: olx)"

# --- migrations (requires `go install github.com/pressly/goose/v3/cmd/goose@latest`) ---

DB_URL ?= postgres://olx:olx@localhost:5432/olx?sslmode=disable

migrate:
	goose -dir migrations postgres "$(DB_URL)" up

migrate-down:
	goose -dir migrations postgres "$(DB_URL)" down

migrate-status:
	goose -dir migrations postgres "$(DB_URL)" status

# --- app --------------------------------------------------------------------

AMQP_URL ?= amqp://olx:olx@localhost:5672/

build:
	go build ./...

# Declare RabbitMQ exchanges, queues, and bindings. Idempotent.
topology:
	AMQP_URL="$(AMQP_URL)" go run ./cmd/topology

# Publish one message into a work queue.
#   make publish q=listings.fetch m='{"url":"https://www.olx.ua/foo"}'
publish:
	@test -n "$(q)" || (echo "usage: make publish q=<queue> m='<body>'" && exit 1)
	@test -n "$(m)" || (echo "usage: make publish q=<queue> m='<body>'" && exit 1)
	AMQP_URL="$(AMQP_URL)" go run ./cmd/publish "$(q)" "$(m)"

# Run a sandbox consumer on a queue. Pass `extra='--fail'` to exercise retry/dead.
#   make consume q=listings.fetch
#   make consume q=listings.fetch extra='--fail --max-retries 2'
consume:
	@test -n "$(q)" || (echo "usage: make consume q=<queue> [extra='--fail ...']" && exit 1)
	AMQP_URL="$(AMQP_URL)" go run ./cmd/consume $(extra) "$(q)"

# Re-generate typed Go from internal/db/queries/*.sql. Idempotent.
sqlc:
	go tool sqlc generate

# Run the upsert idempotency demo against a live Postgres.
db-demo:
	DATABASE_URL="$(DB_URL)" go run ./cmd/db-demo

# Run the fetcher worker. Ctrl+C to stop.
fetcher:
	DATABASE_URL="$(DB_URL)" AMQP_URL="$(AMQP_URL)" go run ./cmd/fetcher

# Run the parser worker. Ctrl+C to stop.
parser:
	DATABASE_URL="$(DB_URL)" AMQP_URL="$(AMQP_URL)" go run ./cmd/parser

# Run the seller-enrich worker. Ctrl+C to stop.
enricher:
	DATABASE_URL="$(DB_URL)" AMQP_URL="$(AMQP_URL)" go run ./cmd/enricher

# Run the discovery worker. Ctrl+C to stop. Bootstrap via the scheduler
# or by hand: make publish q=listings.discover m='{"search_url":"...","page":1}'
#
# --max-page 5 reaches past the promoted block on page 1 (which OLX
# packs with Київ-city ads) into the long tail of oblast-region cities.
discovery:
	AMQP_URL="$(AMQP_URL)" go run ./cmd/discovery --max-page 5 $(args)

# Run the scheduler. Re-publishes discovery tasks every --interval so the
# pipeline picks up new OLX listings without manual prodding.
#   make scheduler args='--interval 10m --searches https://www.olx.ua/uk/...'
scheduler:
	AMQP_URL="$(AMQP_URL)" go run ./cmd/scheduler $(args)

# Rank sellers by real_seller_score. Pass --refresh after adding listings.
#   make classify
#   make classify args='--refresh --limit 10 --min-listings 2'
classify:
	DATABASE_URL="$(DB_URL)" go run ./cmd/classify $(args)

# Refresh the seller_stats materialized view. Run after a fresh batch of
# parsed listings so the API / classifier see new data.
refresh-stats:
	docker compose exec -T postgres psql -U olx -d olx \
	    -c "REFRESH MATERIALIZED VIEW CONCURRENTLY seller_stats;"

# Run the HTTP/JSON API the frontend talks to.
api:
	DATABASE_URL="$(DB_URL)" go run ./cmd/api

# Run the React frontend dev server. Proxies /api to localhost:8080.
#   Opens http://localhost:5173
frontend:
	cd frontend && npm run dev

# --- tests ------------------------------------------------------------------

test:
	go test ./...

test-v:
	go test -v ./...

# Coverage report. Open the HTML in your browser to drill into uncovered lines.
test-cover:
	go test -coverprofile=/tmp/coverage.out ./...
	go tool cover -func=/tmp/coverage.out | tail -5
	@echo "open /tmp/coverage.out  →  go tool cover -html=/tmp/coverage.out"