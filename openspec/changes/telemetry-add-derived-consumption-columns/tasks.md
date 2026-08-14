> **DB-touching change — read design.md before starting.** This adds five nullable derived
> columns to `vehicle_snapshots` (`distance_traveled_km_calc`, `battery_used_pct_calc`,
> `km_per_pct_calc`, `estimated_range_km_calc`, `days_spanned_calc`), backfills all existing
> rows in the same migration via a `LAG()` window pass, adds a new `PreviousSnapshotForVehicle`
> query, and computes the five values in Go via a new pure `deriveConsumption(prev, cur)`
> function wired into `attemptVehicle` before every insert. `InsertVehicleSnapshot`'s
> existing `ON CONFLICT ... DO UPDATE` is extended to refresh the five columns on a same-day
> re-capture. Entire surface is `internal/telemetry/` — no other module, no `cmd/` file.
>
> **Dependencies / parallelism:**
>
> - T1 (migration + backfill) has no dependencies.
> - T2 (`Snapshot` struct fields + `store` interface method signature) has no dependencies;
>   may run in parallel with T1.
> - T3 (`service.go`: `dayStart`, `deriveConsumption`, `dbStore.previousSnapshot`,
>   `dbStore.insertSnapshot` params, `attemptVehicle` wiring) depends on T2. Disjoint from
>   T1's file until T3's `dbStore.insertSnapshot`/`previousSnapshot` bodies, which need the
>   sqlc-generated params from T4 (see T3.4/T3.5 below).
> - T4 (sqlc query.sql edits + regenerate) depends on T1.
> - T5 (`mapping.go`: `rowToSnapshot`) depends on T3, T4.
> - T6 (new offline unit tests: `deriveConsumption`, `dayStart`) depends on T3.
> - T7 (new/updated DB integration tests) depends on T1, T4, T5.
> - T8 (`AGENTS.md` documentation) depends on T1.
> - Verification (V) depends on all tasks.
>
> **Leader-integrated step:** run `make sqlc` after T4.1 (query.sql edits) to regenerate
> `telemetrydb`. The existing `sql:` entry in `sqlc.yaml` already covers the telemetry
> module; no structural `sqlc.yaml` change is needed.

---

## T1. Goose migration + backfill (`internal/telemetry/db/migrations/`) — no dependencies

- [x] T1.1 Create
      `internal/telemetry/db/migrations/20260814000001_add_derived_consumption_columns_vehicle_snapshots.sql`
      with the exact DDL from design.md's "Schema" section: Up adds the five nullable
      columns (`distance_traveled_km_calc DOUBLE PRECISION`, `battery_used_pct_calc
      INTEGER`, `km_per_pct_calc DOUBLE PRECISION`, `estimated_range_km_calc DOUBLE
      PRECISION`, `days_spanned_calc INTEGER`), then runs the single `LAG()`-window
      backfill `UPDATE` exactly as specified (partition by `(account_id, tesla_id)`, order
      by `captured_at`; `km_per_pct_calc`/`estimated_range_km_calc` NULL unless the battery
      divisor is `> 0`; `days_spanned_calc` via `captured_date - prev_captured_date`; rows
      with no predecessor excluded from the UPDATE and left NULL). Down drops all five
      columns. Include the full header comments from design.md (why these five columns, the
      one-time-backfill framing mirroring `20260805000001`, the "no unit conversion needed
      here" note). Do not deviate from the exact column names or formulas specified in
      design.md.
      Acceptance: `goose status` (or `make migrate-up`) shows the migration applied cleanly
      against the live DB; after applying, every existing row except each vehicle's
      earliest row has all five columns non-NULL, and each vehicle's earliest row has all
      five columns NULL; spot-check at least one row's backfilled values by hand against its
      predecessor's `odometer_km`/`battery_level_pct`/`captured_date`; `goose down` (one
      step) removes all five columns without error.

## T2. `Snapshot` struct + `store` interface signature (`internal/telemetry/telemetry.go`) — no dependencies

- [x] T2.1 Add the five new pointer fields to `Snapshot` (`DistanceTraveledKmCalc *float64`,
      `BatteryUsedPctCalc *int`, `KmPerPctCalc *float64`, `EstimatedRangeKmCalc *float64`,
      `DaysSpannedCalc *int`), placed after the existing TPMS fields, with the doc comments
      from design.md D9 (NULL means: no predecessor, or — for the two efficiency fields
      only — a zero/negative battery-used divisor; a genuine zero is always non-NULL).
      Acceptance: `go build ./...` green; existing named-field `Snapshot{...}` literals
      across the codebase remain compile-compatible (additive fields).

- [x] T2.2 Add `previousSnapshot(ctx context.Context, accountID uuid.UUID, teslaID int64,
      before time.Time) (*Snapshot, error)` to the unexported `store` interface
      (`service.go`), with the doc comment from design.md D7/D8 (returns `nil, nil` when no
      predecessor exists — not an error).
      Acceptance: `go build ./...` fails until every `store` implementer (the production
      `dbStore` in T3, and any test fake) implements it — this is expected and tracked by
      T3/T6/T7, not a defect in this task.

## T3. `service.go`: derivation + write-path wiring — depends on T2

- [ ] T3.1 Add the `dayStart(t time.Time, loc *time.Location) time.Time` pure function,
      mirroring `dateOnly` but returning the **local-zone midnight instant** (`time.Date(y,
      m, d, 0, 0, 0, 0, loc)`, no UTC normalization), per design.md D7. Place near
      `dateOnly`.
      Acceptance: `go build ./...` green; directly unit-testable with no DB/network
      dependency (see T6.2).

- [ ] T3.2 Add the `deriveConsumption(prev *Snapshot, cur Snapshot) Snapshot` pure function
      exactly as specified in design.md D8: `prev == nil` returns `cur` unchanged (all five
      fields stay nil); otherwise computes `DistanceTraveledKmCalc`,
      `BatteryUsedPctCalc`, `DaysSpannedCalc` unconditionally, and `KmPerPctCalc` /
      `EstimatedRangeKmCalc` only when `BatteryUsedPctCalc > 0`. `DaysSpannedCalc` uses
      `cur.CapturedDate.Sub(prev.CapturedDate).Hours() / 24` (an exact integer since
      `CapturedDate` values are UTC-midnight-normalized).
      Acceptance: `go build ./...` green; directly unit-testable with no DB/network
      dependency (see T6.1) — this is the primary "Unit tests: included" surface for this
      change.

- [ ] T3.3 Implement `dbStore.previousSnapshot` (depends on T4.1's `PreviousSnapshotForVehicle`
      query existing and `make sqlc` having run): calls
      `q.PreviousSnapshotForVehicle`, maps a `pgx.ErrNoRows` (or the sqlc-generated
      not-found sentinel) to `(nil, nil)`, otherwise maps the row via the existing shared
      `rowToSnapshot` (once T5 adds the five-field mapping) and returns it. Bind `before`
      via the existing `timestamptzFrom` helper — no new pgtype boundary helper needed.
      Acceptance: `go build ./...` green once T4 has run; behavior verified by T7.2.

- [ ] T3.4 Extend `dbStore.insertSnapshot` to pass the five new
      `InsertVehicleSnapshotParams` fields, reusing the **existing**
      `float64PtrToPgFloat8` helper (for the three `DOUBLE PRECISION` columns) and the
      **existing** `intPtrToPgInt4` helper (for the two `INTEGER` columns) — both already
      defined in `service.go` for the Source A charge-enrichment columns. Do not write a new
      pgtype-boundary helper. Depends on T4.1's `InsertVehicleSnapshot` column additions
      having run through `make sqlc`.
      Acceptance: `InsertVehicleSnapshotParams` has the five new fields; `dbStore.insertSnapshot`
      assigns all of them; `go build ./...` green.

- [ ] T3.5 Wire `attemptVehicle` per design.md's "Wiring" section: after `snapshotFrom`
      builds `snap`, call `s.store.previousSnapshot(ctx, v.AccountID, v.TeslaID,
      dayStart(s.now(), s.location()))`; on error, return `(s.logAPIError(v.TeslaID,
      "previousSnapshot", err, ReasonAPIError), vehicleConfig{})` (same error-containment
      shape as the existing `insertSnapshot` failure path — mapped to `ReasonAPIError`,
      retried once by `collectVehicle`'s existing retry loop, no new Reason). On success,
      set `snap = deriveConsumption(prev, snap)` before calling `s.store.insertSnapshot`.
      Acceptance: `go build ./...` and `go vet ./...` green; a `previousSnapshot` failure
      never silently proceeds as "no predecessor" (verified by T7 — a forced store error
      must not zero out a real vehicle's derived columns).

## T4. sqlc query edits + regenerate (`internal/telemetry/db/query.sql`) — depends on T1

- [ ] T4.1 Add the five new columns to `InsertVehicleSnapshot`'s `INSERT` column list and
      `VALUES` (immediately after `captured_date`, matching the migration's physical
      column-append order) and to its `ON CONFLICT ... DO UPDATE SET` clause (design.md D6
      — do NOT skip the `DO UPDATE SET` additions; this is the fix for the same-day
      re-capture staleness bug). Add the five columns to the end of the explicit `SELECT`
      column lists in `ListSnapshotsByVehicle`, `SnapshotsByVehicleSince`,
      `SnapshotsByVehicleBetween`, and `LatestSnapshotsByAccount` (same append-order
      convention). Add the new `PreviousSnapshotForVehicle :one` query exactly as specified
      in design.md's "Write Path" section (backward scan on the existing
      `idx_vehicle_snapshots_vehicle_time` index — cite this in the query's header comment,
      no new index). Update `InsertVehicleSnapshot`'s header comment to mention the five new
      columns and cite design D3/D6/D8.
      After editing, the leader runs `make sqlc` to regenerate `telemetrydb`.
      Acceptance: `query.sql` compiles (goose/sqlc can parse it); after `make sqlc`,
      `telemetrydb.InsertVehicleSnapshotParams` has the five new fields;
      `telemetrydb.VehicleSnapshot` (the shared struct across the four read queries) has the
      five new fields; `telemetrydb.PreviousSnapshotForVehicleParams`/`Row` types exist.

## T5. `mapping.go`: `rowToSnapshot` — depends on T3, T4

- [ ] T5.1 Extend `rowToSnapshot` in `internal/telemetry/mapping.go` to map the five new
      fields using the **existing** `pgNullableFloat64` (for the three `DOUBLE PRECISION`
      columns) and `pgNullableInt32AsInt` (for the two `INTEGER` columns) helpers — both
      already defined in `mapping.go` for the Source A charge-enrichment fields. Do not
      write a new mapping helper.
      Acceptance: `go build ./...` and `go vet ./...` green after `make sqlc`; a snapshot
      round-tripped through `insertSnapshot` → `ListSnapshotsByVehicle`/`rowToSnapshot`
      carries the same five derived values it was written with (verified by T7).

## T6. New offline unit tests — depends on T3

- [ ] T6.1 Add table-driven unit tests for `deriveConsumption` in a new file,
      `internal/telemetry/consumption_test.go` (no DB, no network — pure function tests).
      Cover, at minimum: (a) normal drive day — positive distance, positive battery used,
      both efficiency fields populated with the expected values (mirror the ticket's
      worked example: 40 km / 12% → ~3.33 km/pct → ~333 km estimated range); (b) charging
      day — negative `BatteryUsedPctCalc`, both efficiency fields nil, distance still
      populated; (c) parked/zero-delta day — `BatteryUsedPctCalc == 0`, both efficiency
      fields nil; (d) multi-day gap — `DaysSpannedCalc > 1`, distance/battery-used store the
      full multi-day total, not a per-day average; (e) `prev == nil` (first-ever snapshot)
      — all five fields nil.
      Acceptance: `go test ./internal/telemetry/...` passes; tests are fast (no DB, no
      network, no live Tesla API call).

- [ ] T6.2 Add unit tests for `dayStart` in the same file or alongside the existing
      `dateOnly` tests in `dedupe_test.go`: (a) a capture comfortably inside a calendar day
      in a non-UTC zone (e.g. `America/Bogota`, UTC-5) returns that day's local midnight as
      an absolute instant; (b) a capture whose UTC instant is on one calendar day but whose
      local instant (negative-offset zone) is the previous calendar day returns the LOCAL
      day's midnight, not the UTC day's — the same local-vs-UTC distinction `dateOnly`'s own
      tests already cover, applied to `dayStart`'s different return shape (an absolute
      instant, not a UTC-normalized calendar date).
      Acceptance: `go test ./internal/telemetry/...` passes; no DB, no network.

## T7. New/updated DB integration tests — depends on T1, T4, T5

- [ ] T7.1 Add `TestStore_PreviousSnapshot_RoundTrips` (or similarly named) in a DB
      integration test file: insert two snapshots for a vehicle on two different days;
      assert `previousSnapshot(ctx, accountID, teslaID, dayStart(secondCapturedAt, loc))`
      returns the first snapshot; assert `previousSnapshot` for a vehicle with only one
      stored snapshot, queried with a `before` after that snapshot's `captured_at`, still
      returns it; assert `previousSnapshot` for a vehicle with NO stored snapshots returns
      `nil, nil` (design D8/D10 — not an error).
      Acceptance: `go test ./internal/telemetry/...` (DB-gated) passes.

- [ ] T7.2 Add `TestStore_SnapshotUpsert_RecapturesRecomputeDerivedColumns` (or similarly
      named): insert a day-0 predecessor snapshot; insert a day-1 snapshot (computed
      correctly against day-0 via the real `attemptVehicle`-style flow, or by calling
      `deriveConsumption` directly and inserting the result); insert a SECOND day-1
      snapshot with different `OdometerKm`/`BatteryLevelPct` (the same-day re-capture,
      `ON CONFLICT DO UPDATE`). Assert the resulting single day-1 row's five derived columns
      reflect a recompute against the DAY-0 predecessor (not the first day-1 capture that
      was just replaced) and match the SECOND capture's readings — this is the direct
      regression test for design.md's D7 same-day-recapture refinement and the D6/L1
      staleness fix.
      Acceptance: `go test ./internal/telemetry/...` (DB-gated) passes.

- [ ] T7.3 Add a round-trip test confirming a snapshot inserted with all five derived
      fields set (including negative `BatteryUsedPctCalc` and nil efficiency fields, per
      DU2) comes back identical through `ListSnapshotsByVehicle`/`rowToSnapshot` — verifies
      T5.1's mapping and the pgtype nullable round-trip (mirrors the module's existing
      sentry/TPMS/Source-A round-trip test precedent).
      Acceptance: `go test ./internal/telemetry/...` (DB-gated) passes.

## T8. `internal/telemetry/AGENTS.md` documentation — depends on T1

- [ ] T8.1 Add the five new columns to the "Data ownership" `vehicle_snapshots` bullet list
      (column names, types, nullability) and add a new bullet under "DTO / units
      conventions" documenting: the NULL convention (no predecessor, or a non-positive
      battery-used divisor for the two efficiency fields only); that these are computed in
      Go via `deriveConsumption` at write time, never on read; that the migration backfilled
      all pre-existing rows in the same schema change; and a one-line pointer to
      `telemetry-add-derived-consumption-columns` as the change that introduced them
      (mirroring how the file already cites other introducing changes by name).
      Acceptance: the section reads correctly on its own — a future worker/agent reading
      only `AGENTS.md` understands what the five columns mean and where they come from
      without needing to open this change's design.md.

---

## Verification — depends on all tasks

- [ ] V1. `go build ./...` and `go vet ./...` pass after all tasks are complete.
- [ ] V2. `go test ./...` green and fast. DB integration tests self-skip without
      `DATABASE_URL`; with Docker the testcontainers helper provisions Postgres and applies
      goose migrations automatically, including the new
      `20260814000001_add_derived_consumption_columns_vehicle_snapshots.sql`. NO Tesla API
      call fires.
- [ ] V3. Normal-day correctness: verified by T6.1(a) and, at the DB layer, T7.3.
- [ ] V4. Charging-day / parked-day NULL-ratio correctness (DU2): verified by T6.1(b)/(c).
- [ ] V5. Multi-day-gap correctness (DU1): verified by T6.1(d).
- [ ] V6. First-ever-snapshot correctness (L5): verified by T6.1(e) and T7.1.
- [ ] V7. Same-day-recapture refresh correctness (L1, and the D7 refinement it requires):
      verified by T7.2.
- [ ] V8. Backfill correctness (DU4): verified by T1.1's acceptance criteria (spot-check
      against `deriveConsumption`'s formula by hand).
- [ ] V9. Index plan confirmed no new index was added and none is needed: grep the final
      migration diff for `CREATE INDEX` (expect zero hits) and confirm
      `PreviousSnapshotForVehicle`'s query plan (`EXPLAIN`) uses
      `idx_vehicle_snapshots_vehicle_time` as a backward scan, not a sequential scan.
- [ ] V10. Boundary check: `internal/telemetry` still imports only `account` + `tesla`
      public packages; no other module's internals; `pgtype` does not appear in any public
      type or interface (`previousSnapshot`'s pgtype handling stays confined to
      `service.go`/`mapping.go`). No file outside `internal/telemetry/` was touched.
      `openspec/changes/gateway-battery-consumed-chart/` was not read, referenced, or
      modified.
- [ ] V11. Docs: `internal/telemetry/AGENTS.md` accurately reflects the five new columns
      (T8.1). Root `README.md` "Project Structure"/"Architecture" confirmed NOT to need
      changes (no module added/removed, no new runnable) — this confirmation itself is part
      of verification, not an assumption to skip.
- [ ] V12. `openspec validate telemetry-add-derived-consumption-columns --strict` passes.
