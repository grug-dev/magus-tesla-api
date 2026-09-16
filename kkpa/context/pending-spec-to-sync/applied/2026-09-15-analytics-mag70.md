# Sync proposal — analytics

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `entities/vehicle-metrics/guide.md`
Source spec:  `openspec/specs/analytics/spec.md`
Generated:    `2026-09-15`
Status: APPLIED 2026-09-15

---

## What changed in the spec

MAG-70 (`platform-sweep-tenancy-rule-docs`) touched two requirements:

1. **Recent Energy-Per-Kilometre Derivation** — MODIFIED. It now says the telemetry, Supercharger
   and manual-charge-entry reads are scoped by the **vehicle identifier alone**, and the account
   identifier is used for **exactly one** purpose: resolving the vehicle's car type for the
   pack-capacity lookup. A new scenario states this. The spec records that the old wording was
   never accurate, not that behaviour changed.
2. **Multi-Tenant Scoping on Every Underlying Read** (the efficiency variant) — REMOVED. It
   claimed every underlying read was scoped to the account as defense-in-depth. A different
   requirement with a near-identical title still exists for **per-day consumption**; that one is
   unchanged and says vehicle-identity scoping.

The guide's `## Conventions & gotchas` carries one bullet that the new spec makes false:

- "This capability takes no account identifier anywhere" — the recent-efficiency derivation does
  take one, for the car-type lookup only.

That bullet is rewritten, and one bullet is added recording that the removed defense-in-depth
rule was never true (so nobody "restores" it). Everything else in the section is carried over
unchanged, which is why this is a REPLACE and not an APPEND.

No `## Glossary` block: the concept's aliases and internal names are unchanged. No
`## Component map` block: a spec carries behaviour, not file paths. No `[index]` block: routing
is unaffected.

## [guide] ## Conventions & gotchas — REPLACE

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
- **`km_per_pct_calc` has a stricter NULL rule than the other two travel figures, and it is on the latest-status port now.** It is NULL on a predecessor-less day like its siblings, and ALSO whenever that day's `battery_used_pct_calc` is `<= 0` — the divisor guard in `consumption.go`. A day the vehicle sat parked and charged therefore carries a distance and a consumed percent but no efficiency. The dashboard renders that as a dash, never a `0`.
  _Source: `internal/analytics/consumption.go` divisor guard; migration `20260821000001` column comments._
- **Almost no port here takes an account identifier — the ONE exception is the recent energy-per-kilometre derivation, and it uses the account for car type only.** Every read and every recompute is scoped by vehicle identity alone. The recent Wh/km derivation does receive an account identifier, and its single legal use is resolving that vehicle's car type for the pack-capacity lookup; it must never scope the telemetry, Supercharger, or manual-charge-entry reads. Proving that the requesting account may see a vehicle happens BEFORE the call, in the caller. Adding an account parameter back to any other port here is a regression, not a hardening.
  _Source: spec analytics — Requirement: Recent Energy-Per-Kilometre Derivation; Requirement: Multi-Tenant Scoping On Every Underlying Read; Requirement: Latest Vehicle Status Per Account._
- **The old "defense-in-depth: every underlying read is scoped to the account" rule is gone, and it was never true.** The requirement stating it was REMOVED from the spec (MAG-70). The three reads it claimed to cover — `SnapshotsByVehicleSince`, `ListSessionsByVehicle`, `ListEntriesByVehicle` — have always taken a vehicle identifier alone. Do not add account scoping to them to "restore" a rule that never held.
  _Source: spec analytics — Requirement: Recent Energy-Per-Kilometre Derivation (which absorbed and corrected the removed Multi-Tenant Scoping on Every Underlying Read requirement)._
- **The latest-status read returns nothing for a vehicle outside the given set.** The set you pass is the whole world of that call. A row exists for a vehicle you did not ask about; it must not appear in the result. This is what makes "authorize first, then pass the set" safe.
  _Source: spec analytics — Requirement: Latest Vehicle Status Per Account._
