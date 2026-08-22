# Tasks — RM29-telemetry-drop-derived-columns

Ownership legend: **[module: telemetry worker]** — inside `internal/telemetry/` only.
**[module: analytics worker]** — inside `internal/analytics/` only. **[leader]** —
outside every module's sandbox (root docs, `openspec/`). Per design.md D4 (carries
I4), **no sub-task below edits both `internal/telemetry/` and `internal/analytics/`**;
the wave boundaries are the handoff points. See design.md D1–D11 for the rationale
behind each group.

**Hard ordering constraints (design.md D11 — read it before reordering anything):**

- **Wave 1 (telemetry, additive) → Wave 2 (analytics).** Wave 2 calls
  `SnapshotPrecedingDay`; it cannot compile before Wave 1 declares it.
- **Wave 2 (analytics derives) → Wave 4 (telemetry drops).** This is the edge that
  guarantees there is **no window in which the five values are unreachable**: at the
  end of Wave 2 analytics computes them itself while the columns are still present
  and still correct, so either source would work. Wave 4 before Wave 2 leaves
  analytics reading `Snapshot` fields that no longer exist.
- **Wave 2 → Wave 5 (watermark reset).** Resetting the cursor before the new
  derivation exists would rebuild every row with the old formula.
- **Wave 3 (offline tests) is authored from design.md's Test Contract**, whose
  fixtures are already fixed. It may be drafted in parallel with Wave 2 and wired in
  once Wave 2 lands.
- **Waves 6a/6b (DB-integration tests) are the FINAL implementation waves.** They
  cannot compile before Wave 4's migration and regenerated sqlc types exist.
- **`make sqlc` must be re-run after EACH migration** — after Wave 1's query change,
  after Wave 4's migration, and after Wave 5's (a data-only migration regenerates
  nothing, but the run confirms the schema goose sees still matches what sqlc
  compiled against; a silent drift here is the failure class tier 2's design gate
  was about).
- **Wave 7 (docs) describes the POST-change state**, so it runs after Waves 1–5 land
  (a content dependency, not a compile one).

---

## Wave 1 — telemetry adds the preceding-snapshot port (module: telemetry worker)

> **Purely additive.** Nothing is removed in this wave. `deriveConsumption`,
> `dayStart`, the `previousSnapshot` seam and `PreviousSnapshotForVehicle` all stay
> exactly as they are — they are removed in Wave 4, after analytics no longer needs
> the columns they feed.

- [x] **1.1** `internal/telemetry/db/query.sql` — add a new query
  `SnapshotPrecedingDay :one`, exactly as specified in design.md "New query"
  (`WHERE account_id = @account_id AND tesla_id = @tesla_id AND captured_date < @day
  ORDER BY captured_at DESC LIMIT 1`), including its full doc comment (the
  `captured_date`-vs-`captured_at` rationale and the index-reuse note — both are
  load-bearing, D2). **In THIS wave the projection still lists the five `_calc`
  columns**, identical to `PreviousSnapshotForVehicle`'s, so the shared
  `rowToSnapshot` mapper keeps compiling; Wave 4 removes them from every query at
  once. Do NOT delete `PreviousSnapshotForVehicle` here. Run `make sqlc`.
  `depends_on`: — · `parallel_ok`: no

- [x] **1.2** `internal/telemetry/telemetry.go` — add
  `SnapshotPrecedingDay(ctx context.Context, accountID uuid.UUID, teslaID int64, day time.Time) (*Snapshot, error)`
  to the `Reader` interface, with the doc comment design.md D2 specifies: `day` is a
  bare UTC-midnight-normalized calendar date matching `Snapshot.CapturedDate`;
  `(nil, nil)` means the vehicle has no earlier snapshot at all; a query error is
  returned as-is and MUST NOT be degraded to "no predecessor". No existing method
  signature changes.
  `depends_on`: — · `parallel_ok`: with 1.1

- [x] **1.3** `internal/telemetry/reader.go` — implement `SnapshotPrecedingDay` on the
  reader, calling the generated `SnapshotPrecedingDay` query, mapping
  `pgx.ErrNoRows` → `(nil, nil)` and any other error through unchanged, and reusing
  the existing shared `rowToSnapshot` mapper (no new mapper). Bind `day` with the
  existing `dateFrom` helper — `captured_date` is a `DATE` column, so the parameter
  is `pgtype.Date`, not `pgtype.Timestamptz`. Mirrors the existing `dbStore.
  previousSnapshot` shape one level down.
  `depends_on`: 1.1, 1.2 · `parallel_ok`: no

- [x] **1.4** `internal/telemetry/reader_test.go` — add `SnapshotPrecedingDay` to the
  package's `Reader` fakes (`fakeReadStore`, `fakeHistoryStore` and any other type
  asserted against `Reader`) so the package still compiles. A fake that must never be
  called on this path `panic`s, matching the file's existing convention.
  `depends_on`: 1.2 · `parallel_ok`: with 1.3

---

## Wave 2 — analytics takes over the derivation (module: analytics worker)

> At the end of this wave analytics computes the five figures itself, while
> `vehicle_snapshots` still carries them. **This is the state that makes Wave 4 safe**
> (design.md D11).

- [x] **2.1** `internal/analytics/consumption.go` (new file) — move
  `deriveConsumption` here from `internal/telemetry/service.go:581`, as the pure
  function design.md D5 specifies: `deriveConsumption(prev *telemetry.Snapshot, cur
  telemetry.Snapshot) consumptionCalc`, with the unexported `consumptionCalc` struct
  carrying the five pointer fields. **Copy the body; do not re-derive it** — same
  subtraction order (`cur.OdometerKm - prev.OdometerKm`, `prev.BatteryLevelPct -
  cur.BatteryLevelPct`), same `int(cur.CapturedDate.Sub(prev.CapturedDate).Hours()/24)`
  day count, same `batteryUsed > 0` divisor guard, same `kmPerPct * 100`. `prev ==
  nil` returns the zero `consumptionCalc` (all five nil). Carry the original doc
  comment over, updated for the new return shape. **Do NOT delete telemetry's copy in
  this task** — that is 4.4, in the other module.
  `depends_on`: — · `parallel_ok`: with 2.2

- [x] **2.2** `internal/analytics/consumed.go` — `deriveVehicleMetrics` gains a
  leading `preceding *telemetry.Snapshot` parameter (design.md D7). Inside the loop:
  resolve `prev` as `snapshots[i-1]` for `i >= 1` and `preceding` for `i == 0`; the
  predecessor-less branch now triggers on **`prev == nil` alone** (design.md D6 — the
  `cur.BatteryUsedPctCalc == nil` half of today's condition is removed because the
  field it reads ceases to exist, not because the check was judged unnecessary).
  Replace the five verbatim copies (`DistanceTraveledKmCalc: cur.DistanceTraveledKmCalc`
  …) with the result of `deriveConsumption(prev, cur)` from 2.1, and read
  `distanceKm` for the flag rule off that result rather than off `cur`.
  **Unchanged and still load-bearing:** the predecessor-less row still carries
  `battery_level_pct`/`odometer_km`/`battery_range_km` from `cur`, still leaves every
  derived and consumed field nil, and still forces `Flagged: false` before the
  D5/D5a comparison can run (tier 3 D9 — a stored `0` would falsely flag a vehicle's
  first day as a charge gap). Every existing helper (`effectiveDay`, `calendarDay`,
  `sumSuperchargerPctBetween`, `sumManualPctBetween`, `inferMissingChargingType`,
  `minFlagDistanceKm`) is untouched.
  `depends_on`: 2.1 · `parallel_ok`: no

- [x] **2.3** `internal/analytics/recalculate.go` — `Recalculate` calls
  `r.telemetry.SnapshotPrecedingDay(ctx, accountID, teslaID, snapshots[0].CapturedDate)`
  once, unconditionally, whenever `len(snapshots) > 0` (design.md D7 — do not add the
  "skip it when snapshots[0] is the lookback row" optimisation; D7 explains why the
  branch is not worth its failure mode). A returned error is wrapped and **aborts**
  `Recalculate` — it is NEVER degraded to "no predecessor". Pass the result as
  `deriveVehicleMetrics`' new leading argument.
  `depends_on`: 1.2, 2.2 · `parallel_ok`: no

- [x] **2.4** `internal/analytics/recalculate.go` — implement design.md **D8b**, the
  widened charge-source fetch (**the decision the interview did not cover — read D8b
  in full before implementing**). Compute `chargeStart := lookbackStart`, then, when
  `preceding != nil` and `effectiveDay(*preceding)` is before `chargeStart`, set
  `chargeStart = effectiveDay(*preceding)`; pass `chargeStart` as the start bound to
  BOTH `SuperchargerSessionsByVehicleBetween` and `ListEntriesByVehicleBetween` (end
  bounds unchanged: `end+2d` and `end` respectively). Ordering inside the function:
  the snapshot fetch and the `preceding` lookup (2.3) must both precede the two
  charge-source fetches. In the steady state (`preceding` is the one-day lookback
  row) `chargeStart == lookbackStart` and the fetches are byte-identical to today's.
  Verified by Test Contract Fixture D2.
  `depends_on`: 2.3 · `parallel_ok`: no

- [x] **2.5** `internal/analytics/reader_test.go` and any other in-module fake of
  `telemetry.Reader` — add the `SnapshotPrecedingDay` method so the package compiles.
  A fake used by a test with no gap returns `(nil, nil)`; Fixture D's fake returns the
  gap's predecessor.
  `depends_on`: 1.2 · `parallel_ok`: with 2.1–2.4

---

## Wave 3 — offline characterization tests (module: analytics worker)

> Authored against design.md's Test Contract (Fixtures A–E, all fixed BEFORE this
> wave, per `ai/go-conventions.md`'s "author expected values up front" rule). Pure Go,
> no DB. Roadmap D10: **characterization only** — assertions MOVE, they are never
> re-derived from the implementation.

- [x] **3.1** `internal/analytics/consumption_test.go` (new file) — port
  `internal/telemetry/consumption_test.go`'s `TestDeriveConsumption` and
  `TestDeriveConsumption_NilPrevReturnsCurUnchanged` **with every expected value
  unchanged**, adapted only to the `consumptionCalc` return shape. Bring the
  `assertFloatPtr` / `assertIntPtr` helpers and the `consumptionFloatTol = 1e-9`
  tolerance across verbatim. Add `TestDeriveConsumption_MultiDayGap` (Fixture D:
  `DistanceTraveledKmCalc 210.0`, `BatteryUsedPctCalc 35`, **`DaysSpannedCalc 7` —
  not 1**, `KmPerPctCalc 6.0`, `EstimatedRangeKmCalc 600.0`) and
  `TestDeriveConsumption_ZeroDivisorGuard` (Fixture E: `DistanceTraveledKmCalc 0.0`
  and `BatteryUsedPctCalc 0` both **non-nil**, `KmPerPctCalc` and
  `EstimatedRangeKmCalc` both **nil** — zero is excluded by the `> 0` guard exactly
  like a negative; this is the boundary Fixture B's `-45` does not reach).
  Do NOT port `TestDayStart_*` — `dayStart` is deleted by 4.5 and its guarantee is
  re-asserted by 6a.3 instead.
  `depends_on`: 2.1 · `parallel_ok`: with 3.2

- [x] **3.2** `internal/analytics/consumed_test.go` — update every
  `deriveVehicleMetrics` call site for the new leading `preceding` parameter. Add
  `TestDeriveVehicleMetrics_FixtureD_UsesPrecedingSnapshot`: fetched slice = the
  current row alone, `preceding` = the 2026-08-01 row, and assert the emitted
  `vehicleMetricRow` carries `MetricDate 2026-08-07`, `BatteryLevelPct 55`,
  `OdometerKm 1210.0`, `BatteryRangeKm 220.0`, the five values from 3.1,
  `ConsumedPct 35.0`, `Flagged false`, `MissingChargingType ""`. Add
  `TestDeriveVehicleMetrics_FixtureD2_ChargeInsideTheGap`: same slice, plus a manual
  entry on 2026-08-04 with a `+20` battery delta, expect `ConsumedPct 55.0`.
  **Fixture C's existing assertions must still pass unchanged** — a `nil` `preceding`
  with a single-element slice still yields a row with all five values nil,
  `Flagged == false` (asserted as the boolean, not as "falsy") and
  `MissingChargingType == ""`.
  `depends_on`: 2.2 · `parallel_ok`: with 3.1

- [x] **3.3** `internal/analytics/recalculate` fetch-window coverage — extend the
  existing offline/fake-backed assertions (or add them where none exist) to pin
  design.md D8b: with `preceding` seven days before `lookbackStart`, the fake
  `telemetry.SuperchargerReader` and `charging.Reader` each receive
  `effectiveDay(*preceding)` as their start bound; with `preceding` being the ordinary
  one-day lookback row, both receive **`start - 2 days`** — NOT `lookbackStart`.
  **Corrected by the leader at the wave-2 reconcile.** The original text here said
  `lookbackStart` **unchanged**, copied from a claim in design.md D8b that is
  arithmetically false: `effectiveDay` is `calendarDay(CapturedDate) - 1`, so the
  `start-1d` lookback row yields `start-2d`, which is always `Before(lookbackStart)`.
  The widening therefore fires on every call. See D8b's Correction block for why that is
  accepted rather than fixed. A test written against the withdrawn claim would fail
  against a correct implementation — which is exactly why the design authors expected
  values before the code exists, and exactly why this was corrected here rather than by
  relaxing the test later. Also assert that a
  `SnapshotPrecedingDay` error propagates out of `Recalculate` rather than being
  swallowed into a predecessor-less row (design.md D7).
  `depends_on`: 2.4 · `parallel_ok`: with 3.1, 3.2

---

## Wave 4 — telemetry drops the columns (module: telemetry worker)

> **Do not start this wave until Wave 2 is landed and compiling.** At that point
> analytics derives the five figures itself and the columns are redundant
> (design.md D11).

- [x] **4.1** `internal/telemetry/db/migrations/20260822000001_drop_derived_consumption_columns_vehicle_snapshots.sql`
  — the goose migration **exactly** as specified in design.md "Database Changes →
  Migration 1", including its full comment block. `-- +goose Up` drops the five
  columns; `-- +goose Down` re-adds them AND re-runs `20260814000001`'s `LAG()`
  backfill verbatim. Do not simplify the Down to a bare `ADD COLUMN` — design.md D9
  explains what the backfill can and cannot restore, and the migration must say so
  in its own comment.
  `depends_on`: — · `parallel_ok`: no

- [x] **4.2** `internal/telemetry/db/query.sql` — remove
  `distance_traveled_km_calc, battery_used_pct_calc, km_per_pct_calc,
  estimated_range_km_calc, days_spanned_calc` from **every** query that names them.
  There are ten sites: the `InsertVehicleSnapshot` column list, its `VALUES` list and
  its `ON CONFLICT … DO UPDATE SET` clause, and the projections of
  `ListSnapshotsByVehicle`, `SnapshotsByVehicleSince`, `SnapshotsByVehicleBetween`,
  `SnapshotsByVehicleUpdatedSince`, `LatestSnapshotsByAccount`,
  `PreviousSnapshotForVehicle` and `SnapshotPrecedingDay` (added by 1.1 with the
  columns still present). Also **delete `PreviousSnapshotForVehicle` entirely** — it
  is replaced by `SnapshotPrecedingDay` (design.md D8). Update the file's header
  comment block, which currently documents the five columns. Run `make sqlc`.
  `depends_on`: 4.1 · `parallel_ok`: no

- [x] **4.3** `internal/telemetry/telemetry.go` — delete the five `Snapshot` fields
  (`DistanceTraveledKmCalc`, `BatteryUsedPctCalc`, `KmPerPctCalc`,
  `EstimatedRangeKmCalc`, `DaysSpannedCalc`) and their doc-comment block. Every other
  field, including `UpdatedAt`, is untouched.
  `depends_on`: 4.2 · `parallel_ok`: no

- [x] **4.4** `internal/telemetry/mapping.go` — remove the five assignments from
  `rowToSnapshot` and the doc comment above them describing the divisor guard. If
  `pgNullableFloat64` / `pgNullableInt32AsInt` become unused after this, leave them —
  they serve other nullable columns; verify with `go vet` rather than assuming.
  `depends_on`: 4.3 · `parallel_ok`: no

- [x] **4.5** `internal/telemetry/service.go` — delete `deriveConsumption` (moved to
  analytics by 2.1), `dayStart`, the `previousSnapshot` method on the `store`
  interface and on `dbStore`, and the `prev, err := s.store.previousSnapshot(...)`
  block plus its error branch in `attemptVehicle` (which becomes fetch → map →
  store). Keep `dateOnly` — it stamps `captured_date` and is still the single place
  the poller's zone decides a calendar day. Update `attemptVehicle`'s surrounding
  comments, which currently explain the derived-consumption wiring (design.md D8).
  `depends_on`: 4.4 · `parallel_ok`: no

- [x] **4.6** `internal/telemetry/service_test.go`, `internal/telemetry/reader_test.go`
  — remove the `previousSnapshot` method from `fakeStore`, `fakeReadStore` and
  `fakeHistoryStore` (the store seam is gone). Update
  `internal/telemetry/testdb_test.go`'s comment, which lists `deriveConsumption` among
  this package's offline tests.
  `depends_on`: 4.5 · `parallel_ok`: no

- [x] **4.7** `internal/telemetry/consumption_test.go` — delete the file.
  `TestDeriveConsumption` and `TestDeriveConsumption_NilPrevReturnsCurUnchanged` were
  ported to `internal/analytics/consumption_test.go` by 3.1 **with their expected
  values unchanged** — confirm that task is landed before deleting, so no assertion is
  lost in the gap. `TestDayStart_ComfortablyInsideLocalDay` and
  `TestDayStart_LocalDayBehindUTCDay` go with the file: `dayStart` no longer exists
  (4.5), and the guarantee they protected — a same-day re-capture must not select its
  own row as its predecessor — is re-asserted by 6a.3 against
  `SnapshotPrecedingDay`'s `captured_date < @day` predicate (design.md D2).
  `depends_on`: 3.1, 4.5 · `parallel_ok`: no

- [x] **4.8** `internal/telemetry/db_derived_consumption_integration_test.go` — re-home
  its three tests per design.md's "Characterization parity contract" table:
  `TestStore_PreviousSnapshot_RoundTrips` **stays**, rewritten against the public
  `Reader.SnapshotPrecedingDay` (all three cases kept: predecessor exists;
  single-row vehicle; no snapshots → `(nil, nil)`);
  `TestStore_SnapshotUpsert_RecapturesRecomputeDerivedColumns` is **split** — its
  predecessor-selection half stays here (6a.3), its derived-columns half moves to
  analytics (6b.3); `TestStore_DerivedConsumptionColumns_RoundTrip` is **deleted**, it
  round-trips columns that no longer exist and its coverage is replaced by 6b.1's
  assertions on `vehicle_metrics`. Rename the file to reflect what it now covers
  (e.g. `db_preceding_snapshot_integration_test.go`).
  `depends_on`: 4.5 · `parallel_ok`: with 4.7

- [x] **4.9** `internal/telemetry/db/query.sql` — raise `SnapshotsByVehicleBetween`'s
  `LIMIT 400` (line ~72) to `LIMIT 4000`, and update the D3 comment above it to say why.
  **Why this is in scope, though it is a pre-existing bug:** decision **I3** resets the
  `vehicle_snapshots` watermark so the next `Reconcile` rebuilds each vehicle's ENTIRE
  history in one `Recalculate` window. At the current cap, a vehicle with more than 400
  days of snapshots has its **newest** rows silently dropped (the query is
  `ORDER BY captured_at ASC LIMIT 400`), and `Reconcile` then advances the watermark past
  days it never recomputed — permanently wrong metrics, no error, no log line. That is the
  same invisible-failure class as the F1 blocker in tier 3 and the exact class decision I2
  was chosen to avoid. This tier re-arms it deliberately, so this tier disarms it.
  4000 is ~11 years at the platform's one-snapshot-per-vehicle-per-day cadence; the cap
  remains a runaway-query guard, which is all D3 ever claimed for it, and the bounded
  window stays the real protection. Do NOT redesign this into truncation detection or
  pagination — that is a larger change and is explicitly out of scope.
  Record in the telemetry spec delta that D3's cap value changed and why.
  `depends_on`: 4.2 · `parallel_ok`: no (same file as 4.2)

---

## Wave 5 — analytics resets the recompute watermark (module: analytics worker)

- [x] **5.1** `internal/analytics/db/migrations/20260822000002_reset_vehicle_metric_watermarks.sql`
  — the goose migration **exactly** as specified in design.md "Database Changes →
  Migration 2", including its full comment block. `-- +goose Up` is
  `DELETE FROM vehicle_metric_watermarks WHERE source = 'vehicle_snapshots';`;
  `-- +goose Down` is `SELECT 1;` with the comment explaining why the deletion is
  irreversible **and harmless** (an absent watermark is defined as epoch). This is a
  **data-only** migration — it creates, alters and drops nothing. Do NOT add a Go
  delete query: `internal/analytics/db/query.sql` deliberately has no watermark
  DELETE, and this reset is a one-time schema-change event, not a runtime capability
  (design.md D3).
  `depends_on`: 2.4 · `parallel_ok`: no

- [x] **5.2** Run `make sqlc` and confirm it produces **no diff** under
  `internal/analytics/db/`. A data-only migration must regenerate nothing; a diff
  here means the migration accidentally changed a schema object. Then run
  `make migrate-status` and confirm both new migrations are listed and pending/applied
  as expected — the check that catches a migration goose never sees, which otherwise
  fails silently while the build stays green (tier 2's lesson).
  `depends_on`: 4.1, 5.1 · `parallel_ok`: no

---

## Wave 6a — telemetry DB-integration tests (module: telemetry worker — DB-gated, final)

> `DATABASE_URL`-gated; cannot compile before Wave 4's migration and regenerated sqlc
> types exist. Uses this package's existing `testdb.Provision` harness unchanged.

- [x] **6a.1** `internal/telemetry/db_preceding_snapshot_integration_test.go` —
  **NARROWED BY THE LEADER at the wave-3/4 reconcile: this is now a rename-and-verify,
  not an authoring task.** Task 4.8's own text said the round-trip test "stays,
  rewritten against the public port", and wave 4 did exactly that — the file already
  contains `TestStore_PreviousSnapshot_RoundTrips`, public-port-based, with all three
  cases. 6a.1 as originally written would have had a second worker author the same test
  from scratch. So: **rename** the existing test to
  `TestReader_SnapshotPrecedingDay_RoundTrips` (the `Store_`/`PreviousSnapshot` name now
  misdescribes it — the seam it was named for is deleted) and **verify** it still covers
  all three cases below. The coverage requirement is unchanged; only the authoring is
  already banked. If any case is missing, write it.
  `TestReader_SnapshotPrecedingDay_RoundTrips`: the three cases carried over from
  `TestStore_PreviousSnapshot_RoundTrips` (4.8), now against the public port —
  a vehicle with a predecessor returns it; a single-row vehicle queried with a `day`
  after its only row returns that row; a vehicle with no snapshots returns
  `(nil, nil)` and a nil error.
  `depends_on`: 4.8, 5.2 · `parallel_ok`: with 6a.2

- [x] **6a.2** Same file — `TestReader_SnapshotPrecedingDay_UsesIndexBackwardScan`:
  run `EXPLAIN (FORMAT TEXT)` over `SnapshotPrecedingDay`'s SQL against a seeded
  vehicle and assert the plan text contains `Index Scan Backward` and
  `idx_vehicle_snapshots_vehicle_time`, and does **not** contain `Seq Scan`
  (design.md "Index Plan"; mirrors tier 3's identical assertion for
  `SnapshotsByVehicleUpdatedSince`). If the plan differs, do NOT add an index to make
  the test pass — report it, because design.md's "deliberately not added" reasoning
  depends on this plan being what Postgres actually chooses.
  `depends_on`: 4.8, 5.2 · `parallel_ok`: with 6a.1

- [x] **6a.3** Same file — `TestReader_SnapshotPrecedingDay_SameDayRecaptureNotItsOwnPredecessor`:
  seed a day N−1 row and two successive day N captures (the second replacing the
  first via the dedupe UPSERT), then assert `SnapshotPrecedingDay(day N)` returns the
  **day N−1** row, never either day-N row. This is the guarantee `dayStart` used to
  provide, re-expressed as the `captured_date < @day` predicate (design.md D2) and
  the replacement for the deleted `TestDayStart_*` tests (4.7).
  `depends_on`: 4.8, 5.2 · `parallel_ok`: with 6a.1, 6a.2

---

## Wave 6b — analytics DB-integration tests (module: analytics worker — DB-gated, final)

> `DATABASE_URL`-gated, `testdb.ProvisionDirs` (this module's fixtures span three
> schemas). Cannot compile before Wave 4's migration and regenerated types exist.

- [x] **6b.1** `internal/analytics/db_integration_test.go` — update every fixture
  helper that seeds `vehicle_snapshots`: the direct `INSERT` column list and its
  parameters lose the five `_calc` columns (they no longer exist), and the
  `telemetry.Snapshot` fixture literals lose the five fields. The fixtures now supply
  only `odometer_km`, `battery_level_pct`, `captured_date`, `captured_at`,
  `battery_range_km` and the rest — **which is the point**: the expected
  `vehicle_metrics` values below must now be *produced* by analytics rather than
  copied from the seed. Re-assert Fixtures A, B and C against design.md's Test
  Contract with **every expected value unchanged** (roadmap D10).
  `depends_on`: 4.8, 5.2 · `parallel_ok`: with 6b.2

- [x] **6b.2** Same file — `TestRecalculate_FixtureD_MultiDayGap`: seed only the
  2026-08-01 and 2026-08-08 snapshots, call
  `Recalculate(A, 42, 2026-08-07, 2026-08-07)`, and assert the persisted
  `vehicle_metrics` row carries `distance_traveled_km_calc 210.0`,
  `battery_used_pct_calc 35`, **`days_spanned_calc 7`**, `km_per_pct_calc 6.0`,
  `estimated_range_km_calc 600.0`, `consumed_pct 35.0`, `flagged false`. Then assert
  `ConsumedByDay` and `OdometerDeltaByDay` each return **exactly one** entry for that
  date with the Test Contract's values — **the non-empty assertion is the point**: an
  implementation that omits the `SnapshotPrecedingDay` call returns two empty slices
  here, compiles, and does not error. Add
  `TestRecalculate_FixtureD2_ChargeInsideTheGap` (a manual entry on 2026-08-04 with a
  `+20` delta) asserting `consumed_pct 55.0` — the DB-backed proof of D8b.
  `depends_on`: 4.8, 5.2 · `parallel_ok`: with 6b.1

- [x] **6b.3** Same file — `TestRecalculate_ZeroDivisorGuard` (Fixture E: both
  efficiency columns NULL while `distance_traveled_km_calc 0.0` and
  `battery_used_pct_calc 0` are stored **non-NULL**, and `ConsumedByDay` returns the
  day — a `0` is not an absence) and
  `TestRecalculate_AfterSameDayRecapture_RefreshesSuccessorRow`: the derived-columns
  half of telemetry's old `TestStore_SnapshotUpsert_RecapturesRecomputeDerivedColumns`
  (4.8), re-homed. Seed days N−1, N and N+1, replace day N's row with different
  odometer/battery readings, re-run `Recalculate` over N..N+1, and assert **day N+1's**
  row now reflects the replacement — the stale-successor bug design.md D10 names, which
  was unobservable before this change.
  `depends_on`: 6b.1 · `parallel_ok`: with 6b.2

---

## Wave 7 — documentation (docs-track-structural-change, same change per CLAUDE.md)

- [ ] **7.1** **[module: telemetry worker]** `internal/telemetry/AGENTS.md` — remove
  the "Five derived-consumption columns" bullet from "DTO / units conventions" and the
  five-column sentence from `vehicle_snapshots`' entry under "Data ownership",
  replacing both with a short note that the derivation now lives in
  `internal/analytics` and why (this module never read them back). Add
  `SnapshotPrecedingDay` to the `Reader` entry under "Public interface (the port)",
  with its `captured_date`-bound and index-reuse notes. Remove `deriveConsumption`
  from the "Testing notes" list of offline tests.
  `depends_on`: 4.8 · `parallel_ok`: with 7.2

- [ ] **7.2** **[module: analytics worker]** `internal/analytics/AGENTS.md` — two
  jobs. (a) **Fix the pre-existing staleness the leader flagged**: the "Doc-Pack
  (module)" section still calls this "a pure Go derivation module with no persistence,
  no HTTP surface", and "Responsibility" still says the module "will additionally own
  a database of its own starting at tier 3 — a fact not yet true today". Tier 3
  landed; both statements are false. (Note the file's "Data ownership" and "Testing"
  sections were already corrected by tier 3 — the staleness is narrower than the
  dispatch described; fix only what is actually wrong.) (b) Document this change:
  analytics now **computes** the five per-day consumption figures rather than copying
  them, via `consumption.go`'s `deriveConsumption`; `Recalculate` calls
  `telemetry.Reader.SnapshotPrecedingDay` for the exact predecessor and widens its
  charge-source fetches across a gap (D8b); the module's allowed-imports list gains
  the new `telemetry.Reader` method.
  `depends_on`: 5.2 · `parallel_ok`: with 7.1

- [ ] **7.3** **[leader]** Root `README.md` — no module is added, removed or renamed by
  this change, so the Project Structure tree and the dependency graph are unchanged.
  Verify that, and update only what actually moved: any "Architecture" table row or
  prose describing `vehicle_snapshots` as carrying derived values, and any mention of
  telemetry computing consumption. If nothing in `README.md` names them, record that
  explicitly in the task's completion note rather than leaving it ambiguous.
  `depends_on`: 4.8 · `parallel_ok`: with 7.1, 7.2

- [ ] **7.4** **[leader]** `openspec/roadmaps/RM29-modular-monolith-boundaries.md` —
  flip tier 4's status `[ ]` → `[~]` when these artifacts are created and `[~]` → `[x]`
  at archive; update the "Status" section's "Next unblocked" line. Leader bookkeeping,
  not a worker task; noted here per the roadmap's own status-legend convention.
  `depends_on`: — · `parallel_ok`: n/a

---

## Wave 8 — verification (assistant-run signals, then owner-run suite)

- [ ] **8.1** Run and report: `go build ./...`, `go vet ./...`, `gofmt -l .` (expect
  clean); `make build`, `make vet`, `make bins`; `make ui-guard`, `make i18n-guard`,
  `make money-guard` (all three expected to be no-ops — this change touches no gateway
  markup, no user-facing string and no monetary column; report that they ran, not that
  they were skipped).
  `depends_on`: Waves 1–7 · `parallel_ok`: no (final gate)

- [ ] **8.2** Re-run `make sqlc` one final time and confirm a clean tree, then
  `make migrate-status` and confirm both new migrations are present and applied in the
  expected order. Report the output verbatim.
  `depends_on`: 8.1 · `parallel_ok`: no

- [ ] **8.3** Run `openspec validate --changes --strict` and report the result
  verbatim.
  `depends_on`: all artifact edits · `parallel_ok`: no

- [ ] **8.4** Hand off to the owner. Exact commands to paste (not run by the
  assistant, per the Test-Execution-Policy):
  ```
  go test ./internal/telemetry/... ./internal/analytics/... ./internal/gateway/... ./cmd/...
  ```
  or the full suite:
  ```
  go test ./...
  ```
  Note for the owner: the DB-backed tests **self-skip** when no Postgres is reachable
  and no Docker daemon can provision one, so a green run does not by itself prove
  Waves 6a/6b executed. To exercise them, start Docker and run `make test`, or run one
  file directly, e.g.
  `env -u DATABASE_URL go test ./internal/analytics/ -run TestRecalculate_ -v`, and
  confirm the output says `PASS` rather than `SKIP`. Until the owner runs one of these
  and reports the result, this tier's status is **awaiting-user-verification**, never
  "done" (design.md "Verification signals").
  `depends_on`: 8.1, 8.2, 8.3 · `parallel_ok`: no
