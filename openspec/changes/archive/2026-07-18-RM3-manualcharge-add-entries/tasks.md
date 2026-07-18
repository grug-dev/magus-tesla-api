> **New isolated module, non-breaking.** Creates `internal/manualcharge` from scratch: goose
> migration, sqlc queries + generated code, `manualcharge.Entry` domain type with derived methods,
> `Writer` (Create/Update/Delete) and `Reader` (list by vehicle / by account) public ports,
> service wiring pgx, unit tests (offline), and DATABASE_URL-gated integration tests.
> No Tesla Fleet API, no HTML, no cross-module DB access.
>
> **Dependencies / parallelism:**
>
> - T1 (migration) has no dependencies — it only creates a SQL file.
> - T2 (sqlc query.sql) depends on T1 (schema must exist for sqlc to validate).
>   NOTE: `make sqlc` is a leader-integrated step after T2 and the sqlc.yaml entry is added.
> - T3 (domain type + derived methods + Writer/Reader interfaces) has NO dependencies on T1/T2
>   — it is a pure Go file (manualcharge.go) with no DB imports. Runs in parallel with T1–T2.
> - T4 (service: store seam + Writer/Reader implementations + mapping) depends on T2, T3 (needs
>   generated code and domain types).
> - T5 (unit tests — derived methods offline) depends on T3. No DB required.
> - T6 (DATABASE_URL-gated integration tests — full CRUD + ordering + multi-tenant) depends on
>   T2, T4 (requires generated queries and service implementations).
> - T7 (AGENTS.md) has no dependencies — authoring the module's identity file.
>
> **Leader-integrated / cross-module tasks (outside the `internal/manualcharge` sandbox):**
> - Leader: add a `sql:` entry for `internal/manualcharge` to `sqlc.yaml` (described in design D7).
> - Leader: run `make sqlc` after T2 to regenerate `manualchargedb` package.
> - Leader: run `make migrate-up` to apply the migration to the local database.

---

## T1. Migration — `manual_charge_entries` table (`internal/manualcharge/db/migrations/`) — no dependencies

- [x] T1.1 Create directory `internal/manualcharge/db/migrations/`.
- [x] T1.2 Add goose migration
      `internal/manualcharge/db/migrations/20260718000001_add_manual_charge_entries.sql`.
      Use timestamp `20260718000001`. Create table `manual_charge_entries` with all columns per
      design D1:
      `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`,
      `account_id UUID NOT NULL`,
      `tesla_id BIGINT NOT NULL`,
      `vin TEXT NOT NULL`,
      `charged_on DATE NOT NULL`,
      `energy_added_kwh NUMERIC(6,2) NOT NULL CHECK (energy_added_kwh > 0)`,
      `price NUMERIC(14,2) NOT NULL CHECK (price >= 0)`,
      `currency TEXT NOT NULL DEFAULT 'COP'`,
      `started_at TIMESTAMPTZ` (nullable),
      `ended_at TIMESTAMPTZ` (nullable),
      `start_battery_pct SMALLINT CHECK (start_battery_pct BETWEEN 0 AND 100)` (nullable),
      `end_battery_pct SMALLINT CHECK (end_battery_pct BETWEEN 0 AND 100)` (nullable),
      `charging_type TEXT CHECK (charging_type IN ('AC','DC'))` (nullable),
      `location_kind TEXT CHECK (location_kind IN ('HOME','WORK','OTHER'))` (nullable),
      `location_label TEXT` (nullable),
      `notes TEXT` (nullable),
      `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`,
      `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`,
      `CHECK (ended_at IS NULL OR started_at IS NULL OR ended_at >= started_at)`.
      NO FK on `account_id` or `tesla_id` (boundary rule — design D2f).
      Add index `idx_manual_charge_entries_vehicle_time (account_id, tesla_id, charged_on DESC)`
      (per-vehicle time series — design D3).
      Add index `idx_manual_charge_entries_account_time (account_id, charged_on DESC)`
      (account-wide — design D3).
      Add `COMMENT ON TABLE manual_charge_entries IS ...` (owned by `internal/manualcharge`;
      mutable table, full CRUD via Writer port; no cross-module FK; user-typed data, no raw_data
      JSONB; home/work/third-party sessions not captured by Tesla Fleet API).
      Include `-- +goose Down` that drops both indexes and the table in the right order.

---

## T2. sqlc queries (`internal/manualcharge/db/query.sql`) — depends on T1

- [x] T2.1 Create `internal/manualcharge/db/query.sql` with file header comment: owned by
      `internal/manualcharge`; no other module may import `manualchargedb`
      (ai/architecture.md §2, ai/go-conventions.md §persistence).
- [x] T2.2 Add `-- name: CreateEntry :one`. INSERT all columns (including optional ones as
      nullable params) RETURNING `*`. Covers T3.2's `Writer.Create`.
- [x] T2.3 Add `-- name: UpdateEntry :one`. UPDATE statement that sets `charged_on`,
      `energy_added_kwh`, `price`, `currency`, and all optional fields, plus
      `updated_at = now()`, WHERE `id = @id AND account_id = @account_id` RETURNING `*`.
      The account_id scope prevents cross-tenant updates. Document which columns are
      immutable (`id`, `account_id`, `tesla_id`, `vin`, `created_at`).
- [x] T2.4 Add `-- name: DeleteEntry :exec`. DELETE FROM `manual_charge_entries`
      WHERE `id = @id AND account_id = @account_id`. Account scope prevents cross-tenant
      deletes. Document the intentional double-scope.
- [x] T2.5 Add `-- name: ListEntriesByVehicle :many`. SELECT `*` FROM
      `manual_charge_entries` WHERE `account_id = @account_id AND tesla_id = @tesla_id`
      ORDER BY `charged_on DESC` LIMIT `@limit_count`. Document which index this uses
      (`idx_manual_charge_entries_vehicle_time`) and that the ORDER BY is covered.
- [x] T2.6 Add `-- name: ListEntriesByAccount :many`. SELECT `*` FROM
      `manual_charge_entries` WHERE `account_id = @account_id`
      ORDER BY `charged_on DESC` LIMIT `@limit_count`. Document which index this uses
      (`idx_manual_charge_entries_account_time`) and that the ORDER BY is covered.
      NOTE: after T2 is complete and the leader adds the `sqlc.yaml` entry, the leader
      runs `make sqlc` to generate `internal/manualcharge/db/` package `manualchargedb`.

---

## T3. Domain type + interfaces (`internal/manualcharge/manualcharge.go`) — no dependencies, parallel-ok with T1–T2

- [x] T3.1 Create `internal/manualcharge/manualcharge.go` with package comment: new isolated
      module for user-asserted charge entries; consumes no Tesla Fleet API; no HTML; public
      ports are Writer and Reader.
- [x] T3.2 Define `Entry` domain struct per design D6. No vendor suffix. Fields:
      `ID uuid.UUID`,
      `AccountID uuid.UUID`,
      `TeslaID int64`,
      `VIN string`,
      `ChargedOn time.Time` (DATE maps to `time.Time` in Go, midnight UTC),
      `EnergyAddedKWh float64`,
      `Price float64`,
      `Currency string`,
      `StartedAt *time.Time`,
      `EndedAt *time.Time`,
      `StartBatteryPct *int`,
      `EndBatteryPct *int`,
      `ChargingType *string`,
      `LocationKind *string`,
      `LocationLabel *string`,
      `Notes *string`,
      `CreatedAt time.Time`,
      `UpdatedAt time.Time`.
      Add doc comment: our domain model (no vendor suffix); pgtype confined to DB boundary;
      optional fields are `*T` (nil = not supplied / NULL in DB).
- [x] T3.3 Add derived value-receiver methods on `Entry` (all nil-safe):
      `CostPerKWh() *float64` — returns `&(e.Price / e.EnergyAddedKWh)`; returns nil if
      `e.EnergyAddedKWh == 0` (defensive; CHECK prevents zero, but nil is the safer signal).
      `BatteryDelta() *int` — returns `&(*e.EndBatteryPct - *e.StartBatteryPct)` if both
      non-nil; otherwise nil.
      `SessionDuration() *time.Duration` — returns `&d` where `d = e.EndedAt.Sub(*e.StartedAt)`
      if both non-nil; otherwise nil.
      Add doc comments: derived on read, never stored; no Km/Kmh companions (no distance/speed
      fields — ai/go-conventions.md).
- [x] T3.4 Declare `Writer` interface per design D4:
      `Create(ctx context.Context, e Entry) (Entry, error)`,
      `Update(ctx context.Context, e Entry) (Entry, error)`,
      `Delete(ctx context.Context, accountID uuid.UUID, id uuid.UUID) error`.
      Doc comment: full CRUD port; `Delete` takes `accountID` to scope the SQL WHERE clause
      to the calling user's account; `Create` and `Update` return the stored `Entry`.
- [x] T3.5 Declare `Reader` interface per design D5:
      `ListEntriesByVehicle(ctx context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]Entry, error)`,
      `ListEntriesByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]Entry, error)`.
      Doc comment: both return a non-nil empty slice when no entries exist; `limit = 0` uses
      a server default (100); gateway and other callers MUST NOT import `manualchargedb`.
- [x] T3.6 Declare constructor functions (implemented in T4):
      `func NewWriter(pool *pgxpool.Pool) Writer`
      `func NewReader(pool *pgxpool.Pool) Reader`
      These are the ONLY publicly exported constructors for the port implementations. Add
      compile-time assertions in the service file (T4): `var _ Writer = (*writerService)(nil)`
      and `var _ Reader = (*readerService)(nil)`.

---

## T4. Service: store seam + Writer/Reader implementations + mapping (`internal/manualcharge/service.go`) — depends on T2, T3

- [x] T4.1 Create `internal/manualcharge/service.go`. Declare unexported `store` interface
      with methods mirroring the sqlc-generated query functions:
      `createEntry(ctx, params manualchargedb.CreateEntryParams) (manualchargedb.ManualChargeEntry, error)`,
      `updateEntry(ctx, params manualchargedb.UpdateEntryParams) (manualchargedb.ManualChargeEntry, error)`,
      `deleteEntry(ctx, params manualchargedb.DeleteEntryParams) error`,
      `listEntriesByVehicle(ctx, params manualchargedb.ListEntriesByVehicleParams) ([]manualchargedb.ManualChargeEntry, error)`,
      `listEntriesByAccount(ctx, params manualchargedb.ListEntriesByAccountParams) ([]manualchargedb.ManualChargeEntry, error)`.
- [x] T4.2 Implement `dbStore` struct (unexported) wrapping `*manualchargedb.Queries` and
      satisfying the `store` interface. Each method delegates to the corresponding sqlc query.
      This is the ONLY place `manualchargedb` types are referenced.
- [x] T4.3 Implement `writerService` struct (unexported) holding a `store`. Implement
      `Create`, `Update`, `Delete`:
      `Create` — maps `Entry` fields to `CreateEntryParams` (pgtype at boundary for nullable
      fields; pattern: `pgtype.Int2{Int16: int16(*e.StartBatteryPct), Valid: e.StartBatteryPct != nil}`
      etc.), calls `store.createEntry`, maps result row via `rowToEntry`.
      `Update` — similarly maps to `UpdateEntryParams`; calls `store.updateEntry`; maps row.
      `Delete` — calls `store.deleteEntry(ctx, manualchargedb.DeleteEntryParams{ID: id, AccountID: accountID})`.
      Acceptance: pgtype never appears in the public `Entry`, `Writer`, or `Reader` types.
- [x] T4.4 Implement `readerService` struct (unexported) holding a `store`. Implement
      `ListEntriesByVehicle` and `ListEntriesByAccount`:
      Both use `limit = 100` as the server default when caller passes `limit <= 0`.
      Both call the corresponding `store` method and map each row via `rowToEntry`.
      Both return a non-nil empty slice when 0 rows found.
- [x] T4.5 Implement `rowToEntry(r manualchargedb.ManualChargeEntry) Entry` mapping function
      (unexported, in this file or a sibling `mapping.go`). Map all pgtype nullable columns
      to domain `*T` fields using the `Valid`-field pattern:
      `pgtype.Int2 → *int` (e.g. `start_battery_pct`),
      `pgtype.Timestamptz → *time.Time` (e.g. `started_at`, `ended_at`),
      `pgtype.Text → *string` (e.g. `charging_type`, `location_kind`, `location_label`, `notes`),
      `pgtype.Numeric → float64` for `energy_added_kwh` and `price` (via
      `pgtype.Numeric.Float64Value()`).
      `pgtype.Timestamptz → time.Time` for `created_at`, `updated_at`, `charged_on`.
      Map `pgtype` Date for DATE column `charged_on` using the time value.
      No pgtype in the return type.
- [x] T4.6 Implement `NewWriter(pool *pgxpool.Pool) Writer` and
      `NewReader(pool *pgxpool.Pool) Reader` constructors. Add compile-time interface assertions:
      `var _ Writer = (*writerService)(nil)` and `var _ Reader = (*readerService)(nil)`.

---

## T5. Unit tests — derived methods, offline (`internal/manualcharge/manualcharge_test.go`) — depends on T3

- [x] T5.1 Create `internal/manualcharge/manualcharge_test.go` (package `manualcharge_test`).
      Add unit tests for `Entry.CostPerKWh()`:
      (a) non-nil result when both fields are set (`price=8000, energy=15.5 → ~516.13`);
      (b) nil when `energy_added_kwh = 0` (defensive nil, not a divide-by-zero panic).
- [x] T5.2 Add unit tests for `Entry.BatteryDelta()`:
      (a) non-nil result when both battery fields are set (`start=20, end=80 → 60`);
      (b) nil when `start_battery_pct` is nil;
      (c) nil when `end_battery_pct` is nil.
- [x] T5.3 Add unit tests for `Entry.SessionDuration()`:
      (a) non-nil duration when both `started_at` and `ended_at` are set;
      (b) nil when `started_at` is nil;
      (c) nil when `ended_at` is nil.
      Acceptance: NO DB required; NO Tesla API call fires; tests run in `go test ./...`.

---

## T6. DATABASE_URL-gated integration tests (`internal/manualcharge/db_integration_test.go`) — depends on T2, T4

- [x] T6.1 Create `internal/manualcharge/db_integration_test.go` (package `manualcharge_test`).
      Add a `TestMain` (or a per-test skip) that calls `t.Skip` when `DATABASE_URL` is unset,
      so `go test ./...` stays green without a database.
- [x] T6.2 Test `Writer.Create`:
      (a) Create an entry with all required fields only; read it back via
          `Reader.ListEntriesByAccount`; assert all required fields round-trip faithfully
          (including `currency = 'COP'` default when not supplied).
      (b) Create an entry with all optional fields populated; assert they all round-trip
          (no field silently goes NULL).
      (c) Attempt to create with `energy_added_kwh = 0` or negative; assert an error is
          returned (DB CHECK constraint).
      (d) Attempt to create with `price < 0`; assert an error is returned.
      (e) Attempt to create with `start_battery_pct = 101`; assert an error.
      (f) Attempt to create with `ended_at < started_at`; assert an error (timing CHECK).
- [x] T6.3 Test `Writer.Update`:
      (a) Create an entry; update `price` and `notes`; read back via `ListEntriesByAccount`;
          assert updated fields changed, `created_at` unchanged, `updated_at` advanced.
      (b) Attempt to update an entry belonging to a different `account_id`; assert zero rows
          affected / "not found" behavior (no cross-tenant mutation).
- [x] T6.4 Test `Writer.Delete`:
      (a) Create an entry; delete it; assert it no longer appears in
          `ListEntriesByAccount` results.
      (b) Delete with a mismatched `account_id`; assert the row survives (cross-tenant delete
          protection).
- [x] T6.5 Test `Reader.ListEntriesByVehicle`:
      (a) Create entries for two vehicles (`tesla_id = V1`, `tesla_id = V2`) in the same
          account; assert `ListEntriesByVehicle(V1)` returns only V1 entries, none for V2.
      (b) Create 3 entries for vehicle V1 with different `charged_on` dates; assert they are
          returned newest-first (`charged_on DESC`).
      (c) Request with `limit = 1`; assert exactly 1 entry returned.
      (d) Request for a vehicle with no entries; assert an empty non-nil slice.
- [x] T6.6 Test `Reader.ListEntriesByAccount`:
      (a) Create entries for two different accounts (`account_id = A`, `account_id = B`);
          assert `ListEntriesByAccount(A)` returns only account A's entries.
      (b) Create entries with different `charged_on` dates; assert newest-first ordering.
      (c) Request with `limit = 1`; assert at most 1 entry returned.
      (d) Account with no entries returns an empty non-nil slice.
- [x] T6.7 Multi-tenant isolation spot-check: assert that the service NEVER returns entries
      from a different `account_id` in any read method under any scenario tested above.

---

## T7. `internal/manualcharge/AGENTS.md` — no dependencies

- [x] T7.1 Create `internal/manualcharge/AGENTS.md`. Include:
      `Agent-Name: manualcharge` header;
      `## Doc-Pack (module)` section (no module-specific docs beyond the base pack);
      Module responsibility (user-asserted charge entries, isolated from Tesla Fleet API);
      Public interface (`Writer`: Create/Update/Delete; `Reader`: ListEntriesByVehicle /
      ListEntriesByAccount);
      Allowed imports (NOT `internal/tesla`, NOT other modules' internals, NOT `manualchargedb`
      from outside the module);
      Data ownership (`manual_charge_entries` table, owned exclusively by this module);
      Testing notes (unit tests: no DB; integration tests: DATABASE_URL-gated, self-skip when
      unset; no Tesla API calls in any test; see Tesla-exploration exception in CLAUDE.md which
      does NOT apply here).

---

## Verification — depends on all tasks above

- [x] V1. `go build ./...` passes (no compilation errors in the new module or any file that
      imports it).
- [x] V2. `go vet ./...` passes with no warnings in the new module.
- [x] V3. `go test ./...` green and fast:
      - Unit tests (T5) run offline with no DB.
      - Integration tests (T6) self-skip when `DATABASE_URL` is unset.
      - NO Tesla API call fires anywhere in the test run.
- [x] V4. Boundary check: `internal/manualcharge` does NOT import `internal/tesla`,
      `internal/account`, `internal/telemetry`, or any other module's internals. `pgtype` does
      not appear in any public type, interface, or function signature outside `service.go` and
      any `mapping.go`.
- [x] V5. Migration applies cleanly: `make migrate-up` runs without error; the table and both
      indexes exist in the target database.
- [x] V6. Compile-time interface assertions hold:
      `var _ Writer = (*writerService)(nil)` and `var _ Reader = (*readerService)(nil)` both
      compile without errors.
