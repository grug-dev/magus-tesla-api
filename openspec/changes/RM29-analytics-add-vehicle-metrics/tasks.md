# Tasks — RM29-analytics-add-vehicle-metrics

Ownership legend: **[module: analytics worker]** — inside `internal/analytics/`, the
sandboxed worker's own territory. **[module: telemetry worker]** /
**[module: charging worker]** — inside those sibling modules (this change adds one
port method to each; a module worker sandboxed to that module may do this, or the
leader may, per dispatch). **[leader]** — outside every module's sandbox (`cmd/`,
`internal/gateway/`, `sqlc.yaml`, `Makefile`, root docs, other modules' `AGENTS.md`).
See design.md D1–D13 for the rationale behind each group.

**Hard ordering constraints:**
- Wave 1 (telemetry/charging port additions) must land before Wave 3 (analytics
  domain code), which calls the new `...UpdatedSince` methods.
- Wave 2 (migration + sqlc) must land before Wave 3 — `deriveVehicleMetrics`'s output
  struct and `Recalculate`'s UPSERT need the generated `analyticsdb` types.
- Wave 4 (offline characterization tests) needs Wave 3's code to compile against, but
  is authored from design.md's Test Contract fixtures (already fixed, D-Test Contract)
  — it can be drafted in parallel with Wave 3 and wired in once Wave 3 lands.
- Wave 5 (gateway/cmd re-point) needs Wave 3's public port shape final.
- Wave 6 (DB-integration tests) needs Wave 2's generated types AND Wave 3's code —
  cannot compile before both exist. Final wave before verification.
- Wave 7 (docs) is independent of code content but must describe the POST-change
  state, so it runs after Waves 1–5 land (content, not compile, dependency).

## Wave 1 — sibling-module port additions (module: telemetry worker, module: charging worker)

- [x] **1.1** `internal/telemetry/telemetry.go` — add `UpdatedAt time.Time` to the
  `Snapshot` struct (doc comment: exposes the existing `vehicle_snapshots.updated_at`
  column, no new DB column). `internal/telemetry/mapping.go`'s `rowToSnapshot` (or
  equivalent) maps `r.UpdatedAt` onto it. No existing field, method, or port
  signature changes (design.md 1.1, specs/telemetry/spec.md "Snapshot Last-Updated
  Timestamp Is Exposed On The Domain Type").
  `depends_on`: — · `parallel_ok`: yes

- [x] **1.2** `internal/telemetry/telemetry.go` — add
  `SnapshotsByVehicleUpdatedSince(ctx, accountID, teslaID, since time.Time)
  ([]Snapshot, error)` to `Reader`. `internal/telemetry/db/query.sql` — new query
  `WHERE account_id = $1 AND tesla_id = $2 AND updated_at >= $3`, scoped by
  `account_id` leading (per this project's index convention) — reuses the existing
  `vehicle_snapshots`' `(account_id, tesla_id, captured_at)` composite index's
  leading columns for the equality filters; `updated_at` is a residual filter within
  that scan (no new index required — verify via `EXPLAIN` in the DB-integration test,
  Wave 6). `internal/telemetry/service.go` — implement, reusing `rowToSnapshot`.
  Regenerate via `make sqlc` after 1.1's column mapping and this query both exist
  (design.md D "Watermark lives in its own analytics-owned table" context;
  specs/telemetry/spec.md "Snapshot Updated-Since Read Port").
  `depends_on`: 1.1 · `parallel_ok`: with 1.3

- [x] **1.3** `internal/telemetry/telemetry.go` — add
  `SuperchargerSessionsByVehicleUpdatedSince(ctx, accountID, teslaID, since
  time.Time) ([]SuperchargerSession, error)` to `SuperchargerReader`.
  `internal/telemetry/db/query.sql` — new query,
  `WHERE account_id = $1 AND tesla_id = $2 AND updated_at >= $3`.
  `internal/telemetry/service.go` — implement. `make sqlc` regenerate
  (specs/telemetry/spec.md "Supercharger Session Updated-Since Read Port").
  `depends_on`: — · `parallel_ok`: with 1.1, 1.2

- [x] **1.4** `internal/charging/charging.go` — add
  `ListEntriesByVehicleUpdatedSince(ctx, accountID, teslaID, since time.Time)
  ([]Entry, error)` to `Reader`. `internal/charging/db/query.sql` — new query,
  `WHERE account_id = $1 AND tesla_id = $2 AND updated_at >= $3`.
  `internal/charging/service.go` — implement. `make sqlc` regenerate
  (specs/manual-charge-log/spec.md "List entries by vehicle updated since a given
  instant").
  `depends_on`: — · `parallel_ok`: with 1.1–1.3

## Wave 2 — analytics schema (module: analytics worker)

- [x] **2.1** `internal/analytics/db/migrations/<timestamp>_add_vehicle_metrics.sql`
  — goose migration creating `vehicle_metrics` exactly per design.md "Database
  Changes" DDL (all columns, the two CHECK constraints, the UNIQUE constraint — no
  separate `CREATE INDEX`, per the Index Plan). **DENSE table (design-gate
  revision):** `distance_traveled_km_calc`, `battery_used_pct_calc`,
  `days_spanned_calc`, `consumed_pct` are NULLABLE (only `id`/`account_id`/
  `tesla_id`/`metric_date`, the three raw-observation columns, and `flagged` are
  `NOT NULL`) — do not carry forward this task's original `NOT NULL` list from an
  earlier draft; the schema block in design.md "Database Changes" is authoritative.
  `-- +goose Down` drops the table.
  `depends_on`: — · `parallel_ok`: with 2.2

- [x] **2.2** `internal/analytics/db/migrations/<timestamp+1>_add_vehicle_metric_watermarks.sql`
  — goose migration creating `vehicle_metric_watermarks` exactly per design.md's DDL
  (the `source` CHECK vocabulary, the UNIQUE constraint). `-- +goose Down` drops the
  table.
  `depends_on`: — · `parallel_ok`: with 2.1

- [x] **2.3** `internal/analytics/db/query.sql` — sqlc queries: `UpsertVehicleMetric`
  (INSERT ... ON CONFLICT (account_id, tesla_id, metric_date) DO UPDATE, per D11),
  `DeleteVehicleMetricsInRangeExcept` (or equivalent DELETE-of-stale-rows per D11 —
  design the exact SQL shape, e.g. `DELETE ... WHERE account_id=$1 AND tesla_id=$2
  AND metric_date BETWEEN $3 AND $4 AND metric_date != ALL($5::date[])`; under the
  dense-table revision "stale" means no snapshot exists for that day at all, D11),
  **two separate filtered reads, per D13's dense-table revision (NOT one shared
  query)**: `VehicleMetricsConsumedByVehicleBetween` (`WHERE account_id=$1 AND
  tesla_id=$2 AND metric_date BETWEEN $3 AND $4 AND battery_used_pct_calc IS NOT
  NULL ORDER BY metric_date`, backs `ConsumedByDay`) and
  `VehicleMetricsOdometerByVehicleBetween` (same shape, `AND
  distance_traveled_km_calc IS NOT NULL`, backs `OdometerDeltaByDay`) — both filters
  exclude predecessor-less rows so each Reader method's output stays
  characterization-identical to today (design.md D13, Fixture C). Also
  `GetVehicleMetricWatermark` (single row by `account_id, tesla_id, source`),
  `UpsertVehicleMetricWatermark`.
  `depends_on`: 2.1, 2.2 · `parallel_ok`: no

- [ ] **2.4** `sqlc.yaml` — add the `analytics` entry (package `analyticsdb`, `out:
  internal/analytics/db`, same `uuid → google/uuid.UUID` override as the three
  existing entries). Run `make sqlc` (Claude may run — allowed codegen command) to
  generate `internal/analytics/db/*.go`. **[leader]** (`sqlc.yaml` is outside the
  analytics module's own files, though conventionally the module worker may still
  make this edit since it only concerns this module's own entry — leader confirms at
  commit).
  `depends_on`: 2.3 · `parallel_ok`: no

- [x] **2.5** **[leader — appended during Wave 2, not in the original artifacts]**
  `Makefile` — add `internal/analytics/db/migrations` to `MIGRATIONS_DIRS` (line 38),
  and `.air.toml` — add the same path to `exclude_dir` (line 28). Neither was covered
  by any task in the original tasks.md: 2.4 wires `sqlc.yaml` only. This is the
  silent-failure class tier 2's design gate was about — `sqlc` reads `sqlc.yaml`
  directly, so codegen and `go build` both stay green while `make migrate-up` never
  applies the analytics migrations at all. Appended rather than folded into 2.4
  because tasks are append-only.
  `depends_on`: — · `parallel_ok`: yes

## Wave 3 — analytics domain code (module: analytics worker)

- [x] **3.1** `internal/analytics/consumed.go` — rename `deriveConsumedByDay` to
  `deriveVehicleMetrics`, returning `[]vehicleMetricRow` (new unexported struct,
  every `vehicle_metrics` column except `id`/`created_at`/`updated_at`) instead of
  `[]DayConsumption`. **Loop shape change, required by the dense-table revision
  (design.md D10 — read this section, not just this task, before implementing):**
  iterate `i := 0` to `len(snapshots)-1` (NOT `i := 1` as `deriveConsumedByDay`
  does today — that skips `snapshots[0]` structurally). For `i == 0` (no local
  `prev`) OR `cur.BatteryUsedPctCalc == nil` (D5a), emit a row with
  `battery_level_pct`/`odometer_km`/`battery_range_km` from `cur` and every derived/
  consumed field left as the Go zero-value mapped to SQL NULL on write, `flagged :=
  false`, `missing_charging_type := ""` — the D5/D5a flag comparison MUST NOT run
  for this row (see design.md D9's dedicated rationale: a stored `0` would falsely
  flag a vehicle's first day as a charge gap). Otherwise (`i >= 1` AND
  `cur.BatteryUsedPctCalc != nil`), compute the row exactly as
  `deriveConsumedByDay` computes it today, additionally capturing
  `cur.BatteryLevelPct`, `cur.OdometerKm`, `cur.BatteryRangeKm`,
  `cur.KmPerPctCalc`, `cur.EstimatedRangeKmCalc`. Every existing helper
  (`effectiveDay`, `calendarDay`, `sumSuperchargerPctBetween`,
  `sumManualPctBetween`, `inferMissingChargingType`, `minFlagDistanceKm`) is
  unchanged. Assert against design.md's Test Contract Fixture C in the same task's
  test file (4.1) — do not consider this task done without that fixture passing.
  `depends_on`: — · `parallel_ok`: no (consumed.go is touched by nothing else in
  this wave)

- [x] **3.2** `internal/analytics/recalculate.go` (new file) — `Recalculator`
  interface (`Recalculate(ctx, accountID, teslaID, start, end time.Time) error`,
  `Reconcile(ctx, accountID, teslaID) error`), the unexported `recalculator` struct
  and `NewRecalculator(pool *pgxpool.Pool, telemetryReader telemetry.Reader,
  supercharger telemetry.SuperchargerReader, manual charging.Reader) Recalculator`
  constructor. `Recalculate` fetches per D11 (same lookback shape as today's
  `ConsumedByDay`), calls `deriveVehicleMetrics` (3.1), UPSERTs each row
  (`UpsertVehicleMetric`), then deletes stale rows in range (D11). `recalcOverlap =
  24 * time.Hour` named constant (D4). `Reconcile` reads the three watermarks (D7's
  epoch-when-missing rule), queries each `...UpdatedSince(cursor - recalcOverlap)`,
  derives the affected window per D8 (per-source naive date, min/max, ±1 day,
  clamped to yesterday), calls `Recalculate` once for the union window (skip
  entirely if no source returned rows), then advances each source's watermark
  whose query returned rows to the max `UpdatedAt`/`updated_at` observed
  (`UpsertVehicleMetricWatermark`) — a source with zero returned rows leaves its
  watermark untouched. `source` string constants
  (`sourceVehicleSnapshots`/`sourceSuperchargerSessions`/`sourceManualChargeEntries`
  — unexported, D3).
  `depends_on`: 2.4, 3.1 · `parallel_ok`: no

- [x] **3.3** `internal/analytics/reader.go` — `NewReader`'s signature gains a
  leading `pool *pgxpool.Pool` parameter (RecentEfficiency's existing dependencies
  and window param are otherwise unchanged). `ConsumedByDay`'s implementation
  becomes the `VehicleMetricsConsumedByVehicleBetween` SELECT (2.3 — the
  `battery_used_pct_calc IS NOT NULL`-filtered query, NOT an unfiltered one) mapped
  row-by-row to `DayConsumption` — `DistanceKm := row.DistanceTraveledKmCalc`,
  `DaysSpanned := row.DaysSpannedCalc`, both guaranteed non-NULL by the filter
  (D9's single-column reconciliation, D13 — no nil-check, no fallback-to-1 branch;
  that branch is now unreachable, not removed as a special case). Add
  `OdometerDeltaByDay(ctx, accountID, teslaID, start, end time.Time)
  ([]DayDistance, error)` to `Reader` — the `VehicleMetricsOdometerByVehicleBetween`
  SELECT (2.3 — the `distance_traveled_km_calc IS NOT NULL`-filtered query),
  mapped to `DayDistance{Date, KmDriven: math.Max(0, row.DistanceTraveledKmCalc),
  OdometerKm: row.OdometerKm}` (D13's clamp-on-read, applied only to an
  already-guaranteed-non-NULL value by construction). **Verify against design.md's
  Fixture C in 4.2/6.3: a predecessor-less day's row must NOT appear in either
  method's result** — this is the single most important correctness property this
  task introduces; do not consider it done without that assertion passing.
  `depends_on`: 2.4, 3.2 · `parallel_ok`: no

- [x] **3.4** `internal/analytics/analytics.go` — add the `DayDistance` domain type
  (`Date time.Time`, `KmDriven float64`, `OdometerKm float64`) with a doc comment
  matching `DayConsumption`'s existing style; add `OdometerDeltaByDay` to the
  `Reader` interface's doc comment block; add the `Recalculator` interface's doc
  comment block (mirrors `Reader`'s existing shape) — note this file is where both
  interfaces are declared, `reader.go`/`recalculate.go` hold their implementations,
  matching the module's existing `analytics.go` = "public port declarations" /
  `reader.go` = "Reader impl" split.
  `depends_on`: 3.2, 3.3 · `parallel_ok`: no (same file family, ordered last so the
  doc comments describe the final method set)

## Wave 4 — offline characterization tests (module: analytics worker)

> Authored against design.md's Test Contract fixtures (Fixture A, Fixture B,
> **Fixture C** — the predecessor-less-row fixture added at the database design
> gate — all fixed BEFORE this wave, per `ai/go-conventions.md`'s "author expected
> values up front" rule). Pure Go, no DB — compiles once Wave 3 lands; may be
> drafted in parallel against the fixtures alone and wired to the real functions
> once 3.1–3.4 exist.

- [x] **4.1** `internal/analytics/consumed_test.go` — update for the
  `deriveConsumedByDay` → `deriveVehicleMetrics` rename (signature/return-type
  change); add `TestDeriveVehicleMetrics_FixtureA`,
  `TestDeriveVehicleMetrics_FixtureB`, and **`TestDeriveVehicleMetrics_FixtureC`**
  asserting the exact column values design.md's Test Contract specifies (Fixture A/B:
  including `km_per_pct_calc`/`estimated_range_km_calc` nullability under the
  divisor guard; **Fixture C: a row IS emitted for the predecessor-less day, with
  `distance_traveled_km_calc`/`battery_used_pct_calc`/`km_per_pct_calc`/
  `estimated_range_km_calc`/`days_spanned_calc`/`consumed_pct` all NULL, `flagged
  == false` — NOT true, NOT left to whatever a zero-value comparison would produce
  — and `missing_charging_type == ""`**). Every pre-existing assertion in this file
  that exercised `deriveConsumedByDay`'s `DayConsumption`-shaped output is ported to
  check the corresponding fields on `vehicleMetricRow` — no assertion dropped
  (roadmap D10: characterization, not a rewrite).
  `depends_on`: 3.1 · `parallel_ok`: with 4.2

- [x] **4.2** `internal/analytics/reader_test.go` — add
  `TestReader_ConsumedByDay_ReadsPrecomputedRows`,
  `TestReader_OdometerDeltaByDay_ClampsOnRead`, and
  **`TestReader_ConsumedByDay_ExcludesPredecessorlessRow` /
  `TestReader_OdometerDeltaByDay_ExcludesPredecessorlessRow`** against a fake
  `analyticsdb`-shaped row source (mirroring this file's existing
  `fakeReadStore`/`newFakeReader` pattern one level up — fake the SELECT result,
  not a live DB; the fake for the two new tests supplies a filtered result set, per
  the two separate queries in 2.3/3.3 — it does not need to simulate SQL `WHERE`
  itself). Assert Fixture B's `-2.0` stored delta reads back as `KmDriven: 0.0`
  (D13's clamp) while `ConsumedByDay`'s `DistanceKm` for the same row stays `-2.0`
  (unclamped) — the exact divergence design.md's Test Contract calls out. **Assert
  Fixture C's row (fed to the fake, since the filtered-SELECT fake naturally
  excludes it) produces an empty result from BOTH methods** — this is the test that
  would catch an implementation that forgot D13's filter entirely (it would show up
  as a non-empty result here, not as a build failure).
  `depends_on`: 3.3 · `parallel_ok`: with 4.1

- [x] **4.3** `internal/gateway/handlers/history_test.go` — **[leader]**, outside the
  analytics sandbox. Pin today's `buildOdometerChart` output (before this change's
  gateway edit, Wave 5) for a fixture matching design.md's Fixture A/B, THEN assert
  the post-Wave-5 chart-only `buildOdometerChart` (fed a fake `analytics.Reader.
  OdometerDeltaByDay` returning the same values `deriveVehicleMetrics` would have
  produced) renders byte-identical `HeightPct`/tooltip/label output — the D10
  characterization proof for the gateway's own slice of this change. Written in
  this wave (fixtures fixed now); executed as a normal Go test once Wave 5 lands
  (not gated on Wave 5 to author, gated on it to pass).
  `depends_on`: none (fixtures only) to author; Wave 5 to pass · `parallel_ok`: yes

## Wave 5 — gateway and composition-root re-point (leader — outside analytics sandbox)

- [x] **5.1** `internal/gateway/handlers/history.go` — `buildOdometerChart` becomes
  chart-only: replace its snapshot-delta/clamp/bucketing body with a call to
  `h.analyticsReader.OdometerDeltaByDay(ctx, uid, teslaID, start, end)`, bucket the
  sparse result the same way `buildConsumedChart` already buckets `ConsumedByDay`'s
  sparse result (`byDay` map + iterate `[start..end]`), and keep only `HeightPct`
  scaling, `buildYAxisTicks`, labels, tooltips (`formatKmRaw`/`formatKm`), i18n
  (design.md D6). `buildHistoryView` keeps its existing
  `telemetryReader.SnapshotsByVehicleBetween(readStart, end)` call feeding ONLY
  `buildBatteryChart` now (battery chart's fetch/behavior is byte-for-byte
  unchanged — design.md's Risk note on this exact point). `buildBatteryChart`
  itself: untouched.
  `depends_on`: 3.4 · `parallel_ok`: no

- [x] **5.2** `internal/gateway/gateway.go`, `internal/gateway/handlers/handlers.go`
  — `Deps` gains `AnalyticsRecalculator analytics.Recalculator` (doc comment mirrors
  `AnalyticsReader`'s existing one); `Handler` gains the unexported
  `analyticsRecalculator` field; constructor forwarding updated.
  `depends_on`: 3.4 · `parallel_ok`: with 5.1

- [x] **5.3** `internal/gateway/handlers/charges.go` — `ChargeCreate`: after
  `h.chargingWriter.Create` succeeds, call
  `h.analyticsRecalculator.Recalculate(ctx, uid, entry.TeslaID, entry.ChargedOn,
  entry.ChargedOn)`, log-and-continue on error (never fail the user-facing write —
  mirrors this file's existing error-logging convention for non-fatal follow-ups).
  `ChargeRowUpdate`: resolve the pre-update entry's `ChargedOn` (via the existing
  `chargingReader`/`fetchEntryVM`-style lookup, before calling `Update`), then after
  `h.chargingWriter.Update` succeeds call `Recalculate` for the old date and (if
  different) the new date. `ChargeRowDelete`: resolve the entry's `ChargedOn` via
  `h.fetchEntryVM` BEFORE calling `h.chargingWriter.Delete` (design.md D5 — `Delete`
  does not return the deleted entry), then after the delete succeeds call
  `Recalculate` for that date. specs/gateway/spec.md "Manual Charge Write Path
  Triggers Analytics Recalculation".
  `depends_on`: 5.2 · `parallel_ok`: no

- [ ] **5.4** `cmd/web/main.go` — `analytics.NewReader`'s call site gains the leading
  `pool` argument; construct `analytics.NewRecalculator(pool, telemetry.NewReader(pool),
  telemetry.NewSuperchargerReader(pool), charging.NewReader(pool))` and wire it into
  `gateway.Deps.AnalyticsRecalculator`.
  `depends_on`: 3.4, 5.2 · `parallel_ok`: with 5.5

- [ ] **5.5** `cmd/poller/main.go` — `analytics.NewReader`'s call site gains the
  leading `pool` argument (the poller already has `pool` in scope). Construct
  `analytics.NewRecalculator(...)` alongside the existing `analyticsReader`. Add a
  per-vehicle `Reconcile` call to `reconcilingCollector` (or a sibling decorator
  mirroring `newGapReconciler`'s exact existing shape — same
  `acct.AllRegisteredVehicles` loop, same per-vehicle error-log-and-continue
  isolation, same "runs after a successful cycle" placement) — either folded into
  the SAME loop `newGapReconciler` already runs (one iteration over
  `AllRegisteredVehicles`, two independent calls per vehicle) or a second decorator;
  prefer folding into the same loop to avoid a second `AllRegisteredVehicles` call
  per cycle.
  `depends_on`: 3.4, 5.4 · `parallel_ok`: no

## Wave 6 — DB-integration tests (module: analytics worker — final wave, DB-gated)

> `DATABASE_URL`-gated, cannot compile before Wave 2's generated types and Wave 3's
> code both exist. This is the module's FIRST-EVER DB-backed test file — mirrors
> `internal/telemetry/db_integration_test.go`'s existing `TestMain`/testdb harness
> pattern exactly (new precedent for this module, not a new pattern for the
> codebase).

- [x] **6.1** `internal/analytics/db_integration_test.go` (new file) —
  `TestMain`/testdb setup mirroring `internal/telemetry/testdb_test.go`'s pattern.
  `TestRecalculate_FixtureA`, `TestRecalculate_FixtureB`, and
  **`TestRecalculate_FixtureC`**: seed the fixture snapshots/sessions/entries
  through `telemetry`'s and `charging`'s own writers (not direct SQL — exercises
  the real cross-module read path `Recalculate` depends on), call `Recalculate`,
  assert the persisted `vehicle_metrics` row matches design.md's Test Contract
  exactly (all three fixtures, all columns — **Fixture C's row must exist with
  its five derived columns and `consumed_pct` NULL, `flagged = false` [querying the
  actual boolean value, not just checking it is falsy/zero], `missing_charging_type`
  NULL**).
  `depends_on`: 2.4, 3.2 · `parallel_ok`: with 6.2

- [x] **6.2** `internal/analytics/db_integration_test.go` — `TestReconcile_
  BackfillsOnFirstRun` (D7: no prior watermark → full history), `TestReconcile_
  Idempotent` (D-Test Contract's idempotence scenario: a second `Reconcile` call
  with no source changes performs no net row change and does not regress any
  watermark), `TestReconcile_RevisedOldSupercharger Session` (D-"revised session
  weeks old" scenario from specs/analytics/spec.md).
  `depends_on`: 6.1 · `parallel_ok`: no

- [x] **6.3** `internal/analytics/db_integration_test.go` —
  `TestReader_ConsumedByDay_ReadsBackWhatRecalculateWrote` and
  `TestReader_OdometerDeltaByDay_ReadsBackWhatRecalculateWrote`: call `Recalculate`
  then the corresponding `Reader` method, assert the round-trip matches (closes the
  loop the offline tests in Wave 4 fake). **`TestReader_BothMethods_
  ExcludeFixtureCRow`**: call `Recalculate` over a range covering Fixture C's day
  (so the row genuinely exists in the real database, confirmed by a direct SELECT),
  then call `ConsumedByDay` and `OdometerDeltaByDay` for that same range and assert
  BOTH return an empty result for that date — the real, DB-backed proof of D13's
  filter (Wave 4's version of this test fakes the filtered query result; this one
  exercises the actual SQL `WHERE ... IS NOT NULL` clause end-to-end).
  `depends_on`: 6.1 · `parallel_ok`: with 6.2

- [x] **6.4** **[appended during Wave 4 — coverage rescue, not in the original artifacts]**
  `internal/analytics/db_integration_test.go` — assert `Recalculate`'s FETCH behaviour:
  (a) the exact `[start, end]` lookback window it passes to each of the three source
  ports, (b) that `accountID`/`teslaID` scoping reaches every port, and (c) that an
  error from any one source propagates rather than being swallowed. Wave 4 task 4.2
  removed five offline tests (`TestConsumedByDay_FetchWindows`,
  `..._AccountIDScoping_PassedToEveryPort`, and the three `..._Error_Propagates`)
  because `ConsumedByDay` no longer fetches through those ports at all — that fetch
  moved into `Recalculate`. The removal was correct, but it leaves these three
  behaviours asserted NOWHERE in the suite. This task is what makes the coverage move
  rather than vanish. Appended rather than folded into 6.1/6.2 because tasks are
  append-only.
  `depends_on`: 6.1 · `parallel_ok`: no

## Wave 7 — documentation (leader — docs-track-structural-change, same change per CLAUDE.md)

- [x] **7.1** `internal/analytics/AGENTS.md` — "Data ownership" section rewritten:
  "None" is no longer true; describe `internal/analytics/db` (migrations + sqlc,
  package `analyticsdb`), `vehicle_metrics`, `vehicle_metric_watermarks`, and that
  no other module may import `analyticsdb` (`ai/architecture.md` §2). "Public
  interface" section: add `Recalculator` (both methods) and `OdometerDeltaByDay`
  alongside the existing `Reader` entries; note `ConsumedByDay`'s implementation
  change (precomputed read, not live) while its signature is unchanged. "Testing"
  section: add the new DB-integration testing note (this module now DOES gain a
  `DATABASE_URL`-gated harness, correcting the file's current "must never gain one"
  language) mirroring `internal/telemetry/AGENTS.md`'s existing testing section
  shape.
  `depends_on`: 3.4, 6.1 (describes the post-change state) · `parallel_ok`: yes

- [x] **7.2** Root `README.md` — Project Structure tree: add `internal/analytics/db/`
  under the `analytics/` entry. Architecture table: note analytics now owns a
  database. Dependency graph: no new edges (analytics' import set is unchanged —
  `telemetry`, `charging`, `account` — only its own persistence is new).
  `depends_on`: 3.4 · `parallel_ok`: yes

- [x] **7.3** `internal/gateway/AGENTS.md` — if its `Deps.AnalyticsReader
  analytics.Reader` bullet lists the port's methods, add `OdometerDeltaByDay`; add a
  bullet for the new `Deps.AnalyticsRecalculator analytics.Recalculator` field,
  mirroring the existing `AnalyticsReader` bullet's shape.
  `depends_on`: 5.2 · `parallel_ok`: yes

- [x] **7.4** Root `README.md` / `internal/analytics`'s own workflow docs — document
  the new `sqlc.yaml` entry and the exact commands (`make sqlc`,
  `make db-setup`/`make migrate-up` for the two new migrations) where a human or
  agent will look (CLAUDE.md "Workflow & architectural decisions are documented
  with their steps").
  `depends_on`: 2.4 · `parallel_ok`: with 7.1–7.3

- [ ] **7.5** `openspec/roadmaps/RM29-modular-monolith-boundaries.md` — flip tier 3's
  status `[ ]` → `[~]` when these artifacts are created (leader does this at
  dispatch time, not a worker task) and → `[x]` at archive. Not a task this worker
  performs; noted here for the leader's own tracking, per the roadmap file's own
  status-legend convention.
  `depends_on`: — · `parallel_ok`: n/a (leader bookkeeping, not implementation)

## Wave 8 — verification (assistant-run signals, then owner-run suite)

- [ ] **8.1** Run and report: `go build ./...`, `go vet ./...`, `gofmt -l .`
  (expect clean); `make build`, `make vet`, `make bins`; `make ui-guard`,
  `make i18n-guard` (NOT a no-op this tier — verify the new `charges.go` call
  sites and any new gateway string introduce no hardcoded literal), `make
  money-guard` (expected no-op — no monetary column touched).
  `depends_on`: Waves 1–7 · `parallel_ok`: no (final gate)

- [ ] **8.2** Invoke `Reconcile` for every registered vehicle once, in a throwaway
  script or via `--once`-style manual invocation, per design.md's "Rollout note" —
  confirms the backfill path actually populates `vehicle_metrics` for existing
  history before tier 4 is proposed. Report row counts produced, not a task the
  owner needs to re-run (informational verification, not a migration).
  `depends_on`: 8.1 · `parallel_ok`: no

- [ ] **8.3** Run `openspec validate --changes --strict` and report the result
  verbatim.
  `depends_on`: all artifact edits · `parallel_ok`: no

- [ ] **8.4** Hand off to the owner. Exact commands to paste (not run by the
  assistant, per the Test-Execution-Policy):
  ```
  go test ./internal/analytics/... ./internal/telemetry/... ./internal/charging/... ./internal/gateway/... ./cmd/...
  ```
  or the full suite:
  ```
  go test ./...
  ```
  Until the owner runs one of these and reports the result, this tier's status is
  **awaiting-user-verification**, never "done" (design.md "Verification signals").
  `depends_on`: 8.1, 8.3 · `parallel_ok`: no

## Wave 6b — cross-module test harness (appended 2026-08-21, owner-decided; unblocks 6.1–6.3)

> **Why appended:** Wave 6 found that 6.1–6.3 are impossible from an analytics-sandboxed
> worker — `//go:embed` cannot leave its own directory tree, so `internal/analytics`'s
> harness can only ever apply its own migrations, and an auto-provisioned container for
> `go test ./internal/analytics/...` contains no `vehicle_snapshots`,
> `supercharger_sessions` or `manual_charge_entries` table at all. Separately, `telemetry`
> exposes no public writer for a snapshot or a Supercharger session. The owner chose the
> shared-harness + direct-SQL-seeding route (decision **D19**). Tasks are append-only, so
> 6.1's "not direct SQL" clause is **superseded by D19**, not edited.

- [x] **6.5** `internal/testdb/testdb.go` — **[leader]**, shared test infrastructure,
  outside any module sandbox. Add a multi-directory provisioning entry point so a
  package's DB-backed tests can apply *several* modules' migrations to one throw-away
  database. Filesystem paths, not `embed` — the `..` restriction is an `embed` directive
  restriction and does not apply to `os.DirFS`, and Go always runs a test binary with its
  own package directory as the working directory, so `../telemetry/db/migrations` resolves
  reliably. Migration versions are timestamps and are globally unique across modules, so
  the union applies in one ordered goose run. Existing single-FS `Provision` callers
  (`internal/telemetry`, `internal/charging`) keep working unchanged.
  `depends_on`: — · `parallel_ok`: no (6.1–6.3 all block on it)

- [x] **6.6** `internal/analytics/testdb_test.go` — re-point this module's `TestMain` at
  the multi-directory entry point so the analytics test database carries telemetry's and
  charging's schemas alongside its own. Update the file's header comment, which currently
  documents the single-module limitation as permanent.
  `depends_on`: 6.5 · `parallel_ok`: no

> After 6.6, tasks **6.1, 6.2 and 6.3 are re-dispatched as originally written**, with the
> single D19 deviation: fixtures are seeded with direct `INSERT`s into telemetry's and
> charging's tables rather than through those modules' writers (there are none to call).
> Every assertion — design.md's Test Contract, all columns, Fixture C's NULL
> `consumed_pct` and `flagged = false` — stands exactly as specified.

- [x] **7.6** **[appended with Wave 6b]** Document the multi-directory test harness where
  an agent will look: `ai/go-conventions.md` §persistence/testing (a module whose
  DB-backed tests span more than one module's schema uses the multi-dir entry point, and
  why `embed` cannot), plus `internal/testdb`'s own doc comment. Per CLAUDE.md
  "Workflow & architectural decisions are documented with their steps" — this is a new
  required capability for tiers 5–7, which hit the same wall.
  `depends_on`: 6.5 · `parallel_ok`: with 7.1–7.4

## Wave 8b — integration-wiring fix (appended 2026-08-21, found during Wave 8 verification)

- [x] **5.6** **[appended — Wave 5 gap, found by the leader at the Wave 8 gate]**
  `cmd/web/main.go` — populate `gateway.Deps.AnalyticsRecalculator` via
  `analytics.NewRecalculator(pool, ...)`. Wave 5 added the `Deps` field (5.2), the
  `ChargeCreate` call site (5.3) and the `analytics.NewReader` pool argument (5.4), but
  **nothing ever constructed the recalculator**, so the field was nil in the running
  server: `recalculateAfterChargeWrite` calls `Recalculate` on a nil interface and
  panics on every manual charge create/update/delete. The handler tests do not catch it
  because they inject their own fake `Deps`. `go vet` cannot catch it either — a nil
  interface is a runtime state, not a type error. Also corrects
  `internal/gateway/gateway.go`'s `Deps` comment, which still told the reader
  "internal/analytics owns no database, so there is none to accidentally import".
  Appended rather than reopening 5.2–5.4, which are closed and owner-verified.
  `depends_on`: 5.2, 5.4 · `parallel_ok`: no
