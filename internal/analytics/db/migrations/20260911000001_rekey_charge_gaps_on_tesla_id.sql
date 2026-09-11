-- +goose Up
-- Collapse any (tesla_id, gap_date) duplicates before the new UNIQUE
-- constraint can be added. Keeps the row whose created_at is earliest --
-- created_at records when a vehicle-day was first flagged, the more useful
-- value to preserve. On a database where every tesla_id has always belonged
-- to one account (true today), this deletes zero rows; it exists only so a
-- future or unusual state cannot make this migration fail outright.
DELETE FROM analytics.charge_gaps t
WHERE EXISTS (
    SELECT 1 FROM analytics.charge_gaps o
    WHERE o.tesla_id = t.tesla_id
      AND o.gap_date = t.gap_date
      AND (o.created_at < t.created_at
           OR (o.created_at = t.created_at AND o.id < t.id))
);

ALTER TABLE analytics.charge_gaps
    DROP CONSTRAINT charge_gaps_account_tesla_date_unique;

-- Schema-qualified: an index name is resolved through search_path, and
-- goose does not guarantee analytics is on it. IF EXISTS because
-- DROP COLUMN account_id below would remove this index anyway.
DROP INDEX IF EXISTS analytics.idx_charge_gaps_account;

ALTER TABLE analytics.charge_gaps
    DROP COLUMN account_id;

ALTER TABLE analytics.charge_gaps
    ADD CONSTRAINT charge_gaps_tesla_date_unique UNIQUE (tesla_id, gap_date);

-- +goose Down
ALTER TABLE analytics.charge_gaps
    DROP CONSTRAINT charge_gaps_tesla_date_unique;

-- account_id comes back NULLABLE, not NOT NULL: the DROP COLUMN in Up threw
-- the values away, so there is nothing to backfill a NOT NULL constraint
-- with on a populated table. Down restores the column's shape for a
-- same-session rollback (before any new row is written), not the original
-- constraint strength on a table that already has data -- the same
-- limitation every DROP COLUMN in this codebase's migrations has on Down.
ALTER TABLE analytics.charge_gaps
    ADD COLUMN account_id UUID;

CREATE INDEX idx_charge_gaps_account
    ON analytics.charge_gaps (account_id, gap_date DESC);

ALTER TABLE analytics.charge_gaps
    ADD CONSTRAINT charge_gaps_account_tesla_date_unique
    UNIQUE (account_id, tesla_id, gap_date);
