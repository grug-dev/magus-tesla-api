-- +goose Up
-- idx_supercharger_history_account_updated: serves
-- SuperchargerHistoryByAccountUpdatedSince, the account-wide updated-since
-- read that bounds internal/charging's nightly Supercharger mirror
-- (RM44-platform-add-mirror-watermark, MAG-48, roadmap D20).
--
-- (account_id, updated_at) matches that query exactly: account_id prunes to
-- the tenant, and updated_at ASC satisfies both the `updated_at >= $2`
-- range predicate AND the `ORDER BY updated_at ASC` in one index scan, so
-- the planner needs no sort step. The pre-existing
-- idx_supercharger_history_account_time (account_id,
-- charge_start_date_time DESC) shares only the account_id prefix and is not
-- sorted on updated_at, so it would leave updated_at as a residual filter
-- plus an in-memory sort.
--
-- Owner's call at the design gate: pay a small, permanent write cost on
-- every supercharger_history upsert to remove the chance of a slow nightly
-- batch read later. The alternative -- document the index as a revisit
-- trigger and add it only if a slow-query log ever showed it -- was the
-- design's original recommendation and was NOT chosen.
CREATE INDEX idx_supercharger_history_account_updated
    ON telemetry.supercharger_history (account_id, updated_at);

-- +goose Down
DROP INDEX IF EXISTS telemetry.idx_supercharger_history_account_updated;
