-- +goose Up

-- Step 1: Defensive backfill — set any NULL location_kind rows to 'OTHER' before
-- the NOT NULL constraint is applied. After the planned DB wipe this UPDATE matches
-- zero rows and is a no-op. It is included so the migration is safe to run against
-- any non-empty environment (a CI restore, a staging database, a developer's local
-- database that was never wiped) without failing the subsequent ALTER.
--
-- Why 'OTHER' as the backfill value: it is the most conservative choice for unknown
-- historical data. 'HOME' or 'WORK' would assert a fact not in evidence; 'OTHER'
-- explicitly signals "we do not know the location kind for this session."
--
-- Why this order (UPDATE before ALTER): Postgres evaluates the NOT NULL constraint
-- when the ALTER COLUMN ... SET NOT NULL statement executes. If any existing row has
-- location_kind IS NULL at that moment, the ALTER fails with ERROR: column
-- "location_kind" of relation "manual_charge_entries" contains null values. The UPDATE
-- must run first so the ALTER sees a column with no NULLs. Reversing the order would
-- fail on any non-empty database that has NULL rows.
UPDATE manual_charge_entries
    SET location_kind = 'OTHER'
WHERE location_kind IS NULL;

-- Step 2: Apply the NOT NULL constraint. This ALTER succeeds only because Step 1 has
-- already eliminated all NULLs. The existing CHECK (location_kind IN ('HOME','WORK','OTHER'))
-- constraint is left untouched — this migration only promotes nullability, it does not
-- alter the value constraint.
--
-- No DEFAULT is added: a DEFAULT 'OTHER' would silently fill in 'OTHER' whenever the
-- application omits location_kind, hiding a programming mistake. The intent is to fail
-- loudly when the field is missing (design D2).
ALTER TABLE manual_charge_entries
    ALTER COLUMN location_kind SET NOT NULL;

-- +goose Down

-- Reverse: drop the NOT NULL constraint, restoring the column to nullable.
-- The backfill UPDATE from the Up migration is NOT reversed — rows that were NULL
-- before the Up migration now carry 'OTHER'. This is acceptable: the Down migration
-- is a schema rollback, not a data rollback; the data change is irreversible by
-- design (we cannot know which rows were originally NULL).
ALTER TABLE manual_charge_entries
    ALTER COLUMN location_kind DROP NOT NULL;
