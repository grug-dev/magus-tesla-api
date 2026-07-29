## Why

The dashboard port (change `apex-dashboard-from-stitch`) renders a single-vehicle
**bento grid**, faithfully translating the Stitch "Dashboard - Read-Only Metric
Monitor" screen. Two cards in that bento are **honest placeholders** today —
"Odometer history" and "Battery history" — each showing an "Awaiting nightly
snapshots" empty state, because the gateway has **no read port that returns more
than the single latest snapshot per vehicle**. The only telemetry read method the
gateway is allowed to call is `telemetry.Reader.LatestSnapshotsByAccount`
(`openspec/specs/telemetry/spec.md` §"Latest Snapshot Read Port"), a batched
`DISTINCT ON` query that returns exactly one row per vehicle.

The platform already stores the data the charts need: `vehicle_snapshots` is
**append-only** (Nightly Vehicle Snapshot Capture requirement), so a vehicle with
any tenure has up to one snapshot per nightly run. A 30-day window therefore yields
up to ~30 odometer points and ~30 battery-level points — exactly the shape the two
mini bar charts render. The read path simply does not exist yet.

This change adds the missing **per-vehicle history read port** so the dashboard can
replace the two placeholders with real 30-day series, without crossing a module
boundary or reading telemetry tables directly (the gateway reaches the new method
through the existing `telemetry.Reader` interface).

Decided in the 2026-07-28 dashboard-port review (leader ↔ user): the dashboard is
the first real consumer, and the only data it needs from history today is
`captured_at`, `odometer`, and `battery_level`. Exposing the full `Snapshot` type
is consistent with the existing read port (returns the same domain type) and avoids
a new DTO; the gateway maps to display strings the same way it does for the latest
snapshot.

## What Changes

Primary module: **`internal/telemetry/`** (the port owner + its module-scoped
`internal/telemetry/db`). Gateway consumption is a follow-on gateway change; the
**read port itself** is this change's entire scope.

### (a) Extend the `telemetry.Reader` interface

Add one method to the existing `Reader` interface (`internal/telemetry/telemetry.go`):

```go
type Reader interface {
	LatestSnapshotsByAccount(ctx context.Context, accountID uuid.UUID) ([]Snapshot, error)
	// SnapshotsByVehicleSince returns the nightly snapshots captured for the given
	// vehicle since `since` (inclusive), oldest-first. Empty (non-nil) slice when
	// none exist. Scoped to the vehicle; per-account isolation is enforced by the
	// query's account_id match (the caller passes the vehicle it resolved for the
	// signed-in account, so no cross-account leak is possible).
	SnapshotsByVehicleSince(ctx context.Context, teslaID int64, since time.Time) ([]Snapshot, error)
}
```

Returns the existing `Snapshot` domain type (no new DTO, no vendor suffix). Same
miles-native fields + `Km()` companions; raw_data is included so future fields can
be back-filled without a second read.

### (b) Add the SQL query + index

New `internal/telemetry/db/queries.sql` entry (sqlc-generated): a single range
scan on `vehicle_snapshots` filtered by `tesla_id = $1 AND captured_at >= $2`,
ordered by `captured_at ASC`. The existing `vehicle_snapshots` table already has an
index on `(tesla_id, captured_at DESC)` serving the latest-snapshot query; a
**covering index** keyed `(tesla_id, captured_at ASC)` (or the same multi-column
index used in both directions — index plan decided in design.md) supports
`SnapshotsByVehicleSince` as an index-only range scan. No new table, no new
column, no migration of data — only one new index (a DB object, so design.md is
REQUIRED and the database design gate triggers).

### (c) Honor the existing invariants on the new path

- Empty (non-nil) slice, no error, when no historical snapshots exist (matches the
  latest-port's empty contract so the gateway degrades the same way).
- Per-account isolation: `tesla_id` is only ever resolved by the gateway from
  `account.RegisteredVehicles(uid)`, so a tenant-scoped caller cannot request
  another account's vehicle. The query matches `account_id` too as defense-in-depth
  (design.md confirms whether `account_id` is an extra filter or redundant given the
  caller's resolution path — read path naming convention).
- Miles-native; the gateway converts via `Snapshot.OdometerKm()` / formats with the
  existing `formatKm` helper — no new unit logic in the read port.

### (d) Gateway consumption — out of scope (follow-on)

Wiring `SnapshotsByVehicleSince` into `handlers.dashboardFor` and replacing the two
`dashHistoryEmpty()` placeholders with real bars is a **separate gateway change**
(`apex-dashboard-history-charts`, to be proposed after this port lands). Keeping
the port addition standalone preserves change-locality (write-once, read-many):
every future historical feature (battery-degradation curve, efficiency trend,
phantom-drain detector) consumes the same method without re-touching telemetry.

## Breaking

No. Adding a method to the `telemetry.Reader` **interface** is additive for the
gateway and the default `NewReader` implementation. The only compile break is for
**test fakes** that implement `Reader` literally — they gain a method with a
trivial stub returning `nil, nil` (the `fakeReader` in
`internal/gateway/handlers/handlers_test.go` and any other in-tree fake). The
nightly collector (`cmd/poller`, `telemetry.Scheduler`) depends on `Collector`,
not `Reader`, so it is unaffected. No DB object is renamed/removed; one index is
added.

## Modules affected

- `internal/telemetry/` — primary: adds the `Reader` method, the sqlc-backed
  implementation in `internal/telemetry/db`, the query, and the index migration.
  No change to capture (Collector), to the existing latest read port, or to domain
  types beyond what the method signature implies.
- `internal/gateway/handlers/` — secondary, compile-only: any `Reader` fake gains a
  one-line stub. No gateway runtime change in this change (consumption is the
  follow-on gateway change).
- No other `internal/` module.

## Database Changes

One new index on `vehicle_snapshots` (covering `(tesla_id, captured_at)` for the
ascending range scan), authored as a goose migration under
`internal/telemetry/db/migrations`. **No new table, no new column, no data
back-fill.** design.md is REQUIRED (per `openspec/config.yaml` rules.design) and
will include: the exact `CREATE INDEX` DDL, the rationale for ascending vs
reusing the existing descending index, the read-pattern justification (one range
scan per dashboard render, bounded by the 30-day `since` argument, ~30 rows),
and a rejection note for the alternative of a pre-aggregated `daily_summary`
table (deferred — premature; the raw range scan is cheap at this cardinality and
keeps the source truthful, mirroring the AGENTS.md "Prefer storing historical
events rather than overwriting state" philosophy). The database design gate
triggers and passes on the index-only plan.

## Read Paths Affected

A single new read path, run **once per dashboard render** (or, more precisely,
once per dashboard render that needs the charts; the gateway follow-on decides
whether to fetch history lazily behind an htmx swap or inline). It is a bounded
`tesla_id + captured_at >= since` range scan on `vehicle_snapshots`, served by the
new covering index, returning up to ~30 rows (one per nightly capture) for a
30-day window. Existing read paths are unchanged:
`telemetry.Reader.LatestSnapshotsByAccount` keeps its `DISTINCT ON` plan; the new
method does not share that query's plan (it is NOT "latest per vehicle", it is
"all per vehicle since"). No N+1 — the new method is a single query for a single
(selected) vehicle; the gateway does not loop it across the account's vehicles.

## Capabilities

### Added / Modified Capabilities

- **`telemetry`** — new "Snapshot History Read Port" capability. Behavioral spec
  delta (`openspec/changes/telemetry-add-snapshot-history-read-port/specs/telemetry/spec.md`)
  authoring deferred to the design step per the user's "create the proposals" ask;
  will cover: ordered-oldest-first return, empty-on-no-data, per-vehicle scoping,
  `since`-inclusive boundary, and the "callers never access the telemetry database
  directly" invariant (shared with the latest-port spec).

### Consumed Capabilities (no change to their specs)

- **Nightly Vehicle Snapshot Capture** (unchanged) — the history port reads what
  capture already stores; no capture change.

## Resolved decisions

None yet — this is the **proposal only** (per the user's 2026-07-28 request to
"create the proposals"). Open questions to resolve in a `grill-me` pass before
`design.md` authoring (recorded here so design.md authoring can cite them by ID):

- **D1** — Is `since` a `time.Time` (UTC midnight) or a `days int` (30)? `time.Time`
  keeps the boundary logic in the caller (the gateway computes "30 days ago at the
  call site"); `days int` bakes the window into the port. Recommendation: `time.Time`
  (caller controls the window; the port is a pure data accessor).
- **D2** — Confirm defense-in-depth `account_id` filter: the gateway already resolves
  `teslaID` from `account.RegisteredVehicles(uid)`, so a cross-account request is
  impossible from the gateway. Should the query still filter `account_id` anyway
  (belt-and-braces against a non-gateway caller), or is that redundant and a slight
  perf cost? Recommendation: include it — defense-in-depth over a microsecond.
- **D3** — Ascending new index vs reuse the existing `(tesla_id, captured_at DESC)`
  index for the ascending range scan. A descending index CAN serve an ascending
  range scan with a backward scan; design.md confirms the planner's preference and
  whether a dedicated ascending index is worth the extra write cost on the nightly
  append path.
- **D4** — Cap on returned rows? The 30-day window bounds it at ~30, but a vehicle
  with multiple snapshots per day (if capture cadence ever increases) could grow.
  A `LIMIT` (e.g. 365) as a safety valve vs unbounded. Recommendation: add a
  generous `LIMIT` (e.g. 365) — cheap, prevents an accidental large result set.