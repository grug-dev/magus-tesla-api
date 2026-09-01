# RM36 — Poll Run Tracking

Source ticket: MAG-35 — https://linear.app/magus-monitor/issue/MAG-35/poll-attemps-tracking

## Intention

Every poller invocation — nightly **and** `--once` — persists one `poll_runs` row recording
what that run did: how long it took, how many accounts and vehicles it attempted and with
what outcomes, how many Supercharger sessions it upserted, and **how many Tesla Fleet API
requests it spent**. The API-call count is the point: it is the input to the cost figure
the ticket asks for, and today nothing records it.

The run summary the poller already prints to stdout becomes a row you can query.

## Findings that shaped this roadmap (read before touching anything)

The ticket asks "are those counts per account or in total for the runner?". Answering it
first is what shaped every decision below.

| Counter in the log line | What it actually counts |
|---|---|
| `attempted` | **vehicles**, summed across all accounts — not accounts |
| `succeeded` | vehicles with a stored snapshot (`ReasonOK`) |
| `failures={…}` | vehicle failures keyed by `Reason` (closed set of 3) |
| `charging_upserted` | Supercharger sessions, summed across all accounts |
| `charging_failures` | **accounts** whose `ChargingHistory` call failed |
| `config_capture_failures` | failed `vehicle_config` write-backs |

So the line mixes vehicle-grain and account-grain numbers under one heading, and
**account attempt/success counts do not exist at all**. That ambiguity is what prompted
the ticket.

Three more facts the design rests on:

| Fact | Where | Consequence |
|---|---|---|
| `poll_attempts` already exists, grain **one row per (vehicle, run)**, carrying `run_id` + `triggered_by` since RM29 tier 7 | `internal/telemetry/db/migrations/20260710000002`, `…20260823000002` | Vehicle-level outcomes are already persisted. The new facts are run-grain and do not fit this table. |
| Migration `20260823000002` states outright: *"no separate run-level table is added; a caller wanting run-level facts computes them with GROUP BY run_id"* (RM29 D2) | same file | **This roadmap deliberately reopens that decision.** See D1. |
| `internal/app` declares **Data ownership: None** and forbids importing `pgxpool` **and** `internal/tesla` | `internal/app/AGENTS.md` | The run table cannot live in `app`, and `app` cannot count Tesla calls. Both constraints are load-bearing below. |
| Every Fleet API call in a poller run happens inside `telemetry.CollectAll` | `internal/telemetry/service.go`; `app` cannot import `tesla` | The API-call count never has to cross a module boundary — it rides `CycleReport`, which `telemetry` already owns. |

## Decisions (binding — settled with the user before any artifact was written)

**D1 — New `poll_runs` table in `internal/telemetry`, one row per `run_id`.** `poll_attempts`
keeps its per-vehicle grain untouched. `internal/app` writes the row through a new
`telemetry.RunWriter` port — the same shape `app` already uses for `charging.SessionWriter`
and `analytics.GapWriter`, so `app` gains no pool, no query and no table.

This reopens RM29 D2 knowingly. D2's reasoning ("compute run-level facts with `GROUP BY
run_id`") holds only for facts that *are* in `poll_attempts`. Duration, API-call count and
the charging counters are not, and never can be: a run whose vehicle enumeration fails
writes **zero** `poll_attempts` rows, so a failed run would leave no trace at all.

Rejected — *a new `internal/polling` module*: a module folder, `AGENTS.md`, `db/`, sqlc
entry and architecture-doc updates for one table. `CLAUDE.md` names that over-abstraction,
and `telemetry` already owns `poll_attempts`, `RunContext`, `TriggeredBy` and `CycleReport`.
Rejected — *extending `poll_attempts`*: run facts would repeat on every vehicle row, and a
zero-vehicle run would still record nothing.

**D2 — The Tesla API call count is maintained inside `internal/telemetry`. `internal/tesla`
is not modified.** `telemetry` wraps the `tesla.VehicleService` it holds in a small
counting decorator (internal to `telemetry`), so the increments live in **one** place
rather than scattered at each call site. The total surfaces as a new `CycleReport` field.

Rejected — *a `ctx`-scoped counter inside `tesla.do()`*: it counts every request
automatically, including the wake-retry loop, but changes the adapter for a caller-specific
concern. Rejected — *a counting `http.RoundTripper`*: needs a new `tesla` constructor and a
per-run `Client`, undoing the deliberately shared, stateless `Client`.

*Known accepted risk:* counting outside the HTTP layer can drift if a future call path
bypasses the decorator. The decorator (rather than loose `count++` lines) is the mitigation;
the reviewer checks that every `tesla` call in `telemetry` goes through it.

**D3 — Duration is recorded at run level only.** `poll_runs` carries `started_at`,
`finished_at`, `duration_seconds` for the whole cycle — steps 1, 2 and 3. `poll_attempts`
gains **no** duration column.

Rejected — *also per vehicle*: genuinely useful (the wake wait is the dominant, most
variable cost and it is per car) but out of the ticket's literal scope. Deferred to the
backlog, not discarded. Rejected — *per vehicle only, summing for the run*: the sum omits
steps 2 and 3 and every gap between vehicles, so the derived run duration is simply wrong.

**D4 — Account-level counts are added, with definitions taken from existing code.**
`accounts_attempted` = accounts in the run; `accounts_failed` = accounts that hit one of
the **two whole-account short-circuits already implemented** in `collectAccount`
(`AccessTokenFor` failed, or the up-front `ListVehicles` returned `ErrUnauthorized`);
`accounts_succeeded` = attempted − failed. No new failure semantics are invented.

**D5 — The vehicle counts are denormalized into `poll_runs` as typed columns.**
`vehicles_attempted`, `vehicles_succeeded`, and one column per `Reason`
(`failures_asleep_timeout`, `failures_unauthorized`, `failures_api_error`) — the `Reason`
set is closed in Go, so typed columns cost nothing in flexibility and read faster than
`jsonb`. One `poll_runs` row therefore reproduces the whole log line without a join.

Justified by the declared **Performance-Profile** (read-heavy; denormalize and precompute
for reads, writes run at midnight). `poll_attempts` remains the per-vehicle source of truth;
`poll_runs` is the precomputed run summary.

Rejected — *derive the vehicle counts with `GROUP BY run_id`*: correct normalization, but
every run-summary read then pays a join + aggregate against the declared profile.

**D6 — Both entry points are covered by construction, not by duplicated code.**
`cmd/poller` (nightly) and `cmd/poller --once` both call `app.Processor.ProcessVehicleData`.
Writing the row inside that method covers both paths and makes divergence impossible —
the same argument RM29 tier 7 used to move the cycle out of `cmd/`.

**D7 — The log line is disambiguated in the same change.** `telemetry.LogCycle` renames its
vehicle-grain counters (`attempted` → `vehicles_attempted`, `succeeded` →
`vehicles_succeeded`) and prints the new account counts, API-call count and duration. The
ticket's confusion came from the log line; leaving it unchanged would leave the reported
problem in place.

## Tiers

Legend: `[ ]` pending (change not yet created) · `[~]` in progress (change exists, not archived) · `[x]` done (archived)

| Status | Change | Module | Scope | depends_on | Proposal prompt |
|---|---|---|---|---|---|
| `[x]` | `RM36-telemetry-add-poll-runs` | `telemetry` | Migration creating `poll_runs` (D1/D3/D4/D5) + sqlc queries. New `PollRun` domain type and `RunWriter` port (`RecordRun`). New `CycleReport` fields: `TeslaAPICalls`, `AccountsAttempted`, `AccountsSucceeded`, `AccountsFailed`. Counting decorator around `tesla.VehicleService` inside `telemetry` (D2). Account counters incremented at the two existing whole-account short-circuits in `collectAccount` (D4). `LogCycle` updated per D7. `internal/telemetry/AGENTS.md` Data Ownership updated to list `poll_runs`. | — | Add the `poll_runs` table, the `RunWriter` port and the API-call counting per RM36 D1–D5, D7. Do NOT touch `internal/app` or `internal/tesla`. |
| `[x]` | `RM36-app-record-poll-run` | `app` | `ProcessVehicleData` measures the run (start before step 1, end after step 3) and calls `telemetry.RunWriter.RecordRun` with the `RunContext` + `CycleReport` + timing — including on the step-1 whole-cycle-failure path, so a failed run still records a row (D1's core justification). `NewProcessor` gains a `RunWriter` parameter; `cmd/poller` wiring updated (granted path). `internal/app/AGENTS.md` updated: the module still owns no data — it calls a port. | 1 | Record the poll run per RM36 D1/D6. `app` gains NO pool and NO table — it calls the `telemetry.RunWriter` port, exactly as it already calls `charging.SessionWriter`. |

## Future work

Recorded in `openspec/roadmaps/backlog.md` — that file is the durable record:

- **telemetry — per-vehicle poll duration** (deferred from D3).
- **gateway — a poll-runs read surface** (nothing reads `poll_runs` in this roadmap).
