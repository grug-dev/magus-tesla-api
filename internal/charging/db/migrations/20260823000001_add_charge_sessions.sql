-- +goose Up
-- charge_sessions: the charging module's record of each Tesla Supercharger charge
-- session — its identity, its time window, the session facts a charging read needs,
-- and the human-owned battery-percentage verification/estimate columns
-- (RM29 tier 6, roadmap D4, MAG-26).
--
-- WHAT THIS TABLE IS. A DENSE MIRROR: one row per Supercharger session, whether or
-- not anyone has verified its battery percentages. That is what lets a date-range
-- read over this table alone return every session with its window, site, energy,
-- cost and any verified percentages, with NO join into internal/telemetry — a join
-- the module boundary (ai/architecture.md §2) forbids anyway. See design.md D2.
--
-- WHAT IT DELIBERATELY DOES NOT CARRY, and this list is CLOSED: country_code,
-- unlatch_date_time, billing_type, vehicle_make_type, raw_data. Those are
-- vendor/provenance detail no declared read has asked for, and every copied field is
-- another field this mirror must keep in step, nightly, forever. raw_data
-- additionally must have exactly ONE home to work as the schema-drift hedge
-- ai/go-conventions.md requires, and that home is supercharger_sessions.raw_data.
-- A column earns a place here by being needed by a charging read; a later migration
-- can add one with a one-line backfill, since the source still exists (design.md D1).
--
-- MUTABILITY — the rule that decides every column's behavior here (design.md D1):
-- a mirrored column gets EXACTLY the write semantics its source column has.
--   * Write-once at the source, therefore write-once here: session_id, account_id,
--     vin, charge_start_date_time, charge_stop_date_time, site_location_name.
--   * Refreshed by telemetry's ON CONFLICT DO UPDATE SET, therefore refreshed by
--     ours: tesla_id, energy_kwh, total_cost, currency, is_paid. These DO drift —
--     Tesla's fees settle after the session ends — and the drift is reconciled by an
--     idempotent re-mirror running in the SAME nightly cycle that refreshed the
--     source (design.md D7), never by merging: this table originates no value.
--   * Never written by the mirror at all: the five battery-percentage columns.
--
-- NAMING: telemetry's column names are used verbatim (energy_kwh, total_cost,
-- currency, site_location_name) rather than this module's own manual_charge_entries
-- vocabulary (energy_added_kwh, price, location_label), so the backfill below stays a
-- literal column-to-column copy checkable by inspection. Accepted cost: this module
-- now names the same concepts two ways, and backlog item 12's convergence will
-- involve a rename (design.md D1).
--
-- NO raw_data JSONB, and that is not an oversight — see the closed exclusion list
-- above. Same reasoning as manual_charge_entries and charge_gaps, neither of which
-- carries raw_data.
--
-- NO FK on account_id or tesla_id: a cross-module FK into the account module's
-- tables would couple charging migrations to the account schema — exactly the
-- coupling ai/architecture.md §2 forbids. Referential integrity is upheld by flow:
-- the only writer receives sessions already scoped to an account and already
-- VIN-resolved by internal/telemetry that same night.
--
-- THIS MIGRATION DROPS NOTHING. internal/telemetry keeps all five percentage
-- columns and every reader of them keeps working (design.md D4/D9). A DROP in this
-- same change would run FIRST — MIGRATIONS_DIRS puts telemetry before charging and
-- goose runs directory by directory — and would destroy the data the backfill
-- below is copying.
CREATE TABLE charge_sessions (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- IDENTITY (mirrored)
    account_id              UUID   NOT NULL,        -- multi-tenant scope
    vin                     TEXT   NOT NULL,        -- durable vehicle key; write-once
    tesla_id                BIGINT,                 -- currently-registered vehicle id; REFRESHED every sync; NULL when the VIN is not a currently-registered vehicle
    session_id              BIGINT NOT NULL,        -- Tesla's session id

    -- THE SESSION'S TIME WINDOW (mirrored; write-once at the source, write-once here)
    charge_start_date_time  TIMESTAMPTZ NOT NULL,
    charge_stop_date_time   TIMESTAMPTZ NOT NULL,

    -- SESSION FACTS (mirrored). site_location_name is write-once at the source, so
    -- it is write-once here. The other four are in telemetry's ON CONFLICT DO UPDATE
    -- SET — they change as fees settle and invoices finalize — so they are refreshed
    -- on every mirror pass. All four are nullable at the source and stay nullable
    -- here: NULL means the session had no kWh fee / no fees at all.
    --
    -- total_cost is a MONETARY amount: no unit suffix (CLAUDE.md's named money
    -- exemption), paired with the currency column instead. Its DOUBLE PRECISION type
    -- is copied from telemetry deliberately, to keep the backfill a literal copy —
    -- but float is a questionable type for money, and converging this table with
    -- manual_charge_entries.price NUMERIC(14,2) (backlog item 12) will have to
    -- reconcile the two types with explicit rounding semantics (design.md D1).
    site_location_name      TEXT   NOT NULL,
    energy_kwh              DOUBLE PRECISION,       -- NULL when the session had no kWh fee
    total_cost              DOUBLE PRECISION,       -- NULL when the session had no fees
    currency                TEXT,                   -- NULL when the session had no fees
    is_paid                 BOOLEAN,                -- NULL when the session had no fees

    -- HUMAN-OWNED VERIFICATION CHANNEL (charging-owned; never written by the sync).
    -- NULL = nothing recorded. No code in this repository writes these as of this
    -- migration: the verification UI is backlog item 11. The sync path has no field
    -- for them at all (design.md D6) — protection by compile error, not by comment.
    start_battery_pct       SMALLINT CHECK (start_battery_pct     BETWEEN 0 AND 100),
    end_battery_pct         SMALLINT CHECK (end_battery_pct       BETWEEN 0 AND 100),
    battery_pct_source      TEXT     CHECK (battery_pct_source IN ('user_verified', 'polled')),

    -- FROZEN, WRITE-ONCE snapshot of whatever estimate was on screen at the moment
    -- the percentages above were verified — a permanent drift log, never refreshed,
    -- never read back as a cache (RM27 design D6, carried over verbatim in intent).
    start_battery_pct_est   SMALLINT CHECK (start_battery_pct_est BETWEEN 0 AND 100),
    end_battery_pct_est     SMALLINT CHECK (end_battery_pct_est   BETWEEN 0 AND 100),

    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),  -- first mirrored; never updated
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),  -- last mirror pass that touched this row (same meaning as supercharger_sessions.updated_at — NOT a "data changed" signal; design.md D6)

    -- Upsert target for the nightly mirror, and the point-lookup index for
    -- "this session". Account-scoped rather than telemetry's global
    -- UNIQUE (session_id): every key in this platform is scoped by tenant, and a
    -- global uniqueness rule lets one bad id from one tenant block another's row.
    CONSTRAINT charge_sessions_account_session_unique UNIQUE (account_id, session_id),

    -- PROVENANCE (design.md D5) — a constraint supercharger_sessions never had: a
    -- recorded percentage must say where it came from. Legal to enforce here and not
    -- there because these five columns are the ONLY ones this module will own the
    -- writes for; everything MIRRORED carries no constraint stricter than
    -- telemetry's own. Hence, deliberately: NO stop >= start CHECK (the source
    -- permits a reversed window), NO total_cost >= 0 or energy_kwh >= 0 CHECK (the
    -- source has neither, and negative fee adjustments happen), NO currency format
    -- constraint. A mirror stricter than its source could not repair a source row it
    -- refuses to accept.
    CONSTRAINT charge_sessions_pct_source_required CHECK (
        (start_battery_pct IS NULL AND end_battery_pct IS NULL)
        OR battery_pct_source IS NOT NULL
    )
);

COMMENT ON TABLE charge_sessions IS
    'Tesla Supercharger charge sessions as owned by internal/charging: identity, the '
    'session time window, the session facts (site, energy, cost, currency, paid state), '
    'and the human-owned battery-percentage verification/estimate columns. Dense — one '
    'row per session, verified or not (design D2). Deliberately carries NO country_code, '
    'unlatch_date_time, billing_type, vehicle_make_type or raw_data (closed list, design '
    'D1). Mirrored from telemetry.supercharger_sessions by the nightly orchestrator '
    'through public ports only, in the same cycle that refreshes the source; each '
    'mirrored column has exactly its source column''s write semantics, so energy_kwh / '
    'total_cost / currency / is_paid / tesla_id are refreshed on every pass and '
    'everything else mirrored is write-once. The sync path can never write the five '
    'percentage columns (design D6). No other module reads this table directly.';

COMMENT ON COLUMN charge_sessions.tesla_id IS
    'Currently-registered vehicle id, refreshed on every sync and set NULL when the '
    'VIN is not a currently-registered vehicle of the account — the same contract '
    'telemetry.supercharger_sessions.tesla_id carries. Resolution is inherited from '
    'telemetry, never recomputed here: internal/charging may not import '
    'internal/account (design D3).';
COMMENT ON COLUMN charge_sessions.site_location_name IS
    'Supercharger site name as Tesla reported it. Write-once: absent from telemetry''s '
    'ON CONFLICT DO UPDATE SET, therefore absent from ours (design D1''s rule).';
COMMENT ON COLUMN charge_sessions.energy_kwh IS
    'kWh delivered, derived by telemetry from the session''s fees. NULL when the '
    'session had no kWh fee. REFRESHED on every mirror pass — telemetry recomputes it '
    'nightly as fees settle.';
COMMENT ON COLUMN charge_sessions.total_cost IS
    'Total charged for the session, in the currency column''s currency. Monetary '
    'amount: no unit suffix by the platform money exemption, paired with currency '
    'instead. NULL when the session had no fees. REFRESHED on every mirror pass. '
    'DOUBLE PRECISION is copied from telemetry to keep the backfill a literal copy; '
    'float is a questionable type for money and converging with '
    'manual_charge_entries.price NUMERIC(14,2) will have to reconcile the two.';
COMMENT ON COLUMN charge_sessions.currency IS
    'ISO 4217 code for total_cost. NULL when the session had no fees. REFRESHED on '
    'every mirror pass.';
COMMENT ON COLUMN charge_sessions.is_paid IS
    'Whether every fee on the session is settled. NULL when the session had no fees. '
    'REFRESHED on every mirror pass — this is the column that most visibly changes '
    'after a session ends.';
COMMENT ON COLUMN charge_sessions.start_battery_pct IS
    'Human-verified battery % at charge start (0-100). NULL = nothing recorded. Never '
    'written by the nightly sync — the sync port has no field for it (design D6).';
COMMENT ON COLUMN charge_sessions.end_battery_pct IS
    'Human-verified battery % at charge end (0-100). Same NULL convention and the same '
    'sync-path protection as start_battery_pct.';
COMMENT ON COLUMN charge_sessions.battery_pct_source IS
    'Provenance of start/end_battery_pct: user_verified (a human entered them) or '
    'polled (a future measured-SOC path, not implemented). Required whenever either '
    'percentage is set (charge_sessions_pct_source_required). Never ''estimated'' — '
    'an estimate is computed on read and is never persisted here.';
COMMENT ON COLUMN charge_sessions.start_battery_pct_est IS
    'FROZEN write-once snapshot of the live estimate at the moment start_battery_pct '
    'was verified — a drift-log entry, not a cache. Never refreshed, including by a '
    'later improved model; staleness here is correct, not a bug.';
COMMENT ON COLUMN charge_sessions.end_battery_pct_est IS
    'FROZEN write-once snapshot of the live estimate at the moment end_battery_pct was '
    'verified. Same write-once, never-refreshed, never-a-cache semantics as '
    'start_battery_pct_est.';

-- Per-vehicle bounded-window read path — the ONE read this table is shaped for:
-- "which sessions finished delivering energy to this vehicle within [start, end]".
-- account_id leads (multi-tenant convention: every dashboard read scopes by account
-- first — ai/go-conventions.md §"Read optimization", ai/architecture.md §7.3);
-- tesla_id second satisfies the second WHERE predicate in the same range scan.
--
-- charge_stop_date_time, NOT charge_start_date_time — deliberately UNLIKE
-- idx_supercharger_sessions_vehicle_time, which leads to start-time. Roadmap D12:
-- energy is fully delivered at session STOP, which is exactly what end_battery_pct
-- corresponds to, so a session belongs to the window containing its stop instant even
-- when it started the day before. telemetry's own
-- SuperchargerSessionsByVehicleBetween already documents that its start-time index
-- does not fully serve that predicate; this table fixes that at the index level.
--
-- ASC (no DESC): the read is an ascending, oldest-first bounded window, so an
-- ascending index satisfies both the range predicate and the ORDER BY with no sort
-- step and no backward scan.
CREATE INDEX idx_charge_sessions_vehicle_stop
    ON charge_sessions (account_id, tesla_id, charge_stop_date_time);

-- One-time backfill of every Supercharger session already collected. A literal
-- column-to-column copy (design.md D1's naming decision exists to make it one) —
-- the only transformation anywhere in it is the battery_pct_source CASE below.
--
-- Guarded and idempotent by construction:
--   * to_regclass guard — returns early in a database where telemetry's migrations
--     were never applied (e.g. internal/charging's own module-scoped test
--     provisioning, whose //go:embed cannot reach ../telemetry). PL/pgSQL plans a
--     statement on first execution, so the INSERT below is never planned when the
--     guard returns first. CORRECTED BY RM39 TIER 4 (comment only — not one character
--     of this file's SQL changes, per RM39 D1): this guard now ALWAYS SKIPS. Tier 4
--     moved telemetry's tables into schema `telemetry` and renamed the table to
--     supercharger_history, so to_regclass('public.supercharger_sessions') is NULL
--     permanently. That is harmless in both directions: on a database that already ran
--     this migration the backfill committed its rows long ago and ON CONFLICT DO
--     NOTHING would re-copy nothing anyway; on a fresh database there is nothing to
--     copy, because telemetry's table is created empty in the same `goose up`. The
--     claim it replaces — "in a real database the guard always passes: MIGRATIONS_DIRS
--     runs telemetry before charging" — was true when written and is now false.
--     The dependency is SOFT — ordering affects data
--     completeness, never migration success (design.md D8a). Verified by probe: sqlc
--     parses this file without ever seeing the supercharger_sessions reference,
--     because a PL/pgSQL body is an opaque string to the SQL parser (design.md D8b).
--   * ON CONFLICT DO NOTHING — re-running this block can never overwrite a row, and
--     in particular can never overwrite a percentage verified after the first run, or
--     a fee figure a later mirror pass has already refreshed.
--
-- battery_pct_source: telemetry allowed a percentage with a NULL source, and exactly
-- one live row is in that state. COALESCE(..., 'user_verified') resolves it, guarded
-- on a percentage actually being present so a row with no percentages keeps its NULL
-- source instead of being handed a fabricated one. 'user_verified' is inference, not
-- invention: nothing in this repository can write these columns (query.sql:343 marks
-- them write-protected, service.go:805 skips them), so a percentage that exists was
-- entered by a human — which is what 'user_verified' denotes (design.md D5).
-- +goose StatementBegin
-- BACKFILL-BEGIN
DO $$
BEGIN
    IF to_regclass('public.supercharger_sessions') IS NULL THEN
        RAISE NOTICE 'charge_sessions backfill skipped: supercharger_sessions is not present in this database';
        RETURN;
    END IF;

    INSERT INTO charge_sessions (
        account_id, vin, tesla_id, session_id,
        charge_start_date_time, charge_stop_date_time,
        site_location_name, energy_kwh, total_cost, currency, is_paid,
        start_battery_pct, end_battery_pct, battery_pct_source,
        start_battery_pct_est, end_battery_pct_est,
        created_at, updated_at
    )
    SELECT
        s.account_id,
        s.vin,
        s.tesla_id,
        s.session_id,
        s.charge_start_date_time,
        s.charge_stop_date_time,
        s.site_location_name,
        s.energy_kwh,
        s.total_cost,
        s.currency,
        s.is_paid,
        s.start_battery_pct,
        s.end_battery_pct,
        CASE
            WHEN s.start_battery_pct IS NOT NULL OR s.end_battery_pct IS NOT NULL
                THEN COALESCE(s.battery_pct_source, 'user_verified')
            ELSE s.battery_pct_source
        END,
        s.start_battery_pct_est,
        s.end_battery_pct_est,
        s.created_at,   -- preserve first-seen; this row is not "new" data
        now()           -- but this mirror row was written now
    FROM supercharger_sessions s
    ON CONFLICT (account_id, session_id) DO NOTHING;
END
$$;
-- BACKFILL-END
-- +goose StatementEnd

-- +goose Down
-- Safe by construction TODAY: this tier is EXPAND-only (design.md D4), so every row
-- and every column dropped here still exists in telemetry.supercharger_history
-- (named telemetry.supercharger_sessions when this migration shipped; moved and
-- renamed by RM39 tier 4), which this change never touched. Nothing is lost that is
-- not still upstream.
--
-- UPDATE (RM41-telemetry-drop-estimate-columns, 2026-09-03 -- this comment's own
-- "deferred contract change", D9 step 6): the anticipated drop never happened in the
-- shape D9 described. RM41 dropped only the two frozen ESTIMATE columns
-- (start_battery_pct_est/end_battery_pct_est) from BOTH charging.supercharger_sessions
-- (RM41-charging-drop-estimate-columns, tier 2) and telemetry.supercharger_history
-- (RM41-telemetry-drop-estimate-columns, tier 3) -- neither module is "the only copy"
-- of them, because neither module has them anymore. The three remaining percentage
-- columns (start_battery_pct, end_battery_pct, battery_pct_source) were NOT touched by
-- RM41 and still exist in both tables today, so this Down's original safety claim
-- continues to hold for them unchanged. (Separately, and predating RM41: since
-- RM31-charging-add-session-verification-port, a human's write through
-- SessionVerifier lands only on charging's trio, not telemetry's own -- so
-- telemetry's copy of the trio is not a byte-for-byte upstream mirror the way the
-- raw session facts are. That divergence is unrelated to this DROP and is not
-- evaluated by this comment.)
DROP INDEX IF EXISTS idx_charge_sessions_vehicle_stop;
DROP TABLE IF EXISTS charge_sessions;
