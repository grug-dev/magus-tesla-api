-- +goose Up
-- internal/telemetry — add five derived consumption columns to
-- vehicle_snapshots (MAG-10, telemetry-add-derived-consumption-columns):
-- distance_traveled_km_calc, battery_used_pct_calc, km_per_pct_calc,
-- estimated_range_km_calc, days_spanned_calc. All five are nullable and
-- computed from the current row and its predecessor for the same
-- (account_id, tesla_id), ordered by captured_at. NULL means "no
-- predecessor exists" (the vehicle's first-ever snapshot, design D8/D10);
-- km_per_pct_calc and estimated_range_km_calc are additionally NULL
-- whenever battery_used_pct_calc <= 0 — a zero or negative divisor has no
-- truthful ratio (design D2). The write path computes these in Go
-- (deriveConsumption, service.go) at capture time — this migration's
-- backfill below is the ONE-TIME exception, mirroring
-- 20260805000001_dedupe_vehicle_snapshots_daily.sql's own backfill: a
-- point-in-time conversion of already-stored rows, not an ongoing schema
-- dependency (design D4).
ALTER TABLE vehicle_snapshots
    ADD COLUMN distance_traveled_km_calc DOUBLE PRECISION,
    ADD COLUMN battery_used_pct_calc     INTEGER,
    ADD COLUMN km_per_pct_calc           DOUBLE PRECISION,
    ADD COLUMN estimated_range_km_calc   DOUBLE PRECISION,
    ADD COLUMN days_spanned_calc         INTEGER;

-- Backfill every existing row in one pass via LAG() partitioned by vehicle,
-- ordered by capture time (design D4). The oldest row per vehicle has no
-- predecessor: LAG() returns NULL for it, the WHERE clause below excludes
-- it from the UPDATE, and it correctly keeps NULL in all five columns —
-- the same "first-ever snapshot" case the Go write path gives via
-- deriveConsumption(nil, cur) (design D8/D10). The formulas here are
-- written to match deriveConsumption byte-for-byte: same subtraction
-- order, same ">0" divisor guard, same "x100" range formula, same
-- whole-calendar-day count via captured_date (DATE) subtraction.
WITH prev AS (
    SELECT
        id,
        odometer_km,
        battery_level_pct,
        captured_date,
        LAG(odometer_km)        OVER w AS prev_odometer_km,
        LAG(battery_level_pct)  OVER w AS prev_battery_level_pct,
        LAG(captured_date)      OVER w AS prev_captured_date
    FROM vehicle_snapshots
    WINDOW w AS (PARTITION BY account_id, tesla_id ORDER BY captured_at)
)
UPDATE vehicle_snapshots v
SET
    distance_traveled_km_calc = prev.odometer_km - prev.prev_odometer_km,
    battery_used_pct_calc     = prev.prev_battery_level_pct - prev.battery_level_pct,
    days_spanned_calc         = prev.captured_date - prev.prev_captured_date,
    km_per_pct_calc = CASE
        WHEN (prev.prev_battery_level_pct - prev.battery_level_pct) > 0
        THEN (prev.odometer_km - prev.prev_odometer_km) / (prev.prev_battery_level_pct - prev.battery_level_pct)
        ELSE NULL
    END,
    estimated_range_km_calc = CASE
        WHEN (prev.prev_battery_level_pct - prev.battery_level_pct) > 0
        THEN ((prev.odometer_km - prev.prev_odometer_km) / (prev.prev_battery_level_pct - prev.battery_level_pct)) * 100
        ELSE NULL
    END
FROM prev
WHERE v.id = prev.id
  AND prev.prev_odometer_km IS NOT NULL;

-- +goose Down
-- Full rollback: no row is deleted, only the five added columns. Dividing
-- back out is not needed (nothing here is a unit conversion) — dropping
-- the columns is a complete, lossless-to-everything-else reversal.
ALTER TABLE vehicle_snapshots
    DROP COLUMN IF EXISTS days_spanned_calc,
    DROP COLUMN IF EXISTS estimated_range_km_calc,
    DROP COLUMN IF EXISTS km_per_pct_calc,
    DROP COLUMN IF EXISTS battery_used_pct_calc,
    DROP COLUMN IF EXISTS distance_traveled_km_calc;
