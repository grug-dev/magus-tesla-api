-- +goose Up
-- poll_runs: one row per run_id, written ONCE per app.ProcessVehicleData
-- invocation by internal/app through the new telemetry.RunWriter port
-- (RM36-telemetry-add-poll-runs design D1–D5, D11). This reopens migration
-- 20260823000002's "no separate run-level table" call, but only for facts
-- that were never per-vehicle to begin with: duration, Tesla API-call spend,
-- and the account-level breakdown. poll_attempts (one row per vehicle per
-- run) is UNCHANGED by this migration.
--
-- run_id is the PRIMARY KEY, not a surrogate id: RunContext.RunID is already
-- a globally unique value generated once per invocation (uuid.New() in
-- internal/app) before any row in this table or in poll_attempts exists, so
-- it is already this row's natural, immutable identity. A surrogate
-- `id UUID DEFAULT gen_random_uuid()` alongside it would be a second column
-- meaning the same thing (design D1). Unlike vehicle_snapshots (whose
-- natural key is mutable under a dedupe UPSERT) or poll_attempts (whose
-- natural key is not unique at all — many rows share one vehicle), nothing
-- ever rewrites a poll_runs row: RecordRun is a plain, single INSERT.
--
-- No FK to/from poll_attempts.run_id: poll_attempts rows for a run are
-- written throughout the cycle, while this table's row for the same run_id
-- is written once, at the very end, after the whole run is measured — so a
-- poll_attempts row for a run always exists BEFORE that run's poll_runs row
-- does, and a run that fails before writing any poll_attempts row must
-- still be able to write a poll_runs row referencing nothing. A FK in
-- either direction would reject legitimate writes on both counts (design
-- D2). The two tables correlate by the application-known run_id value
-- alone, mirroring this project's standing no-cross-module-FK convention
-- (ai/architecture.md §2) applied here within one module for the same
-- reason: write order, not a constraint, upholds the correlation.
--
-- finished_at / duration_seconds are NOT NULL: RecordRun is called exactly
-- once per invocation, AFTER the run's end is measured, on every code path
-- including the step-1 whole-cycle-failure short-circuit (roadmap D1/D6) —
-- so every row this port ever writes already knows its own end time and
-- duration at INSERT time. There is no "started but not yet finished" state
-- for this schema to represent (design D3). The only way a run leaves NO
-- row at all is a hard process crash between start and the RecordRun call —
-- an accepted, pre-existing gap matching poll_attempts' own append-only
-- design.
--
-- No CHECK on triggered_by: guarded by the typed Go constant
-- telemetry.TriggeredBy, mirroring poll_attempts.triggered_by's own
-- documented reasoning in migration 20260823000002 (design D4) — nothing in
-- this project writes SQL by hand outside Go.
--
-- No index beyond the PRIMARY KEY's automatic B-tree on run_id. See
-- design.md's "Index Plan" (D5) for the full justification: the only write
-- pattern is INSERT-by-run_id (already served by the PK); nothing in this
-- roadmap reads this table at all (backlog: a future gateway read surface);
-- and even that future "list recent runs" read stays a trivial sequential
-- scan at this table's write volume (roughly one row per poller invocation
-- — nightly, plus the rare --once run) for years to come.
CREATE TABLE poll_runs (
    run_id                      UUID PRIMARY KEY,
    triggered_by                TEXT NOT NULL,
    started_at                  TIMESTAMPTZ NOT NULL,
    finished_at                 TIMESTAMPTZ NOT NULL,
    duration_seconds            DOUBLE PRECISION NOT NULL,

    accounts_attempted          INTEGER NOT NULL,
    accounts_succeeded          INTEGER NOT NULL,
    accounts_failed             INTEGER NOT NULL,

    vehicles_attempted          INTEGER NOT NULL,
    vehicles_succeeded          INTEGER NOT NULL,
    failures_asleep_timeout     INTEGER NOT NULL,
    failures_unauthorized       INTEGER NOT NULL,
    failures_api_error          INTEGER NOT NULL,

    tesla_api_calls             INTEGER NOT NULL,

    charging_sessions_upserted  INTEGER NOT NULL,
    charging_fetch_failures     INTEGER NOT NULL,
    config_capture_failures     INTEGER NOT NULL
);

COMMENT ON TABLE poll_runs IS
    'One row per app.ProcessVehicleData invocation (nightly scheduler or '
    'cmd/poller --once), written once by telemetry.RunWriter.RecordRun '
    'after the whole run completes -- success or the step-1 '
    'whole-cycle-failure path alike (RM36-telemetry-add-poll-runs design '
    'D1/D3/D6). Reproduces the poller''s per-cycle log line '
    '(telemetry.LogCycle) as a queryable row (design D5/D7).';

COMMENT ON COLUMN poll_runs.run_id IS
    'The invocation''s identity, generated once by internal/app '
    '(uuid.New()) and shared with every poll_attempts row that invocation '
    'wrote via RunContext.RunID. No FK to poll_attempts -- see this '
    'file''s header (design D2).';

COMMENT ON COLUMN poll_runs.triggered_by IS
    'scheduler (the nightly poller, including cmd/poller --once) or api '
    '(the parked manual-rerun API, RM29 tier 8) -- same domain and same '
    'no-CHECK reasoning as poll_attempts.triggered_by (design D4).';

COMMENT ON COLUMN poll_runs.accounts_failed IS
    'Accounts that hit one of the two whole-account short-circuits in '
    'collectAccount: AccessTokenFor failure, or the up-front ListVehicles '
    'call returning tesla.ErrUnauthorized (roadmap D4). No other failure '
    'mode counts here; accounts_succeeded = accounts_attempted - '
    'accounts_failed.';

COMMENT ON COLUMN poll_runs.tesla_api_calls IS
    'Every call telemetry made to tesla.VehicleService during this run '
    '(ListVehicles, WakeUp, VehicleData, ChargingHistory), counted by an '
    'internal counting decorator regardless of whether the call succeeded '
    'or failed (roadmap D2, design D9/D10) -- a rejected request still '
    'spends a request against Tesla''s API.';

-- +goose Down
DROP TABLE IF EXISTS poll_runs;
