.PHONY: up down logs ps psql rabbit-ui migrate migrate-down migrate-status topology build publish consume sqlc db-demo fetcher parser

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