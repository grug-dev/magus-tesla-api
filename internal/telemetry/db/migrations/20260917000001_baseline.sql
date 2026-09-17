-- telemetry baseline.
--
-- One self-contained file that creates this module's whole schema. It is a
-- verbatim transcription of a pg_dump --schema-only taken on 2026-09-17, after
-- the telemetry module's migration history was squashed.
--
-- It creates objects and reads nothing, so it depends on no other module and can
-- be applied in any order relative to them. Keep it that way: a migration here
-- must never name another module's schema.
--
-- Existing databases (dev and prod) have this version recorded as applied without
-- it ever running. So a change made HERE reaches new databases only. Anything that
-- must also reach dev and prod belongs in a later, ordinary migration.

-- +goose Up

-- SCHEMA: telemetry
-- +goose StatementBegin
CREATE SCHEMA telemetry;


SET default_tablespace = '';

SET default_table_access_method = heap;
-- +goose StatementEnd

-- TABLE: poll_attempts
CREATE TABLE telemetry.poll_attempts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    polled_by_account_id uuid NOT NULL,
    tesla_id bigint NOT NULL,
    attempted_at timestamp with time zone NOT NULL,
    outcome text NOT NULL,
    reason text NOT NULL,
    run_id uuid,
    triggered_by text DEFAULT 'scheduler'::text NOT NULL
);

-- TABLE: poll_runs
CREATE TABLE telemetry.poll_runs (
    run_id uuid NOT NULL,
    triggered_by text NOT NULL,
    started_at timestamp with time zone NOT NULL,
    finished_at timestamp with time zone NOT NULL,
    duration_seconds double precision NOT NULL,
    accounts_attempted integer NOT NULL,
    accounts_succeeded integer NOT NULL,
    accounts_failed integer NOT NULL,
    vehicles_attempted integer NOT NULL,
    vehicles_succeeded integer NOT NULL,
    failures_asleep_timeout integer NOT NULL,
    failures_unauthorized integer NOT NULL,
    failures_api_error integer NOT NULL,
    tesla_api_calls integer NOT NULL,
    charging_sessions_upserted integer NOT NULL,
    charging_fetch_failures integer NOT NULL,
    config_capture_failures integer NOT NULL
);

-- TABLE: supercharger_history
CREATE TABLE telemetry.supercharger_history (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    session_id bigint NOT NULL,
    vin text NOT NULL,
    tesla_id bigint NOT NULL,
    site_location_name text NOT NULL,
    country_code text NOT NULL,
    charge_start_date_time timestamp with time zone NOT NULL,
    charge_stop_date_time timestamp with time zone NOT NULL,
    unlatch_date_time timestamp with time zone,
    billing_type text NOT NULL,
    vehicle_make_type text NOT NULL,
    energy_kwh double precision,
    total_cost double precision,
    currency text,
    is_paid boolean,
    raw_data jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    start_battery_pct smallint,
    end_battery_pct smallint,
    battery_pct_source text,
    CONSTRAINT supercharger_history_battery_pct_source_check CHECK ((battery_pct_source = ANY (ARRAY['user_verified'::text, 'polled'::text]))),
    CONSTRAINT supercharger_history_end_battery_pct_check CHECK (((end_battery_pct >= 0) AND (end_battery_pct <= 100))),
    CONSTRAINT supercharger_history_start_battery_pct_check CHECK (((start_battery_pct >= 0) AND (start_battery_pct <= 100)))
);

-- TABLE: vehicle_snapshots
CREATE TABLE telemetry.vehicle_snapshots (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid,
    tesla_id bigint NOT NULL,
    captured_at timestamp with time zone NOT NULL,
    raw_data jsonb NOT NULL,
    battery_level_pct integer NOT NULL,
    battery_range_km double precision NOT NULL,
    charging_state text NOT NULL,
    charge_limit_soc_pct integer NOT NULL,
    odometer_km double precision NOT NULL,
    inside_temp_c double precision NOT NULL,
    outside_temp_c double precision NOT NULL,
    locked boolean NOT NULL,
    sentry_mode boolean,
    car_version text NOT NULL,
    charge_energy_added_kwh double precision,
    charger_power_kw integer,
    charger_voltage_v integer,
    charger_actual_current_a integer,
    usable_battery_level_pct integer,
    max_range_charge_counter integer,
    tpms_pressure_fl_psi real,
    tpms_pressure_fr_psi real,
    tpms_pressure_rl_psi real,
    tpms_pressure_rr_psi real,
    captured_date date NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

-- COMMENT: COLUMN poll_attempts.run_id
-- +goose StatementBegin
COMMENT ON COLUMN telemetry.poll_attempts.run_id IS 'Correlates every vehicle''s attempt row from one app.ProcessVehicleData invocation. Generated once per invocation by internal/app (uuid.New()) and passed down via telemetry.RunContext (design.md D5). NULL on every row written before this migration — that run''s identity was never recorded and is not recoverable; never backfilled, never will be.';
-- +goose StatementEnd

-- COMMENT: COLUMN poll_attempts.triggered_by
COMMENT ON COLUMN telemetry.poll_attempts.triggered_by IS 'What triggered the run that wrote this attempt: scheduler (the nightly poller, including cmd/poller --once) or api (a future manual re-run, RM29 tier 8, parked). NOT NULL DEFAULT ''scheduler'' backfills every pre-migration row correctly, since no non-scheduler entry point existed before this tier. Guarded by the typed Go constant telemetry.TriggeredBy — no DB CHECK (design.md D1/D7).';

-- COMMENT: TABLE poll_runs
COMMENT ON TABLE telemetry.poll_runs IS 'One row per app.ProcessVehicleData invocation (nightly scheduler or cmd/poller --once), written once by telemetry.RunWriter.RecordRun after the whole run completes -- success or the step-1 whole-cycle-failure path alike (RM36-telemetry-add-poll-runs design D1/D3/D6). Reproduces the poller''s per-cycle log line (telemetry.LogCycle) as a queryable row (design D5/D7).';

-- COMMENT: COLUMN poll_runs.run_id
COMMENT ON COLUMN telemetry.poll_runs.run_id IS 'The invocation''s identity, generated once by internal/app (uuid.New()) and shared with every poll_attempts row that invocation wrote via RunContext.RunID. No FK to poll_attempts -- see this file''s header (design D2).';

-- COMMENT: COLUMN poll_runs.triggered_by
COMMENT ON COLUMN telemetry.poll_runs.triggered_by IS 'scheduler (the nightly poller, including cmd/poller --once) or api (the parked manual-rerun API, RM29 tier 8) -- same domain and same no-CHECK reasoning as poll_attempts.triggered_by (design D4).';

-- COMMENT: COLUMN poll_runs.accounts_failed
-- +goose StatementBegin
COMMENT ON COLUMN telemetry.poll_runs.accounts_failed IS 'Accounts that hit one of the two whole-account short-circuits in collectAccount: AccessTokenFor failure, or the up-front ListVehicles call returning tesla.ErrUnauthorized (roadmap D4). No other failure mode counts here; accounts_succeeded = accounts_attempted - accounts_failed.';
-- +goose StatementEnd

-- COMMENT: COLUMN poll_runs.tesla_api_calls
COMMENT ON COLUMN telemetry.poll_runs.tesla_api_calls IS 'Every call telemetry made to tesla.VehicleService during this run (ListVehicles, WakeUp, VehicleData, ChargingHistory), counted by an internal counting decorator regardless of whether the call succeeded or failed (roadmap D2, design D9/D10) -- a rejected request still spends a request against Tesla''s API.';

-- COMMENT: TABLE supercharger_history
-- +goose StatementBegin
COMMENT ON TABLE telemetry.supercharger_history IS 'Tesla-billed Supercharger and DC fast-charging sessions per account. Covers sessions returned by GET /api/1/dx/charging/history only (no home/AC charging). The Tesla API itself carries no battery-percentage field; start_battery_pct/end_battery_pct/battery_pct_source are a human-owned verification/override channel, and start_battery_pct_est/end_battery_pct_est are a frozen write-once snapshot of the estimate at verification time (both added by RM27 tier 1, MAG-14) -- all five excluded from the nightly UPSERT so a verified value or its snapshot is never silently overwritten (R3). Owned by internal/telemetry; no other module reads this table directly. UPSERT on session_id (not append-only): billing state is mutable post-session.';
-- +goose StatementEnd

-- COMMENT: COLUMN supercharger_history.start_battery_pct
-- +goose StatementBegin
COMMENT ON COLUMN telemetry.supercharger_history.start_battery_pct IS 'Human-verified/override battery % at charge start (0-100). NULL = no override; reads fall back to internal/battery''s on-read estimate (R5). Excluded from UpsertSuperchargerHistory''s INSERT and ON CONFLICT DO UPDATE SET -- never auto-written by the nightly poller (R3).';
-- +goose StatementEnd

-- COMMENT: COLUMN supercharger_history.end_battery_pct
COMMENT ON COLUMN telemetry.supercharger_history.end_battery_pct IS 'Human-verified/override battery % at charge end (0-100). Same NULL convention and the same R3 write-protection as start_battery_pct.';

-- COMMENT: COLUMN supercharger_history.battery_pct_source
COMMENT ON COLUMN telemetry.supercharger_history.battery_pct_source IS 'Why start/end_battery_pct are set: user_verified (verification UI, out of scope this tier) or polled (future measured-SOC alternative, logged to the backlog, not implemented). NULL means no override exists. Never ''estimated'' -- that state is computed on read by internal/battery and is never persisted here (R6).';

-- CONSTRAINT: poll_attempts poll_attempts_pkey
ALTER TABLE ONLY telemetry.poll_attempts
    ADD CONSTRAINT poll_attempts_pkey PRIMARY KEY (id);

-- CONSTRAINT: poll_runs poll_runs_pkey
ALTER TABLE ONLY telemetry.poll_runs
    ADD CONSTRAINT poll_runs_pkey PRIMARY KEY (run_id);

-- CONSTRAINT: supercharger_history supercharger_history_pkey
ALTER TABLE ONLY telemetry.supercharger_history
    ADD CONSTRAINT supercharger_history_pkey PRIMARY KEY (id);

-- CONSTRAINT: supercharger_history supercharger_history_session_id_unique
ALTER TABLE ONLY telemetry.supercharger_history
    ADD CONSTRAINT supercharger_history_session_id_unique UNIQUE (session_id);

-- CONSTRAINT: vehicle_snapshots vehicle_snapshots_pkey
ALTER TABLE ONLY telemetry.vehicle_snapshots
    ADD CONSTRAINT vehicle_snapshots_pkey PRIMARY KEY (id);

-- CONSTRAINT: vehicle_snapshots vehicle_snapshots_tesla_date_unique
ALTER TABLE ONLY telemetry.vehicle_snapshots
    ADD CONSTRAINT vehicle_snapshots_tesla_date_unique UNIQUE (tesla_id, captured_date);

-- INDEX: idx_supercharger_history_vehicle_time
CREATE INDEX idx_supercharger_history_vehicle_time ON telemetry.supercharger_history USING btree (tesla_id, charge_start_date_time DESC);

-- INDEX: idx_supercharger_history_vehicle_updated
CREATE INDEX idx_supercharger_history_vehicle_updated ON telemetry.supercharger_history USING btree (tesla_id, updated_at);

-- +goose Down
-- +goose StatementBegin
-- Refusing is deliberate. Reversing this file would mean dropping the whole
-- telemetry schema and every row in it. An empty rollback would be worse: goose
-- would mark the file un-applied while every object still exists, and the next
-- forward run would fail on "relation already exists" -- which stops the web and
-- poller containers, because both wait for the migration step to succeed.
DO $$
BEGIN
    RAISE EXCEPTION 'the telemetry baseline is not reversible: recreate the database instead of rolling it back';
END
$$;
-- +goose StatementEnd
