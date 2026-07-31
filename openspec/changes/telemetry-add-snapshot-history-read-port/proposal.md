## Why

The dashboard port (archived change `gateway/2026-07-07-add-vehicle-dashboard`) renders a
single-vehicle **bento grid**, faithfully translating the Stitch "Dashboard - Read-Only Metric
Monitor" screen. Two cards in that bento are **honest placeholders** today — "Odometer history"
and "Battery history" — each showing an "Awaiting nightly snapshots" empty state
(`dashHistoryEmpty()`), because the gateway has **no read port that returns more than the single
latest snapshot per vehicle**. The only telemetry read method the gateway is allowed to call is
`telemetry.Reader.LatestSnapshotsByAccount` (`openspec/specs/telemetry/spec.md` §"Latest Snapshot
Read Port"), a batched `DISTINCT ON` query that returns exactly one row per vehicle.

The platform already stores the data the charts need: `vehicle_snapshots` is **append-only**
(Nightly Vehicle Snapshot Capture requirement), so a vehicle with any tenure has up to one
snapshot per nightly run. A 30-day window therefore yields up to ~30 odometer points and ~30
battery-level points — exactly the shape the two mini bar charts render. The read path simply does
not exist yet.

This change adds the missing **per-vehicle history read port** so the dashboard can replace the
two placeholders with real N-day series, without crossing a module boundary or reading telemetry
tables directly (the gateway reaches the new method through the existing `telemetry.Reader`
interface).

This is **tier 1 of roadmap `RM5-dashboard-odometer-battery-history`**. The gateway consumption
(the API endpoint, the SVG bar charts, and the days selector) is **tier 2**
(`gateway-dashboard-history-charts`). Keeping the port standalone preserves change-locality
(write-once, read-many): every future historical feature (battery-degradation curve, efficiency
trend, phantom-drain detector) consumes the same method without re-touching telemetry.

## What Changes

Primary module: **`internal/telemetry/`** (the port owner + its module-scoped
`internal/telemetry/db`). The **read port itself** is this change's entire scope.

### (a) Extend the `telemetry.Reader` interface

Add one method to the existing `Reader` interface (`internal/telemetry/telemetry.go`):

```go
type Reader interface {
	LatestSnapshotsByAccount(ctx context.Context, accountID uuid.UUID) ([]Snapshot, error)
	// SnapshotsByVehicleSince returns the nightly snapshots captured for the given
	// vehicle (within the given account) since `since` (inclusive), oldest-first.
	// Empty (non-nil) slice when none exist. Scoped by account_id AND tesla_id:
	// per-account isolation is enforced by the query's account_id match (D2,
	// defense-in-depth) even though the gateway only ever resolves tesla_id from
	// account.RegisteredVehicles(uid).
	SnapshotsByVehicleSince(ctx context.Context, accountID uuid.UUID, teslaID int64, since time.Time) ([]Snapshot, error)
}
```

Returns the existing `Snapshot` domain type (no new DTO, no vendor suffix) — which already carries
`Odometer`/`OdometerKm()`, `BatteryLevel`, and `BatteryRange`/`BatteryRangeKm()`, the three fields
tier 2's charts need. Same miles-native fields + `Km()` companions; `raw_data` is included so
future fields can be back-filled without a second read.

**Decision D1 (resolved):** `since` is a `time.Time`, not a `days int`. The window boundary lives
in the **caller** (tier 2's gateway computes "N days ago" from the `days` query param). The port
stays a pure data accessor so every future consumer picks its own window — and it makes the window
"sent to the API" exactly as the user asked.

### (b) Add the SQL query — no new index, no migration

New `internal/telemetry/db/query.sql` entry (sqlc-generated), a single range scan:

```sql
-- name: SnapshotsByVehicleSince :many
SELECT ... (all columns, same list as LatestSnapshotsByAccount)
FROM vehicle_snapshots
WHERE account_id = @account_id
  AND tesla_id = @tesla_id
  AND captured_at >= @since
ORDER BY captured_at ASC
LIMIT 400;
```

**Decision D3 (resolved — no new index):** the existing index
`idx_vehicle_snapshots_vehicle_time (account_id, tesla_id, captured_at)` — created ascending on
`captured_at` in `20260710000002_init_telemetry.sql` — already serves this query as a **forward
range scan**: `(account_id, tesla_id)` are the leading exact-match columns and `captured_at >= $3
ORDER BY captured_at ASC` is a range on the trailing column read in index order, with **no sort
step**. So this change adds **no table, no column, no index, no migration** — only a sqlc query and
a Go method. This corrects this proposal's earlier "one new covering index" assumption.

**Decision D2 (resolved):** the query filters `account_id` as defense-in-depth (belt-and-braces
tenant isolation), matching the method signature. **Decision D4 (resolved):** a `LIMIT 400` is
baked in as a safety valve against an oversized result if capture cadence ever increases; the
30-day window already bounds normal results to ~30 rows.

### (c) Honor the existing invariants on the new path

- Empty (non-nil) slice, no error, when no historical snapshots exist (matches the latest-port's
  empty contract so the gateway degrades the same way).
- Miles-native; tier 2 converts via `Snapshot.OdometerKm()` / `BatteryRangeKm()` and formats with
  the existing `formatKm` helper — no new unit logic in the read port.
- No caller outside the module imports `internal/telemetry/db` (shared with the latest-port spec).

## Breaking

No. Adding a method to the `telemetry.Reader` **interface** is additive for the gateway and the
default `NewReader` implementation. The only compile break is for **test fakes** that implement
`Reader` literally — they gain a method with a trivial stub returning `nil, nil` (the `fakeReader`
in `internal/gateway/handlers/handlers_test.go` and any other in-tree fake). The nightly collector
(`cmd/poller`, `telemetry.Scheduler`) depends on `Collector`, not `Reader`, so it is unaffected. No
DB object is added, renamed, or removed.

## Modules affected

- `internal/telemetry/` — primary: adds the `Reader` method, the sqlc-backed implementation in
  `internal/telemetry/reader.go` + `internal/telemetry/db/query.sql`, and the generated
  `telemetrydb` method. No change to capture (`Collector`), to the existing latest read port, or to
  domain types.
- `internal/gateway/handlers/` — secondary, compile-only: any `Reader` fake gains a one-line stub.
  No gateway runtime change in this change (consumption is tier 2).
- No other `internal/` module.

## Database Changes

**None.** No new table, column, index, constraint, view, or migration — the existing
`idx_vehicle_snapshots_vehicle_time (account_id, tesla_id, captured_at)` index already serves the
new query (see (b), Decision D3). Because this change touches **no database object**, the
`database` design gate does **not** trigger. `design.md` is still provided (per the
performance-sensitive proposal rule — this touches a hot read path) and documents the query plan,
the index-reuse rationale, and the rejected alternative of a pre-aggregated `daily_summary` table.

## Read Paths Affected

A single new read path, run **once per dashboard render** that needs the charts (tier 2 decides
lazy-behind-an-htmx-swap vs inline). It is a bounded `account_id + tesla_id + captured_at >= since`
range scan on `vehicle_snapshots`, served by the existing index, returning up to ~30 rows (one per
nightly capture) for a 30-day window, capped at 400. Existing read paths are unchanged:
`LatestSnapshotsByAccount` keeps its `DISTINCT ON` plan; the new method does not share that query's
plan (it is NOT "latest per vehicle", it is "all per vehicle since"). No N+1 — the new method is a
single query for a single (selected) vehicle; the gateway does not loop it across the account's
vehicles.

## Capabilities

### Added / Modified Capabilities

- **`telemetry`** — new "Snapshot History Read Port" requirement (delta:
  `openspec/changes/telemetry-add-snapshot-history-read-port/specs/telemetry/spec.md`): ordered
  oldest-first return, empty-on-no-data, per-vehicle + per-account scoping, `since`-inclusive
  boundary, miles-native fields, and the "callers never access the telemetry database directly"
  invariant (shared with the latest-port spec).

### Consumed Capabilities (no change to their specs)

- **Nightly Vehicle Snapshot Capture** (unchanged) — the history port reads what capture already
  stores; no capture change.

## Resolved decisions

Resolved with the user in the 2026-07-29 brainstorm (recorded as RM5 roadmap decisions RD1–RD4):

- **D1** — `since` is a `time.Time` (the gateway computes the window). Resolved: caller controls
  the window; the port is a pure data accessor.
- **D2** — Keep the `account_id` defense-in-depth filter (method takes `accountID`). Resolved:
  include it — defense-in-depth over a microsecond.
- **D3** — Reuse the existing `(account_id, tesla_id, captured_at)` ascending index; **no new
  index, no migration**. Resolved by verifying the migration: the index already serves the forward
  range scan.
- **D4** — Add a generous `LIMIT 400` safety cap. Resolved: cheap guard against an accidental large
  result set.
