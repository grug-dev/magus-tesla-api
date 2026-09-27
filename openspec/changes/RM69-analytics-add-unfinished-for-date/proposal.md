# RM69-analytics-add-unfinished-for-date

> Source: MAG-98 — https://linear.app/magus-monitor/issue/MAG-98/nightly-job
> Tier 3 of `openspec/roadmaps/RM69-nightly-retry-unfinished-vehicles.md`.

## Why

The roadmap's retry schedule (tier 2, `internal/app`) wakes up every 30
minutes and must ask one question: which registered vehicles still have no
`vehicle_metrics` row for yesterday? Tier 2's own `design.md` tried the
existing `analytics.Reader.LatestMetricsForVehicles` first and rejected it —
it takes `[]vehicleref.Ref`, and a `Ref` can only be built by
`vehicleref.Authorize`/`vehicleref.All`, both of which `make
vehicleref-guard` forbids calling outside `internal/vehicleref` itself. The
retry schedule has no signed-in user and no single account to check a `Ref`
against — it walks every registered vehicle across every account, the same
way `telemetry.Collector.CollectVehicles` (tier 1) already does in plain
`int64`. Looping a single-vehicle read instead was rejected too: two of
`Reader`'s single-vehicle methods filter out a day with no computable
predecessor, which would misreport a vehicle's first tracked day as "not
done", and the fourth (`BatteryLevelByDay`) has no such filter but would
still mean one query per vehicle every 30 minutes, against this project's
own "batch reads at the port level, never a per-entity loop" rule.

Roadmap decision D12 (added mid-roadmap, tier 3) settles this: a new,
narrow, batch-shaped read — plain `[]int64` in, plain `[]int64` out, one
query for the whole set.

**Amended by the leader:** this read lives on a new, narrow port,
`analytics.UnfinishedReader`, not on the existing `analytics.Reader`.
Reason: only `cmd/poller` ever calls it, and widening `Reader` would break
three gateway test doubles for a method the gateway never uses. See
`design.md` D1's "Amended by the leader" note for the full reasoning.

## What changes

- **New port, `UnfinishedReader`, with one method —
  `UnfinishedForDate`.** Given a set of `tesla_id`s and a calendar day,
  returns exactly the ids from that set with no `vehicle_metrics` row for
  that day. Declared as its own interface in `analytics.go`, with its own
  `NewUnfinishedReader` constructor — not added to the existing `Reader`
  port (leader amendment above).
- **New query, `UnfinishedVehicleIDsForDate`**, in
  `internal/analytics/db/query.sql` — one `unnest(...)` + `NOT EXISTS`
  statement for the whole input set, never a per-vehicle loop. Served by the
  existing `vehicle_metrics_tesla_date_unique` constraint
  (`UNIQUE (tesla_id, metric_date)`) — no new index, no migration.
- **`reader.go`** gains the store seam and the concrete implementation:
  an empty input returns an empty (non-nil) slice with **no query issued**;
  a duplicate id in the input appears at most once in the result; an id with
  no stored row at all — including one that is not registered to any
  account — counts as unfinished.
- **`query_log.go`**: `UnfinishedForDate` logs, alongside `ConsumedByDay` —
  both are methods the nightly poller itself calls, unlike the three
  dashboard-only methods that stay silent.
- **Docs**: `internal/analytics/AGENTS.md`'s port table gains the new
  method, and `kkpa/context/entities/vehicle-metrics/guide.md`'s `Reader`
  port list is updated to name it.

This change adds no caller. Tier 2 (`internal/app`) is the only place
`UnfinishedForDate` is actually invoked, through a small consumer-side
interface (`NotDoneVehicles`) that tier's own `design.md` already specifies
and that this tier's method now makes real.

## Breaking?

**No — additively.** `Reader` is unchanged: every existing method's
signature stays the same, and `Reader` gains no new method. The new method
lives on the new `UnfinishedReader` port instead (leader amendment above),
so none of `Reader`'s four out-of-module test fakes
(`internal/app/processor_test.go`,
`internal/gateway/handlers/handlers_test.go`,
`internal/gateway/handlers/history_test.go`,
`internal/gateway/handlers/external_charges_test.go`) need a stub — the
cross-module fallout `RM40-analytics-add-battery-level-read` hit the last
time `Reader` grew a method does not repeat here.

## Modules affected

- `internal/analytics/` — the only module whose production code changes.
- `internal/app/` — tier 2 is the only place a real caller of
  `UnfinishedReader.UnfinishedForDate` is wired in; untouched by this tier.

## Read paths affected

- **New read path: `UnfinishedForDate`.** Not yet called by any production
  code in this tier — tier 2's `RetryScheduler` is its first caller, on a
  30-minute timer. Read shape: `unnest(tesla_ids) NOT EXISTS (... WHERE
  tesla_id = t.tesla_id AND metric_date = date)`, served entirely by the
  existing `vehicle_metrics_tesla_date_unique` index — see `design.md`
  "Index Plan". No new index, no new write cost.
- **`ConsumedByDay` / `OdometerDeltaByDay` / `BatteryLevelByDay` /
  `LatestMetricsForVehicles` / `Recalculate` / `Reconcile`** — unaffected.
  None reads or writes the new query; no existing query, index, or write
  path is touched.

## Non-goals

- The retry schedule, the 30-minute timer, and `cmd/poller` wiring — tier 2
  (`internal/app`), already designed there and blocked only on this tier.
- Any UI page or alert about retries or failed cars.
- Changing the 03:30 nightly schedule or the manual rerun API.
- Backfilling days lost before this roadmap.
