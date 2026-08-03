-- +goose Up
-- Adds exterior_color and car_type to the vehicles table. See design.md D1:
--   - Nullable (no DEFAULT): NULL encodes "not yet captured" honestly. Tesla's
--     ListVehicles (what seeds a vehicle) does not return vehicle_config, so
--     every row starts NULL until the nightly telemetry collector back-fills it.
--   - No CHECK constraint (deliberate divergence from access_type): these are
--     open-ended Tesla enums (new paint names / model codes ship regularly),
--     unlike access_type's closed OWNER/DRIVER vocabulary. A CHECK ... IN (...)
--     here would need updating every time Tesla ships a new SKU, for no domain
--     logic that branches on validity.
--   - No index (design.md D2): neither column is ever a WHERE/JOIN/ORDER BY
--     predicate in any existing or planned query; both are read as part of the
--     heap row already located by the existing UNIQUE (account_id, tesla_id)
--     constraint's index.
ALTER TABLE vehicles
    ADD COLUMN exterior_color TEXT,
    ADD COLUMN car_type TEXT;

-- +goose Down
ALTER TABLE vehicles
    DROP COLUMN IF EXISTS exterior_color,
    DROP COLUMN IF EXISTS car_type;
