-- +goose Up
-- A charge happened to a car, so tesla_id is the key. A person still typed the
-- row, and nothing else records who: tesla_id cannot say it (a car may have two
-- registered accounts) and the account's vehicle list records registration, not
-- authorship. So the column is kept and renamed rather than dropped. After this
-- migration it is authorship only -- never a key, never a predicate.
--
-- RENAME COLUMN is catalog-only: no row is rewritten, and Postgres rewrites the
-- definitions of the two indexes below to follow the new name. They are dropped
-- anyway, because they lead on a column nothing filters by any more.
ALTER TABLE charging.manual_charge_entries
    RENAME COLUMN account_id TO created_by_account_id;

COMMENT ON COLUMN charging.manual_charge_entries.created_by_account_id IS
    'Which account typed this entry. Authorship only: no query filters, joins, '
    'orders or groups by this column, and none may. Reads are per vehicle, so an '
    'entry is visible to every account registered to its car. Renamed from '
    'account_id, which used to be this table''s tenant key. NOT NULL and not a '
    'foreign key -- a cross-module FK into the account module would couple this '
    'module''s migrations to that schema.';

-- Nothing reads an account-wide charge list any more: the account-wide read
-- became a read over the account's vehicles. An index no query uses is pure
-- write and storage cost. Schema-qualified, because an index name resolves
-- through search_path and goose does not guarantee charging is on it.
DROP INDEX charging.idx_manual_charge_entries_account_time;

DROP INDEX charging.idx_manual_charge_entries_vehicle_time;

-- The same read this index always served, one column narrower: tesla_id
-- equality prunes to the car, and charged_on DESC satisfies the ORDER BY inside
-- that same scan, so the planner needs no sort step.
CREATE INDEX idx_manual_charge_entries_vehicle_time
    ON charging.manual_charge_entries (tesla_id, charged_on DESC);

-- +goose Down
-- A true undo. Nothing was deleted and no value was rewritten, so restoring the
-- catalog restores the table exactly as it was, every account id included.
DROP INDEX IF EXISTS charging.idx_manual_charge_entries_vehicle_time;

COMMENT ON COLUMN charging.manual_charge_entries.created_by_account_id IS NULL;

ALTER TABLE charging.manual_charge_entries
    RENAME COLUMN created_by_account_id TO account_id;

CREATE INDEX idx_manual_charge_entries_vehicle_time
    ON charging.manual_charge_entries (account_id, tesla_id, charged_on DESC);

CREATE INDEX idx_manual_charge_entries_account_time
    ON charging.manual_charge_entries (account_id, charged_on DESC);
