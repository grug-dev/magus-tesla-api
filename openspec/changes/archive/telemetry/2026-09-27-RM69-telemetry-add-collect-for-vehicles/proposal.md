# RM69-telemetry-add-collect-for-vehicles

> Source: MAG-98 — https://linear.app/magus-monitor/issue/MAG-98/nightly-job
> Tier 1 of `openspec/roadmaps/RM69-nightly-retry-unfinished-vehicles.md`.

## Why

The nightly run starts at 03:30 local time and processes every registered
vehicle once. A car with no internet at that moment cannot wake up, so it
gets no snapshot that night. With no snapshot, `internal/analytics` writes no
`vehicle_metrics` row for that day, and the day is lost forever — the Tesla
Fleet API has no endpoint to fetch a past day's state.

The roadmap adds a retry: every 30 minutes, until the end of the local day,
the poller looks for cars still not done and reruns the nightly flow for
those cars only. This tier gives it the piece it needs: a way to run step 1
(fetch + store a snapshot, fetch Supercharger history) for a chosen set of
`tesla_id`s, instead of every registered vehicle.

## What changes

- **New `Collector` method — `CollectVehicles`.** Runs the same election
  (`electPollingVehicles`), the same per-account grouping, the same WakeUp
  flow, and writes the same `poll_attempts` rows as `CollectAll`, but only
  for the given `tesla_id`s. `CollectAll`'s own behavior does not change —
  both methods now share three private functions
  (`enumerateElected`, `collectAccount`, `collectChargingHistory`) so they
  cannot drift apart.
- **`collectAccount` gains a second vehicle-list parameter** — the account's
  full elected vehicle list, used only to resolve Supercharger session VINs.
  `CollectAll` passes the same slice twice, so its own behavior is unchanged.
  See `design.md` D4 for why the Supercharger fetch must see the account's
  full car list even on a retry.
- **New `TriggeredByRetry = "retry"` constant** on the existing `TriggeredBy`
  type. No migration: `poll_runs.triggered_by` and `poll_attempts.triggered_by`
  are plain `text` columns with no `CHECK` constraint — verified against the
  baseline migration's own `COMMENT ON COLUMN` text (`design.md` §Migration
  check).
- **Docs**: `internal/telemetry/AGENTS.md` gains the new port method and the
  `collectAccount` signature note. `kkpa/context/architecture/nightly-cycle.md`
  gets the new method in its Step 1 file list once tier 2 wires a caller
  (tracked as a tasks.md item here so the KB does not go stale mid-roadmap).

## Breaking?

No. `CollectVehicles` is additive to the `Collector` port. `collectAccount`'s
signature changes, but it is unexported — no caller outside this file exists.
`CollectAll`'s external behavior is unchanged; every existing `CollectAll`
test stays green with no test edit needed as proof.

## Modules affected

- `internal/telemetry` only. This tier adds no caller — `internal/app`
  (tier 2 of this roadmap) wires the retry schedule that calls
  `CollectVehicles`.

## Read paths affected

None. This is a write-path (`Collector` port) change only, on the nightly
batch side of the read/write split (`ai/architecture.md` §7). No `Reader`
method changes.

## Non-goals

- The retry schedule/loop itself, the "is this car done" check, and
  `cmd/poller` wiring — roadmap tier 2 (`internal/app`).
- Any UI page or alert about retries or failed cars.
- Changing the 03:30 nightly schedule or the manual rerun API.
- Backfilling days lost before this roadmap.
