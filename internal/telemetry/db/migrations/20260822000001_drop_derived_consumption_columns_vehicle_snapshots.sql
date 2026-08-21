-- +goose Up
-- internal/telemetry — drop the five derived-consumption columns from
-- vehicle_snapshots (MAG-26 RM29 tier 4, RM29-telemetry-drop-derived-columns).
-- They were added by 20260814000001 and computed at capture time by
-- deriveConsumption (service.go). As of this change the derivation is owned by
-- internal/analytics, which computes the same five figures from this table's
-- surviving raw columns (odometer_km, battery_level_pct, captured_date) and
-- persists them on its own vehicle_metrics table (tier 3). Nothing inside
-- telemetry ever read these columns back.
--
-- DESTRUCTIVE: every stored value is discarded. The Down migration below
-- recomputes them rather than restoring them — see the note there.
ALTER TABLE vehicle_snapshots
    DROP COLUMN IF EXISTS days_spanned_calc,
    DROP COLUMN IF EXISTS estimated_range_km_calc,
    DROP COLUMN IF EXISTS km_per_pct_calc,
    DROP COLUMN IF EXISTS battery_used_pct_calc,
    DROP COLUMN IF EXISTS distance_traveled_km_calc;

-- +goose Down
-- Re-add the five columns and REPOPULATE them by recomputation. This is
-- possible only because all five are pure functions of columns this change
-- never touched (odometer_km, battery_level_pct, captured_date). The backfill
-- below is 20260814000001's own LAG() pass, verbatim — same subtraction order,
-- same ">0" divisor guard, same "x100" range formula, same whole-calendar-day
-- count via captured_date (DATE) subtraction.
--
-- WHAT THIS RESTORES: a correct value on every row except each vehicle's
-- oldest, which has no predecessor and correctly keeps NULL in all five.
-- WHAT THIS DOES NOT RESTORE: the literal bytes previously stored, where those
-- had diverged from a fresh recomputation. A row whose predecessor was later
-- REPLACED by a same-day re-capture (the dedupe UPSERT, 20260805000001) carried
-- values derived against a reading that no longer exists in the table; this
-- backfill produces the correct figure there, not the stale one. The rollback
-- is lossy with respect to that bug, not with respect to correct data.
ALTER TABLE vehicle_snapshots
    ADD COLUMN distance_traveled_km_calc DOUBLE PRECISION,
    ADD COLUMN battery_used_pct_calc     INTEGER,
    ADD COLUMN km_per_pct_calc           DOUBLE PRECISION,
    ADD COLUMN estimated_range_km_calc   DOUBLE PRECISION,
    ADD COLUMN days_spanned_calc         INTEGER;

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
