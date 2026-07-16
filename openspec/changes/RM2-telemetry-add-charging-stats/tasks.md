> **Additive, non-breaking change** (RM2-charging-stats tier 2). New `supercharger_sessions`
> table + UPSERT write path + `SuperchargerReader` port (Source B); six new nullable charge-
> enrichment columns on `vehicle_snapshots` + `Snapshot` domain extension (Source A, blocked on
> leader tesla edit). Folds into the existing nightly `collectAccount` cycle. `pgtype` stays
> confined to the DB boundary. No HTML. No cross-module DB access. No Tesla API call fires from
> `go test`.
>
> **Dependencies / parallelism:**
>
> **Source B tracks (independent of Source A):**
> - B1 (migration — supercharger_sessions) has no dependencies.
> - B2 (sqlc queries — upsert + 2 reader queries) depends on B1 (schema must exist).
>   NOTE: `make sqlc` is a leader-integrated step after B2.
> - B3 (SuperchargerSession domain type + derivation helpers + SuperchargerReader interface +
>   CycleReport extension) has no dependencies — disjoint file (`telemetry.go`); may run in
>   parallel with B1–B2.
> - B4 (store seam: extend `store` interface + `dbStore.upsertSuperchargerSession`) depends on B2,
>   B3.
> - B5 (fold ChargingHistory into `collectAccount`) depends on B3, B4.
> - B6 (SuperchargerReader implementation + constructor + `rowToSuperchargerSession`) depends on
>   B2, B3.
> - B7 (offline collector tests — ChargingHistory fold-in + isolation) depends on B5.
> - B8 (DATABASE_URL-gated store/reader tests — upsert + 2 read queries) depends on B2, B6.
>
> **Source A track (blocked until leader adds 6 ChargeStateTesla fields):**
> - A1 (migration — 6 nullable columns on vehicle_snapshots) has no Source B dependencies; can
>   be authored now but applied only after the tesla-module leader edit.
> - A2 (Snapshot domain type extension — 6 nullable fields) BLOCKED on leader tesla edit.
> - A3 (snapshotFrom extension + InsertVehicleSnapshot sqlc param extension) depends on A1, A2,
>   and the leader tesla edit.
> - A4 (rowToSnapshot extension + pgNullable* helpers in mapping.go) depends on A1, A2.
> - A5 (DATABASE_URL-gated store tests for the 6 new nullable columns) depends on A1, A3, A4.
>
> **Leader-integrated / cross-module tasks (outside the `internal/telemetry` sandbox):**
> - Leader: add 6 fields to `ChargeStateTesla` in `internal/tesla/types.go` — prerequisite for
>   Source A tasks A2, A3, A4, A5. Source B is fully independent of this.
> - Leader: run `make sqlc` after B2 (queries added) to regenerate `telemetrydb`. The existing
>   `sql:` entry in `sqlc.yaml` already covers telemetry; no structural sqlc.yaml change needed.

---

## SOURCE B: Supercharger sessions

### B1. Migration — `supercharger_sessions` table (`internal/telemetry/db/migrations/`) — no dependencies

- [x] B1.1 Add goose migration `internal/telemetry/db/migrations/<timestamp>_add_supercharger_sessions.sql`.
      Use the NEXT timestamp after `20260710000002` (e.g. `20260716000001`). Create table
      `supercharger_sessions` with all columns per design DBS1:
      `id UUID PK DEFAULT gen_random_uuid()`,
      `session_id BIGINT NOT NULL`,
      `account_id UUID NOT NULL`,
      `vin TEXT NOT NULL`,
      `tesla_id BIGINT` (nullable),
      `site_location_name TEXT NOT NULL`,
      `country_code TEXT NOT NULL`,
      `charge_start_date_time TIMESTAMPTZ NOT NULL`,
      `charge_stop_date_time TIMESTAMPTZ NOT NULL`,
      `unlatch_date_time TIMESTAMPTZ` (nullable),
      `billing_type TEXT NOT NULL`,
      `vehicle_make_type TEXT NOT NULL`,
      `energy_kwh DOUBLE PRECISION` (nullable),
      `total_cost DOUBLE PRECISION` (nullable),
      `currency TEXT` (nullable),
      `is_paid BOOLEAN` (nullable),
      `raw_data JSONB NOT NULL`,
      `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`,
      `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`.
      NO FK on `account_id` or `tesla_id` (boundary rule — design DBS1).
      Add UNIQUE constraint on `session_id` (upsert conflict target + point lookup).
      Add index `idx_supercharger_sessions_vehicle_time (account_id, tesla_id,
      charge_start_date_time DESC)` (per-vehicle time series, design DBS4).
      Add index `idx_supercharger_sessions_account_time (account_id,
      charge_start_date_time DESC)` (account-wide, design DBS4).
      Include header comment: table is owned by `internal/telemetry`; UPSERT on session_id
      (not append-only); no cross-module FK (boundary); Supercharger/DC fast-charging only.
      Include `-- +goose Down` that drops indexes and the table.

### B2. sqlc queries for supercharger_sessions (`internal/telemetry/db/query.sql`) — depends on B1

- [x] B2.1 Add `-- name: UpsertSuperchargerSession :exec` to `query.sql`. The query is an
      `INSERT INTO supercharger_sessions (...) VALUES (...) ON CONFLICT (session_id)
      DO UPDATE SET raw_data = EXCLUDED.raw_data, energy_kwh = EXCLUDED.energy_kwh,
      total_cost = EXCLUDED.total_cost, currency = EXCLUDED.currency,
      is_paid = EXCLUDED.is_paid, tesla_id = EXCLUDED.tesla_id, updated_at = now()`.
      Immutable columns (session_id, account_id, vin, location, timestamps, billing fields,
      created_at) MUST NOT appear in the DO UPDATE SET clause.
- [x] B2.2 Add `-- name: SuperchargerSessionsByAccount :many` to `query.sql`.
      `SELECT * FROM supercharger_sessions WHERE account_id = @account_id
      ORDER BY charge_start_date_time DESC LIMIT @limit_count`. Uses
      `idx_supercharger_sessions_account_time`. Document in comment which index this uses.
- [x] B2.3 Add `-- name: SuperchargerSessionsByVehicle :many` to `query.sql`.
      `SELECT * FROM supercharger_sessions WHERE account_id = @account_id
      AND tesla_id = @tesla_id ORDER BY charge_start_date_time DESC LIMIT @limit_count`.
      Uses `idx_supercharger_sessions_vehicle_time`. Document in comment.
      NOTE: after B2 is complete, the leader runs `make sqlc` to regenerate `telemetrydb`.

### B3. Domain types + interfaces (`internal/telemetry/telemetry.go`) — no dependencies, parallel-ok

- [x] B3.1 Add `SuperchargerSession` domain type per design DBS5. No vendor suffix. Fields:
      `ID uuid.UUID`, `SessionID int64`, `AccountID uuid.UUID`, `VIN string`,
      `TeslaID *int64` (nullable), `SiteLocationName string`, `CountryCode string`,
      `ChargeStartDateTime time.Time`, `ChargeStopDateTime time.Time`,
      `UnlatchDateTime *time.Time` (nullable), `BillingType string`, `VehicleMakeType string`,
      `EnergyKWh *float64`, `TotalCost *float64`, `Currency *string`, `IsPaid *bool`,
      `RawData []byte`, `CreatedAt time.Time`, `UpdatedAt time.Time`.
      Add doc comment: domain model (no vendor suffix), no Km/Kmh companions (no
      distance/speed fields), distinct from `tesla.ChargingSessionTesla`.
- [x] B3.2 Add derivation helper functions (unexported, `telemetry` package):
      `deriveEnergyKWh(fees []tesla.ChargingFeeTesla) *float64` — sum of
      (usageBase + usageTier1 + usageTier2 + nilOrZero(usageTier3) + nilOrZero(usageTier4))
      for fees where `strings.ToLower(fee.UOM) == "kwh"`; return nil when no kWh fee.
      `deriveTotalCost(fees []tesla.ChargingFeeTesla) *float64` — sum of totalDue over all
      fees; nil when fees empty.
      `deriveCurrency(fees []tesla.ChargingFeeTesla) *string` — first fee's currencyCode;
      nil when fees empty.
      `deriveIsPaid(fees []tesla.ChargingFeeTesla) *bool` — logical AND; nil when fees empty.
      These are pure functions testable offline without DB.
- [x] B3.3 Extend `CycleReport` with two new fields per design DBS7:
      `ChargingSessionsUpserted int` and `ChargingFetchFailures int`. Add doc comments.
      Acceptance: existing CycleReport callers remain compile-compatible (additive struct
      fields, zero value is correct default).
- [x] B3.4 Declare `SuperchargerReader` interface per design DBS6:
      `SuperchargerSessionsByAccount(ctx, accountID uuid.UUID, limit int) ([]SuperchargerSession, error)`
      and `SuperchargerSessionsByVehicle(ctx, accountID uuid.UUID, teslaID int64, limit int)
      ([]SuperchargerSession, error)`. Add doc comment: separate port from `Reader`; callers
      must NOT import `telemetrydb`. Declare `NewSuperchargerReader(pool *pgxpool.Pool) SuperchargerReader`
      function signature (implementation in B6).

### B4. Store seam: `upsertSuperchargerSession` (`internal/telemetry/service.go`) — depends on B2, B3

- [x] B4.1 Add `upsertSuperchargerSession(ctx context.Context, s SuperchargerSession) error`
      to the unexported `store` interface in `service.go`.
- [x] B4.2 Implement `dbStore.upsertSuperchargerSession`: map `SuperchargerSession` to
      `telemetrydb.UpsertSuperchargerSessionParams` at the DB boundary. This is the ONLY
      place pgtype is touched for this method:
      `session_id` → `s.SessionID` (int64, not nullable),
      nullable `tesla_id` → `pgtype.Int8{Int64: *s.TeslaID, Valid: s.TeslaID != nil}`,
      nullable `unlatch_date_time` → `pgtype.Timestamptz{...}` (nil→invalid),
      nullable `energy_kwh` → `pgtype.Float8{...}`,
      nullable `total_cost` → `pgtype.Float8{...}`,
      nullable `currency` → `pgtype.Text{...}`,
      nullable `is_paid` → `pgtype.Bool{...}` (same pattern as `boolPtrToPgBool`).
      `raw_data` → `[]byte(s.RawData)`.
      Acceptance: pgtype never appears in `SuperchargerSession` or any method signature
      outside `service.go`/`mapping.go`.

### B5. Fold ChargingHistory into `collectAccount` (`internal/telemetry/service.go`) — depends on B3, B4

- [x] B5.1 After the existing per-vehicle snapshot loop in `collectAccount`, add the Supercharger
      ingestion pass:
      (a) Call `s.tsla.ChargingHistory(ctx, creds, tesla.ChargingHistoryParams{})`. On failure:
          increment `report.ChargingFetchFailures`; do NOT abort or affect snapshot collection.
      (b) Build `map[string]int64` of VIN → TeslaID from `owned []account.OwnedVehicle`.
      (c) For each `ChargingSessionTesla` in the result:
          - Derive `EnergyKWh`, `TotalCost`, `Currency`, `IsPaid` using the B3.2 helpers.
          - Resolve `TeslaID`: look up the session's VIN in the map; nil if not found.
          - Construct `SuperchargerSession`; set `RawData` to the verbatim session bytes
            `session.Raw` (the `tesla.ChargingSessionTesla.Raw` field captured in UnmarshalJSON,
            L2/D13) — NOT a re-marshal of the typed struct. This is the lossless whole-session blob.
          - Call `s.store.upsertSuperchargerSession(ctx, session)`. On failure: continue
            (per-session isolation; do NOT abort the charging ingestion pass).
          - On success: increment `report.ChargingSessionsUpserted`.
      Acceptance: a `ChargingHistory` call failure only sets `report.ChargingFetchFailures++;`
      the per-vehicle snapshot loop is unaffected. NO additional `poll_attempts` row for charging
      (poll_attempts is per-vehicle; charging is per-account — counts live in CycleReport per DBS7).

### B6. `SuperchargerReader` implementation (`internal/telemetry/reader.go`, `mapping.go`) — depends on B2, B3

- [x] B6.1 Add `rowToSuperchargerSession(r telemetrydb.SuperchargerSession) SuperchargerSession`
      to `mapping.go`. Map all pgtype nullable columns to domain pointer fields using the same
      `Valid`-field pattern as `rowToSnapshot`. Map `pgtype.Int8 → *int64`,
      `pgtype.Timestamptz → *time.Time`, `pgtype.Float8 → *float64`,
      `pgtype.Text → *string`, `pgtype.Bool → *bool`. No pgtype in the return type.
- [x] B6.2 Add `superchargerReader` struct (unexported) to `reader.go` (or a new file)
      implementing `SuperchargerReader`. Each method calls the corresponding sqlc query
      (`SuperchargerSessionsByAccount` / `SuperchargerSessionsByVehicle`), maps rows via
      `rowToSuperchargerSession`, and returns a non-nil empty slice when 0 rows found.
      Handle `limit = 0` by passing a large sentinel (e.g. `math.MaxInt32`) or a server
      default — document the choice.
- [x] B6.3 Implement `NewSuperchargerReader(pool *pgxpool.Pool) SuperchargerReader` — construct
      the reader backed by `telemetrydb.New(pool)`. Add compile-time assertion:
      `var _ SuperchargerReader = (*superchargerReader)(nil)`.

### B7. Offline collector tests (charging fold-in) (`internal/telemetry/*_test.go`) — depends on B5

- [x] B7.1 Extend the existing offline collector test suite (fake `account.Service` +
      `tesla.VehicleService` + fake `store`): add tests verifying
      (a) when `ChargingHistory` succeeds with N sessions, `CycleReport.ChargingSessionsUpserted`
          equals N and `ChargingFetchFailures` equals 0;
      (b) when `ChargingHistory` returns an error, `CycleReport.ChargingFetchFailures` equals 1
          and snapshot collection for that account is unaffected;
      (c) VIN-to-TeslaID resolution: a session with a recognized VIN gets the correct `TeslaID`;
          a session with an unknown VIN gets `TeslaID == nil`;
      (d) derivation helpers: unit tests for `deriveEnergyKWh`, `deriveTotalCost`,
          `deriveCurrency`, `deriveIsPaid` covering kWh-only fees, time-only fees, mixed fees,
          empty fees, all-paid, one-unpaid, zero-fee cases.
      NO Tesla API call fires. NO DB required (fake store).

### B8. DATABASE_URL-gated store/reader tests (`internal/telemetry/db_integration_test.go`) — depends on B2, B6

- [x] B8.1 Add `DATABASE_URL`-gated integration tests (self-skip when unset) for Source B:
      (a) `UpsertSuperchargerSession`: insert a session; read it back via
          `SuperchargerSessionsByAccount`; assert all fields including nullable ones round-trip
          faithfully (NULL when nil pointer, not zero value).
      (b) Upsert same `session_id` a second time with different `is_paid`, `total_cost`,
          `raw_data`: assert the row is updated (not duplicated) and immutable fields unchanged.
      (c) `SuperchargerSessionsByVehicle`: insert sessions for two vehicles in the same account;
          assert only the queried vehicle's sessions are returned.
      (d) `SuperchargerSessionsByAccount`: insert sessions for two accounts; assert only the
          queried account's sessions are returned.
      (e) Ordering: sessions are returned newest charge_start_date_time first.
      (f) Limit: passing limit=1 returns at most 1 row.

---

## SOURCE A: vehicle_snapshots charge enrichment (BLOCKED on leader tesla edit)

> **NOTE:** All Source A tasks require the leader to first add the 6 fields to
> `internal/tesla/types.go` `ChargeStateTesla`:
> `ChargeEnergyAdded float64`, `ChargerPower int`, `ChargerVoltage int`,
> `ChargerActualCurrent int`, `UsableBatteryLevel int`, `FastChargerType string`.
> Tasks A2, A3, A4, A5 MUST NOT be started until that edit is in place.
> Task A1 (migration authoring) may be written now but applied only after the tesla edit.

### A1. Migration — 6 nullable columns on `vehicle_snapshots` (`internal/telemetry/db/migrations/`) — no block on tesla edit for authoring

- [ ] A1.1 Add goose migration `internal/telemetry/db/migrations/<timestamp>_enrich_vehicle_snapshots_charge.sql`
      (timestamp AFTER the B1 migration). Use `ALTER TABLE vehicle_snapshots ADD COLUMN`:
      `charge_energy_added DOUBLE PRECISION` (nullable),
      `charger_power INTEGER` (nullable),
      `charger_voltage INTEGER` (nullable),
      `charger_actual_current INTEGER` (nullable),
      `usable_battery_level INTEGER` (nullable),
      `fast_charger_type TEXT` (nullable).
      Header comment: nullable (pre-enrichment rows NULL, not backfilled); Source A of
      RM2-telemetry-add-charging-stats. No new index (not a filter/sort column on hot path;
      design DSA1). Include `-- +goose Down` that drops the 6 columns.

### A2. `Snapshot` domain type extension (`internal/telemetry/telemetry.go`) — BLOCKED on leader tesla edit

- [ ] A2.1 Add six nullable fields to `Snapshot` per design DSA2:
      `ChargeEnergyAdded *float64`, `ChargerPower *int`, `ChargerVoltage *int`,
      `ChargerActualCurrent *int`, `UsableBatteryLevel *int`, `FastChargerType *string`.
      Add doc comment: nullable — nil when not reported or row predates this extraction.
      No `Km()`/`Kmh()` companions (none are distance/speed fields, design DSA1).
      Acceptance: existing `Snapshot` callers that do not use these fields remain
      compile-compatible (additive struct fields).

### A3. `snapshotFrom` + sqlc param extension (`internal/telemetry/service.go`) — BLOCKED on leader tesla edit; depends on A1, A2

- [ ] A3.1 Extend `snapshotFrom` to map the 6 new `ChargeStateTesla` fields into `Snapshot`,
      storing the ACTUAL DTO value pointer-wrapped (`ptr(data.ChargeState.X)`) — NO zero-is-absent
      heuristic (design DSA3/D12). The plain DTO fields always carry a value (0/"" when idle), so
      every new row is non-NULL; a `0`/`""` is a truthful reading and must be stored. `ptr` is a
      tiny generic `func ptr[T any](v T) *T { return &v }` helper. (NULL is reserved for
      pre-migration rows — handled by not backfilling, DSA1.)
- [ ] A3.2 Extend `dbStore.insertSnapshot` to pass the 6 nullable fields to the sqlc
      `InsertVehicleSnapshotParams`. Map `*float64` → `pgtype.Float8`, `*int` → `pgtype.Int4`,
      `*string` → `pgtype.Text` using the same `Valid`-field pattern. Update the sqlc query
      `InsertVehicleSnapshot` in `query.sql` to include the 6 new column names + params.
      After query.sql is updated the leader runs `make sqlc` to regenerate.

### A4. `rowToSnapshot` extension + helpers (`internal/telemetry/mapping.go`) — BLOCKED on leader tesla edit; depends on A1, A2

- [ ] A4.1 Add helper functions to `mapping.go` per design DSA4:
      `pgNullableFloat64(v pgtype.Float8) *float64`,
      `pgNullableInt32AsInt(v pgtype.Int4) *int`,
      `pgNullableText(v pgtype.Text) *string`.
      Each returns nil when `!v.Valid`, non-nil pointer otherwise.
- [ ] A4.2 Extend `rowToSnapshot` to map the 6 new nullable columns from the sqlc-generated
      `telemetrydb.VehicleSnapshot` row using the helpers from A4.1.

### A5. DATABASE_URL-gated store tests for Source A (`internal/telemetry/db_integration_test.go`) — BLOCKED; depends on A1, A3, A4

- [ ] A5.1 Extend the `DATABASE_URL`-gated integration tests to cover Source A:
      (a) Insert a snapshot with all 6 charge-enrichment fields set to non-nil values; read it
          back via `LatestSnapshotsByAccount`; assert all 6 round-trip faithfully.
      (b) Insert a snapshot with all 6 fields nil (vehicle not charging); assert 6 fields come
          back nil (not zero).
      (c) Verify pre-enrichment semantics: a snapshot row inserted without the 6 columns
          (simulated by NULL parameters) reads back as nil for all 6 fields.

---

## Verification — depends on all Source B tasks (Source A tasks when unblocked)

- [ ] V1. `go build ./...` and `go vet ./...` pass after Source B is complete.
- [ ] V2. `go test ./...` green and fast (DB tests self-skip without `DATABASE_URL`; NO Tesla
      API call fires from the test run).
- [ ] V3. Source B: `supercharger_sessions` table exists; UPSERT idempotent (running twice inserts
      one row, not two); `ChargingFetchFailures` isolation verified (snapshot loop unaffected).
- [ ] V4. Boundary check: `internal/telemetry` still imports only `account` + `tesla` public
      packages; no `accountdb` or `internal/tesla` internals; `pgtype` does not appear in any
      public type or interface.
- [ ] V5. Source A (when leader tesla edit is in): snapshot rows written after the migration carry
      non-nil charge-enrichment fields for a charging vehicle; old rows return nil. `go build ./...`
      passes with the 6 new `ChargeStateTesla` fields in place.
- [ ] V6. `openspec validate RM2-telemetry-add-charging-stats --strict` passes.
