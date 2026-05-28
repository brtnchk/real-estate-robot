-- +goose Up
-- +goose StatementBegin

-- last_enriched_at is the cycle-prevention timestamp for the seller-enrich
-- worker. The worker reads it before doing anything: if it's recent
-- (within w.Freshness), the task is acked without touching the network.
-- Without this, parser → enrich → fetch → parser → enrich would loop
-- indefinitely on every URL we ever see.
ALTER TABLE sellers ADD COLUMN last_enriched_at TIMESTAMPTZ;

-- Index supports the future "what to enrich next" query
--   SELECT olx_user_id FROM sellers
--    WHERE last_enriched_at IS NULL OR last_enriched_at < NOW() - interval '7 days'
-- where we'd kick off enrichment for stale / never-enriched sellers.
CREATE INDEX sellers_last_enriched_at_idx
    ON sellers (last_enriched_at NULLS FIRST);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS sellers_last_enriched_at_idx;
ALTER TABLE sellers DROP COLUMN IF EXISTS last_enriched_at;
-- +goose StatementEnd