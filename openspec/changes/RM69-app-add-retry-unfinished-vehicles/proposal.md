# RM69-app-add-retry-unfinished-vehicles

> Source: MAG-98 — https://linear.app/magus-monitor/issue/MAG-98/nightly-job
> Tier 2 of `openspec/roadmaps/RM69-nightly-retry-unfinished-vehicles.md`.

## Why

The nightly run starts at 03:30 local time and processes every registered vehicle
once. A car with no internet at that moment gets no snapshot, and with no
snapshot `internal/analytics` writes no `vehicle_metrics` row for that day. Tier 1
(`RM69-telemetry-add-collect-for-vehicles`, archived) gave `internal/telemetry` a
way to run step 1 for a chosen set of vehicles instead of every registered one.

This tier uses that new capability. It adds a second schedule inside
`internal/app`: every 30 minutes, from 04:00 until the end of the local day, it
finds the vehicles that still have no `vehicle_metrics` row for yesterday and
runs the same five-step nightly cycle again, scoped to those vehicles only.

## What changes

- **New `Processor` method — `ProcessVehicleDataForVehicles`.** Runs the same
  five steps as `ProcessVehicleData`, scoped to a caller-supplied set of
  `tesla_id`s: step 1 through `telemetry.Collector.CollectVehicles` (tier 1),
  steps 2/3/5 filtered to that set, step 4 (when its own monthly gate fires)
  called once per vehicle in the set. `ProcessVehicleData`'s own behavior does
  not change — both methods share one filtering helper so they cannot drift.
- **New `RetryScheduler` in `internal/app`.** A second, independent schedule
  next to the nightly `Scheduler`. Every 30 minutes from 04:00 to the end of
  the local day it asks "which registered vehicles are not done for
  yesterday", and calls `ProcessVehicleDataForVehicles` only when that set is
  non-empty. `nextRetryTick` is a new pure function, mirroring `nextRun`.
- **A new, small consumer-side interface, declared in `internal/app`**, for
  asking "which of these vehicles are not done yet" — see `design.md`'s
  Blocker section for why this cannot be `analytics.Reader.LatestMetricsForVehicles`
  and what still needs to be built before this tier compiles end to end.
- **`cmd/poller` wiring (leader-owned, separate task group)** — starts
  `RetryScheduler` next to the nightly `Scheduler`, and widens `guardedProcessor`
  so the new method shares the same lock as the nightly tick and the manual
  rerun API.
- **Docs**: `internal/app/AGENTS.md`, `cmd/README.md`, the root `README.md`, and
  `kkpa/context/architecture/nightly-cycle.md` gain the retry path.

## Breaking?

No. `ProcessVehicleDataForVehicles` is additive to the `Processor` port.
`ProcessVehicleData`'s external behavior is unchanged — its existing tests stay
green with no edits needed, proving the shared-helper refactor is
behavior-preserving.

## Modules affected

- `internal/app` (this tier).
- `cmd/poller` — wiring only, no new business logic (leader-owned task group,
  see `tasks.md`).
- `internal/analytics` — NOT touched by this tier's own tasks, but this
  proposal depends on a small new read capability there that does not yet
  exist. See `design.md`'s Blocker section. This is reported to the leader,
  not designed or implemented here.

## Read paths affected

None on the gateway's hot dashboard path. The new capability this tier depends
on in `internal/analytics` (see Blocker) is itself a small, cheap, indexed
existence check over `vehicle_metrics`, called at most once every 30 minutes
by the poller — never by a user-facing request.

## Non-goals

- Any UI page or alert about retries or failed cars.
- Changing the 03:30 nightly schedule.
- Changing the manual rerun API.
- Backfilling days that were lost before this roadmap.
- Designing or implementing the new `internal/analytics` read capability the
  retry's "who is not done" check depends on — see `design.md`'s Blocker
  section. That is a separate module change.
