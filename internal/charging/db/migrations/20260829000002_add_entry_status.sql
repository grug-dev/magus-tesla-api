-- +goose Up
-- MAG-18 / RM33 tier 1: a manual charge entry gains a lifecycle status, an energy
-- provenance marker, and the odometer reading taken at the charge event -- and
-- energy_added_kwh stops being mandatory.
--
-- WHY EACH ADD COLUMN IS A SINGLE STATEMENT, unlike 20260720000001's two-step
-- backfill-then-constrain (roadmap D1). That migration needed two steps because its
-- backfill value ('OTHER') deliberately was NOT the column default -- a DEFAULT there
-- would have silently masked a missing field. Here the opposite is true: the chosen
-- backfill value IS the intended default in both cases ('IN_PROGRESS' for status,
-- 'USER' for energy_source), so ADD COLUMN ... NOT NULL DEFAULT ... assigns it to
-- every pre-existing row as part of the same statement. There is nothing left for a
-- separate UPDATE to do.
--
-- WHY 'IN_PROGRESS' AND NOT 'DONE' AS THE BACKFILL (roadmap D1). Every historical row
-- renders as unfinished until reviewed. The user chose this over the recommended
-- 'DONE' deliberately, as a prompt to re-check historical entries. It is not an
-- oversight; do not "correct" it.
--
-- WHY TEXT + CHECK AND NOT A POSTGRES ENUM (roadmap D1). Consistency with the
-- location_kind and charging_type columns this table already carries, and because
-- altering an enum's value set later is painful (ALTER TYPE ... ADD VALUE cannot run
-- inside a transaction block before PG12, and a value can never be removed).
--
-- COST: all three adds are METADATA-ONLY on PostgreSQL 11+. status and energy_source
-- carry a non-volatile constant DEFAULT, which since PG11 is stored in the catalogue
-- rather than written into every row; odometer_km is nullable with no default. The
-- DROP NOT NULL is a catalogue update. Each briefly takes ACCESS EXCLUSIVE. This
-- migration does NOT rewrite the table -- unlike 20260829000001, which did.

ALTER TABLE manual_charge_entries
    ADD COLUMN status TEXT NOT NULL DEFAULT 'IN_PROGRESS'
        CHECK (status IN ('IN_PROGRESS','DONE'));

ALTER TABLE manual_charge_entries
    ADD COLUMN energy_source TEXT NOT NULL DEFAULT 'USER'
        CHECK (energy_source IN ('USER','ESTIMATED'));

ALTER TABLE manual_charge_entries
    ADD COLUMN odometer_km INTEGER
        CHECK (odometer_km >= 0);

-- energy_added_kwh becomes optional (roadmap D2). FORCED, not chosen: an IN_PROGRESS
-- record legitimately has no end_battery_pct, so the derivation of D3 has no inputs
-- and no honest value exists. NULL is the honest answer; a fabricated 0 is not, and
-- would in any case violate the CHECK below.
--
-- THE EXISTING CHECK (energy_added_kwh > 0) IS DELIBERATELY KEPT. A CHECK constraint
-- evaluates to NULL -- not false -- on a NULL input, and Postgres accepts a row whose
-- CHECK is NULL. So the constraint continues to reject 0 and every negative value
-- exactly as before, while permitting the new NULL. Nothing about it needs changing,
-- and it must NOT be dropped and re-added.
--
-- THE GENERATED COLUMN inferred_capacity_kwh_calc IS NOT TOUCHED (roadmap D4). Its
-- expression continues to reference energy_added_kwh. Dropping a source column's
-- NOT NULL neither invalidates the generation expression nor triggers a rewrite:
-- Postgres re-checks a generation expression only when the expression itself or a
-- referenced column's TYPE changes, and every STORED value already written stays as
-- it is. What the column now yields for the new row shapes:
--   * energy NULL, percentages valid and increasing -> the CASE's WHEN is TRUE (it
--     tests only the two percentages -- see 20260829000001's header), so the THEN
--     branch evaluates NULL / <positive divisor>, and SQL arithmetic on NULL yields
--     NULL. The column is NULL. It is NOT an error and NOT a division by zero.
--   * energy DERIVED (energy_source = 'ESTIMATED') -> the column returns EXACTLY the
--     pack capacity constant, by algebra: (C * d/100) / (d/100) = C. This is the known,
--     accepted consequence roadmap D4 documents, and it is precisely why
--     energy_source exists: backlog #18's capacity average MUST filter
--     WHERE energy_source = 'USER', or it will average its own seed value back into
--     itself and stop converging on the pack's real capacity.
ALTER TABLE manual_charge_entries
    ALTER COLUMN energy_added_kwh DROP NOT NULL;

COMMENT ON COLUMN manual_charge_entries.status IS
    'Lifecycle state of this user-asserted charge record: IN_PROGRESS (logged at plug-in '
    'time, may lack the end-of-session facts) or DONE (complete). The required-field set is '
    'a function of this value and lives in Go, in charging.RequiredFieldsFor -- deliberately '
    'NOT as a database CHECK (roadmap D5): a CHECK backstop would turn every future change '
    'to the skip set into a migration, working against the ticket''s maintainability '
    'requirement. Pre-existing rows backfilled to IN_PROGRESS by this column''s DEFAULT, by '
    'the user''s explicit choice, so historical entries surface as unreviewed. Not indexed: '
    'nothing predicates on it.';

COMMENT ON COLUMN manual_charge_entries.energy_source IS
    'Provenance of energy_added_kwh: USER when the value came from the person, ESTIMATED '
    'when this module derived it from the pack capacity and the battery delta on write '
    '(roadmap D3/D4). Always computed by internal/charging, never accepted from a caller -- '
    'the same shape charge_sessions.battery_pct_source already uses. It exists so a future '
    'per-vehicle capacity average (backlog #18) can filter WHERE energy_source = ''USER'': '
    'inferred_capacity_kwh_calc on an ESTIMATED row returns exactly the capacity constant by '
    'algebra, so including such rows would seed that average with its own output. This fact '
    'CANNOT be reconstructed later -- once 31.00 is stored, a typed value and a derived one '
    'are indistinguishable. Not indexed: nothing predicates on it yet.';

COMMENT ON COLUMN manual_charge_entries.odometer_km IS
    'Odometer reading in kilometres observed AT this charge event -- an observation belonging '
    'to the event, not current vehicle state, which is why it lives here and not on a vehicle '
    'table (roadmap D6). INTEGER, not NUMERIC(10,1): whole kilometres are what the user reads '
    'off the dash (the user chose this over the recommended one-decimal type). The _km suffix '
    'is mandatory under the project display-unit rule (ai/go-conventions.md). NULL means not '
    'recorded. Not indexed: nothing predicates on it.';

-- +goose Down
-- DESTRUCTIVE, and it REFUSES rather than destroys where it cannot be honest.
--
-- Dropping the three columns destroys their data irrecoverably: recorded odometer
-- readings, and every energy provenance marker. status is recoverable only in the
-- trivial sense that it was uniformly IN_PROGRESS at Up time -- not after users have
-- set it. Nothing here is recomputable from the surviving columns, unlike
-- 20260829000001's Down.
--
-- Restoring energy_added_kwh's NOT NULL is impossible on any database that has since
-- accepted a NULL-energy row, and there is no honest repair: backfilling 0 violates
-- the CHECK, and any other invented number is a lie about the user's charge. So the
-- guard below aborts the rollback with an actionable message instead of deleting the
-- operator's rows or fabricating a value. Resolve those rows first, then re-run.
-- +goose StatementBegin
DO $$
DECLARE
    null_rows BIGINT;
BEGIN
    SELECT count(*) INTO null_rows
      FROM manual_charge_entries
     WHERE energy_added_kwh IS NULL;

    IF null_rows > 0 THEN
        RAISE EXCEPTION
            'cannot roll back 20260829000002: % row(s) in manual_charge_entries have a NULL '
            'energy_added_kwh, which the restored NOT NULL constraint forbids. Supply or '
            'delete those rows deliberately, then re-run this rollback. This migration will '
            'not invent an energy value or delete a user row on your behalf.', null_rows;
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE manual_charge_entries ALTER COLUMN energy_added_kwh SET NOT NULL;
ALTER TABLE manual_charge_entries DROP COLUMN IF EXISTS odometer_km;
ALTER TABLE manual_charge_entries DROP COLUMN IF EXISTS energy_source;
ALTER TABLE manual_charge_entries DROP COLUMN IF EXISTS status;
