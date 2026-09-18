-- Makes AC the default charging type on charging.manual_charge_entries.
--
-- Two parts, and they cover two different populations:
--
--   1. The backfill fixes the rows that already exist. The column has been
--      nullable since the baseline and the /external-charges form offered an
--      empty "-" option, so most historical entries carry NULL. The user's
--      decision is that those are AC: a manual charge entry is a home, work
--      or third-party charge the person typed in by hand, and those are AC
--      in practice -- a DC fast charge shows up in the Supercharger mirror,
--      not here.
--
--   2. SET DEFAULT covers future INSERTs that omit the column. It does NOT
--      cover the gateway, whose sqlc INSERT always names charging_type
--      explicitly, so the DEFAULT never fires for it. The gateway side is
--      handled in the form instead: the "-" option was removed from the
--      charging_type <select> on both the create form and the inline row
--      editor, so a submitted entry is always AC or DC.
--
-- The column stays NULLABLE on purpose. charging.Writer takes a *string, so
-- a NOT NULL here would turn a nil from any future non-gateway caller into a
-- 500 instead of the NULL it means today. Tightening that is a separate
-- decision with its own code change.

-- +goose Up

-- Backfill first, DEFAULT second: SET DEFAULT is catalog-only and does not
-- touch existing rows, so the order does not matter for correctness -- but
-- doing the data first keeps the "what fixed the old rows" answer in one
-- statement.
UPDATE charging.manual_charge_entries
SET charging_type = 'AC'
WHERE charging_type IS NULL;

ALTER TABLE charging.manual_charge_entries
    ALTER COLUMN charging_type SET DEFAULT 'AC';

-- COMMENT: COLUMN manual_charge_entries.charging_type
-- +goose StatementBegin
COMMENT ON COLUMN charging.manual_charge_entries.charging_type IS 'How this charge was delivered: AC (home, work or third-party wall charging) or DC (fast charging), constrained by manual_charge_entries_charging_type_check. DEFAULT ''AC'' -- a manual entry is a charge the person typed in by hand, which is AC in practice, since a DC Supercharger session arrives through the telemetry mirror instead. The DEFAULT is a backstop only: the gateway INSERT names this column explicitly, so what actually makes new entries AC is the /external-charges form, whose charging_type select offers only AC and DC with AC first. Pre-existing NULL rows were backfilled to AC by this migration, by the user''s explicit choice -- after that backfill a backfilled row and a user-chosen AC row are indistinguishable, which is accepted. Still NULLABLE: charging.Writer takes a *string and a NOT NULL would turn a nil from a future non-gateway caller into an error. Not indexed: nothing predicates on it.';
-- +goose StatementEnd

-- +goose Down
-- Drops the DEFAULT only. The backfill is NOT reversed -- once a row reads
-- 'AC' there is no way to tell a backfilled row from one the user chose, so
-- un-backfilling would blank real data. Mirrors this module's other
-- backfilling migrations (status, price_source, start_battery_source), which
-- reverse schema and never data.
ALTER TABLE charging.manual_charge_entries
    ALTER COLUMN charging_type DROP DEFAULT;
