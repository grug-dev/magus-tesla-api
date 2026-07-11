> **Additive, non-breaking change** (tier 4 of `openspec/roadmaps/nightly-vehicle-telemetry.md`).
> Read-only port on `internal/telemetry` — new `Reader` interface + one DISTINCT ON sqlc query +
> domain-boundary mapping + tests. No migration, no new table, no collection-logic change.
> `Snapshot` domain type is reused as-is (already has `BatteryRangeKm()`/`OdometerKm()` companions).
> The `Collector` port and all existing methods are untouched.
>
> **Dependencies / parallelism:**
> - 1 (sqlc query addition) has no dependencies — disjoint file (`db/query.sql`). MAY run
>   immediately.
> - 2 (leader-integrated sqlc regen) depends on 1. **Leader-integrated** — must run `make sqlc`
>   to regenerate `internal/telemetry/db/query.sql.go`. Blocks 3 and 5.
> - 3 (store seam extension + pgStore read impl + rowToSnapshot) depends on 2 (needs the generated
>   `LatestSnapshotsByAccount` function). MAY run in parallel with 4.
> - 4 (`Reader` interface declaration in `telemetry.go`) has no dependencies — disjoint file;
>   MAY run in parallel with 1–3.
> - 5 (`NewReader` constructor in `reader.go`) depends on 2, 3, 4 (needs the seam, the impl,
>   and the interface).
> - 6 (offline unit test) depends on 3, 4, 5 (needs the fake seam, the interface, and the
>   constructor).
> - 7 (`DATABASE_URL`-gated store integration test) depends on 2, 3 (needs the real sqlc query
>   + pgStore read method; no dependency on 4–6).
> - 8 (AGENTS.md update) has no dependencies — disjoint file; MAY run any time.
> - 9 (verification) depends on 1–8.
>
> **Leader-integrated task (outside the `internal/telemetry` sandbox):** 2 (`make sqlc` regen).
> The telemetry worker adds the query to `query.sql` (task 1); the leader runs `make sqlc`
> (task 2) and commits the regenerated `query.sql.go`; the worker then implements against
> the generated code (tasks 3–7).

## 1. Add sqlc query (`internal/telemetry/db/query.sql`) — no dependencies, parallel-ok

- [x] 1.1 Append a `LatestSnapshotsByAccount :many` query to
      `internal/telemetry/db/query.sql` using `DISTINCT ON (tesla_id)` to return
      exactly one row per vehicle — the one with the highest `captured_at` — for a
      given `account_id`. Full column list (same as the existing `ListSnapshotsByVehicle`
      query). `ORDER BY tesla_id, captured_at DESC` so Postgres can use the existing
      `(account_id, tesla_id, captured_at)` index. Add a doc comment explaining the
      DISTINCT ON approach and that it is the batch read for the dashboard (avoids N+1).
      Parameter name: `@account_id`. Accept a `uuid.UUID` parameter.

## 2. sqlc regeneration (`internal/telemetry/db/`) — LEADER-INTEGRATED, depends on 1

- [x] 2.1 Run `make sqlc` (or `sqlc generate`) to regenerate `internal/telemetry/db/query.sql.go`.
      Confirm that only `internal/telemetry/db/query.sql.go` changed (models.go is
      untouched; no new struct is needed — `VehicleSnapshot` already covers the output).
      Confirm no cross-module import of `telemetrydb` was introduced. This is a
      leader-integrated step outside the telemetry worker sandbox.

## 3. Store seam extension + pgStore read implementation — depends on 2

- [x] 3.1 Extend the unexported `store` interface (the `dbStore` seam used by the collection
      service) to add a read method matching the new `LatestSnapshotsByAccount` shape:
      `LatestSnapshotsByAccount(ctx context.Context, accountID uuid.UUID) ([]Snapshot, error)`.
      This extension keeps the `Reader` implementation offline-testable via the same
      fake-store pattern used for `Collector`.
- [x] 3.2 Add (or extract) a `rowToSnapshot(r telemetrydb.VehicleSnapshot) Snapshot` internal
      helper that converts a sqlc row to the domain `Snapshot`. Mapping rules: (a)
      `pgtype.Timestamptz.Time` → `time.Time` for `CapturedAt`; (b) `pgtype.Bool` →
      `*bool` for `SentryMode` (`{Valid: false}` → `nil`, otherwise `&r.SentryMode.Bool`);
      (c) `int32` → `int` for `BatteryLevel` and `ChargeLimitSoc`; (d) all other fields
      are value-compatible. If a `rowToSnapshot` helper already exists in `service.go`
      from tier 3, move it to a shared `mapping.go` to avoid duplication.
- [x] 3.3 Add the `LatestSnapshotsByAccount` implementation on the concrete `pgStore`:
      call `s.q.LatestSnapshotsByAccount(ctx, accountID)`, map each row via
      `rowToSnapshot`, return `([]Snapshot{}, nil)` (not `(nil, nil)`) when the query
      returns zero rows. `pgtype` must not appear in the return type.

## 4. `Reader` interface declaration (`internal/telemetry/telemetry.go`) — no dependencies, parallel-ok

- [x] 4.1 Add the `Reader` interface in `internal/telemetry/telemetry.go` (alongside
      `Collector` — all port declarations in one file):

      ```go
      // Reader exposes the telemetry module's stored snapshots for read-only
      // consumption by the gateway and other callers. It is the second half of the
      // telemetry public port; the first half (Collector) is the write path.
      // Callers must never import telemetrydb directly — all access goes through
      // this interface.
      type Reader interface {
          // LatestSnapshotsByAccount returns the most-recently captured snapshot for
          // each vehicle belonging to the given account. If the account has no stored
          // snapshots it returns an empty (non-nil) slice and a nil error. Order of
          // the returned slice is unspecified.
          LatestSnapshotsByAccount(ctx context.Context, accountID uuid.UUID) ([]Snapshot, error)
      }
      ```

      Add a compile-time assertion `var _ Reader = (*reader)(nil)` once the concrete
      type exists (task 5).

## 5. `NewReader` constructor (`internal/telemetry/reader.go`) — depends on 2, 3, 4

- [x] 5.1 Create `internal/telemetry/reader.go` with an unexported `reader` struct that
      holds a `store` interface value (the same seam type from task 3.1). Add
      `NewReader(pool *pgxpool.Pool) Reader` that constructs a `pgStore` (wrapping
      `telemetrydb.New(pool)`) and returns a `reader` holding it. The `reader.LatestSnapshotsByAccount`
      method delegates to `r.store.LatestSnapshotsByAccount`. Add the compile-time assertion
      `var _ Reader = (*reader)(nil)`.

## 6. Offline unit test (`internal/telemetry/reader_test.go`) — depends on 3, 4, 5

- [x] 6.1 Add `internal/telemetry/reader_test.go` with an offline unit test using a
      `fakeStore` that implements the extended `store` seam. No DB, no network, no Tesla
      call. Test cases:
      - **Latest-wins-per-vehicle:** populate the fake with two snapshots for vehicle A
        (different `CapturedAt`) and one for vehicle B; assert `LatestSnapshotsByAccount`
        returns exactly two `Snapshot`s and each carries the expected `CapturedAt` and
        field values (the newer one for vehicle A).
      - **Empty-account:** fake returns nil/empty rows; assert the method returns a
        non-nil empty slice and nil error.
      - **Sentry-mode nil fidelity:** fake returns a row where `SentryMode` is nil;
        assert the returned `Snapshot.SentryMode` is nil (not a false pointer).
      - **Km companions:** call `BatteryRangeKm()` and `OdometerKm()` on a returned
        snapshot; assert they equal the miles value multiplied by `1.609344`.

## 7. `DATABASE_URL`-gated store integration test (`internal/telemetry/db_read_integration_test.go`) — depends on 2, 3

- [x] 7.1 Add `internal/telemetry/db_read_integration_test.go` that self-skips when
      `DATABASE_URL` is unset (`if os.Getenv("DATABASE_URL") == "" { t.Skip(...) }`).
      Test cases (using the real Postgres query):
      - **Multi-vehicle latest-wins:** insert two `vehicle_snapshots` rows for vehicle A
        with different `captured_at` timestamps and one row for vehicle B (same account).
        Call `LatestSnapshotsByAccount`; assert exactly two results, vehicle A returns the
        newer row, vehicle B returns its only row. Assert all typed fields match what
        was inserted.
      - **Sentry-mode nil ↔ NULL:** insert a row with `sentry_mode = NULL` (nil `pgtype.Bool`);
        assert the returned `Snapshot.SentryMode` is nil.
      - **Empty account:** call `LatestSnapshotsByAccount` for an account UUID that has
        no rows; assert empty slice and nil error.
      Follow the tier-3 `db_integration_test.go` pattern for pool setup and teardown.

## 8. `AGENTS.md` public-interface update (`internal/telemetry/AGENTS.md`) — no dependencies, parallel-ok

- [x] 8.1 In `internal/telemetry/AGENTS.md`, append `Reader` to the `## Public interface (the port)`
      section. Do NOT remove, rewrite, or move any existing content. Append only:

      > - `Reader` — `LatestSnapshotsByAccount(ctx context.Context, accountID uuid.UUID) ([]Snapshot, error)`:
      >   return the latest stored `Snapshot` for each vehicle owned by the given account
      >   (batch, single Postgres query — no N+1); empty slice (non-nil) when the account
      >   has no snapshots. `NewReader(pool *pgxpool.Pool) Reader` is the constructor. The
      >   gateway (tier 5) depends on this interface, never on `telemetrydb`.

## 9. Verification — depends on 1–8

- [x] 9.1 `go build ./...` and `go vet ./...` pass with no errors or warnings. The generated
      `internal/telemetry/db/query.sql.go` must be present and current (task 2 completed).
      `internal/telemetry` compiles with the new `Reader` interface and `NewReader` constructor.
- [x] 9.2 `go test ./...` is green and fast. The offline unit test (task 6) runs without a
      database or network. The `DATABASE_URL`-gated integration test (task 7) self-skips when
      `DATABASE_URL` is unset and passes when set. No Tesla API call fires from the test run.
- [x] 9.3 `openspec validate telemetry-add-snapshot-read-port --strict` passes and every
      tasks.md checkbox reflects real completion.
- [x] 9.4 Boundary check: `internal/telemetry` imports only its allowed packages (`account` +
      `tesla` public ports, `pgx/v5` + `pgxpool`, `telemetrydb`, `uuid`, stdlib). No other
      module imports `internal/telemetry/db` (`telemetrydb`). No `pgtype` type appears in any
      public type or interface signature. No km field on any domain type or any DB column.
      `internal/gateway`, `html/template`, and `templ` are not imported by telemetry.
