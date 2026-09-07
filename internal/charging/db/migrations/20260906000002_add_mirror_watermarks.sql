-- +goose Up
-- mirror_watermarks: one cursor per account, holding the highest
-- telemetry.supercharger_history.updated_at internal/charging's nightly
-- mirror has already synchronized (RM44-platform-add-mirror-watermark,
-- MAG-48, roadmap D4/D20-D22). Owned by internal/charging; no other module
-- may import the generated chargingdb package (ai/architecture.md §2).
--
-- NO tesla_id column: the mirror read this cursor bounds is account-wide,
-- not per vehicle (roadmap D20) — telemetry's only per-vehicle
-- updated-since query filters tesla_id = X, which can never return a
-- tesla_id IS NULL row, defeating the orphan-recovery path this table
-- exists to keep working (roadmap D3). A per-account cursor matches the
-- per-account read exactly.
--
-- NO source column: unlike analytics.vehicle_metric_watermarks, this
-- table's owning module (charging) mirrors exactly one upstream table
-- (telemetry.supercharger_history). A second mirrored source would be an
-- additive migration adding the column then, not a speculative one now
-- (roadmap D21, "do not over-abstract" — CLAUDE.md §Non-negotiables).
--
-- Two alternatives were considered and rejected (roadmap D4, cited here,
-- not re-derived):
--   1. Reuse or move analytics.vehicle_metric_watermarks — rejected for
--      CORRECTNESS. That table tracks a different read (analytics reads
--      charging.supercharger_sessions; this cursor bounds a read of
--      telemetry.supercharger_history). Sharing one date across both loses
--      data on a normal night: at 05:15 the mirror writes new charging
--      rows and sets a shared cursor to 05:15; at 05:16 analytics asks
--      charging "what changed since 05:15?" and is told nothing, skipping
--      the rows the mirror just wrote. The rule: a cursor belongs to the
--      module that READS, never the module that is read — telemetry must
--      not own a table describing how far a consumer has read it.
--   2. A source_updated_at column on charging.supercharger_sessions
--      (cursor = MAX() per account) — rejected on DESIGN, not correctness.
--      It works, including the months-idle case. Rejected because it puts
--      mirror bookkeeping inside a domain table, and the watermark-table
--      shape is already proven (this table follows the identical shape
--      analytics.vehicle_metric_watermarks already established).
CREATE TABLE charging.mirror_watermarks (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id        UUID NOT NULL,
    source_updated_at TIMESTAMPTZ NOT NULL,

    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT mirror_watermarks_account_unique UNIQUE (account_id)
);

COMMENT ON TABLE charging.mirror_watermarks IS
    'One Supercharger-mirror cursor per account (RM44-platform-add-mirror-watermark, '
    'MAG-48). Holds the highest telemetry.supercharger_history.updated_at this '
    'module''s nightly mirror has already synchronized for that account. No row '
    'yet for an account means "epoch": the next mirror run backfills that '
    'account''s whole history once. Owned by internal/charging; no other module '
    'reads this table directly.';

COMMENT ON COLUMN charging.mirror_watermarks.source_updated_at IS
    'The maximum updated_at internal/charging''s mirror has observed from '
    'telemetry.supercharger_history for this account, as of its last run. The '
    'mirror queries SuperchargerHistoryByAccountUpdatedSince(source_updated_at - '
    '24h) and advances this column only when that query returns at least one row '
    '-- a run that returns zero rows leaves this column UNTOUCHED (roadmap D5: '
    'advancing it to now() on an empty read would permanently and silently lose '
    'any row that commits a moment late).';

-- Index Plan: the sole read pattern this table serves is a single-row
-- lookup, WHERE account_id = $1, which mirror_watermarks_account_unique's
-- own index serves entirely. No separate CREATE INDEX.

-- +goose Down
DROP TABLE IF EXISTS charging.mirror_watermarks;
