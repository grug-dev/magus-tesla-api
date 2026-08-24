# Design — RM29-analytics-add-vehicle-metrics

> Numbering note: this document's decisions are numbered **D1, D2, …**, scoped to this
> change only, distinct from the roadmap's own **D1–D10** in
> `openspec/roadmaps/RM29-modular-monolith-boundaries.md` (cited as **roadmap D1**,
> **roadmap D7**, etc.) and from the binding interview outcomes in the dispatch prompt
> (cited as **IO-1**..**IO-6**). D1–D6 below each carry exactly one IO decision, named
> in their heading, verbatim in substance; D7 onward are decisions this artifacts pass
> had to make to turn IO-1..IO-6 into a buildable schema and task list.

## Context

`internal/analytics` is the platform's derived-metrics module: today it computes two
things live, on every call — rolling efficiency (`RecentEfficiency`) and per-day
corrected battery consumption (`ConsumedByDay`) — reading `internal/telemetry`,
`internal/charging` and `internal/account` directly, with **no database of its own**
(`internal/analytics/AGENTS.md` "Data ownership: None"). Meanwhile the gateway's
`buildOdometerChart` (`internal/gateway/handlers/history.go`) independently
recomputes an odometer delta from the same telemetry snapshots, including a
clock-skew clamp — a second, gateway-local implementation of vehicle-domain math.

Roadmap D1 requires analytics to become a **precomputed read model**: a
`vehicle_metrics` table, one row per vehicle-day, carrying both duplicated raw
observations and analytics' own derived figures, populated incrementally rather than
recomputed on every read. This is the roadmap's gold-standard tier (roadmap D8) — every
later tier's own read-model table (tier 5's `charge_gaps` relocation, tier 6's
`charge_sessions`) inherits this tier's primary-key style, watermark shape and
index-plan reasoning without re-deriving them.

Two things make this a real feature, not a rename: it is this codebase's **second-ever
incremental-write-with-watermark** design (`charge_gaps`, tier-precedent RM28, is
upsert/delete-on-full-recompute, not incremental), and it is the **first schema created
by a module worker sandboxed to `internal/analytics`** rather than by the module that
already owns a `db/` package (`telemetry`, `account`, `charging` each got their first
migration before this pipeline's module-sandboxing existed in its current form).

## Goals / Non-Goals

**Goals**
- `analytics.vehicle_metrics`: the precomputed daily read model (roadmap D1), sole
  backing store for `OdometerDeltaByDay` (new) and `ConsumedByDay` (reimplemented).
- `analytics.vehicle_metric_watermarks`: the per-source incremental-recompute cursor
  (roadmap D7), one row per `(vehicle, source)`, three independent sources.
- `Recalculator.Recalculate` (explicit-window recompute+upsert) and
  `Recalculator.Reconcile` (watermark-driven, per-vehicle, called nightly and
  interim-wired into the manual-charge write path).
- Gateway's `buildOdometerChart` becomes chart-only (roadmap D5): no delta, no clamp,
  no day-bucketing left in `internal/gateway`.
- Every column tier 4 (`RM29-telemetry-drop-derived-columns`) needs to drop from
  `vehicle_snapshots` has a home in `vehicle_metrics` — verified explicitly below
  (D9).

**Non-Goals**
- `RecentEfficiency` — untouched, stays live. Not a per-day-shaped computation, not
  named in IO-1..IO-6, and out of scope under the owner's standing minimal-scope
  preference.
- The Supercharger Stats tile math (`buildSuperchargerTiles`) — deferred to tier 6
  (`RM29-charging-add-charge-sessions`), which relocates the underlying session rows
  into `charging.charge_sessions`; computing it here would be rewritten there.
- Dropping `vehicle_snapshots`' five `_calc` columns — tier 4, gated on this tier
  having backfilled every vehicle's history first (see "Rollout" below).
- `internal/app`, `ProcessVehicleData` — tier 7. This tier's `Recalculate`/`Reconcile`
  calls are wired directly into `cmd/poller` and `internal/gateway/handlers/charges.go`
  (interim composition-root arrangement; IO-5 says tier 7 relocates the *call*, not
  the logic).
- Manual ↔ Supercharger source convergence — never in RM29.
- A `vin` column on `vehicle_metrics` (considered, rejected — see D9): no consumer in
  this tier needs it, unlike `charge_gaps`' notification use case.

## Decisions

### D1 — Grain and primary key (carries IO-1)

`vehicle_metrics` is one row per `(account_id, tesla_id, metric_date)`. Surrogate
`id UUID PRIMARY KEY DEFAULT gen_random_uuid()` plus
`CONSTRAINT vehicle_metrics_account_tesla_date_unique UNIQUE (account_id, tesla_id,
metric_date)` — 1:1 with `vehicle_snapshots`' deduped daily grain, and structurally
identical to that table's own `vehicle_snapshots_account_tesla_date_unique` constraint
(`20260805000001_dedupe_vehicle_snapshots_daily.sql`) and to `charge_gaps`'
`charge_gaps_account_tesla_date_unique` (`20260815000002_add_charge_gaps.sql`) — the
platform's established per-vehicle-day key shape, reused rather than re-invented.

**Rejected:**
- **A natural composite PK** `(account_id, tesla_id, metric_date)` — diverges from
  every other table in this codebase (`vehicle_snapshots`, `supercharger_sessions`,
  `manual_charge_entries`, `charge_gaps` all use a surrogate UUID PK plus a separate
  UNIQUE constraint). No consumer needs the natural key as a foreign-key target.
- **A per-snapshot row with an FK to `vehicle_snapshots.id`** — a hard FK from
  `analytics` into `telemetry`'s table is a direct module-boundary violation
  (`ai/architecture.md` §2, "no cross-module database leaks"), and a row keyed to a
  snapshot id breaks the moment daily-dedupe (`vehicle_snapshots`' own
  `ON CONFLICT ... DO UPDATE`) replaces that row with a new capture for the same day —
  the FK target would need to be rewritten in lock-step, coupling two modules'
  write paths.

### D2 — Watermark lives in its own analytics-owned table (carries IO-2)

```sql
CREATE TABLE vehicle_metric_watermarks (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id        UUID   NOT NULL,
    tesla_id          BIGINT NOT NULL,
    source            TEXT   NOT NULL,
    source_updated_at TIMESTAMPTZ NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (account_id, tesla_id, source)
);
```

(Owner's shape, verbatim — not re-derived.) `source` is constrained to a closed
3-value vocabulary (D3): see full DDL in "Database Changes" below.

**Rationale:** survives "zero `vehicle_metrics` rows yet" (the first-ever run for a
vehicle — a `MAX(updated_at)` over `vehicle_metrics` would have nothing to read); one
scalar fact stored once, not repeated per day-row; the cursor read is a single-row
lookup by the table's own unique index.

**Rejected:**
- **`MAX(source_updated_at)` over `vehicle_metrics`** — no cursor exists before the
  first row is written; needs its own index (this table needs none beyond its
  UNIQUE constraint's); a row later deleted or recomputed away (e.g. its predecessor
  snapshot removed) silently rewinds the cursor with no signal.
- **Any fixed trailing-N-days window** — contradicts the roadmap's binding constraint
  (see roadmap "Constraint that forced D7"): Supercharger billing can revise a session
  weeks old, so a trailing window would silently stop seeing revisions once they age
  out of it.

### D3 — One cursor row per (vehicle, source); three independent sources (carries IO-3)

`source` values: `'vehicle_snapshots'`, `'supercharger_sessions'`,
`'manual_charge_entries'` — named after the physical table each source's data lives
in (self-describing, mirrors `missing_charging_type`'s `'MANUAL'`/`'SUPERCHARGER'`
free-standing string-label convention; no FK, just a label). Each source's cursor
advances independently, on its own row.

**Rationale:** `manual_charge_entries` is user-edited at any hour (a user can back-date
or correct an entry mid-afternoon); the other two move only at the nightly poll. A
shared cursor would couple those two very different clocks — a single edit to a manual
entry would force re-scanning `vehicle_snapshots`/`supercharger_sessions` too, for no
reason. A source added later (e.g. a future weather adapter feeding a metric) starts
its own row at the "no watermark yet" epoch (D7 below) and backfills independently,
never rewinding the other two sources' progress.

### D4 — Commit-skew guard: 24h safety overlap on read (carries IO-4)

Every `Reconcile` read queries a source with `updated_at > cursor - recalcOverlap`,
where `recalcOverlap = 24 * time.Hour` is a named Go constant in
`internal/analytics/recalculate.go` (mirrors `chargingSourceLimit`'s existing
named-constant convention in `reader.go`). `Recalculate` itself (the UPSERT) is
idempotent on `(account_id, tesla_id, metric_date)`, so a row the overlap re-reads
that has not actually changed produces a byte-identical UPSERT — a correctness no-op,
paid for in a handful of extra read rows.

**Rationale:** at ~1 snapshot/vehicle/day and a handful of charge events per vehicle
per week, a 24h overlap re-reads at most a day's worth of rows per source per vehicle —
negligible against the read-heavy Performance-Profile's explicit tolerance for
off-hours write-path cost. It also self-heals a partially-failed run: if `Reconcile`
crashes after advancing vehicle A's watermark but before vehicle B's, the next run's
overlap re-covers the boundary instant without needing a transaction spanning every
vehicle.

**Rejected:**
- **Advancing the cursor to run-start time** — still loses a transaction that began
  before run-start and committed after it (the exact commit-skew case this guard
  exists for); couples correctness to how closely two clocks (the DB server's `now()`
  and the reconciler's wall clock) agree; does not self-heal a partial failure the
  way idempotent overlap-re-reading does.
- **No guard at all** — a user editing a manual charge entry mid-run has that edit
  permanently and silently skipped if its `updated_at` lands in the instant between
  the query and the cursor's naive advance.

### D5 — Write freshness: Recalculate after the write, not lazy read-through (carries IO-5)

`charges.go`'s `ChargeCreate`, `ChargeRowUpdate`, `ChargeRowDelete` each call
`recalculator.Recalculate(ctx, uid, teslaID, affectedDate, affectedDate)` — where
`affectedDate` is the entry's `ChargedOn` (Create/Update: the submitted entry's date;
Delete: the pre-delete entry's date, resolved via the existing `fetchEntryVM` lookup
before the delete commits) — immediately after the corresponding `chargingWriter.
Create`/`Update`/`Delete` call succeeds. An `Update` whose `ChargedOn` changed
recalculates **both** the old and the new date (two calls, or one call spanning
`min(old,new)..max(old,new)` when adjacent — implementation detail, not a contract
change).

**Rationale:** today `ConsumedByDay` is computed live, so editing a charge entry
changes what the very next chart render shows, with no separate refresh step — a
real, currently-working user-visible behavior. After this tier, `ConsumedByDay` reads
`vehicle_metrics` (D-precompute below) instead of computing live; without this
write-path call, that same edit would sit stale until the next nightly `Reconcile` —
a **user-visible regression**, and a direct violation of D10's characterization bar
("pin current output, assert identical after the move" — a chart that goes stale for
hours is not identical output). The interim caller is the gateway handler
(`internal/gateway/handlers/charges.go`); **tier 7's `app.RecalculateVehicleData`
relocates this call, not its logic** — the same call, moved to a new composition
root, exactly as `cmd/poller`'s existing gap-reconciliation call will also move in
tier 7.

**Rejected:**
- **Accepting staleness until the next midnight `Reconcile`** — the user-visible
  regression above.
- **Lazy read-through recompute** (recompute inside `ConsumedByDay`/
  `OdometerDeltaByDay` when the read finds stale/missing data) — puts a write on the
  read path, directly fighting the read-heavy Performance-Profile; two concurrent
  readers would both trigger a recompute of the same row, wasted work with no
  coordination.

### D6 — Scope: exactly what moves out of the gateway this tier (carries IO-6)

`buildOdometerChart` (`history.go`, ~line 365) moves out: the per-day odometer delta
(`cur.OdometerKm - prev.OdometerKm`), its negative clamp (the RD5 clock-skew/anomaly
rule), and the `effectiveDayUTC` day-bucketing **as used by this one function**. After
this tier `buildOdometerChart` calls `analyticsReader.OdometerDeltaByDay(ctx, uid,
teslaID, start, end)` and does only: bucket the sparse result into `[start..end]`
(the same `byDay` map + iterate-days pattern `buildConsumedChart` already uses for
`ConsumedByDay`'s sparse output — no new pattern invented), `HeightPct` scaling
relative to the window's max, `buildYAxisTicks`, labels, tooltips (`formatKmRaw`,
`formatKm`), and i18n.

**Explicitly NOT moved, and why:**
- `buildBatteryChart` — untouched. It renders `s.BatteryLevelPct` directly with no
  delta, no clamp, no domain decision beyond "which day does this raw observation
  belong to" (its own `effectiveDayUTC` call). Per roadmap D5's own test ("does the
  value change when the user picks a different date range?") — no: `BatteryLevelPct`
  is what Tesla reported for that day, verbatim, regardless of range. It was never a
  D5 violation, so this tier does not touch it. (`vehicle_metrics` DOES duplicate
  `battery_level_pct` per roadmap D1, for a future consumer — see D9 — but nothing in
  this tier requires the gateway to switch this chart's read source.)
- **Supercharger Stats tile math** — explicitly deferred to tier 6 (see Non-Goals);
  recorded here again because IO-6 named it directly.

### D7 — `Reconcile`'s "no watermark yet" case: treated as epoch, backfills in one pass

When `Reconcile` finds no `vehicle_metric_watermarks` row for a `(vehicle, source)`,
the cursor is treated as `time.Time{}` (Go's zero value, year 1) rather than "now" —
so `updated_at > cursor - 24h` matches every row that source has ever stored, and the
vehicle's **entire history** backfills into `vehicle_metrics` on its first `Reconcile`
call. After that call succeeds, the watermark row is inserted (not merely updated),
recording the max `updated_at` observed. This is what "survives the zero-metrics-rows
first run" (D2's stated rationale) concretely means, made explicit here because IO-2
named the property without spelling out the sentinel value.

### D8 — `Reconcile`'s date-range derivation: coarse and generous, not pixel-exact

For each source whose watermark query returns at least one row, `Reconcile` computes
that source's own naive affected date (`vehicle_snapshots`: each returned row's
`EffectiveDate`; `supercharger_sessions`: each returned row's `ChargeStopDateTime`'s
calendar date; `manual_charge_entries`: each returned row's `ChargedOn`), takes the
min and max across ALL sources' returned rows, widens by **one day on each side**, and
calls `Recalculate` once for that single `[min-1, max+1]` window (clamped to not
exceed yesterday — today's data is not captured until tomorrow's poll, matching
`cmd/poller`'s existing `newGapReconciler` convention).

**Rationale:** exact day-to-source-row attribution already exists inside
`deriveVehicleMetrics` (D12) — a supercharger session's precise day is decided by the
D12 interval-matching rule (`[prevCapturedAt, curCapturedAt)`), which needs the
surrounding snapshot pair to evaluate. Re-implementing that attribution a second time
in `Reconcile` just to pick a tighter window would duplicate logic for a purely
cost-side benefit (fewer rows re-touched by an already-idempotent UPSERT). The ±1-day
widening absorbs the one case a naive per-source date could miss by exactly one day
(a session stopping just after local midnight, attributed to the following day's
interval). `Recalculate`'s own internal fetch already re-widens by a further day for
its snapshot lookback (D12) — the two widenings compose safely; over-fetching only
costs rows read, never changes a result (same reasoning `ConsumedByDay`'s existing
reader.go already documents for its own lookback).

### D9 — Column list, and the T4-drop reconciliation (verified, not assumed)

`vehicle_metrics` carries four groups of columns:

1. **Identity:** `id`, `account_id`, `tesla_id`, `metric_date`.
2. **Duplicated raw observations (roadmap D1 — intentional):** `battery_level_pct`,
   `odometer_km`, `battery_range_km` — copied verbatim from the day's own
   `telemetry.Snapshot` (`cur.BatteryLevelPct`, `cur.OdometerKm`,
   `cur.BatteryRangeKm`), no computation.
3. **The five `_calc` columns tier 4 needs a home for** — copied verbatim from
   `telemetry.Snapshot`'s already-computed pointer fields (`cur.
   DistanceTraveledKmCalc`, `cur.BatteryUsedPctCalc`, `cur.KmPerPctCalc`, `cur.
   EstimatedRangeKmCalc`, `cur.DaysSpannedCalc`) — **no re-derivation**: `telemetry`
   already computes these at write time (`service.go`'s `deriveConsumption`); this
   tier only copies, exactly as `internal/analytics/consumed.go`'s existing
   `deriveConsumedByDay` already does for two of the five
   (`cur.BatteryUsedPctCalc`, `cur.DistanceTraveledKmCalc`, `cur.DaysSpannedCalc` —
   see its source, quoted in the codebase today).
4. **D13 corrected consumption (`DayConsumption`'s existing fields):**
   `consumed_pct`, `flagged`, `missing_charging_type`.

**The overlap, reconciled:** `DayConsumption.DistanceKm` and `DayConsumption.
DaysSpanned` are — today, in the live code — read directly from `cur.
DistanceTraveledKmCalc` and `cur.DaysSpannedCalc` (`consumed.go`, lines computing
`distanceKm`/`daysSpanned`). They are **not a second value** — they are the *same*
telemetry-sourced number `DayConsumption` already exposes under its own field names.
**Single column wins:** `vehicle_metrics.distance_traveled_km_calc` and
`vehicle_metrics.days_spanned_calc` are stored exactly once; `ConsumedByDay`'s mapping
from a `vehicle_metrics` row to a `DayConsumption` reads `DistanceKm :=
row.DistanceTraveledKmCalc` and `DaysSpanned := row.DaysSpannedCalc` directly — no
duplicate column, no divergence risk between "the odometer chart's delta" and "the
consumed chart's distance" (they are provably the same number, sourced once).

**Confirmation tier 4 needs (explicit, as required):** every one of
`distance_traveled_km_calc`, `battery_used_pct_calc`, `km_per_pct_calc`,
`estimated_range_km_calc`, `days_spanned_calc` — the exact five columns
`20260814000001_add_derived_consumption_columns_vehicle_snapshots.sql` added to
`vehicle_snapshots` — has a same-named column in `vehicle_metrics` (D-schema below).
Tier 4 can drop all five from `vehicle_snapshots` once every reader of them (this
tier's own `Recalculate`, which reads them from `telemetry.Snapshot` **before** they
are dropped) has been re-pointed at `vehicle_metrics` — a hard ordering dependency
already recorded in the roadmap table ("T3 → T4").

**Nullability — DENSE table, mirroring `vehicle_snapshots` exactly (revised at the
database design gate, owner's explicit change; supersedes this section's original
sparse-write framing).** `vehicle_metrics` writes **one row for every calendar day
that has a snapshot, whether or not that day has a predecessor** — 1:1 with
`vehicle_snapshots`' own grain, not a sparse subset of it. `distance_traveled_km_calc`,
`battery_used_pct_calc`, `days_spanned_calc` and `consumed_pct` are **nullable**,
carrying the identical meaning NULL already carries on `vehicle_snapshots` today:
"no predecessor exists for this row." `km_per_pct_calc`/`estimated_range_km_calc`
stay nullable for the pre-existing, independent reason (the divisor guard — a
zero-or-negative `battery_used_pct_calc` has no truthful ratio; unchanged, not
re-derived). `flagged` is the one exception: it is `NOT NULL` and is stored `false`
— never NULL — on a predecessor-less row (see the dedicated flagged/consumed_pct
rationale below); `missing_charging_type` stays NULL on such a row exactly as it
already does on any non-flagged row, so the existing
`vehicle_metrics_missing_type_iff_flagged` CHECK constraint is satisfied unchanged.

**Owner's rationale for dense over sparse:** a sparse table means a vehicle's
first-ever day has **no row at all** — safe for this tier's two consumers
(`ConsumedByDay`/`OdometerDeltaByDay`, both of which already skip that day today,
D5a), but a footgun for a **future** consumer: roadmap D1 says "UI/APIs read
Analytics", so if the Battery chart (or any other raw-observation reader) is ever
re-pointed at `vehicle_metrics` instead of `telemetry.Reader` directly, a sparse
table would make a vehicle's first day silently vanish from that future reader with
no NULL to detect and no error — a correctness bug that would surface only when
someone builds that future feature, far from this tier's own tests. A dense table
lets that future re-point happen safely: the row is always there; only some of its
columns are NULL. The accepted cost is exactly what's addressed below: existing and
new readers must explicitly handle NULL, and this tier's own `ConsumedByDay`/
`OdometerDeltaByDay` must filter predecessor-less rows OUT to stay
characterization-identical to today's live-computed output (D10) — see D13.

**`consumed_pct`/`flagged` — the one representation choice this revision requires
(not mechanical, decided explicitly):** a predecessor-less row has no raw
battery-level delta to correct, so `consumed_pct` is **NULL**, not `0`. Storing `0`
would be actively wrong, not merely imprecise: the existing flag condition (D5/D5a)
is "`ConsumedPct < 0`, or `ConsumedPct == 0` while `DistanceKm > minFlagDistanceKm`" —
a stored `0` on a day that plausibly has real (nonzero) distance travelled (a
vehicle's very first tracked day is often also a normal driving day) would evaluate
that second branch and mark the day **flagged**, i.e. every vehicle's first-ever day
would masquerade as a suspected charge gap, in production, silently. `flagged` is
therefore computed and stored as `false` (not NULL — the column stays `NOT NULL`)
for a predecessor-less row: there is no gap-detection question to ask about a day
with no computed consumption figure at all, and `false` is the correct, unambiguous
answer to "is this row a suspected charge gap?", not an evasion of the question.
`missing_charging_type` stays `NULL`, consistent with `flagged = false` under the
existing CHECK constraint — no schema change needed for this, only the write-path
rule that a predecessor-less row's `flagged` computation short-circuits to `false`
before the D5/D5a comparison ever runs.

**`vin` column — considered, rejected.** `charge_gaps` carries a `vin` column for a
future notification consumer that needs a durable vehicle key independent of
`tesla_id`. No consumer of `vehicle_metrics` in this tier needs that (both
`OdometerDeltaByDay` and `ConsumedByDay` are always called with a resolved
`(accountID, teslaID)` pair already). Adding it now would be schema for a
speculative future need — the owner's standing minimal-scope preference and this
project's anti-over-abstraction stance (`CLAUDE.md` "AI efficiency") both argue
against it. Add it in a later tier if a `vehicle_metrics`-reading notification
consumer materializes, the same way `charge_gaps` added it for its own.

### D10 — `deriveVehicleMetrics`: one derivation function, superset of `deriveConsumedByDay`, now DENSE

`internal/analytics/consumed.go`'s existing `deriveConsumedByDay` is renamed
`deriveVehicleMetrics` and extended to return the fuller `vehicleMetricRow` (package-
private struct: every `vehicle_metrics` column except `id`/`created_at`/`updated_at`)
instead of `[]DayConsumption`. It keeps every existing helper unchanged
(`effectiveDay`, `calendarDay`, `sumSuperchargerPctBetween`, `sumManualPctBetween`,
`inferMissingChargingType`, `minFlagDistanceKm`).

**Loop shape change, required by the dense-table revision:** `deriveConsumedByDay`
today iterates snapshot PAIRS starting at index `i := 1` (`prev, cur :=
snapshots[i-1], snapshots[i]`), which structurally never visits `snapshots[0]` as
`cur` at all — that row is silently used only as `prev` for `i=1`. Under the dense
table, `deriveVehicleMetrics` iterates **every fetched index `i := 0` to
`len(snapshots)-1`**, so a day whose snapshot is `snapshots[0]` (no local
predecessor available in the fetched slice — this happens exactly when that day is
the vehicle's true first-ever capture, given `Recalculate`'s 1-day-lookback fetch,
D11) still gets a row. For `i == 0` (no local `prev`) **or** `cur.BatteryUsedPctCalc
== nil` (telemetry itself recorded no predecessor for this row, D5a's original
signal — kept as a second, independent check for defense-in-depth: a `prev` being
locally absent and `cur.BatteryUsedPctCalc` being nil are expected to co-occur, but
the function trusts telemetry's own field, not just local array position), the
emitted row carries `battery_level_pct`, `odometer_km`, `battery_range_km` from
`cur` (always available) and `NULL` for every one of `distance_traveled_km_calc`,
`battery_used_pct_calc`, `km_per_pct_calc`, `estimated_range_km_calc`,
`days_spanned_calc`, `consumed_pct` — with `flagged := false` and
`missing_charging_type := ""` (mapped to SQL NULL), the D5/D5a flag comparison
never evaluated at all for this row (see D9's dedicated rationale for why `flagged`
must not be left to a stray `0`-vs-distance comparison here). For every other row
(`i >= 1` AND `cur.BatteryUsedPctCalc != nil`), the row is computed EXACTLY as
`deriveConsumedByDay` computes it today (unchanged formulas, unchanged charge-event
matching against `prev.CapturedAt`/`effectiveDay(prev)`), now additionally carrying
`cur.BatteryLevelPct`, `cur.OdometerKm`, `cur.BatteryRangeKm`, `cur.KmPerPctCalc`,
`cur.EstimatedRangeKmCalc` alongside the fields it already extracted.

`Recalculate` is the function's only caller (D11); `ConsumedByDay` and
`OdometerDeltaByDay` no longer call it at all — they SELECT from `vehicle_metrics`
instead (D-precompute), filtering predecessor-less rows back out to stay
characterization-identical to today (D13).

### D11 — `Recalculate`'s fetch shape mirrors `ConsumedByDay`'s existing reader.go exactly; writes one row per snapshot day

`Recalculate(ctx, accountID, teslaID, start, end)` fetches
`telemetry.Reader.SnapshotsByVehicleBetween(ctx, accountID, teslaID, start.AddDate(0,0,-1),
end.AddDate(0,0,1))`, `telemetry.SuperchargerReader.
SuperchargerSessionsByVehicleBetween(..., start-1, end+2)`, and
`charging.Reader.ListEntriesByVehicleBetween(..., start-1, end)` — the identical
1-day/2-day lookback widening `reader.go`'s current `ConsumedByDay` implementation
already uses (copied, not redesigned — this tier reuses a working, already-reviewed
fetch shape). Runs `deriveVehicleMetrics` (D10, now dense) over the fetched data —
producing one `vehicleMetricRow` for EVERY fetched snapshot whose `effectiveDay`
falls in `[start, end]`, whether or not that row has a predecessor — then UPSERTs
every one of them into `vehicle_metrics` on the `(account_id, tesla_id, metric_date)`
conflict target; then **deletes** any existing `vehicle_metrics` row in `[start, end]`
whose `metric_date` is **not** among the rows just produced (self-healing symmetry
with `telemetry.GapWriter.ReconcileWindow`'s existing UPSERT+DELETE pattern for
`charge_gaps` — same shape, reused rather than invented). **Under the dense-table
revision, "stale" now means "no snapshot exists for that day at all anymore"** — not
"no computable value", since a computable-or-not row is produced for every day that
has a snapshot; the DELETE branch fires only when a snapshot itself was removed
(e.g. a backfill correction), a narrower and simpler condition than before.

### D12 — Charge-event day attribution is unchanged from today's `ConsumedByDay`

`deriveVehicleMetrics` keeps `sumSuperchargerPctBetween`'s and
`sumManualPctBetween`'s exact existing matching rules (interval-based for
Supercharger sessions, exclusive-start/inclusive-end for manual entries — both
already specified in the current `specs/analytics/spec.md` "Charge-to-Day Matching
Is Source-Specific" requirement, unchanged by this tier). No re-litigation.

### D13 — `ConsumedByDay`/`OdometerDeltaByDay`: SELECT + map + a NOT-NULL filter, no derivation logic

**The dense-table revision (owner's design-gate change) makes this filter
necessary, and it is the load-bearing piece that keeps both methods
characterization-identical to today's live-computed output (roadmap D10).** Today's
`deriveConsumedByDay` never emits a predecessor-less day at all (D5a: `if
cur.BatteryUsedPctCalc == nil { continue }`), so `ConsumedByDay`'s existing callers
(the gateway's consumed chart) have never once seen such a day in the result. Now
that `vehicle_metrics` stores a row for that day too (D9, D10), both read methods
MUST filter it back out, or the gateway would start rendering data it has never
rendered before — a real behavior change, not a cosmetic one, and a direct D10
violation if missed.

- `ConsumedByDay`: `SELECT ... FROM vehicle_metrics WHERE account_id = $1 AND
  tesla_id = $2 AND metric_date BETWEEN $3 AND $4 AND battery_used_pct_calc IS NOT
  NULL ORDER BY metric_date` — mapped row-by-row into `DayConsumption`. The filter
  column is `battery_used_pct_calc` (not `consumed_pct` — both are NULL under the
  identical condition, D9, so either column is an equivalent predicate; this
  document picks `battery_used_pct_calc` as the canonical one because it is the
  column `deriveConsumedByDay`'s own existing D5a check already named). Every row
  that passes this filter has non-NULL `distance_traveled_km_calc`/
  `days_spanned_calc` too (D9's "co-occur" guarantee), so the mapping reads
  `DistanceKm := row.DistanceTraveledKmCalc` and `DaysSpanned :=
  row.DaysSpannedCalc` directly with no nil-check and no fallback-to-1 branch (the
  old code's `if cur.DaysSpannedCalc != nil { daysSpanned = *cur.DaysSpannedCalc }`
  fallback is now dead code, made unreachable by the filter rather than removed
  as a special case).
- `OdometerDeltaByDay`: `SELECT ... FROM vehicle_metrics WHERE account_id = $1 AND
  tesla_id = $2 AND metric_date BETWEEN $3 AND $4 AND distance_traveled_km_calc IS
  NOT NULL ORDER BY metric_date` — mapped row-by-row into `DayDistance`.
  `OdometerDeltaByDay`'s clamp (roadmap D5's move) is applied in this mapping step,
  on an already-guaranteed-non-NULL value: `KmDriven := math.Max(0,
  row.DistanceTraveledKmCalc)` — the **only** place a negative distance is ever
  clamped; the stored column itself stays the raw, unclamped value (matching
  `telemetry`'s own "a truthful reading is always stored non-NULL, never clamped"
  precedent for the column it was copied from, D9). The clamp is never applied to a
  NULL — the filter guarantees that.

Both filter conditions are semantically the same underlying fact ("this row has a
predecessor") expressed against each method's own primary column, for readability;
they are not two different rules. Neither filter needs a new index: it is a
residual predicate evaluated against the ≤`historyRangeMaxDays` = 90 rows the
`(account_id, tesla_id, metric_date)` range scan already returns (Index Plan,
below) — negligible cost, no `EXPLAIN`-visible difference from an unfiltered scan
of the same row count.

### D-precompute — `ConsumedByDay`'s "No Cache" spec requirement is superseded, not violated

The existing `specs/analytics/spec.md` requirement "No Cache — Every Result Is
Recomputed On Read" is explicitly **replaced** by this change (see
`specs/analytics/spec.md`'s `## MODIFIED Requirements` in this change's own spec
delta) — not silently broken. The **user-visible contract it protects** ("editing a
charge entry changes the chart with no separate refresh step") is preserved by D5's
write-path `Recalculate` call, not by read-time recomputation. This is the direct,
intended consequence of roadmap D1 ("Analytics is a precomputed read model") — a
precomputed read model and "recomputed on every read" are mutually exclusive by
definition, so this requirement could not survive roadmap D1 unchanged. Recording
this explicitly, as instructed, rather than leaving a stale requirement in the main
spec that the code silently no longer satisfies.

## Database Changes (design gate — full schema, rationale, index plan)

### Schema: `vehicle_metrics`

> **Revised at the database design gate** (owner's explicit, one-item change to an
> otherwise-approved design): the table is DENSE, not sparse — one row per
> `(account_id, tesla_id, metric_date)` for **every day that has a snapshot**, 1:1
> with `vehicle_snapshots`' own grain, mirroring that table's own nullability
> exactly. See D9's "Nullability" subsection for the full rationale (the future
> re-point footgun a sparse table would create) and its dedicated
> `consumed_pct`/`flagged` representation rationale.

```sql
CREATE TABLE vehicle_metrics (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id             UUID NOT NULL,
    tesla_id               BIGINT NOT NULL,
    metric_date            DATE NOT NULL,

    -- Duplicated raw observations (roadmap D1 — intentional, D9). Always present,
    -- independent of predecessor existence — sourced directly from the day's own
    -- snapshot.
    battery_level_pct      INTEGER NOT NULL,
    odometer_km            DOUBLE PRECISION NOT NULL,
    battery_range_km       DOUBLE PRECISION NOT NULL,

    -- The five columns tier 4 drops from vehicle_snapshots, copied verbatim (D9).
    -- NULLABLE, mirroring vehicle_snapshots' own nullability EXACTLY: NULL means
    -- "no predecessor exists for this row's day" — the identical meaning NULL
    -- already carries on vehicle_snapshots today. A row exists for every day with a
    -- snapshot, whether or not it has a predecessor (dense — not a sparse subset).
    distance_traveled_km_calc DOUBLE PRECISION,  -- NULL iff no predecessor
    battery_used_pct_calc     INTEGER,           -- NULL iff no predecessor
    km_per_pct_calc            DOUBLE PRECISION, -- NULL iff no predecessor OR battery_used_pct_calc <= 0
    estimated_range_km_calc    DOUBLE PRECISION, -- NULL under the same condition
    days_spanned_calc          INTEGER,          -- NULL iff no predecessor

    -- D13 corrected consumption (DayConsumption's existing fields). consumed_pct is
    -- NULL under the identical "no predecessor" condition as battery_used_pct_calc —
    -- there is no raw delta to correct. flagged is NEVER NULL: it is FALSE (not
    -- unknown) on a predecessor-less row — see D9: a stored 0 would risk marking a
    -- vehicle's first-ever day a false-positive charge gap; flagged short-circuits
    -- to false before the D5/D5a comparison runs at all for such a row.
    consumed_pct            DOUBLE PRECISION,     -- NULL iff no predecessor
    flagged                 BOOLEAN NOT NULL,     -- false (never NULL) on a predecessor-less row
    missing_charging_type   TEXT CHECK (missing_charging_type IN ('MANUAL', 'SUPERCHARGER')),

    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT vehicle_metrics_account_tesla_date_unique
        UNIQUE (account_id, tesla_id, metric_date),
    CONSTRAINT vehicle_metrics_missing_type_iff_flagged CHECK (
        (flagged AND missing_charging_type IS NOT NULL) OR
        (NOT flagged AND missing_charging_type IS NULL)
    )
);
```

This CHECK constraint is **unaffected** by the dense-table revision: a
predecessor-less row has `flagged = false` and `missing_charging_type = NULL`,
which satisfies the constraint's `(NOT flagged AND missing_charging_type IS NULL)`
branch exactly like any other non-flagged row — no constraint edit was needed to
accommodate the new row shape, only the write-path rule that computes `flagged`
(D9, D10).

**No FK on `account_id`/`tesla_id`.** A cross-module FK from `analytics` into
`account`'s tables would couple `analytics` migrations to `account`'s schema — exactly
the coupling `ai/architecture.md` §2 forbids. Referential integrity is upheld by flow:
the only writer (`Recalculate`, called with an `(accountID, teslaID)` pair already
resolved from `account.RegisteredVehicles`/`AllRegisteredVehicles`) never invents an
identity pair. Mirrors `charge_gaps`' and `manual_charge_entries`'/`supercharger_
sessions`' identical precedent (cited in `20260815000002_add_charge_gaps.sql`'s own
comment).

**No `raw_data JSONB`.** This table stores a Go-computed conclusion (`analytics`'s own
derivation over already-stored data), not an external API response — the `raw_data`
mandate (`ai/go-conventions.md` §Persistence) applies only to tables ingesting an
external API payload. Mirrors `charge_gaps`' and `manual_charge_entries`'s identical
"no raw_data" precedent for their own non-vendor data.

**The `flagged`/`missing_charging_type` CHECK** is new relative to `DayConsumption`'s
Go-level invariant ("`MissingChargingType` is valid only when `Flagged`" —
`analytics.go`'s existing doc comment) — promoted to a database constraint here
because this is the first time that invariant is persisted rather than computed
fresh on every call. Mirrors `charge_gaps.missing_charging_type`'s existing
`CHECK (... IN (...))` pattern, extended with the flagged-pairing condition this
table additionally needs (that column didn't exist as nullable on `charge_gaps`,
which stores only flagged days).

### Schema: `vehicle_metric_watermarks`

```sql
CREATE TABLE vehicle_metric_watermarks (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id        UUID NOT NULL,
    tesla_id          BIGINT NOT NULL,
    source            TEXT NOT NULL CHECK (
        source IN ('vehicle_snapshots', 'supercharger_sessions', 'manual_charge_entries')
    ),
    source_updated_at TIMESTAMPTZ NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT vehicle_metric_watermarks_account_tesla_source_unique
        UNIQUE (account_id, tesla_id, source)
);
```

Owner's shape (IO-2), with the `source` CHECK vocabulary this artifacts pass names
(D3). No FK, no `raw_data` — same two justifications as above (a cursor is a Go-
computed conclusion, and an FK here would couple `analytics` to nothing external
anyway — `source` is a closed string label, not a reference).

### Index Plan

**Every read pattern this tier declares is served by a table's own UNIQUE constraint
index — no separate `CREATE INDEX` for either table**, mirroring
`charge_gaps`' "Read path 1... this is the SAME index Postgres builds automatically to
enforce the UNIQUE constraint... so it costs nothing beyond what the constraint
already requires" reasoning exactly:

| # | Read pattern | Served by |
|---|---|---|
| 1 | Gateway history charts: `WHERE account_id = $1 AND tesla_id = $2 AND metric_date BETWEEN $3 AND $4 [AND <col> IS NOT NULL] ORDER BY metric_date` (≤ `historyRangeMaxDays` = 90 rows; the trailing `IS NOT NULL` is `ConsumedByDay`'s/`OdometerDeltaByDay`'s D13 filter, evaluated as a residual predicate against the already-tiny range-scanned result — no index of its own) | `vehicle_metrics_account_tesla_date_unique`'s own index — `account_id` leads, satisfying this project's "account_id is the leading index column" convention (`ai/go-conventions.md` §Read optimization) for free |
| 2 | `Recalculate`'s UPSERT conflict target `(account_id, tesla_id, metric_date)` | Same index — the `ON CONFLICT` target IS the constraint's index |
| 3 | `Recalculate`'s DELETE-of-stale-rows scan, `WHERE account_id = $1 AND tesla_id = $2 AND metric_date BETWEEN $3 AND $4` | Same index |
| 4 | `Reconcile`'s single-row watermark lookup, `WHERE account_id = $1 AND tesla_id = $2 AND source = $3` | `vehicle_metric_watermarks_account_tesla_source_unique`'s own index |
| 5 | `RecentEfficiency` | Unaffected — reads `telemetry`/`charging` directly, never touches either new table |

**Deliberately not added:** a dedicated `(account_id, metric_date DESC)` index for a
hypothetical account-wide, all-vehicles query (the pattern `charge_gaps`' second
index — `idx_charge_gaps_account` — exists to serve for its own future notification
consumer). No consumer in this tier issues that query — both `OdometerDeltaByDay` and
`ConsumedByDay` are always called with a resolved `teslaID`. Per the read-heavy
Performance-Profile's own framing ("index aggressively... never at the cost of...")
the aggressive-indexing license is bounded by **declared** read patterns, not
speculative ones; adding an unused index would slow every `Recalculate`/`Reconcile`
UPSERT for no current benefit. Add it in a later tier if an account-wide consumer
appears, the same way `charge_gaps` added its own for exactly that reason.

## Test Contract (authored before implementation, per `ai/go-conventions.md`)

Three fixtures, with their exact expected output, for the characterization tests (pin
today's live-computed values) and the `Recalculate`/DB-integration tests (assert the
persisted row and the read-back `DayConsumption`/`DayDistance` match). Fixture C is
new, added at the database design gate to pin the dense-table revision's
predecessor-less-row representation (D9, D10, D13) — Fixtures A and B are otherwise
unaffected by that revision (both already describe a day WITH a predecessor, so
their expected rows are unchanged from the original design).

### Fixture A — plain day, no charge events

Two consecutive `telemetry.Snapshot`s for `(accountID=A, teslaID=42)`:

| | `CapturedDate` | `OdometerKm` | `BatteryLevelPct` | `BatteryRangeKm` |
|---|---|---|---|---|
| predecessor | 2026-08-10 | 1000.0 | 80 | 300.0 |
| current | 2026-08-11 | 1050.0 | 65 | 280.0 |

No Supercharger sessions, no manual entries in range.

`effectiveDay(current) = CapturedDate(current) - 1 day = 2026-08-10` → `metric_date`.

**Expected `vehicle_metrics` row** (`Recalculate(A, 42, 2026-08-10, 2026-08-10)`):

| Column | Value |
|---|---|
| `metric_date` | `2026-08-10` |
| `battery_level_pct` | `65` |
| `odometer_km` | `1050.0` |
| `battery_range_km` | `280.0` |
| `distance_traveled_km_calc` | `50.0` (`1050.0 - 1000.0`) |
| `battery_used_pct_calc` | `15` (`80 - 65`) |
| `km_per_pct_calc` | `3.3333...` (`50.0 / 15`) |
| `estimated_range_km_calc` | `333.333...` (`3.3333... * 100`) |
| `days_spanned_calc` | `1` |
| `consumed_pct` | `15.0` (`15 + 0`) |
| `flagged` | `false` |
| `missing_charging_type` | `NULL` |

**Expected `OdometerDeltaByDay(A, 42, 2026-08-10, 2026-08-10)`:**
`[]DayDistance{{Date: 2026-08-10, KmDriven: 50.0, OdometerKm: 1050.0}}` (clamp is a
no-op here — `50.0 >= 0`).

**Expected `ConsumedByDay(A, 42, 2026-08-10, 2026-08-10)`:**
`[]DayConsumption{{Date: 2026-08-10, ConsumedPct: 15.0, DistanceKm: 50.0,
Flagged: false, MissingChargingType: "", DaysSpanned: 1}}` — **identical** to what
today's live `deriveConsumedByDay` produces for this same fixture (characterization
parity, D10 of the roadmap).

### Fixture B — negative odometer clamp + flagged/missing-charge day

Two consecutive snapshots for `(accountID=A, teslaID=42)`:

| | `CapturedDate` | `OdometerKm` | `BatteryLevelPct` |
|---|---|---|---|
| predecessor | 2026-08-12 | 2000.0 | 40 |
| current | 2026-08-13 | 1998.0 | 85 |

(Odometer decreased by 2km — a clock-skew/read anomaly; battery level rose 45 points
with no matching charge event logged in either source — a missing-charge day.)

`effectiveDay(current) = 2026-08-12`.

**Expected `vehicle_metrics` row:**

| Column | Value |
|---|---|
| `distance_traveled_km_calc` | `-2.0` (`1998.0 - 2000.0`, stored **raw, unclamped**) |
| `battery_used_pct_calc` | `-45` (`40 - 85`) |
| `km_per_pct_calc` | `NULL` (divisor `-45 <= 0`) |
| `estimated_range_km_calc` | `NULL` (same guard) |
| `days_spanned_calc` | `1` |
| `consumed_pct` | `-45.0` (`-45 + 0`, no matched charge event) |
| `flagged` | `true` (`consumed_pct < 0`) |
| `missing_charging_type` | `'MANUAL'` (no Supercharger session matched in the interval at all → `inferMissingChargingType`'s existing "MANUAL otherwise" branch) |

**Expected `OdometerDeltaByDay`:** `KmDriven: 0.0` (clamp applied on read: `math.
Max(0, -2.0)`), `OdometerKm: 1998.0` — demonstrates the clamp moved from
`buildOdometerChart` to `analytics` (roadmap D5) produces the identical displayed
value (`0` km driven) the gateway shows today, satisfying D10.

**Expected `ConsumedByDay`:** `ConsumedPct: -45.0`, `Flagged: true`,
`MissingChargingType: telemetry.MissingChargingTypeManual`, `DistanceKm: -2.0`
(**unclamped** — `DayConsumption.DistanceKm` was never clamped by the gateway even
today; only the odometer chart's displayed delta is).

### Fixture C — a vehicle's true first-ever snapshot (no predecessor at all)

One `telemetry.Snapshot` for `(accountID=A, teslaID=42)`, with no earlier snapshot
existing for this vehicle in `telemetry` at all:

| | `CapturedDate` | `OdometerKm` | `BatteryLevelPct` | `BatteryRangeKm` |
|---|---|---|---|---|
| current (first-ever) | 2026-08-05 | 500.0 | 90 | 320.0 |

`Recalculate(A, 42, 2026-08-04, 2026-08-04)` fetches
`SnapshotsByVehicleBetween(2026-08-04, 2026-08-05)`, which returns exactly this one
row (nothing exists before it) — so it is `snapshots[0]` with no local `prev` (D10's
`i == 0` case). `effectiveDay(current) = 2026-08-04` → `metric_date`.

**Expected `vehicle_metrics` row** — a row IS written (dense table, D9):

| Column | Value |
|---|---|
| `metric_date` | `2026-08-04` |
| `battery_level_pct` | `90` |
| `odometer_km` | `500.0` |
| `battery_range_km` | `320.0` |
| `distance_traveled_km_calc` | `NULL` |
| `battery_used_pct_calc` | `NULL` |
| `km_per_pct_calc` | `NULL` |
| `estimated_range_km_calc` | `NULL` |
| `days_spanned_calc` | `NULL` |
| `consumed_pct` | `NULL` |
| `flagged` | `false` (NOT NULL — never `0`/`NULL` masquerading as unflagged; the D5/D5a
  comparison never runs for this row, D9) |
| `missing_charging_type` | `NULL` |

**Expected `OdometerDeltaByDay(A, 42, 2026-08-04, 2026-08-04)`:** `[]DayDistance{}`
(empty — the row exists but is filtered out by `distance_traveled_km_calc IS NOT
NULL`, D13).

**Expected `ConsumedByDay(A, 42, 2026-08-04, 2026-08-04)`:** `[]DayConsumption{}`
(empty — filtered out by `battery_used_pct_calc IS NOT NULL`, D13) — **identical**
to what today's live `deriveConsumedByDay` produces for this fixture: it also skips
a predecessor-less day entirely (D5a), so both methods' OUTPUT is unchanged by the
dense-table revision even though the underlying table now stores a row for this day
(characterization parity, roadmap D10). This is the concrete proof that the D9/D13
filter is not optional: an implementation that forgot it would return a
one-`DayDistance`/one-`DayConsumption`-element slice here instead of the empty one
today's gateway has always received, and the odometer/consumed charts would start
rendering a bar for a day they have never shown a bar for before.

**Direct-read confirmation (the property the dense revision exists to buy):** a
`SELECT * FROM vehicle_metrics WHERE account_id = A AND tesla_id = 42 AND metric_date
= '2026-08-04'` (a hypothetical future raw-observation consumer, not built by this
tier) DOES find this row, with `battery_level_pct = 90`, `odometer_km = 500.0`,
`battery_range_km = 320.0` populated — the exact property a sparse table would not
have had.

### `Reconcile` idempotence contract

Given Fixture A already persisted (one `vehicle_metrics` row,
`vehicle_metric_watermarks` row for `vehicle_snapshots` advanced to the predecessor
snapshot's `UpdatedAt`), a second `Reconcile(A, 42)` call with no new/changed source
rows: performs zero `vehicle_metrics` writes beyond the byte-identical UPSERT the
24h-overlap re-read produces (D4), and leaves every watermark row's
`source_updated_at` either unchanged or advanced (never regressed).

## Rollout note (informs tier 4, not a task of this tier)

Because `Recalculate`'s `_calc` columns are **copied from**, not derived independently
of, `telemetry.Snapshot`'s existing pointer fields, `vehicle_metrics` only reflects a
vehicle's full history once `Reconcile` has run at least once for every registered
vehicle (D7's backfill-on-first-run). This tier's tasks include invoking `Reconcile`
for every vehicle as part of verification (not a migration-time backfill — unlike
`vehicle_snapshots`' own dedupe migration, there is no SQL backfill here: the backfill
is `Reconcile`'s own first-run behavior, run once per vehicle by the same code path
that runs nightly thereafter). Tier 4 must not drop `vehicle_snapshots`' `_calc`
columns until this backfill has run for every vehicle in production — noted here for
tier 4's own design.md to cite, not a gate this tier's tasks must themselves enforce
beyond running it.

## Risks / Trade-offs

- **Constructor signature break.** `analytics.NewReader` gains a leading
  `*pgxpool.Pool` parameter. Both call sites (`cmd/web/main.go`, `cmd/poller/main.go`)
  update in this change — a compile error surfaces immediately if either is missed
  (no silent breakage possible).
- **Two new cross-module port methods land outside `internal/analytics`'s sandbox.**
  `telemetry.Reader.SnapshotsByVehicleUpdatedSince`,
  `telemetry.SuperchargerReader.SuperchargerSessionsByVehicleUpdatedSince`,
  `charging.Reader.ListEntriesByVehicleUpdatedSince` all require edits inside
  `internal/telemetry` and `internal/charging` — tasks.md assigns these to their own
  module workers (or the leader), never to the `analytics` worker, mirroring tier 1's
  Wave 2 cross-module pattern.
  `telemetry.Snapshot` also gains an `UpdatedAt` field exposing a DB column
  (`vehicle_snapshots.updated_at`) that already exists but was never mapped to the Go
  struct — additive, no existing field renamed or removed.
- **`Reconcile`'s coarse date-range widening (D8) could re-touch more days than
  strictly necessary** on a run following a very old, very stale watermark (e.g. a
  vehicle whose first `Reconcile` ever runs against a year of history) — bounded by
  the fact every UPSERT it performs is idempotent and this only ever happens once per
  vehicle (D7), not on every nightly run.
- **The dense-table revision (D9, design-gate change) makes `ConsumedByDay`'s and
  `OdometerDeltaByDay`'s D13 `IS NOT NULL` filter load-bearing for D10
  characterization parity, where the original sparse design needed no such filter
  at all** (a predecessor-less day simply had no row to accidentally return). An
  implementation that writes the dense row (D10/D11) but omits the D13 filter would
  compile, would not error, and would silently start showing the gateway a bar for
  a day it has never shown one for (Fixture C is the test contract's explicit proof
  this must not happen — Wave 4/Wave 6 tasks assert the empty-result case directly,
  not just the happy-path non-empty one). Mitigated by naming this exact failure
  mode in the Test Contract rather than leaving it implicit.
- **A UI reading raw observations alone (no delta needed)** now sees a row on a
  vehicle's first-ever day (the exact property the dense revision was chosen to
  provide) — no current consumer exercises this path (RecentEfficiency is
  unaffected/out of scope; the Battery chart still reads `telemetry.Reader`
  directly, unchanged), so this is recorded as an enabled-but-unused capability,
  not a risk.

## Verification signals

Per the Test-Execution-Policy: the assistant runs and reports `go build ./...`,
`go vet ./...`, `gofmt -l`, `make build`, `make vet`, `make bins`, the standalone
guards (`ui-guard`/`i18n-guard`/`money-guard` — `i18n-guard` is NOT a no-op here,
unlike tier 1: `charges.go`'s existing error strings are untouched, but verify no new
hardcoded string is introduced by the `Recalculate` call sites), and `sqlc generate` /
`make sqlc` after the migration + `query.sql` land. `openspec validate --changes
--strict` is run and reported. The owner alone runs `go test ./internal/analytics/...
./internal/telemetry/... ./internal/charging/... ./internal/gateway/... ./cmd/...` (or
the full suite) — until they do and report the result, this tier's implementation
status is **awaiting-user-verification**, never "done."
