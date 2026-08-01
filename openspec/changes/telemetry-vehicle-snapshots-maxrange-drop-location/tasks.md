# Tasks: telemetry-vehicle-snapshots-maxrange-drop-location

> Trims the `vehicle_snapshots` typed surface: ADD `max_range_charge_counter` (nullable, with a
> one-shot raw_data backfill), DROP `latitude` / `longitude` / `fast_charger_type` (all recoverable
> from `raw_data`). See `design.md` for the schema, index plan (no new index), and rationale.
> Task 1 touches `internal/tesla` and is **leader-owned cross-module integration** — the telemetry
> worker MUST NOT edit `internal/tesla`.

## 1. Tesla adapter — DTO field (leader-owned, cross-module)

- [ ] 1.1 Add an int field `MaxRangeChargeCounter` with JSON tag `max_range_charge_counter` to `ChargeStateTesla` in `internal/tesla/types.go`, so telemetry can extract it from the typed `vehicle_data` DTO. Adapter stays faithful — `Latitude`/`Longitude` remain on `DriveStateTesla`, `FastChargerType` remains on `ChargeStateTesla`. No new API call → no `raw.go`/`explore-tesla-api` change. (depends_on: none · leader-owned, outside the telemetry sandbox)

## 2. Migration + backfill (telemetry)

- [ ] 2.1 New goose migration `internal/telemetry/db/migrations/20260801000001_add_maxrange_drop_location_fastchargertype.sql`: Up = `ADD COLUMN max_range_charge_counter INTEGER` (nullable) + one-shot backfill `UPDATE … FROM raw_data->'charge_state'->>'max_range_charge_counter'` (guarded by `jsonb_typeof(...) = 'number'`) + `DROP COLUMN IF EXISTS latitude, longitude, fast_charger_type`; Down = re-add the three dropped columns (`IF NOT EXISTS`, nullable, best-effort schema-only — data not back-populated) + `DROP COLUMN IF EXISTS max_range_charge_counter`. (depends_on: none)

## 3. sqlc queries + regen (telemetry)

- [ ] 3.1 Update `internal/telemetry/db/queries.sql`: add `max_range_charge_counter` to `InsertVehicleSnapshot` params and to the SELECT projections of `LatestSnapshotsByAccount`, `SnapshotsByVehicleSince`, `ListSnapshotsByVehicle`; remove `latitude`, `longitude`, `fast_charger_type` from all of them. Then run `make sqlc` (regenerates `telemetrydb` `models.go` / `query.sql.go`). Never hand-edit generated files. (depends_on: 2.1)

## 4. Domain write + read path (telemetry)

- [ ] 4.1 `internal/telemetry/telemetry.go` `Snapshot` struct: add `MaxRangeChargeCounter *int` (nil = not reported / pre-extraction; `*0` = reported zero, per RM2 D12/DSA3), remove `Latitude float64`, `Longitude float64`, `FastChargerType *string`. (depends_on: 3.1)
- [ ] 4.2 `internal/telemetry/service.go`: `snapshotFrom` extracts `MaxRangeChargeCounter` pointer-wrapped from `data.ChargeState.MaxRangeChargeCounter` (a reported `0` becomes non-nil), and stops populating `Latitude`/`Longitude`/`FastChargerType`; update `dbStore.insertSnapshot` params (add the counter, drop the three). `pgtype` stays confined to this file. (depends_on: 4.1, 1.1)
- [ ] 4.3 `internal/telemetry/mapping.go`: read-side DB→domain map adds `max_range_charge_counter` (`pgtype.Int4` → `*int` via the RM2 `pgNullableInt32AsInt` pattern) and drops the `Latitude`/`Longitude`/`FastChargerType` rows. (depends_on: 4.1)

## 5. Fix test fixtures (telemetry)

- [ ] 5.1 Update telemetry tests that set or assert the dropped fields (`db_integration_test.go`, `db_read_integration_test.go`, `db_sourcea_integration_test.go`, and any handler-facing fixtures) — remove `Latitude`/`Longitude`/`FastChargerType` assignments and assertions; add coverage for `max_range_charge_counter` nil↔NULL fidelity and the backfill. Append-only: do not weaken existing assertions. (depends_on: 4.2, 4.3)

## 6. Verify

- [ ] 6.1 `make check` (build + vet + ui-guard + test) passes. (depends_on: 1.1, 5.1)
