-- +goose Up
-- internal/telemetry — store DISPLAY units in vehicle_snapshots.
-- SUPERSEDES the "units stay API-native (miles/bar); km/PSI derived on read"
-- invariant from 20260710000002_init_telemetry.sql and 20260802000001_add_tpms_pressure_columns.sql.
-- Rationale: writes happen once per vehicle per night; reads happen on every
-- dashboard render and every history-chart bar. Converting on write and never on
-- read matches CLAUDE.md's read-heavy Performance-Profile (RM7 Decision 1).
-- Every unit-bearing column also gains a unit suffix so the schema is
-- self-describing (RM7 Decision 2). raw_data is UNTOUCHED and still holds the
-- original miles/bar payload losslessly (RM7 Decision 10).

ALTER TABLE vehicle_snapshots RENAME COLUMN battery_range          TO battery_range_km;
ALTER TABLE vehicle_snapshots RENAME COLUMN odometer               TO odometer_km;
ALTER TABLE vehicle_snapshots RENAME COLUMN tpms_pressure_fl       TO tpms_pressure_fl_psi;
ALTER TABLE vehicle_snapshots RENAME COLUMN tpms_pressure_fr       TO tpms_pressure_fr_psi;
ALTER TABLE vehicle_snapshots RENAME COLUMN tpms_pressure_rl       TO tpms_pressure_rl_psi;
ALTER TABLE vehicle_snapshots RENAME COLUMN tpms_pressure_rr       TO tpms_pressure_rr_psi;
ALTER TABLE vehicle_snapshots RENAME COLUMN inside_temp            TO inside_temp_c;
ALTER TABLE vehicle_snapshots RENAME COLUMN outside_temp           TO outside_temp_c;
ALTER TABLE vehicle_snapshots RENAME COLUMN battery_level          TO battery_level_pct;
ALTER TABLE vehicle_snapshots RENAME COLUMN usable_battery_level   TO usable_battery_level_pct;
ALTER TABLE vehicle_snapshots RENAME COLUMN charge_limit_soc       TO charge_limit_soc_pct;
ALTER TABLE vehicle_snapshots RENAME COLUMN charge_energy_added    TO charge_energy_added_kwh;
ALTER TABLE vehicle_snapshots RENAME COLUMN charger_power          TO charger_power_kw;
ALTER TABLE vehicle_snapshots RENAME COLUMN charger_voltage        TO charger_voltage_v;
ALTER TABLE vehicle_snapshots RENAME COLUMN charger_actual_current TO charger_actual_current_a;

-- One-time, in-place unit conversion of the 6 value-changing columns (RM7 Decision 3).
-- The 9 rename-only columns are already in their target unit: Tesla reports
-- temperatures in Celsius, and %, kWh, kW, V and A are API-native.
-- NULL tpms values stay NULL (NULL * factor = NULL), so the "NULL means not
-- reported / pre-migration" invariant survives untouched; a truthful 0.0
-- converts to 0.0 and stays non-NULL.
-- updated_at is deliberately NOT bumped: these rows were not re-captured, and
-- claiming they were would falsify the audit column added in 20260805000001.
UPDATE vehicle_snapshots SET
    battery_range_km     = battery_range_km     * 1.609344,
    odometer_km          = odometer_km          * 1.609344,
    tpms_pressure_fl_psi = tpms_pressure_fl_psi * 14.503773773,
    tpms_pressure_fr_psi = tpms_pressure_fr_psi * 14.503773773,
    tpms_pressure_rl_psi = tpms_pressure_rl_psi * 14.503773773,
    tpms_pressure_rr_psi = tpms_pressure_rr_psi * 14.503773773;

-- +goose Down
-- Reverses both steps. Unlike 20260805000001's dedupe, this Down IS a true
-- rollback: no row is deleted and no column is dropped, so dividing by the same
-- constants restores the original values to within float round-trip tolerance.
UPDATE vehicle_snapshots SET
    battery_range_km     = battery_range_km     / 1.609344,
    odometer_km          = odometer_km          / 1.609344,
    tpms_pressure_fl_psi = tpms_pressure_fl_psi / 14.503773773,
    tpms_pressure_fr_psi = tpms_pressure_fr_psi / 14.503773773,
    tpms_pressure_rl_psi = tpms_pressure_rl_psi / 14.503773773,
    tpms_pressure_rr_psi = tpms_pressure_rr_psi / 14.503773773;

ALTER TABLE vehicle_snapshots RENAME COLUMN charger_actual_current_a TO charger_actual_current;
ALTER TABLE vehicle_snapshots RENAME COLUMN charger_voltage_v        TO charger_voltage;
ALTER TABLE vehicle_snapshots RENAME COLUMN charger_power_kw         TO charger_power;
ALTER TABLE vehicle_snapshots RENAME COLUMN charge_energy_added_kwh  TO charge_energy_added;
ALTER TABLE vehicle_snapshots RENAME COLUMN charge_limit_soc_pct     TO charge_limit_soc;
ALTER TABLE vehicle_snapshots RENAME COLUMN usable_battery_level_pct TO usable_battery_level;
ALTER TABLE vehicle_snapshots RENAME COLUMN battery_level_pct        TO battery_level;
ALTER TABLE vehicle_snapshots RENAME COLUMN outside_temp_c           TO outside_temp;
ALTER TABLE vehicle_snapshots RENAME COLUMN inside_temp_c            TO inside_temp;
ALTER TABLE vehicle_snapshots RENAME COLUMN tpms_pressure_rr_psi     TO tpms_pressure_rr;
ALTER TABLE vehicle_snapshots RENAME COLUMN tpms_pressure_rl_psi     TO tpms_pressure_rl;
ALTER TABLE vehicle_snapshots RENAME COLUMN tpms_pressure_fr_psi     TO tpms_pressure_fr;
ALTER TABLE vehicle_snapshots RENAME COLUMN tpms_pressure_fl_psi     TO tpms_pressure_fl;
ALTER TABLE vehicle_snapshots RENAME COLUMN odometer_km              TO odometer;
ALTER TABLE vehicle_snapshots RENAME COLUMN battery_range_km         TO battery_range;
