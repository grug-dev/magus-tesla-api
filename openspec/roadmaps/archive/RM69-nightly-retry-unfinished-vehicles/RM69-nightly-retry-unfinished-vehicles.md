# RM69 — Retry vehicles the nightly run did not finish

Source ticket: MAG-98 — https://linear.app/magus-monitor/issue/MAG-98/nightly-job

The nightly run starts at 03:30 local time. It processes every registered vehicle. A car
with no internet at 03:30 cannot wake up, so it gets no snapshot that night. Without a
snapshot, analytics writes no `vehicle_metrics` row for that day. The day is lost.

This roadmap adds a retry. Every 30 minutes, until the end of the local day, the poller
looks for cars that are still not done. It runs the same nightly flow again, only for
those cars.

## Tier table

Status legend: `[ ]` pending (change not created) · `[~]` in progress (change exists, not archived) · `[x]` done (archived)

| Status | Change | Module | Scope | depends_on | Proposal prompt |
|---|---|---|---|---|---|
| `[x]` | `RM69-telemetry-add-collect-for-vehicles` | `internal/telemetry` | New Collector method that runs step 1 for a given set of `tesla_id`s only: same poll election, same per-account grouping, same WakeUp flow, same `poll_attempts` rows, and the account Supercharger history fetch for every account that owns a car in the set. New `TriggeredByRetry = "retry"` constant. No migration. `CollectAll` behaviour does not change. Module `AGENTS.md` / `README.md` and the KB updated. | — | Create the OpenSpec artifacts for `RM69-telemetry-add-collect-for-vehicles`. Binding decisions D1–D11 in this roadmap. Reuse `CollectAll`'s own code path: the new method and `CollectAll` must share `electPollingVehicles`, `collectAccount` and `collectChargingHistory` so they cannot drift. `CollectAll` must behave exactly as today. `poll_runs.triggered_by` and `poll_attempts.triggered_by` have no DB CHECK, so `retry` needs no migration — verify this and record it. design.md MUST author the expected values of the tests up front (D11). Tests: unit + DB integration. |
| `[x]` | `RM69-analytics-add-unfinished-for-date` | `internal/analytics` | New read method on the analytics public port: `UnfinishedForDate(ctx, teslaIDs []int64, date) ([]int64, error)` — returns the ids from the input that have NO `vehicle_metrics` row for `metric_date = date`. One query for the whole set, served by the existing UNIQUE index `(tesla_id, metric_date)`. No DB object changes. Module `AGENTS.md` / `README.md` and KB updated. | tier 1 | Create the OpenSpec artifacts for `RM69-analytics-add-unfinished-for-date`. Binding decisions D1–D12 in this roadmap. Plain `[]int64`, not `vehicleref.Ref`: the caller is a server-side job that spans every account, and `make vehicleref-guard` forbids building a Ref outside the gateway. One query, never a per-vehicle loop. Empty input returns an empty slice with no query. State in design.md that no DB object changes and that `vehicle_metrics_tesla_date_unique` serves the read. design.md MUST author the test expected values up front (D11). Tests: unit + DB integration. |
| `[x]` | `RM69-app-add-retry-unfinished-vehicles` | `internal/app` | A car-subset variant of the five-step cycle on `Processor`. A retry schedule in `scheduler.go`: every 30 min from 04:00 to the end of the local day. Each tick reads the done state from analytics, and calls the subset cycle only when at least one car is not done. `cmd/poller` wiring, sharing the `guardedProcessor` lock. Reads the done state through `analytics.UnfinishedForDate`. KB `kkpa/context/architecture/nightly-cycle.md`, `internal/app/AGENTS.md`, `cmd/README.md`, root `README.md` and `docs/0-set-up/deployment.md` (if a new env var is added) updated. | tier 1, tier 3 | Create the OpenSpec artifacts for `RM69-app-add-retry-unfinished-vehicles`. Binding decisions D1–D11 in this roadmap. Read the done state only through the analytics public port (`LatestMetricsForVehicles` or a narrow sibling in analytics — decide in design.md; a new analytics method is a separate module change and must be raised, not written). Keep the existing app rules: `Processor` has no `Scheduler` field, and `ProcessVehicleData` never consults a clock — the retry scheduler computes "today − 1" through `internal/clock` and passes cars in. The `cmd/poller` wiring is leader-owned integration code. design.md MUST author the expected values of the tests up front (D11). Tests: unit + DB integration. |

## Decisions (binding on every tier)

Settled with the user on 2026-09-27, in the comprehension interview, before any artifact
was written. Workers treat these as given and never re-open them.

- **D1 — "Done" is a result, not a failure reason.** A car is done for the night when
  `analytics.vehicle_metrics` has a row for it with `metric_date = local today − 1`.
  Every registered car that is not done gets a retry. The reason it failed does not
  matter: `asleep-timeout`, `api-error`, `unauthorized`, or a DB error in steps 2–5.
  The dev DB confirms one metrics row per snapshot day (68 of 68 for both cars).

- **D2 — Supercharger is fetched but is not part of "done".** A car that did not charge
  has no session, so no row proves the step ran. The retry still fetches the account's
  Supercharger history for every account that owns a retried car.

- **D3 — The retry runs the full nightly flow, for the not-done cars only.** Step 1
  (snapshot + Supercharger), step 2 (charging mirror), step 3 (analytics Reconcile and
  gaps), step 4 (monthly capacity) and step 5 (monthly metrics). Every step is limited
  to the retried cars.

- **D4 — Wake the car exactly like the nightly run.** WakeUp plus the
  `POLLER_WAKE_TIMEOUT` wait. The user chose this over a free state check. Cost per day:
  at most `F × 40` WakeUps, where `F` is the number of cars not done after 03:30. On a
  normal night `F = 0`, and the retry makes **zero** Tesla calls. The done check is a
  DB read only.

- **D5 — Window: every 30 minutes, from 04:00 until the end of the local day.** Local
  means `POLLER_TIMEZONE`, read through `internal/clock`. After midnight the next
  nightly run takes over. Whether the interval and window are env vars or constants is
  decided in the app tier's design.md, with 30 min / 04:00 / end of day as defaults.

- **D6 — The loop lives in `internal/app`, inside `cmd/poller`.** It is a second
  schedule next to the nightly `Scheduler`, in the same process. No new container, no
  host cron, no new module.

- **D7 — One lock for every cycle.** The retry uses the same `guardedProcessor` mutex as
  the nightly tick and the manual rerun API. A tick that finds the lock busy is skipped;
  the next tick tries again in 30 minutes.

- **D8 — A retry that runs is recorded as `triggered_by = retry`.** It writes one
  `poll_runs` row and the usual `poll_attempts` rows. A tick that finds no not-done car
  calls nothing and writes nothing, so a normal day adds no empty rows.

- **D9 — Same account as the nightly run.** A car registered to several accounts is
  fetched through the account the poll election picks, exactly as `CollectAll` does.

- **D10 — A late snapshot is accepted as it is.** A snapshot taken at 14:00 gets
  `captured_date = today`, so `metric_date = today − 1`, like a 03:30 one. It includes
  today's driving until 14:00. The daily split moves a little; the totals stay correct.
  No change to the date rules.

- **D11 — Tests: unit + DB integration.** The user runs them. Each design.md authors the
  expected values before implementation. The pure window math and the not-done selection
  are unit tests in an early wave. DB-backed tests go in the final wave.

- **D12 — The done check gets its own analytics read method.** Added mid-roadmap as tier 3,
  on 2026-09-27, by the leader on autopilot. `LatestMetricsForVehicles` takes
  `[]vehicleref.Ref`, and `make vehicleref-guard` forbids building a Ref outside the gateway.
  The new `analytics.UnfinishedForDate` takes plain `[]int64` and runs one query. The
  existing UNIQUE index `(tesla_id, metric_date)` serves it, so no DB object changes.
  Rejected: building Refs in `cmd/poller` (the guard does not scan `cmd/`). A Ref proves one
  user may see a car; this job has no user.

## Out of scope

- Any UI page or alert about retries or failed cars.
- Changing the 03:30 nightly schedule.
- Changing the manual rerun API.
- Backfilling days that were lost before this roadmap.
