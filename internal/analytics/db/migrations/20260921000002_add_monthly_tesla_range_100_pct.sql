-- Adds analytics.vehicle_monthly_metrics.tesla_range_100_pct_km_calc: the
-- month-level counterpart of the daily column 20260921000001 added.
--
-- This is the column the whole feature exists for. One day's value carries
-- roughly 4 km of noise, because battery_level_pct is an integer and Tesla
-- quantizes it to whole points. A month of days averages that away, so the
-- slow fall of THIS column -- not of any single day -- is the battery
-- degradation signal.
--
-- It is a RATIO OF SUMS, never an average of the daily ratios:
--   SUM(battery_range_km) / SUM(battery_level_pct) * 100
-- which is also the rule all_km_per_pct_calc already follows on this table.
-- Here it earns its place twice over. The 1-point quantization is a FIXED
-- absolute error, so a day at 99% battery is about five times more accurate
-- than a day at 20%; a ratio of sums weights each day by its own battery
-- level, which is exactly the weight that accuracy difference calls for.
-- Averaging the daily ratios would instead let the month's least reliable
-- days pull the figure around.
--
-- NOT NULL DEFAULT 0, matching every other column on this table. There is no
-- companion day-count column: unlike all_km_per_pct_calc, whose 0 is a real
-- reachable value, this figure can only be 0 if the vehicle reported zero
-- range on every single day of the month, so 0 here already means "no data"
-- with no second column needed to disambiguate it.

-- +goose Up

ALTER TABLE analytics.vehicle_monthly_metrics
    ADD COLUMN tesla_range_100_pct_km_calc double precision NOT NULL DEFAULT 0; -- delta:allow: a ratio of sums projected to 100%, not a day-over-day delta

-- COMMENT: COLUMN vehicle_monthly_metrics.tesla_range_100_pct_km_calc
-- +goose StatementBegin
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.tesla_range_100_pct_km_calc IS 'The range this vehicle would have on a FULL battery, over this whole month, from Tesla''s own reported range. SUM(vehicle_metrics.battery_range_km) divided by SUM(vehicle_metrics.battery_level_pct), times 100 -- a ratio of sums, never an average of the daily tesla_range_100_pct_km_calc values, mirroring all_km_per_pct_calc''s identical rule. The ratio of sums also weights each day by its own battery level, which is the correct weight: battery_level_pct is an integer, so its 1-point quantization is a fixed absolute error, and a day at 99% battery is roughly five times more accurate than a day at 20%. Counts EVERY day of the month that has a vehicle_metrics row with battery_level_pct > 0 -- including a predecessor-less day, which every other figure on this table skips. That is why all_day_count does NOT describe this column and must never be read as its sample size. No day-count column of its own, deliberately: 0 here can only happen if the vehicle reported zero range on every day of the month, so 0 already means "no data" unambiguously. Watch it month over month -- a steady fall is battery degradation. A single low month is not: cold weather and Tesla''s own range estimator move it too.';
-- +goose StatementEnd

-- Backfill: every pre-existing month row, from the daily table. Grouped once
-- over vehicle_metrics and joined back on (tesla_id, period), so the whole
-- backfill is one pass per table rather than one query per month row. A month
-- with no qualifying day gets no match and keeps the column's DEFAULT 0,
-- which is exactly what this column's "0 means no data" contract says.
UPDATE analytics.vehicle_monthly_metrics vmm
SET tesla_range_100_pct_km_calc = agg.range_at_full_km
FROM (
    SELECT
        tesla_id,
        date_trunc('month', metric_date)::date AS period,
        SUM(battery_range_km) / NULLIF(SUM(battery_level_pct), 0) * 100 AS range_at_full_km
    FROM analytics.vehicle_metrics
    WHERE battery_level_pct > 0
    GROUP BY tesla_id, date_trunc('month', metric_date)::date
) agg
WHERE agg.tesla_id = vmm.tesla_id
  AND agg.period = vmm.period
  AND agg.range_at_full_km IS NOT NULL;

-- +goose Down
-- Drops the column only; never un-backfills, mirroring this module's other
-- column migrations.
ALTER TABLE analytics.vehicle_monthly_metrics
    DROP COLUMN tesla_range_100_pct_km_calc;
