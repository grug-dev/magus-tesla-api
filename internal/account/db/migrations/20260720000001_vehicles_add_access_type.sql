-- +goose Up
-- Adds access_type to the vehicles table. See design.md D1:
--   - Nullable (no DEFAULT): NULL encodes "not yet captured" honestly; a fabricated
--     default would be factually wrong for pre-existing rows.
--   - CHECK constraint (not a Postgres enum): lightweight, additive, no table rewrite.
--   - No index (design.md D2): access_type is never a WHERE/JOIN/ORDER predicate;
--     it is read as part of the heap row already located by account_id. No write
--     cost, no read benefit — index omitted.
ALTER TABLE vehicles
    ADD COLUMN access_type TEXT
    CHECK (access_type IS NULL OR access_type IN ('OWNER','DRIVER'));

-- +goose Down
ALTER TABLE vehicles DROP COLUMN IF EXISTS access_type;
