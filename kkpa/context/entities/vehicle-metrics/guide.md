# vehicle_metrics / _calc fields — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `vehicle metrics`, `calc fields`, `calculated fields`, `metrics reconciliation`, `derived metrics`, `watermark source`, `vehicle status`, `latest vehicle status`, `battery level by day`, `per-day battery level`, `battery history`, `tire pressure`, `tyre pressure`, `TPMS`, `travel progress`, `battery drain`, `tyre pressure delta`, `tyre pressure variance`, `pressure change`
- **Internal name:** `analytics.Recalculator` (`Recalculate` / `Reconcile`) — table `vehicle_metrics` (analytics-owned), watermarks in `vehicle_metric_watermarks`. Read side for latest-per-vehicle status: `analytics.Reader.LatestMetricsForVehicles` returning `analytics.VehicleStatus`. Read side for the per-day battery history: `analytics.Reader.BatteryLevelByDay` returning `analytics.DayBattery`. **Changed by RM31 tier 3:** the Supercharger input moved from `internal/telemetry`'s port over its own, still-`public`, `supercharger_sessions` to `internal/charging`'s `SuperchargerSessionAnalyticsReader` over `charging.supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3), and the watermark `source` vocabulary became `('vehicle_snapshots', 'charge_sessions', 'manual_charge_entries')` — later changed again by `RM39-analytics-fix-watermark-vocabulary` (roadmap tier 3b) to `('vehicle_snapshots', 'supercharger_sessions', 'manual_charge_entries')`, reusing the string that named `telemetry`'s table before RM31 to now name `charging`'s table instead (see that change's `design.md` §6). **Changed by RM38 tier 1:** `vehicle_metrics` gained eight raw vehicle-status observation columns and a latest-row-per-vehicle read port. **Changed by RM40 tier 1:** a bounded per-day battery-level/range read port was added over the same table — no new column, no migration. **Changed by RM50 tier 1:** `vehicle_metrics` gained four TPMS raw-observation columns (with a one-off backfill migration for pre-existing rows), and `LatestMetricsForVehicles`'s projection widened by two more columns that already existed on the table (`distance_traveled_km_calc`, `consumed_pct`) — no new query, no new index. **Changed by RM50 tier 3:** `vehicle_metrics` gained four TPMS **delta** (`_calc`) columns, one per wheel, backfilled for pre-existing rows by a self-join migration (not cross-module — the tier 1 raw columns already sit on the same table), and `LatestMetricsForVehicles`'s projection widened by these four new columns. In the UI the two travel-progress figures are called **Travel Progress** and **Battery Drain** (RM50 tier 2). **Changed by MAG-81:** `consumed_pct` moved from `consumed.go` into `deriveConsumption` (`consumption.go`), which now takes the day's already-summed `chargePct` and returns `ConsumedPct` on `consumptionCalc` — because it needs that same figure as the divisor for `km_per_pct_calc`. No migration, no column added or removed; one formula moved and one divisor changed.

The `_calc` columns: `distance_traveled_km_calc`, `battery_used_pct_calc`, `km_per_pct_calc`,
`estimated_range_km_calc`, `days_spanned_calc` — plus charge-corrected `consumed_pct`, which
since MAG-81 is derived in the same function and is the divisor the last two are built on.

The eight status observation columns (RM38): `locked`, `sentry_mode`, `car_version`,
`inside_temp_c`, `outside_temp_c`, `charging_state`, `charge_limit_soc_pct`, `captured_at` —
raw per-day observations, not derived figures.

The per-day battery read (RM40) serves `battery_level_pct` and `battery_range_km` — both
original `NOT NULL` columns of the table, both raw per-day observations like the RM38 eight,
**not** derived `_calc` figures.

The four TPMS (tire-pressure) columns (RM50 tier 1): `tpms_pressure_fl_psi`, `tpms_pressure_fr_psi`,
`tpms_pressure_rl_psi`, `tpms_pressure_rr_psi` — raw per-day observations like the RM38 eight
and `max_range_charge_counter`, not derived `_calc` figures. Names match
`telemetry.vehicle_snapshots`' own column names exactly (front-left/front-right/rear-left/
rear-right), already in PSI — no conversion at this layer.

The four TPMS **delta** columns (RM50 tier 3): `tpms_pressure_fl_psi_calc`,
`tpms_pressure_fr_psi_calc`, `tpms_pressure_rl_psi_calc`, `tpms_pressure_rr_psi_calc` — one
per wheel, `_calc` figures like `distance_traveled_km_calc`, **not** raw observations like the
four columns above. Each is this row's raw reading minus the previous day's, in PSI.

`analytics.VehicleStatus` (the `LatestMetricsForVehicles` result type) field list: `TeslaID`,
`BatteryLevelPct`, `BatteryRangeKm`, `OdometerKm` (never nil — raw observations always
present) plus these pointer fields, nil meaning "no value", never a fabricated default —
`InsideTempC`, `OutsideTempC`, `Locked`, `SentryMode`, `CarVersion`, `ChargingState`,
`ChargeLimitSocPct`, `CapturedAt`, `MaxRangeChargeCounter` (RM38/MAG-47), and, as of RM50 tier 1,
`TpmsPressureFLPSI`, `TpmsPressureFRPSI`, `TpmsPressureRLPSI`, `TpmsPressureRRPSI`,
`DistanceTraveledKmCalc`, `ConsumedPct`, `KmPerPctCalc`, and, as of RM50 tier 3, `TpmsPressureFLPSICalc`,
`TpmsPressureFRPSICalc`, `TpmsPressureRLPSICalc`, `TpmsPressureRRPSICalc`.

## Component map

Files involved, grouped by layer. Each row: the file's role in this concept.

### Owning module — internal/analytics

| File | Role |
|---|---|
| `internal/analytics/analytics.go` | Port declarations: `Recalculator` interface (`Recalculate`, `Reconcile`), `GapReconciliationWindow` (30d), `Reader` (reads `vehicle_metrics` — `ConsumedByDay`, `OdometerDeltaByDay`). |
| `internal/analytics/recalculate.go` | The ONLY writer to `vehicle_metrics`: `Recalculate` (bounded `[start,end]` window, idempotent UPSERT on `(tesla_id, metric_date)`) and `Reconcile` (watermark-driven). Also owns `recalcOverlap` (24h commit-skew guard) and the three watermark source labels. |
| `internal/analytics/consumption.go` | Pure math: `deriveConsumption(prev, cur, chargePct)` → `consumptionCalc` — the per-day deltas behind the five `_calc` fields **plus `consumed_pct`** (MAG-81). Divisor guard: `km_per_pct`/`estimated_range` only when `consumed_pct > 0`. |
| `internal/analytics/consumed.go` | Pure assembly: `deriveVehicleMetrics(preceding, snapshots, sessions, entries, start, end)` builds the full row set. It **matches and sums** the supercharger + manual-charge deltas for each day's span (`sumSuperchargerPctBetween` / `sumManualPctBetween`) and hands the total to `deriveConsumption`; since MAG-81 the addition into `consumed_pct` happens there, and this file reads `calc.ConsumedPct` back. |
| `internal/analytics/db/query.sql` → `query.sql.go` | sqlc source of truth — the `vehicle_metrics` UPSERT + reads; `db/models.go` mirrors the columns. |
| `internal/analytics/db/migrations/20260821000001_add_vehicle_metrics.sql` | Table DDL + the `_calc` column NULL-semantics comments (D13). |

### Callers — who triggers the computation

| File | Call | When |
|---|---|---|
| `internal/app/processor.go` (`recalculateAnalytics`, step 3 of `ProcessVehicleData`) | `Reconcile` per vehicle, then gap reconciliation reads the fresh rows | Nightly batch — **the only caller that keeps the table advancing**; driven by `internal/app/scheduler.go`. |
| `internal/gateway/handlers/external_charges.go` (`recalculateAfterExternalChargeWrite`) | `Recalculate(uid, teslaID, chargedOn, chargedOn)` | After every manual-charge create/update/delete; errors logged and swallowed. |
| `cmd/web/main.go` / `cmd/poller/main.go` | `analytics.NewRecalculator(pool, telemetryReader, superchargerReader, chargingReader)` | Composition roots injecting the port into gateway Deps / the nightly processor. |

### Source data (read-only inputs — owned by other modules)

| File | Role |
|---|---|
| `internal/telemetry` (`Reader`) | `vehicle_snapshots` reads — see `architecture/telemetry-ingest-only.md`. |
| `internal/charging` (`Reader`, `SuperchargerSessionAnalyticsReader`) | `manual_charge_entries` reads + the Supercharger session reads over `charging.supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3; RM31 tier 3 moved the Supercharger input here from `internal/telemetry`). |

## How maintenance works

- **Add a new `_calc`-style derived column:** migration in `internal/analytics/db/migrations/` → add column + UPSERT to `db/query.sql` → `make sqlc` → extend `consumptionCalc` (consumption.go) / `deriveVehicleMetrics` (consumed.go) → write it in `recalculate.go`. All math stays in the zero-I/O functions; `recalculate.go` only orchestrates reads + the UPSERT.
- **Change the math of an existing field:** edit `deriveConsumption` / `deriveVehicleMetrics` only — the UPSERT is a full-row replace, so the next `Recalculate`/`Reconcile` run self-heals history (idempotent on `(tesla_id, metric_date)`).
- **Add a new source table:** new watermark source label + `Reconcile` read branch in `recalculate.go`; the source's owning module exposes a bounded read port (never import another module's `db/`).
- **Read the metrics:** `analytics.Reader` (`ConsumedByDay`, `OdometerDeltaByDay`) — the gateway history fragment reads these, never `vehicle_metrics` directly.
- **Change where the Supercharger input comes from:** it is `internal/charging`'s `SuperchargerSessionAnalyticsReader`, **not** `internal/telemetry` — RM31 tier 3 moved it so that a human battery-% correction written to `charging.supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3) reaches `vehicle_metrics`. `internal/telemetry` still supplies `vehicle_snapshots` and nothing else for this concept. Analytics must import only those modules' public interfaces.
- **Change a watermark source label:** the closed vocabulary is `vehicle_snapshots`, `supercharger_sessions`, `manual_charge_entries`, enforced by a CHECK constraint on `vehicle_metric_watermarks.source` and mirrored in `recalculate.go`'s source labels. Renaming one means a migration that changes the CHECK **and** disposes of the existing rows — RM31 tier 3 DELETEd the then-retired (pre-RM31) `supercharger_sessions` rows in favor of `charge_sessions`, and `RM39-analytics-fix-watermark-vocabulary` (roadmap tier 3b) later reversed that, DELETEing `charge_sessions` rows and reusing `supercharger_sessions` — now naming `charging`'s table, not `telemetry`'s (see that change's `design.md` §6) — because an absent cursor is defined as the epoch and the next nightly `Reconcile` rebuilds that source's history in one pass.

## Conventions & gotchas

- **Ordering is a correctness requirement:** nightly `Reconcile` (step 1) MUST run before gap reconciliation (step 2) — `ConsumedByDay` is a plain SELECT over `vehicle_metrics`, and the gap writer DELETES flags for days that no longer flag, so reconciling against stale metrics destroys state. A vehicle whose `Reconcile` fails is skipped for the gap step entirely. _Source: `internal/app/processor.go` `recalculateAnalytics` doc comment._
- **Predecessor-less days have NULL `_calc`s** — the first snapshot of a vehicle's history has no delta to derive; `days_spanned_calc`/`distance_traveled_km_calc`/`battery_used_pct_calc` are NULL, and `km_per_pct_calc`/`estimated_range_km_calc` share the same NULL plus the `consumed_pct <= 0` divisor guard (MAG-81 changed that guard from `battery_used_pct <= 0`). _Source: `internal/analytics/consumption.go`. The `20260821000001` column comments still state the OLD guard and are deliberately not rewritten — an applied migration is a record of what it did._
- **First `Reconcile` backfills full history** — watermarks start empty, so a new vehicle reads its entire source history once. Subsequent runs are incremental off the three per-source watermarks (`vehicle_metric_watermarks`), each advanced independently. _Source: `internal/analytics/recalculate.go`._
- **24h `recalcOverlap`** — every `Reconcile` read uses `updated_at >= cursor - 24h`, so commit-skew between sources self-heals next run (idempotent UPSERT makes the re-read a no-op). _Source: `recalculate.go` D4._
- **The `_calc` columns' ONLY home is `vehicle_metrics`** — the derived consumption columns were dropped from `vehicle_snapshots` (RM29 tier 4, migration `20260822000001`); never re-add read-time derivation to telemetry or the gateway. _Source: `internal/telemetry/db/migrations/20260822000001…`._
- **`consumed_pct` is charge-corrected** — `battery_used_pct_calc` plus that day's matched supercharger + manual charge deltas; a predecessor-less day's `consumed_pct` has no raw delta to correct. The two sums are still matched in `consumed.go`, but the addition itself lives in `deriveConsumption` (MAG-81), so the figure and the efficiency that divides by it cannot drift apart. _Source: `internal/analytics/consumption.go` `deriveConsumption`; matching in `consumed.go`._
- **"Yesterday" is resolved in the poller's own timezone, not UTC** — `recalculateAnalytics` computes its window with `time.Now().In(p.loc)`; `internal/analytics` itself stays location-free (each row's bucket day travels with it). _Source: `internal/app/processor.go` D-B12 comment._

- **Analytics owns `vehicle_metrics` and `vehicle_metric_watermarks` and NOTHING else — every input arrives through another module's public read port.** It imports the public `Reader` of `internal/telemetry`, the public `Reader` **and `SuperchargerSessionAnalyticsReader`** of `internal/charging`, and the public `Service` of `internal/account`. Never `internal/telemetry/db`, `internal/charging/db`, or `internal/account/db`, and never a shared pool reaching into another module's tables. _Source: spec analytics — Requirement: No Cross-Module Database Access._
- **The Supercharger input is `internal/charging` over `charging.supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3), not `internal/telemetry` over its own, still-`public`, `supercharger_sessions`.** This is load-bearing, not cosmetic: `charging.supercharger_sessions` is the table a human battery-% verification writes to, so reading anywhere else would make the correction invisible to `vehicle_metrics`. _Source: spec analytics — Requirement: No Cross-Module Database Access._
- **Three independent cursors, each advanced alone.** One cursor per (vehicle, source) over telemetry snapshots, Supercharger sessions, and manual charge entries; advancing one must never rewind or skip another. A source with no cursor is treated as never incorporated, so its first reconciliation backfills that source's whole history for the vehicle. _Source: spec analytics — Requirement: Incremental Recompute Via An Analytics-Owned Watermark._
- **An absent watermark row means epoch — which is why a source migration can safely DELETE cursors.** Dropping a retired source's rows costs one full re-read on the next nightly pass; carrying the old cursor value forward risks silently skipping any row in the new table older than the inherited cursor. _Source: spec analytics — Requirement: Incremental Recompute Via An Analytics-Owned Watermark._
- **A weeks-old revision is picked up because the cursor is `updated_at`-driven, not a trailing window.** A Supercharger session from three weeks ago whose `updated_at` refreshes today recomputes the day it affects. This is exactly the mechanism a human battery-% edit rides. _Source: spec analytics — Requirement: Incremental Recompute Via An Analytics-Owned Watermark._
- **Charge-to-day matching is source-specific and half-open for Supercharger sessions.** A session matches a day by its stop instant against `[predecessor capture, this day's capture)` — inclusive of the start, **exclusive** of the end; a manual entry matches by its logged calendar date, inclusive. Do not unify the two rules. _Source: spec analytics — Requirement: Charge-to-Day Matching Is Source-Specific._

- **The eight status observations are populated on EVERY row, including a predecessor-less day — the opposite rule to the `_calc` columns.** They are raw observations copied verbatim from that day's own capture, not deltas, so there is nothing for a missing predecessor to invalidate. Gating them behind the `prev == nil` check that the `_calc` columns use would blank a vehicle's first tracked day. _Source: spec analytics — Requirement: Precomputed Vehicle Status Observations._
- **The eight are copied verbatim, never re-derived or converted.** They mirror the day's telemetry capture exactly as reported; adding a computation, a default, or a unit conversion on this path is a defect. _Source: spec analytics — Requirement: Precomputed Vehicle Status Observations._
- **Existing rows are never retroactively populated — there was no backfill.** A row written before the status columns existed keeps all eight absent until a new capture triggers a recalculation of that same day. Absence must never be replaced with a fabricated default such as "unlocked" or "sentry off". _Source: spec analytics — Requirement: Precomputed Vehicle Status Observations._
- **NULL on `sentry_mode` is ambiguous; NULL on the other seven is not.** For `sentry_mode`, NULL means EITHER "the vehicle did not report sentry" OR "this row predates the status columns". Disambiguate with `captured_at`: NULL sentry with a non-NULL `captured_at` means "not reported"; both NULL means "predates tracking". _Source: spec analytics — Requirement: Precomputed Vehicle Status Observations._
- **The four TPMS columns are populated on EVERY row, including a predecessor-less day — the same rule as the RM38 eight, the opposite rule to the `_calc` columns.** They are raw observations copied verbatim from that day's own capture, not deltas, so there is nothing for a missing predecessor to invalidate. _Source: `openspec/changes/RM50-analytics-add-tire-pressure-columns/design.md` D2._
- **Unlike every other raw-observation column on this table, the TPMS columns' migration DID backfill pre-existing rows** — a one-off, user-confirmed `UPDATE ... FROM telemetry.vehicle_snapshots` inside the migration itself, not a wait for the next `Recalculate`/`Reconcile`. This is a deliberate, recorded deviation from "No Cross-Module Database Access": a `goose`-run SQL statement, never a Go import, run once at deploy time. A row whose day has no matching snapshot keeps all four columns NULL, not an error. _Source: design.md Part C._
- **The four TPMS delta (`_calc`) columns follow the `_calc` NULL rule, the OPPOSITE of the four raw TPMS columns above.** A raw TPMS column is populated on every row, predecessor or not. A delta column (`tpms_pressure_fl_psi_calc` etc.) is NULL for two independent reasons: the day has no predecessor row at all, OR either day's own raw wheel reading is itself NULL — the same rule `distance_traveled_km_calc` already follows. A `0.0` delta means "no pressure change"; it must never also mean "unknown". _Source: `openspec/changes/RM50-analytics-add-tire-pressure-variance/design.md` D1/D2._
- **The delta columns' migration also backfilled pre-existing rows, but by a self-join, not a cross-module read.** `20260908000003` reads only `analytics.vehicle_metrics` joined against itself on `metric_date - 1` — tier 1's migration already copied the raw readings onto this same table, so no other module's schema is touched. This is NOT a "No Cross-Module Database Access" deviation and needs no `// boundary:allow:` comment. _Source: design.md Part B._
- **A tyre-pressure delta partly reflects ambient air temperature, not only a real pressure change** — roughly 1 PSI per 5.5°C. This is accepted, not a defect. Never add a dead-zone threshold or a target-pressure comparison to "correct" it. _Source: RM50 roadmap RD3; design.md "The accepted cost, restated for a future reader"._
- **`LatestMetricsForVehicles`'s `distance_traveled_km_calc`/`consumed_pct` projection is not a new read** — both columns already existed on `vehicle_metrics` (written by `Recalculate` since RM29); RM50 only added them to this ONE query's SELECT list. No new index: both are projected only, never filtered/ordered on, so `idx_vehicle_metrics_latest` still serves the query unchanged. _Source: design.md D3._
- **`LatestMetricsForVehicles` returns the capability's own domain type, never another module's capture record.** It yields `analytics.VehicleStatus`, never `telemetry.Snapshot` — the gateway must not receive a telemetry type through this port. _Source: spec analytics — Requirement: Latest Vehicle Status Per Account._
- **One result per vehicle, each on that vehicle's OWN latest day — not the account's latest day overall.** With two vehicles whose most recent computed days differ, each entry must describe its own vehicle's latest day. This is what the `DISTINCT ON (tesla_id) … ORDER BY tesla_id, metric_date DESC` shape guarantees; changing the ORDER BY breaks it silently. _Source: spec analytics — Requirement: Latest Vehicle Status Per Account._
- **An empty vehicle set, or a set whose vehicles have no computed rows, returns an empty result and no error** — never an error, and never a nil-versus-empty distinction the caller has to handle. The input is a set of vehicles now, not an account. _Source: spec analytics — Requirement: Latest Vehicle Status Per Account._

- **The per-day battery read reports a day even when it has no computable predecessor** — unlike distance travelled, battery-percentage-used, and the corrected consumed-percentage figure, which all exclude predecessor-less days. Battery level and range are raw observations, not deltas against a prior day, so the predecessor question does not apply to them. Filtering them the way the sibling reads are filtered would silently hide every vehicle's **first tracked day**. _Source: spec analytics — Requirement: Per-Day Battery Level and Range Read._
- **A day with no precomputed observation is ABSENT from the result, never zero-valued** — the read is sparse. No fabricated or zero entry is substituted, because a stored zero is a real battery reading and would be indistinguishable from a missing one. _Source: spec analytics — Requirement: Per-Day Battery Level and Range Read._
- **An empty range, or a vehicle with no observations, returns an empty result and NO error** — the same empty-result contract every other read port on this capability carries. Callers range over the result directly; there is no nil case to guard. _Source: spec analytics — Requirement: Per-Day Battery Level and Range Read._
- **Every per-day battery read is scoped to the given vehicle identifier alone — it takes NO account identifier.** A vehicle belongs to exactly one account at a time, so vehicle identity already gives the isolation an account identifier would have added. The tenant boundary did not disappear; it moved OUT of this capability. _Source: spec analytics — Requirement: Per-Day Battery Level and Range Read; Requirement: Multi-Tenant Scoping On Every Underlying Read._
- **The date carried by a per-day result is a FINAL bucket key** — consumers bucket on it verbatim and must never re-project it through a day-normalizing helper of their own. Identical to the rule the per-day consumption and distance reads already carry. _Source: spec analytics — Requirement: Per-Day Battery Level and Range Read._

- **`vehicle_metric_watermarks.source` is a closed vocabulary of table names stored AS DATA, and
  a table rename in another module invalidates it.** The column is never schema-qualified (the
  values are data, not SQL table references), a CHECK constraint pins the legal set, and
  `Recalculator.Reconcile` keys its per-source cursor on the string. So when a module renames a
  table this vocabulary names, the fix is an analytics-owned migration — not an edit in the
  module that did the renaming. Precedent twice over: `20260828000001` (RM31) and
  `20260902000004` (RM39 tier 3b).
  _Source: spec analytics — Requirement: Incremental Recompute Via An Analytics-Owned Watermark._
- **Retire a vocabulary value by DELETing its rows, never by UPDATEing them.** An absent watermark
  row is DEFINED as the epoch, so the next nightly `Reconcile` backfills that source's whole
  history in one pass — self-healing. Carrying the cursor value forward would make correctness
  depend on the other module's mirror pass never having gapped, which the migration cannot
  verify, and a stalled mirror would strand a carried cursor with nothing able to detect it.
  _Source: spec analytics — Requirement: Incremental Recompute Via An Analytics-Owned Watermark;
  RM39 roadmap decisions D8/D21._
- **The migration's Down DELETE is load-bearing, not tidying.** Restoring the old vocabulary while
  a row still holds the new value makes `ADD CONSTRAINT` fail with SQLSTATE 23514 and leaves the
  table with NO constraint at all. Each direction must clear the rows written under the
  vocabulary the other direction retires. Found by the round-trip test, which is why the test
  asserts the round trip rather than only the forward migration.
  _Source: migration `20260828000001`'s own Down block, re-confirmed by `20260902000004`._
- **`sqlc` mirrors the database's `COMMENT ON` text into `models.go` doc comments, so a migration
  that rewrites a comment REQUIRES `make sqlc`.** Easy to miss, because the change alters no
  column type and the build stays green either way. It has now been missed twice on this exact
  table — fixed by commit `3882a53` after RM31, and caught again in RM39 tier 3b's review round 1.
  _Source: RM39 tier 3b review finding F1._
- **The value `'supercharger_sessions'` means two different tables depending on era.** Before
  RM31 it named `internal/telemetry`'s table; since RM39 tier 3b it names `internal/charging`'s.
  No live row is ambiguous (the CHECK forbade the string in between, so the eras cannot coexist
  in data), but old backups, archived specs and `git log` are. A test asserting mid-migration
  state must pin the literal of the era it runs in, not the current Go constant.
  _Source: spec analytics; RM39 tier 3b design.md §6 and review finding F2._

- **Each wheel is independent — one absent reading never blanks the other three.** A capture that reports three wheels and not the fourth persists those three exactly as reported and leaves only the fourth absent. The same holds for the four delta columns: a wheel missing on either day makes that wheel's delta absent, and the other three still compute. Never substitute a fabricated reading.
  _Source: spec analytics — Requirement: Precomputed Tyre Pressure Observations; Requirement: Precomputed Tyre Pressure Day-Over-Day Deltas._
- **On a predecessor-less latest day, the derived figures are absent while the raw observations are present.** `LatestMetricsForVehicles` still returns battery, range, odometer, all eight status observations and all four raw TPMS readings, but `DistanceTraveledKmCalc`, `ConsumedPct` and the four `TpmsPressure*PSICalc` deltas are nil. A caller that renders a zero there is showing a value the capability never computed.
  _Source: spec analytics — Requirement: Latest Vehicle Status Per Account._
- **A pre-migration latest row reports absent TPMS values, never a fabricated pressure or delta.** The rule that already applies to the eight status observations applies to the four raw TPMS readings and the four deltas: a row whose day predates tracking, and which the one-off backfill did not match, keeps them absent.
  _Source: spec analytics — Requirement: Latest Vehicle Status Per Account._
- **Old rows get their deltas ONLY from the one-time backfill, never from a lazy read.** A row persisted before delta tracking stays absent until either the backfill migration ran, or a new capture triggers a recalculation of that same day. Reading the row does not compute the delta on the fly.
  _Source: spec analytics — Requirement: Precomputed Tyre Pressure Day-Over-Day Deltas._
- **`km_per_pct_calc` divides by `consumed_pct`, NOT by `battery_used_pct_calc` (MAG-81).** `consumed_pct` is the raw battery drop plus that day's recorded charges, so it is the only figure that describes what the day really spent. Both are computed in `deriveConsumption`, which takes the already-summed `chargePct` from `consumed.go`; `consumed.go` reads `calc.ConsumedPct` back rather than re-adding the same operands, so the formula has one home.
- **`km_per_pct_calc` still has a stricter NULL rule than the other two travel figures, and it is on the latest-status port.** It is NULL on a predecessor-less day like its siblings, and ALSO whenever that day's `consumed_pct` is `<= 0` — the divisor guard in `consumption.go`. That now means only one thing: **the day's charge was never recorded**, which is the case `charge_gaps` flags. Before MAG-81 the guard read `battery_used_pct_calc <= 0`, so it ALSO swallowed every day the vehicle drove and charged normally — 12 of 124 stored days on the owner's own history. The dashboard renders a NULL as a dash, never a `0`.
  _Source: `internal/analytics/consumption.go` divisor guard; migration `20260821000001` column comments._
- **Almost no port here takes an account identifier — the ONE exception is the recent energy-per-kilometre derivation, and it uses the account for car type only.** Every read and every recompute is scoped by vehicle identity alone. The recent Wh/km derivation does receive an account identifier, and its single legal use is resolving that vehicle's car type for the pack-capacity lookup; it must never scope the telemetry, Supercharger, or manual-charge-entry reads. Proving that the requesting account may see a vehicle happens BEFORE the call, in the caller. Adding an account parameter back to any other port here is a regression, not a hardening.
  _Source: spec analytics — Requirement: Recent Energy-Per-Kilometre Derivation; Requirement: Multi-Tenant Scoping On Every Underlying Read; Requirement: Latest Vehicle Status Per Account._
- **The old "defense-in-depth: every underlying read is scoped to the account" rule is gone, and it was never true.** The requirement stating it was REMOVED from the spec (MAG-70). The three reads it claimed to cover — `SnapshotsByVehicleSince`, `ListSessionsByVehicle`, `ListEntriesByVehicle` — have always taken a vehicle identifier alone. Do not add account scoping to them to "restore" a rule that never held.
  _Source: spec analytics — Requirement: Recent Energy-Per-Kilometre Derivation (which absorbed and corrected the removed Multi-Tenant Scoping on Every Underlying Read requirement)._
- **The latest-status read returns nothing for a vehicle outside the given set.** The set you pass is the whole world of that call. A row exists for a vehicle you did not ask about; it must not appear in the result. This is what makes "authorize first, then pass the set" safe.
  _Source: spec analytics — Requirement: Latest Vehicle Status Per Account._

## Column detail — the three analytics tables

Moved here from `internal/analytics/AGENTS.md`, which every worker dispatched to that module
re-reads in full. The ownership rules and the port contracts stayed there; this is the
column-by-column detail.

- `vehicle_metrics` — one row per `(tesla_id, metric_date)` for every day
  the vehicle reported, holding both the raw observations and the five derived `_calc`
  columns. It is a **precomputed read model**: written by `Recalculator`, read by
  `Reader`. It is **dense** — a day with no computable predecessor still gets a row,
  with its `_calc` columns and `consumed_pct` NULL and `flagged` an explicit `false`
  (`design.md` D9/D10). That is why both `Reader` queries filter `IS NOT NULL` rather
  than trusting a zero.
  - **Eight more columns** (`locked`, `sentry_mode`, `car_version`, `inside_temp_c`,
    `outside_temp_c`, `charging_state`, `charge_limit_soc_pct`, `captured_at`), added by
    `RM38-analytics-add-vehicle-status-columns` (MAG-12 tier 1). All eight are copied
    verbatim from the day's own `telemetry.Snapshot` and — unlike the five `_calc`
    columns above — are always populated regardless of whether that day has a
    computable predecessor. **All eight are nullable, and no backfill was run**
    (roadmap D2): every row that existed before this migration keeps all eight NULL
    forever, self-healing only on that vehicle's next `Reconcile`. `sentry_mode`'s NULL
    is **ambiguous** — it can mean either "the vehicle did not report sentry" or
    "this row predates the migration" — where every other column's NULL means only the
    latter; do not attempt to disambiguate it here without first reading
    `openspec/changes/RM38-analytics-add-vehicle-status-columns/design.md` D2/D3/D8,
    which also documents the `captured_at`-as-proxy disambiguation a future consumer
    can use.
  - **`max_range_charge_counter`** (migration `20260905000001`) is a **ninth** column of
    exactly that shape: copied verbatim from the day's own `telemetry.Snapshot`, always
    populated regardless of a computable predecessor, nullable, **no backfill**. Two
    things set it apart from the eight above. It carries **no unit suffix** because it
    is a count, not a measurement (`ai/go-conventions.md` §display units). And its NULL
    is **ambiguous like `sentry_mode`'s**, not like the other seven: the vehicle may not
    have reported it (the telemetry source field is itself a `*int`) or the row may
    predate the migration — disambiguate via `captured_at`. A reported `0` is stored as
    `0`, never NULL.
  - **`tpms_pressure_fl_psi`/`fr`/`rl`/`rr`** (migration `20260908000002`,
    `RM50-analytics-add-tire-pressure-columns`) are four more columns of exactly the
    same shape as `max_range_charge_counter`: copied verbatim from the day's own
    `telemetry.Snapshot`, always populated regardless of a computable predecessor,
    nullable. Names match `telemetry.vehicle_snapshots`' own column names exactly
    (`fl`/`fr`/`rl`/`rr` = front-left/front-right/rear-left/rear-right), already in PSI —
    no conversion at this layer. **Unlike** `max_range_charge_counter`, this migration
    **DID backfill** every pre-existing row from `telemetry.vehicle_snapshots` in the
    same migration (a one-off, user-confirmed deviation from "No Cross-Module Database
    Access" — a `goose`-run SQL statement, never a Go import; see design.md Part C for
    the full rationale). NULL still means one of two things — the vehicle did not report
    TPMS at that capture, or the row predates the migration and had no matching
    snapshot to backfill from — but no consumer needs to disambiguate them (unlike
    `sentry_mode`/`max_range_charge_counter`, this NULL is not otherwise ambiguous:
    `telemetry.Snapshot`'s own TPMS fields never had a fabricated non-nil default).
  - **`tpms_pressure_fl_psi_calc`/`fr`/`rl`/`rr`** (migration `20260908000003`,
    `RM50-analytics-add-tire-pressure-variance`) are four **derived delta** columns, one
    per wheel: this row's raw reading minus the previous day's row, in PSI. **This is
    the opposite NULL rule from the raw `tpms_pressure_*_psi` columns just above.** A raw
    column is always populated regardless of a predecessor; a delta column is NULL when
    EITHER of two things is true — the day has no predecessor row at all, OR either
    day's own raw wheel reading is itself NULL (`design.md` D2) — the same rule
    `distance_traveled_km_calc` already follows. Computed in `consumption.go`'s
    `deriveConsumption` via the `tpmsDeltaPSI` helper, populated only in the
    "has a predecessor" branch of `deriveVehicleMetrics`
    (`consumed.go`), same as `DistanceTraveledKmCalc`. This delta partly reflects
    ambient air temperature change (about 1 PSI per 5.5°C), not only a genuine
    pressure change — accepted, not a defect (roadmap RD3); never add a threshold or a
    target-pressure comparison to "fix" it. **This migration DID backfill** every
    pre-existing row, but unlike tier 1's raw-column backfill, this one reads only
    `analytics.vehicle_metrics` joined against itself (a self-join on
    `metric_date - 1`) — not a cross-module read, so it needs no
    `// boundary:allow:` comment. A row whose previous day is missing keeps all four
    columns NULL after the backfill, never a fabricated `0`. Full rationale:
    `openspec/changes/RM50-analytics-add-tire-pressure-variance/design.md` D1–D3.
- `vehicle_metric_watermarks` — one recompute cursor per `(tesla_id, source)`,
  three sources. Drives `Reconcile`'s incremental pass; no row means "epoch",
  i.e. backfill the vehicle's full history (`design.md` D7).
- `charge_gaps` — one row per flagged vehicle-day whose battery math does not add up
  (migration `20260815000002`, originally `RM28-telemetry-add-charge-gap-storage`,
  MAG-15; moved into this module, unchanged, by `RM29-analytics-own-charge-gaps`,
  MAG-26 tier 5; re-keyed on `tesla_id` alone by migration `20260911000001`,
  `analytics-rekey-charge-gaps-on-tesla-id`, MAG-64). Written through the `GapWriter` port, driven by this module's own
  `ConsumedByDay`-derived flagging logic (D5/D5a) via `internal/app`'s nightly
  reconciliation — this module both derives the gap AND stores the conclusion; no
  other module writes or reads this table. Columns: `id UUID PRIMARY KEY`,
  `tesla_id BIGINT NOT NULL` (**always resolved, NOT
  NULL** — this module filters out any vehicle/session it cannot attribute to a
  currently-registered vehicle before gap detection ever runs), `vin TEXT NOT NULL`,
  `gap_date DATE NOT NULL` (the flagged calendar day, plain `DATE` — no time-of-day
  component), `missing_charging_type TEXT NOT NULL CHECK (IN ('MANUAL',
  'SUPERCHARGER'))` (which charge source is suspected missing — `SUPERCHARGER` when
  a Supercharger session exists that day with NULL start/end battery percentages,
  `MANUAL` otherwise), `created_at TIMESTAMPTZ NOT NULL DEFAULT now()` (when FIRST
  flagged — preserved across every re-upsert of the same still-flagged day),
  `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()` (refreshed to `now()` on every
  re-confirmation). `UNIQUE (tesla_id, gap_date)` constraint
  (`charge_gaps_tesla_date_unique`) is both the write-idempotency mechanism
  (`ON CONFLICT DO UPDATE`) and the index that serves `GapWriter`'s own
  read-before-diff query. **This table has no second index**, by design: the UNIQUE
  index alone serves every query the module runs, and a `gap_date DESC` twin would
  add nothing because Postgres reads the same index backward at no cost. **No FK** on `tesla_id` (same
  no-cross-module-FK precedent as `vehicle_metrics`/`vehicle_metric_watermarks` —
  referential integrity is upheld by flow, not a DB constraint,
  `ai/architecture.md` §2). **No `raw_data` JSONB** — this table stores a
  Go-computed conclusion (this module's own derivation), not an external API
  response, so the mandatory-`raw_data` rule (`ai/go-conventions.md` §persistence)
  does not apply here.

## Related KB

- Architecture: `architecture/telemetry-ingest-only.md` (the snapshot/supercharger sources this table derives from)
- Workflows: `workflows/manual-charge-crud.md` (the post-write `Recalculate` trigger), `workflows/supercharger-stats-read.md`
