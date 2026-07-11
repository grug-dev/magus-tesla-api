-- +goose Up
-- Telemetry collection storage — owned by internal/telemetry (tier 3 of
-- openspec/roadmaps/nightly-vehicle-telemetry.md). Two APPEND-ONLY tables written
-- by the nightly collection service; no other module reads them directly.
--
-- No FK from account_id/tesla_id into the account module's accounts/vehicles
-- tables (design D2): a cross-module FK would couple telemetry's schema to the
-- account module's and is exactly the coupling ai/architecture.md §2 forbids.
-- Referential integrity is upheld by the flow instead — telemetry only ever
-- writes account_id/tesla_id values it just received from
-- account.AllRegisteredVehicles. tesla_id is the int64 Fleet API vehicle id.
--
-- Units stay API-native: distance/range are stored in MILES (never km). The
-- kilometre equivalent is derived on read by the domain type's Km()/Kmh()
-- companions (ai/go-conventions.md non-negotiable) — never a stored column.

-- vehicle_snapshots: one immutable row per SUCCESSFUL capture (design D1). Holds
-- the lossless raw vehicle_data payload plus extracted typed columns for cheap
-- indexed dashboard reads. Never overwritten or deleted (append-only): no
-- updated_at, no UNIQUE that would block a second capture of the same vehicle.
CREATE TABLE vehicle_snapshots (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id       UUID NOT NULL,
    tesla_id         BIGINT NOT NULL,
    captured_at      TIMESTAMPTZ NOT NULL,
    raw_data         JSONB NOT NULL,
    battery_level    INTEGER NOT NULL,
    battery_range    DOUBLE PRECISION NOT NULL,
    charging_state   TEXT NOT NULL,
    charge_limit_soc INTEGER NOT NULL,
    odometer         DOUBLE PRECISION NOT NULL,
    inside_temp      DOUBLE PRECISION NOT NULL,
    outside_temp     DOUBLE PRECISION NOT NULL,
    locked           BOOLEAN NOT NULL,
    -- sentry_mode is NULLABLE on purpose: the tier-1 adapter models it as *bool,
    -- so nil (vehicle did not report sentry) must stay distinct from a reported
    -- off. Collapsing absent into false would lose history (design D1).
    sentry_mode      BOOLEAN,
    car_version      TEXT NOT NULL,
    latitude         DOUBLE PRECISION NOT NULL,
    longitude        DOUBLE PRECISION NOT NULL
);

-- Per-vehicle time-series read path every dashboard uses (latest snapshot, range
-- over time): filter by (account_id, tesla_id), order by captured_at.
CREATE INDEX idx_vehicle_snapshots_vehicle_time
    ON vehicle_snapshots (account_id, tesla_id, captured_at);

-- poll_attempts: one row per (vehicle, run), written for EVERY attempt regardless
-- of outcome (design D5). It is the availability / sleep-behavior signal the
-- roadmap wants, so success and failure are both recorded. outcome is
-- success|failure; reason is ok|asleep-timeout|unauthorized|api-error.
CREATE TABLE poll_attempts (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id   UUID NOT NULL,
    tesla_id     BIGINT NOT NULL,
    attempted_at TIMESTAMPTZ NOT NULL,
    outcome      TEXT NOT NULL,
    reason       TEXT NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS poll_attempts;
DROP TABLE IF EXISTS vehicle_snapshots;
