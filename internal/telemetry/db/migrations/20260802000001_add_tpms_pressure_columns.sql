-- internal/telemetry — add TPMS pressure typed columns to vehicle_snapshots.
-- Four nullable REAL columns: tpms_pressure_{fl,fr,rl,rr} in bar (API-native).
-- NULL semantics: NULL means the vehicle did not report TPMS at capture (no sensors,
-- absent reading) OR the row predates this extraction (pre-migration rows). A reported
-- 0.0 bar is stored non-NULL — the "NULL is reserved exclusively for pre-migration rows
-- / not reported" invariant (telemetry AGENTS.md DTO/units conventions). No backfill:
-- values remain in raw_data->vehicle_state for any row that needs retroactive extraction.
--
-- No new index: tpms_pressure_* are projected columns read alongside battery/odometer
-- on the same heap row. There is no new query predicate — no WHERE tpms_pressure_fl < X
-- or ORDER BY tpms_pressure_fl in any existing or planned query. The existing
-- idx_vehicle_snapshots_vehicle_time (account_id, tesla_id, captured_at) continues to
-- serve all read paths (design D1, no-index justification).
--
-- Owned by internal/telemetry. No cross-module FK. No raw_data change.

-- +goose Up

ALTER TABLE vehicle_snapshots
    ADD COLUMN tpms_pressure_fl REAL,    -- bar; NULL when not reported or pre-migration
    ADD COLUMN tpms_pressure_fr REAL,    -- bar; NULL when not reported or pre-migration
    ADD COLUMN tpms_pressure_rl REAL,    -- bar; NULL when not reported or pre-migration
    ADD COLUMN tpms_pressure_rr REAL;    -- bar; NULL when not reported or pre-migration

-- +goose Down

ALTER TABLE vehicle_snapshots
    DROP COLUMN IF EXISTS tpms_pressure_fl,
    DROP COLUMN IF EXISTS tpms_pressure_fr,
    DROP COLUMN IF EXISTS tpms_pressure_rl,
    DROP COLUMN IF EXISTS tpms_pressure_rr;
