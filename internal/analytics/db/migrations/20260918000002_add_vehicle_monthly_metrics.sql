-- Adds analytics.vehicle_monthly_metrics: one precomputed row per
-- (tesla_id, period), the month-level counterpart of vehicle_metrics.
-- This tier fills every column through capacity_measured with a real
-- computed value; every ext_*/sc_* column and currency start at their
-- documented zero value, filled by a later change once it computes real
-- charging aggregates.

-- +goose Up

CREATE TABLE analytics.vehicle_monthly_metrics (
    id                          uuid DEFAULT gen_random_uuid() NOT NULL,
    tesla_id                    bigint NOT NULL,
    period                      date NOT NULL,

    all_distance_km             double precision NOT NULL DEFAULT 0,
    all_consumed_pct            double precision NOT NULL DEFAULT 0,
    all_km_per_pct_calc         double precision NOT NULL DEFAULT 0,
    all_day_count               integer NOT NULL DEFAULT 0,

    weekday_distance_km         double precision NOT NULL DEFAULT 0,
    weekday_consumed_pct        double precision NOT NULL DEFAULT 0,
    weekday_km_per_pct_calc     double precision NOT NULL DEFAULT 0,
    weekday_day_count           integer NOT NULL DEFAULT 0,

    weekend_distance_km         double precision NOT NULL DEFAULT 0,
    weekend_consumed_pct        double precision NOT NULL DEFAULT 0,
    weekend_km_per_pct_calc     double precision NOT NULL DEFAULT 0,
    weekend_day_count           integer NOT NULL DEFAULT 0,

    capacity_kwh                double precision NOT NULL DEFAULT 0,
    capacity_measured           boolean NOT NULL DEFAULT false,

    currency                    text NOT NULL DEFAULT 'COP',

    ext_ac_energy_kwh           double precision NOT NULL DEFAULT 0,
    ext_ac_cost                 numeric(14,2) NOT NULL DEFAULT 0,
    ext_ac_entry_count          integer NOT NULL DEFAULT 0,
    ext_ac_ending_battery_dist  jsonb NOT NULL DEFAULT '{"0-20":0,"20-40":0,"40-60":0,"60-80":0,"80-100":0}',

    ext_dc_energy_kwh           double precision NOT NULL DEFAULT 0,
    ext_dc_cost                 numeric(14,2) NOT NULL DEFAULT 0,
    ext_dc_entry_count          integer NOT NULL DEFAULT 0,
    ext_dc_ending_battery_dist  jsonb NOT NULL DEFAULT '{"0-20":0,"20-40":0,"40-60":0,"60-80":0,"80-100":0}',

    sc_energy_kwh               double precision NOT NULL DEFAULT 0,
    sc_cost                     numeric(14,2) NOT NULL DEFAULT 0,
    sc_session_count            integer NOT NULL DEFAULT 0,
    sc_ending_battery_dist      jsonb NOT NULL DEFAULT '{"0-20":0,"20-40":0,"40-60":0,"60-80":0,"80-100":0}',

    created_at                  timestamp with time zone DEFAULT now() NOT NULL,
    updated_at                  timestamp with time zone DEFAULT now() NOT NULL,

    CONSTRAINT vehicle_monthly_metrics_tesla_period_unique UNIQUE (tesla_id, period),
    CONSTRAINT vehicle_monthly_metrics_period_is_month_start CHECK (EXTRACT(day FROM period) = 1)
);

COMMENT ON TABLE analytics.vehicle_monthly_metrics IS 'One precomputed row per (tesla_id, period): the calendar month this platform derived for one vehicle. Upsert-only, no soft delete -- running the sync again for the same pair rewrites the row.';

COMMENT ON COLUMN analytics.vehicle_monthly_metrics.id IS 'Surrogate key. Never referenced by any other table.';
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.tesla_id IS 'The vehicle. No FK -- matches every other analytics table''s convention.';
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.period IS 'First day of the calendar month this row summarizes. Enforced by the CHECK; always written via date_trunc, never by the caller.';

COMMENT ON COLUMN analytics.vehicle_monthly_metrics.all_distance_km IS 'Sum of vehicle_metrics.distance_traveled_km_calc over every computable day in the month.';
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.all_consumed_pct IS 'Sum of vehicle_metrics.consumed_pct over every computable day in the month.';
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.all_km_per_pct_calc IS 'Sum of distance over sum of consumed_pct, both restricted to computable days with consumed_pct > 0. 0 when no such day exists this month.';
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.all_day_count IS 'Number of computable days in the month. 0 means nothing above can be trusted.';

COMMENT ON COLUMN analytics.vehicle_monthly_metrics.weekday_distance_km IS 'Same meaning as all_distance_km, restricted to days whose period''s date is Monday-Friday.';
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.weekday_consumed_pct IS 'Same meaning as all_consumed_pct, restricted to weekdays.';
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.weekday_km_per_pct_calc IS 'Same meaning as all_km_per_pct_calc, restricted to weekdays.';
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.weekday_day_count IS 'Same meaning as all_day_count, restricted to weekdays.';

COMMENT ON COLUMN analytics.vehicle_monthly_metrics.weekend_distance_km IS 'Same meaning as all_distance_km, restricted to Saturday/Sunday.';
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.weekend_consumed_pct IS 'Same meaning as all_consumed_pct, restricted to Saturday/Sunday.';
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.weekend_km_per_pct_calc IS 'Same meaning as all_km_per_pct_calc, restricted to Saturday/Sunday.';
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.weekend_day_count IS 'Same meaning as all_day_count, restricted to Saturday/Sunday.';

COMMENT ON COLUMN analytics.vehicle_monthly_metrics.capacity_kwh IS 'Copied from charging.MonthlyCapacityReader.CapacityForMonth. 0 whenever that port did not return a measured number. Both "no row" and "a row with no measurement" collapse to capacity_kwh = 0, capacity_measured = false -- a confirmed, not an open, design choice.';
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.capacity_measured IS 'true only when the port returned a real, non-nil kWh figure.';

COMMENT ON COLUMN analytics.vehicle_monthly_metrics.currency IS 'The reference currency all three *_cost columns are expressed in.';

COMMENT ON COLUMN analytics.vehicle_monthly_metrics.ext_ac_energy_kwh IS 'Sum of EnergyAddedKWh over this month''s external charge entries with ChargingType = AC. Zero-filled by this version; a later change computes it.';
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.ext_ac_cost IS 'Sum of Price over the same entries. Zero-filled by this version.';
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.ext_ac_entry_count IS 'Count of the same entries -- the disambiguator for the two columns above. Zero-filled by this version.';
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.ext_ac_ending_battery_dist IS 'Ending-battery-percentage distribution over the same entries, five 20-point buckets. Zero-filled by this version.';

COMMENT ON COLUMN analytics.vehicle_monthly_metrics.ext_dc_energy_kwh IS 'Same meaning as ext_ac_energy_kwh, for ChargingType = DC entries.';
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.ext_dc_cost IS 'Same meaning and type as ext_ac_cost, for ChargingType = DC entries.';
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.ext_dc_entry_count IS 'Same meaning as ext_ac_entry_count, for ChargingType = DC entries.';
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.ext_dc_ending_battery_dist IS 'Same meaning as ext_ac_ending_battery_dist, for ChargingType = DC entries.';

COMMENT ON COLUMN analytics.vehicle_monthly_metrics.sc_energy_kwh IS 'Same family as ext_ac_energy_kwh, over this month''s Supercharger sessions.';
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.sc_cost IS 'Same meaning and type as ext_ac_cost, over this month''s Supercharger sessions.';
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.sc_session_count IS 'Same family as ext_ac_entry_count, over this month''s Supercharger sessions.';
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.sc_ending_battery_dist IS 'Same family as ext_ac_ending_battery_dist, over this month''s Supercharger sessions.';

COMMENT ON COLUMN analytics.vehicle_monthly_metrics.created_at IS 'Set once, on the row''s first INSERT; never refreshed on conflict.';
COMMENT ON COLUMN analytics.vehicle_monthly_metrics.updated_at IS 'Refreshed to now() on every UPSERT, including a call that writes identical figures.';

-- +goose Down
DROP TABLE analytics.vehicle_monthly_metrics;
