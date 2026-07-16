-- Source A of RM2-telemetry-add-charging-stats: charge enrichment columns on vehicle_snapshots.
-- Six new NULLABLE columns extracted from ChargeState in the nightly vehicle_data fetch.
-- Nullable because pre-enrichment rows (every row written before this migration) predate
-- extraction of these fields — adding NOT NULL with a default of 0 or '' would falsely
-- represent historical rows as having had a 0 kWh add, which is misleading. NULL means
-- "this was not extracted at capture time"; data is recoverable from raw_data JSONB.
-- No new index: these fields are not filter/sort columns on the dashboard hot path.
-- The existing (account_id, tesla_id, captured_at) index continues to serve all reads.
-- (ai/go-conventions.md §persistence, design DSA1)

-- +goose Up
ALTER TABLE vehicle_snapshots
    ADD COLUMN charge_energy_added    DOUBLE PRECISION,  -- kWh added this charge session; NULL pre-enrichment
    ADD COLUMN charger_power          INTEGER,            -- kW; NULL pre-enrichment
    ADD COLUMN charger_voltage        INTEGER,            -- V; NULL pre-enrichment
    ADD COLUMN charger_actual_current INTEGER,            -- A; NULL pre-enrichment
    ADD COLUMN usable_battery_level   INTEGER,            -- %; NULL pre-enrichment
    ADD COLUMN fast_charger_type      TEXT;               -- e.g. "Tesla", "Combo"; NULL pre-enrichment

-- +goose Down
ALTER TABLE vehicle_snapshots
    DROP COLUMN IF EXISTS fast_charger_type,
    DROP COLUMN IF EXISTS usable_battery_level,
    DROP COLUMN IF EXISTS charger_actual_current,
    DROP COLUMN IF EXISTS charger_voltage,
    DROP COLUMN IF EXISTS charger_power,
    DROP COLUMN IF EXISTS charge_energy_added;
