# Proposal — RM61-analytics-remove-dead-efficiency-branch

Source: MAG-40 — https://linear.app/magus-monitor/issue/MAG-40/recalculated-battery-start

Roadmap: `openspec/roadmaps/RM61-manual-charge-start-derivation.md` — **tier 3 of 3**, module
`analytics`, implementing roadmap decision **RD9** (RD8's design half superseded, its scope
half stands).

Design gate: **NOT tripped.** This change touches no database object — no new table, column,
index, or migration. It deletes a Go function, a Go type, two whole files, and their tests.
Per `openspec/config.yaml` §design, design.md is required only for a DB-touching change; this
one is not, and design.md says so in one line instead of leaving a reviewer to re-derive it.

Unit tests: **included** — the roadmap header says so (RD7). This tier is a deletion, so the
honest shape of "included" is: no new test is written, and the proposal states exactly which
existing coverage is dropped and why it is safe to drop, per §"Tests removed" below.

---

## Why

`internal/analytics` carries two unrelated things today: the metric the dashboard actually
shows (`vehicle_metrics`, read through `ConsumedByDay`, `OdometerDeltaByDay`, `BatteryLevelByDay`,
`LatestMetricsForVehicles`), and a second, older metric — `RecentEfficiency` — that nothing
calls.

`RecentEfficiency` was this module's original metric (`battery-add-efficiency-metric`), before
the dashboard grew its own precomputed `vehicle_metrics` table. Nothing in the codebase ever
migrated `RecentEfficiency` callers onto `vehicle_metrics` — the dashboard's efficiency tile
was built against `VehicleStatus.KmPerPctCalc` from the start
(`internal/gateway/handlers/handlers.go:579`), a different figure entirely, computed from a
different source. `RecentEfficiency` was simply never wired to anything.

The proof is a repo-wide grep, confirmed on 2026-09-16 (see §"Verification" below): the only
hits are the `Reader` interface declaration, the implementation, its own tests, two test fakes,
and two `cmd/*/main.go` comments that describe the method without calling it. One of the test
fakes states the fact outright — `internal/gateway/handlers/history_test.go:146` panics with
`"fakeAnalyticsReader: RecentEfficiency is never called by the gateway's history fragment"`.

`RecentEfficiency` is also the platform's **second definition of pack capacity** — a
`car_type → kWh` map in `internal/analytics/capacity.go`, separate from
`internal/charging`'s own `packCapacityKWh` constant that RM61 tier 1 already extended with a
measured-capacity lookup. MAG-40's REQ 1 asked to reconcile the two. Roadmap decision RD8
first planned to do that by adding a capacity port from `charging` into `analytics`. RD9
reverses that plan: since `RecentEfficiency` is dead code, reconciling its capacity table would
have paid real engineering effort to correct a number no user can ever see. Deleting the branch
removes the second definition instead, at lower cost and with no risk to any displayed number.

## What Changes

- **REMOVED** — `Reader.RecentEfficiency` (the interface method, `analytics.go:48`, and its doc
  comment) and its implementation `(*reader).RecentEfficiency` (`reader.go:134`).
- **REMOVED** — the `Efficiency` struct (`analytics.go:402`) and every value of that type.
- **REMOVED** — `derive.go` entirely: `deriveEfficiency` and `socReadings`.
- **REMOVED** — `capacity.go` entirely: the `packCapacityKWh` map and `capacityFor`.
- **REMOVED** — `carTypeFor` and the `vehicleLookup` interface (`reader.go`).
- **REMOVED** — the `DefaultWindow` and `chargingSourceLimit` constants.
- **REMOVED** — the two helper functions `RecentEfficiency` alone used:
  `sumSuperchargerKWh` and `sumManualKWh` (`reader.go`) — see §"Verification" for why these are
  safe to remove alongside it.
- **CHANGED** — `reader` struct (`reader.go`) collapses from seven fields to one
  (`metrics vehicleMetricsStore`); `NewReader` collapses from six parameters to one
  (`pool *pgxpool.Pool`). `NewRecalculator` is **untouched** — it keeps its own four
  parameters (`pool`, `telemetry.Reader`, `charging.SuperchargerSessionAnalyticsReader`,
  `charging.Reader`); those four are read by `Recalculate`/`Reconcile`, not by
  `RecentEfficiency`, and stay in use.
- **CHANGED** — the package doc comment at the top of `analytics.go`: it currently describes
  the module's "first (and currently only) metric" as the efficiency figure this change
  deletes. It is rewritten to describe what the module actually does — derive and serve
  `vehicle_metrics`.
- **REMOVED (tests)** — see §"Tests removed" below for the exact list and why each is dead
  weight, not lost coverage.
- **CHANGED (leader-owned, outside this module)** — the `analytics.NewReader(...)` call sites
  in `cmd/web/main.go:70` and `cmd/poller/main.go:125` (both shrink to `analytics.NewReader(pool)`,
  and the comment immediately above each, which explains an argument that no longer exists,
  is rewritten), and the `RecentEfficiency` stub methods on the fakes at
  `internal/gateway/handlers/history_test.go:145` and `internal/app/processor_test.go:242`
  (removed — `analytics.Reader` no longer declares the method, so the stub would fail to
  compile as an override of nothing and is simply dead). These four edits are **outside**
  `internal/analytics/` and are the leader's, per this dispatch's sandbox rule; tasks.md
  lists them explicitly so the build does not go red between waves.
- **CHANGED** — `internal/analytics/AGENTS.md` — its "Responsibility" section currently states
  the module's "first (and currently only) metric is rolling energy-per-kilometre... with a
  pack-capacity correction". That becomes false the moment this change lands. Rewritten to
  describe the module's actual, surviving responsibility: deriving and serving
  `vehicle_metrics`. The `Reader` method table drops the `RecentEfficiency` row.
- **CHANGED** — the `analytics` capability's delta spec (`specs/analytics/spec.md` in this
  change folder): removes the three requirements that exist solely to describe
  `RecentEfficiency`'s behavior, and lightly corrects two requirements that mention it only
  in passing. See design.md §"Spec delta" for the itemized list.
- **UNCHANGED** — every file this change does not name above. In particular: `consumed.go`,
  `consumption.go`, `recalculate.go`, `gap_writer.go`, `mapping.go`, and all of
  `vehicle_metrics`/`vehicle_metric_watermarks`/`charge_gaps` — none of that code path reads
  `RecentEfficiency`, `Efficiency`, `deriveEfficiency`, `capacityFor`, `carTypeFor`, or
  `vehicleLookup` (confirmed by the same grep, §"Verification").

**Out of scope, deliberately (RD9):** no capacity port is added to `charging`; no
measured-capacity wiring is added to `analytics`. RD8's plan to do that is superseded, not
implemented.

## Breaking?

**Source-breaking for exactly two files, both fixed in this same tier by the leader; no
data or behavior any user can observe changes.**

- `analytics.Reader` loses a method. Any type asserting it satisfies `analytics.Reader` today
  must drop its `RecentEfficiency` stub or fail to compile. Two such types exist in the whole
  repo: `internal/gateway/handlers/history_test.go`'s `fakeAnalyticsReader` and
  `internal/app/processor_test.go`'s `fakeAnalyticsReader`. Both are leader-owned fixes in
  this same tier (see §"What Changes" above), so `go build ./...` stays green across the
  whole repo once this tier lands as a unit.
- `analytics.NewReader`'s signature shrinks from six parameters to one. Its two call sites,
  `cmd/web/main.go:70` and `cmd/poller/main.go:125`, are both leader-owned fixes in this same
  tier for the same reason.
- **No stored data changes.** This change touches no table, no migration, no query. Every
  `vehicle_metrics`/`vehicle_metric_watermarks`/`charge_gaps` row is untouched.
- **No number any user can see changes** — the roadmap's own "Done when" criterion. The
  dashboard's efficiency tile reads `VehicleStatus.KmPerPctCalc`, not `Efficiency.WhPerKm`;
  nothing renders the latter today, so nothing on screen changes.

## Modules affected

- **`analytics`** — owner. Every non-leader-owned edit is inside `internal/analytics/`.
- **`gateway`** — one test file only (`history_test.go`'s fake), fixed by the leader in this
  tier. No production gateway file changes; the dashboard's efficiency tile is unaffected
  because it never read `RecentEfficiency`.
- **`app`** — one test file only (`processor_test.go`'s fake), fixed by the leader in this
  tier. No production `internal/app` file changes.
- **`cmd/web`, `cmd/poller`** — one call site each, fixed by the leader in this tier.
- No other module. `internal/charging`, `internal/telemetry`, `internal/account`,
  `internal/tesla`, `internal/clock`, `internal/vehicleref` are untouched — this change is a
  pure subtraction from `analytics`'s own surface.

## Read paths affected

Per `openspec/config.yaml` §proposal.

**None.** This change deletes a read path (`RecentEfficiency`) that nothing calls — there is no
existing caller whose query plan, index usage, or response shape changes. Every surviving read
path (`ConsumedByDay`, `OdometerDeltaByDay`, `BatteryLevelByDay`, `LatestMetricsForVehicles`) is
untouched: none of their implementations reference `RecentEfficiency`, `Efficiency`,
`deriveEfficiency`, `capacityFor`, `carTypeFor`, `vehicleLookup`, `DefaultWindow`, or
`chargingSourceLimit` (confirmed by the same grep, §"Verification" below — every hit outside
`reader.go`/`derive.go`/`capacity.go`/`analytics.go` is a test file, a test fake, or a `cmd`
comment).

## Verification

Roadmap RD9's whole scope rests on `RecentEfficiency` having no real caller. The grep below was
run on 2026-09-16, over the working tree at the time of this proposal, and is the gate this
tier's every deletion task depends on:

```
grep -rn RecentEfficiency --include="*.go" --include="*.templ" .
```

Result — 7 files, every hit accounted for, no handler, no template, no `cmd` production call:

| File | What the hit is |
|---|---|
| `internal/analytics/analytics.go` | the `Reader` interface method declaration + doc comment |
| `internal/analytics/reader.go` | the implementation + its own doc comment |
| `internal/analytics/reader_test.go` | this method's own unit tests and their fakes |
| `internal/analytics/db_integration_test.go` | one doc-comment cross-reference to `reader_test.go`'s fake |
| `internal/gateway/handlers/history_test.go` | a test fake's stub, which **panics** if ever called |
| `internal/app/processor_test.go` | a test fake's no-op stub |
| `cmd/web/main.go`, `cmd/poller/main.go` | one doc comment each, explaining an unused constructor argument — no call |

No hit is inside a `.templ` file, an `internal/gateway` handler (non-test), or any `cmd`
call expression. This is the fact design.md's Test Contract fixes as this change's own
expected re-run result — see design.md §"Test Contract" for the post-deletion form of the same
grep.

## Tests removed

Per this dispatch's instruction: name the coverage being dropped and why it is dead, rather
than writing new tests to satisfy "unit tests: included."

- **`internal/analytics/reader_test.go`** — the eight `TestRecentEfficiency_*` functions
  (`HappyPath_ComputesValue`, `WindowExcludesOldEntries`, `UnknownVehicle_ApproximateTrue`,
  `TelemetryError_Propagates`, `SuperchargerError_Propagates`, `ChargingError_Propagates`,
  `AccountError_Propagates`, `AccountIDScoping_PassedToEveryPort`), plus the four fakes that
  exist solely to drive them (`fakeTelemetryReader`, `fakeSuperchargerReader`,
  `fakeManualReader`, `fakeVehicleLookup`) and the two tiny helpers `fp`/`sp` those tests alone
  use. These tests covered `RecentEfficiency`'s own port-wiring — which port each dependency
  read, in what order, with what error propagation. That behavior is deleted along with the
  method it tested; there is nothing left for these tests to verify. The file's other tests
  (`TestReader_ConsumedByDay_ReadsPrecomputedRows`, `TestReader_OdometerDeltaByDay_ClampsOnRead`,
  `TestReader_ConsumedByDay_ExcludesPredecessorlessRow`,
  `TestReader_OdometerDeltaByDay_ExcludesPredecessorlessRow`,
  `TestReader_VehicleMetricsStoreError_Propagates`) and the `fakeVehicleMetricsStore` fake that
  backs them are **untouched** — they exercise the `vehicleMetricsStore`-only path this change
  does not touch.
- **`internal/analytics/derive.go` and `internal/analytics/derive_test.go`** — deleted whole,
  both the production file and its test file. Every test in it
  (`TestDeriveEfficiency_KnownCapacity_NetConsumption`,
  `TestDeriveEfficiency_UnknownCapacity_ApproximateTrue`,
  `TestDeriveEfficiency_FewerThanTwoSnapshots_NotOK`,
  `TestDeriveEfficiency_NonIncreasingOdometer_NotOK`,
  `TestDeriveEfficiency_NetChargeExceedsConsumption_NotOK`,
  `TestSocReadings_UsableAtBothEndpoints`, `TestSocReadings_FallsBackWhenEitherEndpointNil`,
  `TestDeriveEfficiency_BatteryDeltaPctSignConvention`) exercises `deriveEfficiency` or
  `socReadings` directly — both deleted, so both sets of tests have nothing left to call. The
  file's `snap()` helper, used only by these tests and by the `TestRecentEfficiency_*` tests
  above, has no remaining caller after both are gone and is deleted with it (confirmed:
  `grep -n "\bsnap(" internal/analytics/*_test.go` returns hits only inside `derive_test.go`
  and `reader_test.go`).
- **`internal/analytics/capacity.go`** — deleted whole. It has no dedicated `_test.go` file;
  `capacityFor` was exercised only indirectly, through the now-deleted `TestRecentEfficiency_*`
  tests above.

**What is NOT removed:** `TestRecalculate_*` and every other `Recalculate`/`Reconcile` test in
`db_integration_test.go`, `recalculate_test.go`, `consumed_test.go`, `consumption_test.go`, and
`db_gap_writer_integration_test.go` — none of them call `RecentEfficiency`, `deriveEfficiency`,
`socReadings`, or `capacityFor`.

## Impact

- **Affected specs:** `analytics` — three requirements **REMOVED** in full ("Recent
  Energy-Per-Kilometre Derivation", "Unknown Pack Capacity Yields an Approximate Value, Never
  a Blank Tile", "Insufficient Data Returns ok=false, Never a Fabricated Value"), two
  requirements **MODIFIED** in one sentence/example each ("No Cross-Module Database Access"
  drops its `internal/account` import claim; "Module-Scoped Database Schema"'s
  "public interface is unaffected by the schema move" scenario drops `RecentEfficiency` from
  its example method list). Full itemized delta: design.md §"Spec delta".
- **Affected code:** `internal/analytics/` (this tier); `cmd/web/main.go`, `cmd/poller/main.go`,
  `internal/gateway/handlers/history_test.go`, `internal/app/processor_test.go` (leader-owned,
  same tier, listed above and in tasks.md).
- **Design gate: not tripped.** No database object is touched. design.md states this in one
  line rather than leaving it to be re-derived.
- **`MIGRATIONS_DIRS` order check:** not applicable — no migration in this change.
- **Deferred, explicitly not in scope (RD9):** a capacity port from `charging` to `analytics`;
  measured-capacity wiring anywhere in `analytics`. RD8's plan to build both is superseded by
  this tier's deletion, not carried forward.

## Modules affected — summary table

| Module | Change |
|---|---|
| `analytics` | Owner. Deletes the dead efficiency branch, its tests, its `AGENTS.md` section, its spec. |
| `gateway` | One test fake fixed (leader-owned); no production file changes. |
| `app` | One test fake fixed (leader-owned); no production file changes. |
| `cmd/web`, `cmd/poller` | One `NewReader` call site each, shrunk to one argument (leader-owned). |
