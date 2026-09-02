Source: MAG-41 — https://linear.app/magus-monitor/issue/MAG-41/fix-boundary-guard
Roadmap: openspec/roadmaps/RM40-gateway-drop-telemetry-dependency.md
Tier: 2 of 2 (`gateway`; depends on tier 1, `RM40-analytics-add-battery-level-read`,
implemented, reviewer-approved and archived — `analytics.Reader.BatteryLevelByDay`
already exists and is consumed here for the first time)
Unit tests: excluded (new); existing tests repaired where this change breaks them
(roadmap D7). The owner's standing default is no new unit tests. `handlers_test.go`
and `history_test.go` stop compiling the moment `TelemetryReader` leaves `Deps` —
reworking their fakes is repair, not new coverage. design.md authors the exact
expected values these repaired fixtures must produce, up front, per
`ai/go-conventions.md`'s "author expected values first" rule.

## Why

`RM40-gateway-drop-telemetry-dependency` (roadmap, read in full before this proposal)
wants `make boundary-guard` to pass clean, with **zero** `// boundary:allow:` escape
hatches: `internal/gateway/` must stop naming `internal/telemetry` at all. The
roadmap's own investigation established that exactly ONE production call site
remains — `internal/gateway/handlers/history.go:321`'s battery-history chart read —
consuming only three fields (`EffectiveDate`, `BatteryLevelPct`, `BatteryRangeKm`) off
`telemetry.Snapshot`. Tier 1 (already implemented) added
`analytics.Reader.BatteryLevelByDay(ctx, accountID, teslaID, start, end)
([]analytics.DayBattery, error)`, the fourth sibling in the module's existing
`ConsumedByDay`/`OdometerDeltaByDay`/`BatteryLevelByDay` vocabulary, SELECTing the
same three values from `vehicle_metrics` — the table `internal/analytics` already
owns and already serves the gateway's other two history charts from. This tier
retargets the gateway's one remaining call site onto that port and removes every
other `internal/telemetry` name (import, `Deps`/`Handler` field, `telemetry.Snapshot`
type) from `internal/gateway/`.

Every decision below is carried verbatim from the roadmap (D1–D8, settled with the
owner via `grill-me` before the roadmap was written) — none is re-opened here.

## What Changes

- **`internal/gateway/handlers/history.go`** — `buildHistoryView` replaces the
  `h.telemetryReader.SnapshotsByVehicleBetween(ctx, uid, teslaID, readStart, end)`
  call with `h.analyticsReader.BatteryLevelByDay(ctx, uid, teslaID, start, end)`.
  **The `readStart := start.AddDate(0, 0, -1)` one-day lookback is dropped entirely
  (roadmap D5)** — `vehicle_metrics.metric_date` is already the effective day, so the
  port returns exactly `[start, end]` with no extra day needed. The existing
  independent-failure behavior is preserved byte-for-byte: a battery-read error
  empties ONLY `v.Battery`, never `v.Odometer` or `v.Consumed`.
- **`buildBatteryChart` is retyped** from `func(ctx, snaps []telemetry.Snapshot,
  start, end time.Time) fragments.HistoryChart` to `func(ctx, days
  []analytics.DayBattery, start, end time.Time) fragments.HistoryChart`. It buckets
  on `DayBattery.Date` **verbatim** — never through `effectiveDayUTC` (roadmap D4) —
  mirroring exactly how `buildConsumedChart`/`buildOdometerChart` already bucket
  their own ports' `Date` fields. Every other line (the empty-chart rule, the
  per-day tooltip format, the `Present`/`HeightPct` shape, the Y-axis ticks) is
  unchanged; see design.md's Test Contract for why this is a pure characterization
  swap with byte-identical rendered output.
- **`internal/telemetry` import removed** from `history.go`.
- **`internal/gateway/handlers/handlers.go`** — `TelemetryReader` removed from
  `Deps`, `telemetryReader` removed from the `Handler` struct and `New()`'s wiring,
  `internal/telemetry` import removed.
- **`internal/gateway/gateway.go`** — `TelemetryReader` removed from `Deps` and from
  the `handlers.Deps` literal `NewEngine` builds, `internal/telemetry` import
  removed.
- **`internal/gateway/handlers/handlers_test.go`** — `fakeReader` (a `telemetry.Reader`
  test double) and `newHandlerWithReader` are deleted outright: every call site that
  used them (`TestDashboardFor_RegisteredEmptyPromptsConnect`,
  `TestDashboardFor_AccountReadErrorShowsNotice`,
  `TestVehicleSelect_FiresVehicleChangedTrigger`,
  `TestHome_SignedInRedirectsToDashboard`) never actually exercised
  `telemetryReader` — they only needed *a* `*Handler`, so they move to the existing
  `newHandler(acct, tsvc)` helper (or drop the now-nonexistent field from a direct
  `Deps{...}` literal). `internal/telemetry` import removed.
- **`internal/gateway/handlers/history_test.go`** — `fakeHistoryReader` (a
  `telemetry.Reader` test double backing the battery chart's old fixture path) is
  deleted; `fakeAnalyticsReader.BatteryLevelByDay`'s tier-1 compile-only panic stub
  is replaced with a real, call-recording, fixture-backed implementation (mirroring
  `ConsumedByDay`/`OdometerDeltaByDay`'s own shape on the same fake). Every one of
  the ~15 call sites that built a `fakeHistoryReader{historySnaps: ...}` fixture for
  the battery chart is reworked to build an `analytics.DayBattery` fixture instead,
  producing the SAME rendered numbers as before (design.md Test Contract). The
  `distancesFromSnaps`/`snapsForDays` helpers, which exist ONLY to feed the
  ODOMETER chart's `analytics.DayDistance` derivation and have no relationship to
  the battery-chart swap, are retyped off `telemetry.Snapshot` onto a new,
  test-file-local fixture struct so the file's `internal/telemetry` import can be
  dropped entirely (see design.md "distancesFromSnaps investigation" — this is the
  one non-trivial finding of this tier). `internal/telemetry` import removed.

**NOT in this change (leader-owned, cross-module integration):**
- `cmd/web/main.go:58`'s `TelemetryReader: telemetry.NewReader(pool)` injection into
  `gateway.Deps` must be removed once `Deps.TelemetryReader` no longer exists — this
  file is outside `internal/gateway/`, so it is not this worker's sandbox. Lines
  ~72 and ~86 of the same file call `telemetry.NewReader(pool)` for OTHER,
  unrelated consumers (not the gateway) and MUST NOT be touched — they stay.

## Breaking

**No — externally.** No HTTP route, request shape, or response shape changes. The
`GET /ui/dashboard/history` endpoint's `?start=&end=` contract, its three-chart
response, and every tooltip/label wording are unchanged (design.md Test Contract
proves this precisely for the battery chart, the only one this tier touches).

**No — internally, but one field is REMOVED, not just added:** `gateway.Deps` and
`handlers.Deps` both lose `TelemetryReader telemetry.Reader`. This IS a breaking
change to `gateway.Deps`'s Go-level contract — any external caller of
`gateway.NewEngine` that still sets `Deps.TelemetryReader` will fail to compile.
`cmd/web` is the only such caller in this repository, and its own fix is
leader-owned (see above), landing in the same wave this tier's artifacts are
applied.

## Modules Affected

- **`internal/gateway/`** — the only module whose files this worker edits:
  `handlers/history.go`, `handlers/handlers.go`, `gateway.go`,
  `handlers/handlers_test.go`, `handlers/history_test.go`.
- **`internal/analytics/`** — read, not written. `BatteryLevelByDay` already exists
  (tier 1); this tier is its first production caller.
- **`cmd/web/`** — read, not written by this worker; its `main.go:58` fix is
  leader-owned integration work in the same wave (see "What Changes").
- **`internal/telemetry/`** — untouched. This tier removes gateway's *dependency*
  on it; the module itself, its `Reader` port, and every OTHER consumer of
  `telemetry.NewReader(pool)` (the nightly poller, `cmd/web`'s other two call
  sites) are unaffected.

## Read Paths Affected — performance-sensitive, per `openspec/config.yaml`'s proposal rule

- **`GET /ui/dashboard/history`'s battery-chart read** moves from
  `telemetry.Reader.SnapshotsByVehicleBetween(readStart, end)` (one extra day
  fetched, `vehicle_snapshots` table) to `analytics.Reader.BatteryLevelByDay(start,
  end)` (exactly the requested window, `vehicle_metrics` table). Both reads are a
  single bounded range `SELECT` served by an existing, already-indexed,
  `account_id`-leading index (`vehicle_metrics_account_tesla_date_unique` per tier
  1's design.md; `(account_id, tesla_id, captured_at)` for the old telemetry read).
  **This is performance-neutral, not an improvement or a regression** — one bounded
  range read on an equally-indexed table replaces another; the one-day lookback
  drop (D5) removes exactly one row's worth of I/O from an already-cheap query, an
  effect too small to characterize as a win. No new index, no schema change, no
  write-path cost (Performance-Profile: read-heavy).
- **`OdometerDeltaByDay`/`ConsumedByDay`/`LatestMetricsByAccount`** — unaffected;
  this tier changes no other read path.

## Capabilities

### Modified Capabilities

- **Dashboard History Charts (`gateway`)** — the Battery history chart's data
  source changes from `internal/telemetry` to `internal/analytics`; see
  `specs/gateway/spec.md` for the full modified requirement. No user-visible
  request/response contract changes; see design.md's Test Contract for the
  characterization proof that rendered chart output is unaffected, plus the one
  accepted, self-healing edge case (roadmap D6: a day lagging the nightly
  recalculation watermark can briefly render as an empty bar where it previously
  would not have).

### Out of scope (explicitly deferred)

- **Renaming `i18n.KeyHistoryNoSnapshotTooltip`.** After this change the key's
  English text ("no snapshot") technically means "no `vehicle_metrics` row," but
  the roadmap explicitly flags this as a known wording nuance, not something to
  action — "renaming a bilingual catalogue key is a separate concern and the owner
  did not ask for it." Do not add a task to rename it.
- **Backfilling `vehicle_metrics`** to close the day-coverage gap with
  `vehicle_snapshots` (roadmap D6). Accepted as self-healing; not actionable within
  a refactor that touches no database object.
- **`cmd/web/main.go`'s injection removal** — leader-owned, see "What Changes."

## Testing

Per the Test-Execution-Policy: the implementing worker writes tests and runs `go
build ./...`, `go vet ./...`, `gofmt -l`, `make build`, `make vet`, `make bins`, and
the standalone guards (`make boundary-guard` is this change's own definition of
done) — never `go test ./...`, `make test`, `make test-with-db`, or `make check`.
The owner runs `go test ./internal/gateway/...` and reports results; until then this
tier's implementation status is **awaiting-user-verification**, never "done." See
design.md "Test Contract" for the concrete fixtures and expected values authored
before implementation.

## Resolved decisions

Roadmap D1 (`internal/analytics` owns the read — settled in tier 1, restated here
only because it is why this tier's port already exists), D2 (the port's exact
name/shape — `BatteryLevelByDay` / `DayBattery`, settled in tier 1), D4
(`DayBattery.Date` is a final bucket key, bucketed verbatim, never re-projected
through `effectiveDayUTC`), D5 (the gateway's 1-day lookback is dropped — THIS
tier's own change), D6 (the `vehicle_metrics`/`vehicle_snapshots` day-coverage
difference is accepted, not backfilled), D7 (no new unit tests; existing tests
repaired so the suite compiles and passes), D8 (zero escape hatches; the guard
pattern is not widened) are all carried verbatim from the roadmap and not
re-litigated here.

**No database design gate applies to this tier.** This change touches no database
object at all — no table, column, index, constraint, view, or migration. It reads
an existing port (`analytics.Reader.BatteryLevelByDay`) added and indexed by tier 1.
