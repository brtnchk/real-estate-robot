# OLX Real Estate Robot

Сервис на Go, который парсит объявления недвижимости с OLX и отделяет
**реальных продавцов** (частные лица) от перекупов, агентств и спам-аккаунтов.

## Goal

Главный артефакт — таблица `sellers` с признаками «реальности», вычисленными
по истории объявлений пользователя:

- количество активных объявлений (`listings_active`)
- разброс по районам и городам (`districts_count`, `cities_count`)
- переиспользование фото между объявлениями (через `phash`)
- давность аккаунта и поведение во времени

UI / публичного API пока не нужно — нужны качественные данные в БД и понятная
методика классификации.

## Tech stack

- **Go 1.22+** — сервис целиком на Go, одно репо, несколько бинарей в `cmd/`.
- **PostgreSQL 16** — основное хранилище. Реляционка + `JSONB` для сырого OLX.
- **RabbitMQ 3.13** — очереди между стадиями (discover → fetch → parse → enrich).
- **pgx/v5** — драйвер PG (нативный, поддерживает `COPY`).
- **amqp091-go** — клиент RabbitMQ (поддерживаемый форк `streadway/amqp`).
- **goose** — миграции БД, формат SQL с аннотациями `-- +goose Up/Down`.
- **HTML-парсер: goquery + `window.__PRERENDERED_STATE__`**. OLX — Next.js,
  всё структурированные данные лежат в JSON-блобе на странице. Это в разы
  стабильнее CSS-селекторов. Парсер падает обратно на `<title>` если блоба
  нет (для тестов / случайных не-OLX страниц).

## Layout

```
.
├── cmd/                  бинари (scraper, enricher, ...)
├── internal/             код, не предназначенный для импорта снаружи
├── migrations/           SQL-миграции goose, NNNN_name.sql
├── docker-compose.yml    postgres + rabbitmq для локалки
├── .env.example          креды и URL для приложения
├── Makefile              up / psql / migrate / ...
└── .claude/              настройки Claude Code для этого проекта
```

## Data model (high level)

- `sellers` — продавец OLX, главная сущность для классификации
- `listings` — текущее состояние объявления
- `listing_snapshots` — append-only история (цена, статус) с дедупом по `raw_hash`
- `listing_photos` — фото + perceptual hash (`phash`) для поиска переиспользований
- `scrape_jobs` — операционный лог
- `seller_stats` — materialized view с агрегатами для классификации;
  обновляется `REFRESH MATERIALIZED VIEW CONCURRENTLY`

Полная схема — в `migrations/0001_init.sql`. Менять её **только новыми
миграциями** (`0002_*.sql`, …), не редактировать уже накатанные.

## Queue topology (план)

```
discovery → listing_urls → fetcher → raw_html → parser → Postgres
                                                       ↓
                                            new_sellers → enrich → listing_urls
```

- Для упавших задач — DLX (dead-letter exchange) + retry через TTL.
- Rate-limit к OLX — через `prefetch` у consumer'а, не через `sleep`.
- Каждый воркер должен быть **идемпотентен** относительно сообщения
  (повторная доставка не должна ломать БД).

## Conventions

- Timestamp-поля в БД — `TIMESTAMPTZ`, всегда `NOW()` в UTC.
- Телефоны **не хранятся в открытом виде** — только `sha256` от нормализованного
  номера в `sellers.phone_hash`.
- У каждого объявления и продавца есть `raw JSONB` со снапшотом ответа OLX.
  Сначала кладём «как есть», потом нормализуем — это страхует парсер от
  изменений схемы на стороне OLX.
- Апсёрты — через `ON CONFLICT (olx_*_id) DO UPDATE`, не через `SELECT → INSERT/UPDATE`.
- Имена очередей RabbitMQ — `kebab-case`, существительные (`listing-urls`,
  `raw-html`), не глаголы.
- Логи — структурные (`slog`), не `fmt.Println`.

## Local dev

```bash
cp .env.example .env
make up                # поднять postgres + rabbitmq
make migrate           # накатить схему (требует goose в PATH)
make psql              # консоль БД
# RabbitMQ UI: http://localhost:15672  (olx / olx)
```

## Out of scope (пока)

- Распознавание captcha и серьёзный обход rate-limit OLX.
- Frontend / публичный API.
- Деплой в прод — всё локально через docker-compose.
- Юридическая сторона скрейпа OLX (ToS, robots.txt) — это отдельный разговор.
