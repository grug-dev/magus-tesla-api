-- +goose Up
-- inferred_capacity_kwh_calc: the pack capacity implied by one charge record, in kWh
--     inferred_capacity_kwh_calc = energy_added / ((end_battery_pct - start_battery_pct) / 100)
-- (MAG-25, charging-add-inferred-capacity). Added to BOTH tables this module owns:
-- manual_charge_entries (user-typed) and charge_sessions (the Supercharger mirror).
--
-- WHY A GENERATED COLUMN AND NOT GO (design.md D2). The ticket requires the value to be
-- current on every INSERT and every UPDATE. This module has THREE independent write paths
-- into these tables -- Writer.Create/Update, SessionWriter.MirrorSessions (whose
-- ON CONFLICT DO UPDATE SET refreshes energy_kwh nightly as fees settle), and
-- SessionVerifier.VerifySession (whose entire purpose is letting a human change the two
-- percentages this formula divides by) -- plus every write path a future change adds.
-- A Go-side computation would have to be correct in all of them, forever, and no reviewer
-- can verify that from a diff. GENERATED ALWAYS AS ... STORED makes it an engine property
-- instead: the value cannot be stale, and the column cannot be written at all (any attempt
-- fails with "column ... can only be updated to DEFAULT", SQLSTATE 428C9).
--
-- THE ALTER *IS* THE BACKFILL (design.md D9). ADD COLUMN ... GENERATED ... STORED computes
-- the value for every pre-existing row as part of this statement, so the ticket's "and
-- calculated for existing rows" needs NO separate backfill UPDATE -- and unlike
-- 20260823000001's backfill, this migration reads nothing outside internal/charging, so it
-- needs no to_regclass guard, no DO $$ block, and no cross-module ordering assumption.
-- Cost: this is a table-rewriting ALTER holding ACCESS EXCLUSIVE. Both tables hold one
-- household's charge history, so the rewrite is effectively instantaneous. Noted so a
-- reader does not mistake it for a metadata-only add.
--
-- GUARD SEMANTICS, identical on both tables (design.md D3). Computed ONLY when every input
-- is present AND end_battery_pct > start_battery_pct; NULL otherwise. The strict > is not
-- cosmetic:
--   * end = start is a DIVISION BY ZERO. Postgres raises SQLSTATE 22012 -- it does not
--     yield NULL -- so without this clause an ordinary row (plugged in at 74%, unplugged at
--     74%) would be REJECTED at INSERT. On charge_sessions that rejection aborts the whole
--     nightly MirrorSessions transaction for the account (RM29: one bad entry rejects the
--     whole call).
--   * end < start yields a NEGATIVE capacity, which is not a physical quantity. A
--     decreasing SoC across a charge record means the percentages are wrong; the honest
--     output is "unknown" = NULL, never a stored negative.
--
-- TYPE: unconstrained NUMERIC, scale pinned to 3 by ROUND(..., 3) (design.md D4). NOT
-- NUMERIC(8,3), which was the first proposal and which REJECTS LEGAL ROWS: the largest
-- value a legal manual_charge_entries row can produce is 9999.99 / 0.01 = 999999.000,
-- against that type's 99999.999 ceiling ("ERROR: numeric field overflow"). charge_sessions
-- is worse -- energy_kwh is DOUBLE PRECISION with no CHECK, mirrored from data Tesla
-- controls, so NO fixed precision is provably safe there, and one overflowing row would
-- abort the night's mirror. Unconstrained precision cannot overflow; ROUND(..., 3) still
-- pins every stored value to the three decimals the ticket's worked examples use.
-- Postgres's round(numeric, integer) rounds half AWAY FROM ZERO, not banker's rounding.
--
-- THE TWO EXPRESSIONS DIFFER IN EXACTLY TWO WAYS, both forced by the source column
-- (design.md D4), and in no other way:
--   1. the "energy IS NOT NULL" guard appears only on charge_sessions, whose energy_kwh is
--      nullable; manual_charge_entries.energy_added_kwh is NOT NULL, so the check there
--      would be dead code falsely implying nullability.
--   2. the ::NUMERIC cast on energy appears only on charge_sessions, whose energy_kwh is
--      DOUBLE PRECISION; the cast is what makes both tables expose the SAME type to
--      readers. manual_charge_entries stays in NUMERIC end to end -- no float round-trip.
--
-- NO INDEX on either column, deliberately (design.md D6): nothing filters, joins, sorts or
-- groups by this column -- every read on both tables selects the whole row and predicates
-- on account_id/tesla_id/a date, all already served by the three existing indexes. An index
-- on a projected-never-predicated column is pure write and storage cost on a path that
-- gains nothing. Same reason internal/analytics indexes none of vehicle_metrics' five _calc
-- columns. Revisit only when a consumer actually filters on it, at which point a partial
-- (account_id, tesla_id) WHERE inferred_capacity_kwh_calc IS NOT NULL is the natural shape.
--
-- NO CHECK CONSTRAINT (e.g. > 0): the expression already cannot produce a non-positive
-- value -- energy_added_kwh is CHECKed > 0, and the guard forces a positive divisor -- so a
-- constraint here would be unreachable, and on charge_sessions (whose energy_kwh carries no
-- CHECK at the source, RM29 D1: "a mirror stricter than its source could not repair a
-- source row it refuses to accept") it would be actively wrong.

ALTER TABLE manual_charge_entries
    ADD COLUMN inferred_capacity_kwh_calc NUMERIC
    GENERATED ALWAYS AS (
        CASE
            WHEN start_battery_pct IS NOT NULL
             AND end_battery_pct   IS NOT NULL
             AND end_battery_pct > start_battery_pct
            THEN ROUND(
                     energy_added_kwh
                     / ((end_battery_pct - start_battery_pct)::NUMERIC / 100),
                     3
                 )
            ELSE NULL
        END
    ) STORED;

ALTER TABLE charge_sessions
    ADD COLUMN inferred_capacity_kwh_calc NUMERIC
    GENERATED ALWAYS AS (
        CASE
            WHEN energy_kwh        IS NOT NULL
             AND start_battery_pct IS NOT NULL
             AND end_battery_pct   IS NOT NULL
             AND end_battery_pct > start_battery_pct
            THEN ROUND(
                     energy_kwh::NUMERIC
                     / ((end_battery_pct - start_battery_pct)::NUMERIC / 100),
                     3
                 )
            ELSE NULL
        END
    ) STORED;

COMMENT ON COLUMN manual_charge_entries.inferred_capacity_kwh_calc IS
    'Pack capacity in kWh implied by this entry: energy_added_kwh / ((end_battery_pct - '
    'start_battery_pct) / 100), rounded to 3 decimals. GENERATED ALWAYS AS ... STORED -- '
    'recomputed by the engine on every INSERT and UPDATE, and unwritable by any caller '
    '(design D2). NULL when either percentage is absent or when end_battery_pct is not '
    'strictly greater than start_battery_pct -- an equal delta would be a division by zero '
    'and a negative delta a negative capacity, neither of which is a physical quantity '
    '(design D3). Unconstrained NUMERIC because NUMERIC(8,3) would reject a legal '
    'max-energy/min-delta row (design D4). NOT indexed: nothing predicates on it '
    '(design D6).';

COMMENT ON COLUMN charge_sessions.inferred_capacity_kwh_calc IS
    'Pack capacity in kWh implied by this session: energy_kwh / ((end_battery_pct - '
    'start_battery_pct) / 100), rounded to 3 decimals. Same GENERATED ALWAYS AS ... STORED '
    'mechanism, same guard, and the same unconstrained NUMERIC type as '
    'manual_charge_entries.inferred_capacity_kwh_calc -- the two expressions differ only in the '
    'energy IS NOT NULL guard (energy_kwh is nullable here) and the ::NUMERIC cast '
    '(energy_kwh is DOUBLE PRECISION here), both forced by the source column (design D4). '
    'NULL additionally whenever energy_kwh is NULL, i.e. the session had no kWh fee. This '
    'column recomputes when the nightly mirror refreshes energy_kwh AND when a human '
    'corrects the percentages through SessionVerifier.VerifySession -- neither write path '
    'names this column, and neither has to (design D2).';

-- +goose Down
-- NON-DESTRUCTIVE by construction, unlike 20260823000001's Down: this column stores nothing
-- that is not fully recomputable from energy_added_kwh/energy_kwh, start_battery_pct and
-- end_battery_pct, none of which this migration touches. Re-running Up reproduces every
-- value exactly, on both tables.
ALTER TABLE charge_sessions        DROP COLUMN IF EXISTS inferred_capacity_kwh_calc;
ALTER TABLE manual_charge_entries  DROP COLUMN IF EXISTS inferred_capacity_kwh_calc;
