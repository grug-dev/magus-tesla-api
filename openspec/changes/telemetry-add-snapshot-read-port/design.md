## Context

Tier 4 of `openspec/roadmaps/nightly-vehicle-telemetry.md`. Tier 3
(`telemetry-add-nightly-snapshots`) is archived; `vehicle_snapshots` exists and is
populated nightly. This tier adds the read surface; no collection logic changes.

The gateway (tier 5) will call the new `Reader` port in-process to populate the
dashboard — one call per page render, returning the latest snapshot for every vehicle
an account owns. The port must be added here, not in the gateway, because telemetry
owns its data.

## Goals / Non-Goals

**Goals:**
- Expose a Go interface (`Reader`) the gateway can consume without touching
  `telemetrydb` or `vehicle_snapshots` directly.
- Return the latest stored `Snapshot` per vehicle for a given `accountID` in a
  single Postgres query (no N+1).
- Keep `pgtype` internal to the module; map to/from domain types at the DB boundary.
- Preserve `sentry_mode` nil ↔ SQL NULL fidelity on reads (mirrors the write path).
- Offline-testable via a fake store seam; `DATABASE_URL`-gated store test for the
  real query.

**Non-Goals:**
- Any mutation, new table, or migration.
- Per-vehicle single-lookup convenience method (the batch method suffices; a
  single-vehicle helper can be added in a future change if needed).
- Pagination or date-range filtering (dashboards need only the latest row per vehicle).
- HTTP/JSON surface for this module (none required — `ai/architecture.md` §3).
- Displaying the data (tier 5, `gateway-read-stored-vehicles`).

## Decisions

### D1 — Read port shape: `Reader` interface, separate from `Collector`

```go
// Reader exposes the telemetry module's stored snapshots for read-only
// consumption by the gateway and other callers. It is the second half of
// the telemetry public port; the first half (Collector) is the write path.
// Callers must never import telemetrydb directly — all access goes through
// this interface.
type Reader interface {
    // LatestSnapshotsByAccount returns the most-recently captured snapshot
    // for each vehicle belonging to the given account. If the account has
    // no stored snapshots, it returns an empty (non-nil) slice and a nil
    // error. Order of the returned slice is unspecified.
    LatestSnapshotsByAccount(ctx context.Context, accountID uuid.UUID) ([]Snapshot, error)
}
```

`NewReader(pool *pgxpool.Pool) Reader` is the constructor; the concrete type is
unexported (`reader` or embedded in the existing `store`). The `Reader` interface is
declared in `internal/telemetry/telemetry.go` alongside `Collector`, keeping all port
declarations in one file (the module's contract file). The implementation lives in
`internal/telemetry/reader.go` (or `store.go` if the existing unexported `dbStore` is
extended — see D3).

**Why a separate interface from `Collector`:** the gateway only needs to read; giving
it a `Collector` would expose `CollectAll` unnecessarily. Go's interface segregation
principle: depend only on the methods you use. Tier 5 can be wired with just `Reader`.

### D2 — SQL query: DISTINCT ON for latest-per-vehicle in one round trip

```sql
-- name: LatestSnapshotsByAccount :many
-- Return the latest stored snapshot for each vehicle owned by the given account.
-- DISTINCT ON (tesla_id) with ORDER BY tesla_id, captured_at DESC picks the row
-- with the highest captured_at per tesla_id — one Postgres index scan, no N+1.
SELECT DISTINCT ON (tesla_id)
    id, account_id, tesla_id, captured_at, raw_data,
    battery_level, battery_range, charging_state, charge_limit_soc,
    odometer, inside_temp, outside_temp, locked, sentry_mode,
    car_version, latitude, longitude
FROM vehicle_snapshots
WHERE account_id = @account_id
ORDER BY tesla_id, captured_at DESC;
```

The existing `(account_id, tesla_id, captured_at)` index added in tier 3 covers this
query: the planner can satisfy the `WHERE account_id = ?` filter and the
`ORDER BY tesla_id, captured_at DESC` from the same index, making this a single
efficient range scan with no sort.

**Why not re-use `ListSnapshotsByVehicle`:** that query requires a `tesla_id` argument
and returns all rows for one vehicle. Calling it N times (once per vehicle) is N+1 —
exactly the anti-pattern this batch design avoids. The dashboard lists all of a user's
vehicles in one render; a single DISTINCT ON query is the correct design.

**Why not a CTE / subquery approach:** DISTINCT ON is idiomatic Postgres, uses the
existing index, and produces one query node. A subquery (`SELECT ... FROM vs WHERE
captured_at = (SELECT MAX(...) GROUP BY tesla_id)`) produces a correlated subquery or
a join, both of which require a separate aggregation pass. DISTINCT ON is simpler and
more efficient here.

### D3 — Reuse the unexported `dbStore` seam; extend it for the read method

The tier-3 `service.go` already defines an unexported `store` interface (the `dbStore`
seam) so the collection service is testable without a real DB. The new read method
should extend that same seam so the `Reader` implementation is also offline-testable.

The concrete `pgStore` struct (which wraps `*telemetrydb.Queries`) gains the new
method. The `store` interface in `service.go` (or a shared internal `store.go`) is
extended with the new read signature. The `Reader` concrete type (`reader` struct) holds
a `store` interface value — making its `LatestSnapshotsByAccount` fully unit-testable
via a fake that implements the extended seam.

**Concrete pgStore method:**
```go
func (s *pgStore) LatestSnapshotsByAccount(ctx context.Context, accountID uuid.UUID) ([]Snapshot, error) {
    rows, err := s.q.LatestSnapshotsByAccount(ctx, accountID)
    if err != nil {
        return nil, err
    }
    snaps := make([]Snapshot, 0, len(rows))
    for _, r := range rows {
        snaps = append(snaps, rowToSnapshot(r))
    }
    return snaps, nil
}
```

`rowToSnapshot` is an internal mapping helper that converts
`telemetrydb.VehicleSnapshot` → `Snapshot`:
- `pgtype.Timestamptz.Time` → `time.Time` (plain domain type; `pgtype` stays inside
  the module — `ai/go-conventions.md` §persistence).
- `pgtype.Bool` → `*bool` for `sentry_mode`: `{Valid: false}` → `nil`,
  `{Valid: true, Bool: false}` → `(*bool)(false)`, `{Valid: true, Bool: true}` →
  `(*bool)(true)`. Preserves nil ↔ NULL fidelity (design D1 of tier 3).
- `int32` → `int` for `BatteryLevel` and `ChargeLimitSoc` (sqlc generates `int32`;
  domain `Snapshot` uses `int` — same conversion the write path already does).
- All other fields are value-compatible (float64, string, bool, uuid.UUID, []byte).

`rowToSnapshot` may already exist in `service.go` from tier 3 (the write path maps in
the opposite direction). If so, extract it to a shared `mapping.go` to avoid
duplication. If it was inlined, factor it out here.

### D4 — `pgtype` never leaks out of the module

The `Reader` interface returns `[]Snapshot` (our domain type). `pgtype.Timestamptz`,
`pgtype.Bool`, and the generated `telemetrydb` package are all confined to
`internal/telemetry/` — no caller (the gateway, cmd/) ever sees them. This mirrors the
write path established in tier 3 and is the invariant enforced in `AGENTS.md`.

### D5 — Empty-account result: return `([]Snapshot{}, nil)`, never `(nil, nil)`

When an account has no snapshots (e.g. the poller hasn't run yet), the query returns
zero rows. The store method returns a non-nil empty slice (the `make([]Snapshot, 0,
len(rows))` pattern) so callers can range over the result without a nil check.
Returning `nil` would be a footgun for the gateway rendering a vehicle list.

### D6 — No migration, no schema change

`vehicle_snapshots` and `poll_attempts` are unchanged. The only DB artifact added is
the new `LatestSnapshotsByAccount` sqlc query — a `SELECT`, not a DDL statement. The
`make sqlc` regen adds one new function to the generated `query.sql.go`; the
`models.go` is untouched (no new types needed — `VehicleSnapshot` already exists).

### D7 — Testing: fake store seam (offline) + `DATABASE_URL`-gated real-query test

**Offline unit test** (`reader_test.go`):
- Define a `fakeStore` that implements the extended `store` seam.
- Cases: (a) account with two vehicles → two snapshots returned, each the latest;
  (b) account with no snapshots → empty slice, nil error.
- Assert `BatteryRangeKm()` / `OdometerKm()` return correct values (the Km companion
  methods are on `Snapshot` — just call them, confirm they use `milesToKm = 1.609344`).
- No DB, no network, no Tesla call.

**DATABASE_URL-gated integration test** (`db_read_integration_test.go`):
- Inserts two snapshots for vehicle A (different `captured_at`), one for vehicle B.
- Calls `LatestSnapshotsByAccount` and asserts: vehicle A returns the newer snapshot;
  vehicle B returns its only snapshot; exactly two rows returned total.
- A second call for an account with no data returns empty slice, nil error.
- Self-skips when `DATABASE_URL` is unset (`t.Skip` at top of test), same pattern as
  tier-3 `db_integration_test.go`.

## Persistence scope

The `telemetry` module owns `vehicle_snapshots` (and `poll_attempts`). No other module
reads or writes these tables. The new query is additive to `internal/telemetry/db/`
only. `sqlc.yaml` and `Makefile` are unchanged (the second `sql:` entry and the
migration wiring were added in tier 3 — nothing new needed for a query-only addition).
`make sqlc` is run as a leader-integrated step after `query.sql` is updated.

## Summary of new files and modifications

| Path | Action | Notes |
|---|---|---|
| `internal/telemetry/telemetry.go` | Modify | Add `Reader` interface declaration |
| `internal/telemetry/store.go` | New (or modify `service.go`) | Extend `store` seam + pgStore read impl + `rowToSnapshot` helper |
| `internal/telemetry/reader.go` | New | `NewReader(pool) Reader` constructor + unexported `reader` struct |
| `internal/telemetry/db/query.sql` | Modify | Add `LatestSnapshotsByAccount :many` DISTINCT ON query |
| `internal/telemetry/db/query.sql.go` | Regenerated | Leader-integrated `make sqlc` |
| `internal/telemetry/reader_test.go` | New | Offline unit test (fake store seam) |
| `internal/telemetry/db_read_integration_test.go` | New | `DATABASE_URL`-gated store test |
| `internal/telemetry/AGENTS.md` | Modify | Append `Reader` to public-interface section |
