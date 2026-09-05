# vehicle_metrics / _calc fields — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `vehicle metrics`, `calc fields`, `calculated fields`, `metrics reconciliation`, `derived metrics`, `watermark source`, `vehicle status`, `latest vehicle status`, `battery level by day`, `per-day battery level`, `battery history`
- **Internal name:** `analytics.Recalculator` (`Recalculate` / `Reconcile`) — table `vehicle_metrics` (analytics-owned), watermarks in `vehicle_metric_watermarks`. Read side for latest-per-vehicle status: `analytics.Reader.LatestMetricsByAccount` returning `analytics.VehicleStatus`. Read side for the per-day battery history: `analytics.Reader.BatteryLevelByDay` returning `analytics.DayBattery`. **Changed by RM31 tier 3:** the Supercharger input moved from `internal/telemetry`'s port over its own, still-`public`, `supercharger_sessions` to `internal/charging`'s `SuperchargerSessionAnalyticsReader` over `charging.supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3), and the watermark `source` vocabulary became `('vehicle_snapshots', 'charge_sessions', 'manual_charge_entries')` — later changed again by `RM39-analytics-fix-watermark-vocabulary` (roadmap tier 3b) to `('vehicle_snapshots', 'supercharger_sessions', 'manual_charge_entries')`, reusing the string that named `telemetry`'s table before RM31 to now name `charging`'s table instead (see that change's `design.md` §6). **Changed by RM38 tier 1:** `vehicle_metrics` gained eight raw vehicle-status observation columns and a latest-row-per-vehicle read port. **Changed by RM40 tier 1:** a bounded per-day battery-level/range read port was added over the same table — no new column, no migration.

The `_calc` columns: `distance_traveled_km_calc`, `battery_used_pct_calc`, `km_per_pct_calc`,
`estimated_range_km_calc`, `days_spanned_calc` — plus charge-corrected `consumed_pct` derived
alongside them.

The eight status observation columns (RM38): `locked`, `sentry_mode`, `car_version`,
`inside_temp_c`, `outside_temp_c`, `charging_state`, `charge_limit_soc_pct`, `captured_at` —
raw per-day observations, not derived figures.

The per-day battery read (RM40) serves `battery_level_pct` and `battery_range_km` — both
original `NOT NULL` columns of the table, both raw per-day observations like the RM38 eight,
**not** derived `_calc` figures.

## Component map

Files involved, grouped by layer. Each row: the file's role in this concept.

### Owning module — internal/analytics

| File | Role |
|---|---|
| `internal/analytics/analytics.go` | Port declarations: `Recalculator` interface (`Recalculate`, `Reconcile`), `GapReconciliationWindow` (30d), `Reader` (reads `vehicle_metrics` — `ConsumedByDay`, `OdometerDeltaByDay`). |
| `internal/analytics/recalculate.go` | The ONLY writer to `vehicle_metrics`: `Recalculate` (bounded `[start,end]` window, idempotent UPSERT on `(account_id, tesla_id, metric_date)`) and `Reconcile` (watermark-driven). Also owns `recalcOverlap` (24h commit-skew guard) and the three watermark source labels. |
| `internal/analytics/consumption.go` | Pure math: `deriveConsumption(prev, cur)` → `consumptionCalc` — the per-day deltas behind the five `_calc` fields (divisor guard: `km_per_pct`/`estimated_range` only when `battery_used_pct > 0`). |
| `internal/analytics/consumed.go` | Pure assembly: `deriveVehicleMetrics(preceding, snapshots, sessions, entries, start, end)` builds the full row set, adding supercharger + manual-charge corrections into `consumed_pct`. |
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
- **Change the math of an existing field:** edit `deriveConsumption` / `deriveVehicleMetrics` only — the UPSERT is a full-row replace, so the next `Recalculate`/`Reconcile` run self-heals history (idempotent on `(account_id, tesla_id, metric_date)`).
- **Add a new source table:** new watermark source label + `Reconcile` read branch in `recalculate.go`; the source's owning module exposes a bounded read port (never import another module's `db/`).
- **Read the metrics:** `analytics.Reader` (`ConsumedByDay`, `OdometerDeltaByDay`) — the gateway history fragment reads these, never `vehicle_metrics` directly.
- **Change where the Supercharger input comes from:** it is `internal/charging`'s `SuperchargerSessionAnalyticsReader`, **not** `internal/telemetry` — RM31 tier 3 moved it so that a human battery-% correction written to `charging.supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3) reaches `vehicle_metrics`. `internal/telemetry` still supplies `vehicle_snapshots` and nothing else for this concept. Analytics must import only those modules' public interfaces.
- **Change a watermark source label:** the closed vocabulary is `vehicle_snapshots`, `supercharger_sessions`, `manual_charge_entries`, enforced by a CHECK constraint on `vehicle_metric_watermarks.source` and mirrored in `recalculate.go`'s source labels. Renaming one means a migration that changes the CHECK **and** disposes of the existing rows — RM31 tier 3 DELETEd the then-retired (pre-RM31) `supercharger_sessions` rows in favor of `charge_sessions`, and `RM39-analytics-fix-watermark-vocabulary` (roadmap tier 3b) later reversed that, DELETEing `charge_sessions` rows and reusing `supercharger_sessions` — now naming `charging`'s table, not `telemetry`'s (see that change's `design.md` §6) — because an absent cursor is defined as the epoch and the next nightly `Reconcile` rebuilds that source's history in one pass.

## Conventions & gotchas

- **Ordering is a correctness requirement:** nightly `Reconcile` (step 1) MUST run before gap reconciliation (step 2) — `ConsumedByDay` is a plain SELECT over `vehicle_metrics`, and the gap writer DELETES flags for days that no longer flag, so reconciling against stale metrics destroys state. A vehicle whose `Reconcile` fails is skipped for the gap step entirely. _Source: `internal/app/processor.go` `recalculateAnalytics` doc comment._
- **Predecessor-less days have NULL `_calc`s** — the first snapshot of a vehicle's history has no delta to derive; `days_spanned_calc`/`distance_traveled_km_calc`/`battery_used_pct_calc` are NULL, and `km_per_pct_calc`/`estimated_range_km_calc` share the same NULL plus the `battery_used_pct <= 0` divisor guard. _Source: migration `20260821000001` column comments._
- **First `Reconcile` backfills full history** — watermarks start empty, so a new vehicle reads its entire source history once. Subsequent runs are incremental off the three per-source watermarks (`vehicle_metric_watermarks`), each advanced independently. _Source: `internal/analytics/recalculate.go`._
- **24h `recalcOverlap`** — every `Reconcile` read uses `updated_at >= cursor - 24h`, so commit-skew between sources self-heals next run (idempotent UPSERT makes the re-read a no-op). _Source: `recalculate.go` D4._
- **The `_calc` columns' ONLY home is `vehicle_metrics`** — the derived consumption columns were dropped from `vehicle_snapshots` (RM29 tier 4, migration `20260822000001`); never re-add read-time derivation to telemetry or the gateway. _Source: `internal/telemetry/db/migrations/20260822000001…`._
- **`consumed_pct` is charge-corrected** — `battery_used_pct_calc` plus that day's matched supercharger + manual charge deltas; a predecessor-less day's `consumed_pct` has no raw delta to correct. _Source: `internal/analytics/consumed.go` `deriveVehicleMetrics`._
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
- **`LatestMetricsByAccount` returns the capability's own domain type, never another module's capture record.** It yields `analytics.VehicleStatus`, never `telemetry.Snapshot` — the gateway must not receive a telemetry type through this port. _Source: spec analytics — Requirement: Latest Vehicle Status Per Account._
- **One result per vehicle, each on that vehicle's OWN latest day — not the account's latest day overall.** With two vehicles whose most recent computed days differ, each entry must describe its own vehicle's latest day. This is what the `DISTINCT ON (tesla_id) … ORDER BY tesla_id, metric_date DESC` shape guarantees; changing the ORDER BY breaks it silently. _Source: spec analytics — Requirement: Latest Vehicle Status Per Account._
- **An account with no computed rows returns an empty result and no error** — never an error, and never a nil-versus-empty distinction the caller has to handle. _Source: spec analytics — Requirement: Latest Vehicle Status Per Account._

- **The per-day battery read reports a day even when it has no computable predecessor** — unlike distance travelled, battery-percentage-used, and the corrected consumed-percentage figure, which all exclude predecessor-less days. Battery level and range are raw observations, not deltas against a prior day, so the predecessor question does not apply to them. Filtering them the way the sibling reads are filtered would silently hide every vehicle's **first tracked day**. _Source: spec analytics — Requirement: Per-Day Battery Level and Range Read._
- **A day with no precomputed observation is ABSENT from the result, never zero-valued** — the read is sparse. No fabricated or zero entry is substituted, because a stored zero is a real battery reading and would be indistinguishable from a missing one. _Source: spec analytics — Requirement: Per-Day Battery Level and Range Read._
- **An empty range, or a vehicle with no observations, returns an empty result and NO error** — the same empty-result contract every other read port on this capability carries. Callers range over the result directly; there is no nil case to guard. _Source: spec analytics — Requirement: Per-Day Battery Level and Range Read._
- **Every per-day battery read is scoped to the requesting account's own vehicle** — two accounts whose vehicles share a vehicle identifier never see each other's observations, even on the same calendar day. This is the capability's tenant boundary, not an optimization. _Source: spec analytics — Requirement: Per-Day Battery Level and Range Read._
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

## Related KB

- Architecture: `architecture/telemetry-ingest-only.md` (the snapshot/supercharger sources this table derives from)
- Workflows: `workflows/manual-charge-crud.md` (the post-write `Recalculate` trigger), `workflows/supercharger-stats-read.md`
