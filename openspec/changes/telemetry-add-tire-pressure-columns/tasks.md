> **Additive, non-breaking column-extraction change.** Four nullable `REAL` columns on
> `vehicle_snapshots`; four `*float64` fields + `barToPSI` constant + four nil-safe PSI
> companion methods on `telemetry.Snapshot`; `snapshotFrom` write-path extension; sqlc
> query projection updates; `rowToSnapshot` read-path wiring. No new read method, no new
> index, no gateway change (D2/D3 out of scope).
>
> **Dependencies / parallelism:**
>
> - T1 (Tesla adapter DTO enrichment) is a prerequisite for T3, T4, T5 — it must land first.
> - T2 (goose migration) has no dependencies; may run in parallel with T1.
> - T3 (Snapshot struct + barToPSI + companion methods + unit tests) depends on T1
>   (for `data.VehicleState.TpmsPressureFL` to exist in Go) but is otherwise disjoint
>   from T2 and T4. May be authored in parallel with T2.
> - T4 (snapshotFrom + dbStore.insertSnapshot) depends on T1, T2, T3.
> - T5 (sqlc query edits + regenerate + rowToSnapshot) depends on T2, T4.
> - T6 (integration tests) depends on T2, T5.
>
> **Leader-integrated step:** run `make sqlc` after T5.1 (query.sql edits) to regenerate
> `telemetrydb`. The existing `sql:` entry in `sqlc.yaml` already covers the telemetry
> module; no structural sqlc.yaml change is needed.

---

## T1. Tesla adapter DTO enrichment (`internal/tesla/types.go`) — no dependencies

- [ ] T1.1 Add four plain `float64` fields to `VehicleStateTesla` in
      `internal/tesla/types.go` (design D0). Fields:
      `TpmsPressureFL float64 \`json:"tpms_pressure_fl"\``,
      `TpmsPressureFR float64 \`json:"tpms_pressure_fr"\``,
      `TpmsPressureRL float64 \`json:"tpms_pressure_rl"\``,
      `TpmsPressureRR float64 \`json:"tpms_pressure_rr"\``.
      Add a doc comment block: "TPMS (tire-pressure monitoring system) pressures in
      bar — API-native. Plain float64 (not pointer) in the DTO: the Fleet API includes
      these in vehicle_state when the vehicle has TPMS sensors. snapshotFrom
      pointer-wraps them via ptr() so a reported 0.0 bar is stored non-NULL and
      pre-migration rows stay NULL (D12/DSA3 convention)."
      Place the block immediately after `CarVersion` (the last existing field), before
      the closing brace of `VehicleStateTesla`.
      **No new `VehicleService` interface method.** This is a leaf additive DTO field
      addition only — the existing `VehicleData` call path already returns
      `VehicleStateTesla` with the full `vehicle_state` payload; adding JSON fields
      to the struct is sufficient.
      **No `Raw*` method or `explore-tesla-api` surface update.** The sync rule
      applies only to new `VehicleService` port methods, not to DTO field additions.
      Acceptance: `go build ./...` passes with the four new fields in place; `go vet
      ./...` clean. The existing `VehicleData` caller in `internal/telemetry` gains
      access to these fields without any interface change.

## T2. Goose migration (`internal/telemetry/db/migrations/`) — no dependencies

- [ ] T2.1 Create `internal/telemetry/db/migrations/20260802000001_add_tpms_pressure_columns.sql`
      with the exact DDL from design.md (reproduced here for implementer convenience):

      ```sql
      -- +goose Up
      ALTER TABLE vehicle_snapshots
          ADD COLUMN tpms_pressure_fl REAL,
          ADD COLUMN tpms_pressure_fr REAL,
          ADD COLUMN tpms_pressure_rl REAL,
          ADD COLUMN tpms_pressure_rr REAL;

      -- +goose Down
      ALTER TABLE vehicle_snapshots
          DROP COLUMN IF EXISTS tpms_pressure_fl,
          DROP COLUMN IF EXISTS tpms_pressure_fr,
          DROP COLUMN IF EXISTS tpms_pressure_rl,
          DROP COLUMN IF EXISTS tpms_pressure_rr;
      ```

      Include the full header comment from design.md (NULL semantics, no-index
      justification, ownership). No DEFAULT, no NOT NULL, no CHECK constraint.
      Acceptance: `goose status` shows the migration as applied when `make migrate-up`
      is run; no existing rows fail; `goose down` (one step) removes the four columns
      without error.

## T3. `Snapshot` struct + `barToPSI` + PSI companions + unit tests (`internal/telemetry/telemetry.go`) — depends on T1

- [ ] T3.1 Add the `barToPSI` constant to `internal/telemetry/telemetry.go`, immediately
      after the `milesToKm` constant:
      ```go
      // barToPSI is the exact bar→PSI conversion factor. Every bar field on a domain
      // type exposes a companion *float64 value-receiver method (ai/go-conventions.md
      // non-negotiable). Tesla sends tire pressure in bar; PSI is derived on read,
      // never stored as a column or a struct field.
      const barToPSI = 14.503773773
      ```
      Acceptance: `go build ./...` green; constant is exported and accessible to callers
      that need to verify the conversion factor in tests.

- [ ] T3.2 Add four `*float64` pointer fields to `Snapshot`, immediately after
      `MaxRangeChargeCounter` (the last existing field). Doc comment block per design D1:
      ```go
      // TPMS (tire-pressure monitoring system) pressure fields in bar (API-native).
      // nil when the vehicle did not report TPMS at capture (no sensors, absent
      // reading) OR the row predates this extraction (pre-migration). A truthfully
      // reported 0.0 bar is stored non-NULL (pointer-wrapped via ptr() in snapshotFrom —
      // D12/DSA3 convention). Use the companion PSI() methods for display in PSI.
      // NULL is reserved exclusively for pre-migration rows / not reported.
      TpmsPressureFL *float64 // bar — see TpmsPressureFLPSI
      TpmsPressureFR *float64 // bar — see TpmsPressureFRPSI
      TpmsPressureRL *float64 // bar — see TpmsPressureRLPSI
      TpmsPressureRR *float64 // bar — see TpmsPressureRRPSI
      ```
      Acceptance: existing `Snapshot` struct literals that do not name these fields
      remain compile-compatible (additive named fields, zero value is nil).

- [ ] T3.3 Add four nil-safe value-receiver PSI companion methods on `Snapshot`, placed
      after `OdometerKm()` (the existing last companion). One per corner:
      ```go
      // TpmsPressureFLPSI returns the front-left tire pressure converted from bar to
      // PSI. Returns nil when TpmsPressureFL is nil (not reported / pre-migration row).
      func (s Snapshot) TpmsPressureFLPSI() *float64 {
          if s.TpmsPressureFL == nil {
              return nil
          }
          return ptr(*s.TpmsPressureFL * barToPSI)
      }
      ```
      Repeat for FR, RL, RR (naming: `TpmsPressureFRPSI`, `TpmsPressureRLPSI`,
      `TpmsPressureRRPSI`). Acceptance: methods compile; nil-in returns nil; non-nil
      0.0-in returns non-nil *0.0 (not nil — `ptr(0.0 * barToPSI)` is `ptr(0.0)` which
      is non-nil, representing a truthfully reported zero pressure).

- [ ] T3.4 Add unit tests for the four PSI companion methods in an appropriate
      `*_test.go` file under `internal/telemetry/`. Tests must NOT make any live Tesla
      API call or require `DATABASE_URL`. Cover:
      (a) nil input → nil output for each of the four companions.
      (b) A known non-nil bar value → the expected PSI value (e.g. 2.5 bar *
          14.503773773 ≈ 36.259 PSI); assert within a small float tolerance (1e-6).
      (c) 0.0 bar → non-nil *0.0 PSI (truthful zero, not nil).
      Acceptance: `go test ./internal/telemetry/...` passes; tests are fast (no DB,
      no network).

## T4. `snapshotFrom` + `dbStore.insertSnapshot` (`internal/telemetry/service.go`) — depends on T1, T2, T3

- [ ] T4.1 Extend `snapshotFrom` in `service.go` to map the four new TPMS DTO fields
      into the `Snapshot`, immediately after the `MaxRangeChargeCounter` line:
      ```go
      // TPMS pressure enrichment — actual DTO values, pointer-wrapped (D12/DSA3).
      // ptr(v) returns &v; a 0.0 bar is a truthful reading and is stored non-NULL.
      // NULL is reserved for pre-migration rows (values remain in raw_data).
      TpmsPressureFL: ptr(data.VehicleState.TpmsPressureFL),
      TpmsPressureFR: ptr(data.VehicleState.TpmsPressureFR),
      TpmsPressureRL: ptr(data.VehicleState.TpmsPressureRL),
      TpmsPressureRR: ptr(data.VehicleState.TpmsPressureRR),
      ```
      Acceptance: `go build ./...` green; `go vet ./...` clean.

- [ ] T4.2 Extend `dbStore.insertSnapshot` in `service.go` to pass the four new fields
      to `InsertVehicleSnapshotParams`. Map `*float64 → pgtype.Float8` using the
      existing `float64PtrToPgFloat8` helper (already in `service.go`):
      ```go
      TpmsPressureFL: float64PtrToPgFloat8(s.TpmsPressureFL),
      TpmsPressureFR: float64PtrToPgFloat8(s.TpmsPressureFR),
      TpmsPressureRL: float64PtrToPgFloat8(s.TpmsPressureRL),
      TpmsPressureRR: float64PtrToPgFloat8(s.TpmsPressureRR),
      ```
      This step depends on T5.1 (the sqlc query must include the new columns before
      `make sqlc` regenerates the params struct). Implement after T5.1 is done and
      `make sqlc` has run.
      Acceptance: `InsertVehicleSnapshotParams` has the four new `pgtype.Float8`
      fields; `dbStore.insertSnapshot` assigns them; `go build ./...` green.

## T5. sqlc query edits + regenerate + `rowToSnapshot` (`internal/telemetry/db/query.sql`, `mapping.go`) — depends on T2, T4.1

- [ ] T5.1 Edit `internal/telemetry/db/query.sql` to project the four new columns in all
      four relevant queries. For each query, add the four column names to the explicit
      SELECT list (or the INSERT/VALUES lists for `InsertVehicleSnapshot`):

      **`InsertVehicleSnapshot`** — add to the INSERT column list:
      `tpms_pressure_fl, tpms_pressure_fr, tpms_pressure_rl, tpms_pressure_rr`
      and to the VALUES clause:
      `@tpms_pressure_fl, @tpms_pressure_fr, @tpms_pressure_rl, @tpms_pressure_rr`

      **`ListSnapshotsByVehicle`** — add to SELECT list:
      `tpms_pressure_fl, tpms_pressure_fr, tpms_pressure_rl, tpms_pressure_rr`

      **`SnapshotsByVehicleSince`** — add to SELECT list:
      `tpms_pressure_fl, tpms_pressure_fr, tpms_pressure_rl, tpms_pressure_rr`

      **`LatestSnapshotsByAccount`** — add to SELECT list:
      `tpms_pressure_fl, tpms_pressure_fr, tpms_pressure_rl, tpms_pressure_rr`

      Update the comment header on `InsertVehicleSnapshot` to mention the four new
      TPMS columns (NULL semantics, D12/DSA3 convention, no new index).
      After editing, the leader runs `make sqlc` to regenerate `telemetrydb`.
      Acceptance: `query.sql` compiles (goose can parse it); after `make sqlc`, the
      generated `telemetrydb.InsertVehicleSnapshotParams` and
      `telemetrydb.VehicleSnapshot` structs include the four `pgtype.Float8` fields.

- [ ] T5.2 Extend `rowToSnapshot` in `internal/telemetry/mapping.go` to map the four
      new nullable sqlc columns to `Snapshot` pointer fields using the existing
      `pgNullableFloat64` helper:
      ```go
      TpmsPressureFL: pgNullableFloat64(r.TpmsPressureFL),
      TpmsPressureFR: pgNullableFloat64(r.TpmsPressureFR),
      TpmsPressureRL: pgNullableFloat64(r.TpmsPressureRL),
      TpmsPressureRR: pgNullableFloat64(r.TpmsPressureRR),
      ```
      Acceptance: `go build ./...` and `go vet ./...` green after `make sqlc`; the
      four fields are correctly mapped from `pgtype.Float8` to `*float64`.

## T6. Integration tests (`internal/telemetry/db_integration_test.go`) — depends on T2, T5

- [ ] T6.1 Add `DATABASE_URL`-gated integration tests (self-skip when unset; the
      existing `testdb_test.go` / testcontainers helper provisions Postgres automatically
      when Docker is available) for the four new TPMS columns. Cover:
      (a) **Non-nil round-trip:** insert a snapshot with all four TPMS fields set to
          non-zero values (e.g. `ptr(2.5)`, `ptr(2.6)`, `ptr(2.4)`, `ptr(2.5)`);
          read it back via `LatestSnapshotsByAccount`; assert all four come back
          non-nil with the correct values (within float tolerance).
      (b) **Nil round-trip:** insert a snapshot with all four TPMS fields as `nil`;
          read it back; assert all four are nil (not zero — a nil pointer, not `*0.0`).
      (c) **Zero-value non-nil:** insert a snapshot with `ptr(0.0)` for all four;
          read it back; assert all four are non-nil `*0.0` (truthful zero preserved,
          not collapsed into nil).
      (d) **PSI companion nil-safety:** for the nil round-trip row above, assert that
          `TpmsPressureFLPSI()` (etc.) returns nil on the domain `Snapshot`.
      (e) **PSI companion conversion:** for the non-nil round-trip row, assert that
          `TpmsPressureFLPSI()` returns a non-nil value approximately equal to
          `2.5 * barToPSI` (within 1e-6 tolerance).
      Acceptance: tests self-skip when `DATABASE_URL` is unset; `go test ./...` green
      with Docker running; NO live Tesla API call fires.

---

## Verification — depends on all tasks

- [ ] V1. `go build ./...` and `go vet ./...` pass after all tasks are complete.
- [ ] V2. `go test ./...` green and fast. DB integration tests self-skip without
      `DATABASE_URL`; with Docker the testcontainers helper provisions Postgres and
      applies goose migrations automatically. NO Tesla API call fires.
- [ ] V3. Nil fidelity: a snapshot row inserted with nil TPMS fields reads back as nil
      (not zero); a row with `ptr(0.0)` reads back as non-nil `*0.0`. Verified by T6.1
      (b) and (c).
- [ ] V4. PSI companion nil-safety: `TpmsPressureFLPSI()` (and FR/RL/RR) returns nil
      when the field is nil; returns `*float64` otherwise. Verified by T3.4 and T6.1 (d/e).
- [ ] V5. Boundary check: `internal/telemetry` still imports only `account` + `tesla`
      public packages; no `accountdb` or `internal/tesla` internals; `pgtype` does not
      appear in any public type or interface. The `tesla` module change (T1) is a leaf
      additive DTO field — no new interface method, no `Raw*` method, no explore-tesla-api
      surface change.
- [ ] V6. `openspec validate telemetry-add-tire-pressure-columns --strict` passes.
