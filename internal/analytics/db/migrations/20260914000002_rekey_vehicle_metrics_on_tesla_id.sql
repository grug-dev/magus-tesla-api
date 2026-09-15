-- +goose Up
-- Collapse any (tesla_id, metric_date) duplicates before the new UNIQUE
-- constraint can be added. Keeps the row with the latest updated_at, ties
-- broken by id -- the write path's own UPSERT already treats "most recently
-- written" as "most correct" for every column but created_at; this applies
-- that same rule one column narrower. Measured on the dev database
-- (2026-09-14): 0 duplicate groups exist today. This step exists so the
-- migration is still safe against a database migrated before this Go change
-- deploys.
DELETE FROM analytics.vehicle_metrics a
USING analytics.vehicle_metrics b
WHERE a.tesla_id = b.tesla_id
  AND a.metric_date = b.metric_date
  AND (a.updated_at < b.updated_at
       OR (a.updated_at = b.updated_at AND a.id < b.id));

ALTER TABLE analytics.vehicle_metrics
    DROP CONSTRAINT vehicle_metrics_account_tesla_date_unique;

-- Schema-qualified: an index name is resolved through search_path, and goose
-- does not guarantee analytics is on it.
DROP INDEX analytics.idx_vehicle_metrics_latest;

ALTER TABLE analytics.vehicle_metrics
    DROP COLUMN account_id;

ALTER TABLE analytics.vehicle_metrics
    ADD CONSTRAINT vehicle_metrics_tesla_date_unique UNIQUE (tesla_id, metric_date);

-- Recreated under the SAME name, one column narrower: the
-- query it serves orders tesla_id ASC, metric_date DESC -- a mixed direction
-- the new all-ascending UNIQUE index cannot produce in one scan. Dropping
-- this index without recreating it would reintroduce the incremental sort it
-- exists to avoid.
CREATE INDEX idx_vehicle_metrics_latest
    ON analytics.vehicle_metrics (tesla_id, metric_date DESC);

-- Same collapse rule as above, one table over: source_updated_at is a
-- cursor Reconcile only ever advances forward, so the most recently
-- advanced (updated_at) row is also the correct one to keep.
DELETE FROM analytics.vehicle_metric_watermarks a
USING analytics.vehicle_metric_watermarks b
WHERE a.tesla_id = b.tesla_id
  AND a.source = b.source
  AND (a.updated_at < b.updated_at
       OR (a.updated_at = b.updated_at AND a.id < b.id));

ALTER TABLE analytics.vehicle_metric_watermarks
    DROP CONSTRAINT vehicle_metric_watermarks_account_tesla_source_unique;

ALTER TABLE analytics.vehicle_metric_watermarks
    DROP COLUMN account_id;

ALTER TABLE analytics.vehicle_metric_watermarks
    ADD CONSTRAINT vehicle_metric_watermarks_tesla_source_unique UNIQUE (tesla_id, source);

-- +goose Down
-- NOT a full rollback of history: rows deleted by the Up migration's collapse
-- steps are not recoverable, and every account_id value dropped by DROP
-- COLUMN is gone. Down only reverses this migration's own schema changes,
-- restoring shape, not data -- the same limitation every DROP COLUMN in this
-- codebase's migrations has on Down (mirrors the telemetry and charge_gaps
-- rekey precedents).
ALTER TABLE analytics.vehicle_metric_watermarks
    DROP CONSTRAINT vehicle_metric_watermarks_tesla_source_unique;

ALTER TABLE analytics.vehicle_metric_watermarks
    ADD COLUMN account_id UUID;

ALTER TABLE analytics.vehicle_metric_watermarks
    ADD CONSTRAINT vehicle_metric_watermarks_account_tesla_source_unique
    UNIQUE (account_id, tesla_id, source);

DROP INDEX analytics.idx_vehicle_metrics_latest;

ALTER TABLE analytics.vehicle_metrics
    DROP CONSTRAINT vehicle_metrics_tesla_date_unique;

ALTER TABLE analytics.vehicle_metrics
    ADD COLUMN account_id UUID;

CREATE INDEX idx_vehicle_metrics_latest
    ON analytics.vehicle_metrics (account_id, tesla_id, metric_date DESC);

ALTER TABLE analytics.vehicle_metrics
    ADD CONSTRAINT vehicle_metrics_account_tesla_date_unique
    UNIQUE (account_id, tesla_id, metric_date);
