.PHONY: up down logs ps psql rabbit-ui migrate migrate-down migrate-status

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