Source: MAG-7 — https://linear.app/magus-monitor/issue/MAG-7/date-filters
Roadmap: openspec/roadmaps/RM8-history-date-range.md
Tier: 1 of 2 (telemetry; tier 2 is `RM8-gateway-history-date-range`, module `gateway`)

## Why

The dashboard history endpoint today fetches via `telemetry.Reader.SnapshotsByVehicleSince(ctx,
uid, teslaID, since)` — an **open lower bound** (`captured_at >= since`, `LIMIT 400`). Linear
MAG-7 wants the endpoint to take a **bounded calendar-day window** `?start=YYYY-MM-DD&end=YYYY-MM-DD`
(`end` inclusive) instead of the opaque `?days=N` count, with a fixed `[start..end]` date axis so
the odometer and battery charts share identical day labels (fixing the MAG-7 odometer/battery
offset). The RM8 roadmap splits that feature across two modules:

- **Tier 1 (this change, `telemetry`):** give the read port a first-class **bounded-window** method.
- **Tier 2 (`gateway`, separate change):** rewrite the HTTP handler, the fixed-axis rendering, the
  preset selector, and the convention doc.

Per Decision #1 of the 2026-08-09 grill-me interview (recorded in
`openspec/roadmaps/RM8-history-date-range.md` Decisions #1, #2, #4), the start/end convention covers
the **internal Go read port too**, not only the HTTP surface — so the bounded-window read is a
first-class `telemetry.Reader` method rather than a gateway-only clamp over `Since`. A bounded port
makes the convention honest: the gateway cannot leave the window open by accident, and any future
read consumer inherits the same bounded semantics.

## What Changes

- **Add `SnapshotsByVehicleBetween(ctx, accountID, teslaID, start, end)` to the `telemetry.Reader`
  interface.** `start` and `end` are whole calendar days, UTC-midnight-bounded; `end` is
  **inclusive** (mirrors the HTTP contract, Decision #2). The method returns every `Snapshot` whose
  **`EffectiveDate` calendar day falls in `[start, end]` inclusive**, ordered ascending by
  EffectiveDate (== ascending by `captured_at`; `EffectiveDate` is monotonic in `CapturedAt`).
- **No DB schema change, no migration, no new index.** This is a new sqlc query over the existing
  `vehicle_snapshots` columns, reusing the existing `idx_vehicle_snapshots_vehicle_time
  (account_id, tesla_id, captured_at)` ASCENDING index as a forward range scan (design D2 / D3).
- **The port is a clean `Between(start, end)`.** The 1-day lookback the gateway needs for the first
  odometer delta is a **gateway concern** — tier 2 will pass `start-1day` to this method; the port
  has **no lookback parameter** (Decision #4). Window math (UTC-midnight bounds, inclusive-`end`
  semantics) is honored by translating `start`/`end` into `captured_at` bounds inside the
  implementation; the public method stays a plain window.
- **`SnapshotsByVehicleSince` is kept unchanged** (additive / non-breaking — Decision #4). Existing
  callers (the gateway's current `?days=N` history handler, until tier 2 lands) still use it; it is
  NOT removed or deprecated in this change. `Since` (open lower bound) and `Between` (bounded window)
  serve different access patterns; deprecation/removal, if ever, is a separate change.
- **Reuse the single DB→domain mapper.** The new query returns `telemetrydb.VehicleSnapshot` rows
  with the same explicit column list as `SnapshotsByVehicleSince`; they map to the domain `Snapshot`
  via the existing `rowToSnapshot` (`internal/telemetry/mapping.go`), which already populates
  `EffectiveDate = CapturedAt.AddDate(0,0,-1)` (added by `telemetry-add-effective-date`). No mapper
  change, no per-method duplication.
- **Read paths affected (hot reads named per the perf rule):**
  - `telemetry.Reader.SnapshotsByVehicleBetween` — the **dashboard history hot path** (per-vehicle
    snapshot window for the history charts). Once tier 2 lands, every history-chart render routes
    here. The query is an indexed forward range scan over `(account_id, tesla_id, captured_at)` with
    no sort step and no new DB object. `SnapshotsByVehicleSince` (the current hot path) is untouched.
  - No write path, no collection path, no other read path is affected.

## Capabilities

### New Capabilities
- `telemetry`: **Snapshot Range Read** — a bounded `[start, end]`-inclusive range read over the
  existing nightly-snapshot table, returning snapshots whose `EffectiveDate` calendar day falls in
  the window, ordered ascending by EffectiveDate. See `specs/telemetry/spec.md`.

### Modified Capabilities
<!-- none — `SnapshotsByVehicleSince` and `LatestSnapshotsByAccount` are unchanged. -->

## Impact

- **Module:** `internal/telemetry` only. The gateway consumes `SnapshotsByVehicleBetween` in tier 2
  (separate change); this tier adds the port and its impl.
- **Files:**
  - `internal/telemetry/telemetry.go` — add `SnapshotsByVehicleBetween` to the `Reader` interface.
  - `internal/telemetry/db/query.sql` — add the `SnapshotsByVehicleBetween :many` query (mirror
    `SnapshotsByVehicleSince`'s column list, index-reuse comment, and LIMIT; add the inclusive
    `end` bound on `captured_at`).
  - `make sqlc` — regenerate `internal/telemetry/db/` generated files (codegen, not a build).
  - `internal/telemetry/reader.go` + `internal/telemetry/service.go` — implement the method on the
    `reader`/`dbStore` types, translating `start`/`end` to `captured_at` bounds and mapping rows
    via the existing `rowToSnapshot`.
  - `internal/telemetry/reader_test.go` (or `db_read_integration_test.go`) — add read tests: empty
    window, partial window, end-inclusive boundary, ascending-by-EffectiveDate ordering, tenant
    isolation (`account_id` filter).
- **APIs:** `telemetry.Reader` gains one method. This is a **non-breaking** interface addition — any
  `Reader` implementer outside the module (none today; `reader.go`'s `*reader` is the only
  implementation) would need to add the method, but the only consumer is the gateway, which gains the
  call site in tier 2. Existing `Reader` call sites are unaffected.
- **Dependencies:** none added. No DB migration. No index change.
- **Breaking:** No. This change is additive / non-breaking.
- **Performance:** the new read path is an indexed forward range scan over the existing
  `(account_id, tesla_id, captured_at)` index (no sort step, no new index). Bounded `end` makes the
  scan *cheaper* than the open `Since` scan for the same dashboard window. `LIMIT 400` safety cap
  mirrors `Since`. See `design.md` §Index plan.
- **Cookbook binding interview:** the proposal rule requires the grill-me skill. The binding
  interview is the 2026-08-09 grill-me with the user, whose outcomes are recorded verbatim as
  Decisions #1, #2, #4 in `openspec/roadmaps/RM8-history-date-range.md`. D1 = bounded read is a
  first-class `telemetry.Reader` method; D2 = `start`/`end` UTC-midnight-bounded, `end` inclusive;
  D4 = the 1-day lookback is a gateway concern, no port parameter. Those decisions are NOT
  re-litigated here — they are applied.