# Telemetry as the data hub — consumer map — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.

## Glossary

- **Known as:** `telemetry module`, `vehicle snapshots`, `nightly collection`, `telemetry hub`, `who reads telemetry`, `poll run`, `run summary`
- **Internal name:** `internal/telemetry` — ports `telemetry.Reader`, `telemetry.SuperchargerReader` (reads), `telemetry.Collector` (write), `telemetry.RunWriter` (run summary write) — tables `vehicle_snapshots`, `supercharger_sessions`, `poll_attempts`, `poll_runs`

## Component map

Files involved, grouped by layer. Each row: the file's role in this concept.

### The owning module — internal/telemetry

| File | Role |
|---|---|
| `internal/telemetry/telemetry.go` | Package doc (the purpose statement) + public ports: `Reader` (snapshot reads), `SuperchargerReader` (session reads), `Collector` (nightly write). Domain types (`Snapshot`, `SuperchargerSession`, `CycleReport`, `RunContext`) — no vendor suffix. |
| `internal/telemetry/service.go` | `NewService(pool, acct, tsla, cfg) Collector` — the ONLY writer (nightly collection, wake logic, upserts incl. `UpsertSuperchargerSession`). |
| `internal/telemetry/reader.go` | `NewReader(pool)` / `NewSuperchargerReader(pool)` read impls; pgtype→domain mapping stays here. |
| `internal/telemetry/db/queries.sql` → `query.sql.go` | sqlc source of truth; every read method the ports expose has its query here. |
| `internal/telemetry/wake.go`, `report.go`, `scheduler.go`(relocated) | Collection support: vehicle wake, cycle reporting. The scheduler now lives in `internal/app/scheduler.go`. |

### Consumers — who reads telemetry data

| File | Port calls | Use case |
|---|---|---|
| `internal/gateway/gateway.go` + `handlers/handlers.go` | `Deps.TelemetryReader` (`telemetry.Reader`) | Wiring: `cmd/web/main.go` injects `telemetry.NewReader(pool)`. |
| `internal/gateway/handlers/handlers.go` (`navHeaderFor`) | `LatestSnapshotsByAccount` | Nav-header battery %/status + dashboard vehicle cards. |
| `internal/gateway/handlers/history.go` | `SnapshotsByVehicleBetween` | Dashboard history fragment — battery-level chart (odometer chart moved to analytics, RM29 D5). |
| `internal/gateway/handlers/charges.go` (`buildChargesPage`) | `LatestSnapshotsByAccount` | Create-form `start_battery_pct` suggestion. |
| ~~gateway supercharger page~~ (REMOVED by RM30/MAG-30) | — | `/supercharger-stats` now reads the charging mirror via `charging.SessionReader` (`charge_sessions`) — see `workflows/supercharger-stats-read.md`. |
| `internal/analytics/analytics.go` + `reader.go` | `SnapshotsByVehicleSince` | Derived metrics: `ConsumedByDay`, `OdometerDeltaByDay` over `vehicle_metrics`. Telemetry supplies **snapshots only** — `NewReader`'s `supercharger` argument is `charging.SuperchargerSessionAnalyticsReader`, not a telemetry port (**changed by RM31**). |
| `internal/analytics/recalculate.go` | `SnapshotPrecedingDay`, `SnapshotsByVehicleUpdatedSince`, `SnapshotsByVehicleBetween` | `Recalculator` re-derives `vehicle_metrics` rows (nightly + after manual-charge writes — see `entities/vehicle-metrics/guide.md`). **Snapshot reads only since RM31** — its Supercharger source moved to `charging.SuperchargerSessionAnalyticsReader` over `charge_sessions`. |
| `internal/app/processor.go` | `SuperchargerSessionsByAccount` (limit 0 = every session) | Step 2 of `ProcessVehicleData`: mirrors Supercharger sessions into `charging.charge_sessions` via `charging.SessionWriter` — what the Supercharger Stats page reads (RM30) **and, since RM31, what `internal/analytics` derives from**. This is now the **only remaining caller of `telemetry.SuperchargerReader` repo-wide**. See `architecture/nightly-cycle.md`. |

### Driving adapters (the write side's callers)

| File | Role |
|---|---|
| `internal/app/scheduler.go` | Daily timer; calls `Processor.ProcessVehicleData` (the scheduler is a peer adapter, NOT inside the Processor). |
| `cmd/poller/main.go` | Constructs `telemetry.NewService(...)`, `app.NewScheduler(...)`; thin composition only. |
| `cmd/web/main.go` | Injects the two telemetry readers into `gateway.Deps` (plus fresh copies into analytics' reader/recalculator constructors). |

## How maintenance works

- **Add a new telemetry read consumer:** construct `telemetry.NewReader(pool)` / `NewSuperchargerReader(pool)` in the consumer's composition root (`cmd/web/main.go` or the module's constructor), accept the PORT interface in `Deps`/constructor — never import `internal/telemetry/db`. Gateway handlers go through `resolveSelectedVehicle` for per-vehicle reads.
- **Add a new read method:** `internal/telemetry/db/queries.sql` → `make sqlc` → implement on `Reader`/`SuperchargerReader` in `reader.go` + declare in `telemetry.go`. Bounded windows follow the platform `?start=&end=` convention (see `SuperchargerSessionsByVehicleBetween`).
- **Add a new collected field:** capture path only — `telemetry.Collector`/`service.go` + `db/queries.sql` (+ migration). Units convert exactly once at capture time (display units, RM7 D1/D3); never add read-time conversion.
- **Change the nightly cycle:** `internal/app/processor.go` (`ProcessVehicleData` 3-step flow) — never re-add orchestration to `cmd/poller`. Full step/port/table map: `architecture/nightly-cycle.md`.

- **Record a new run-level fact:** add the field to `telemetry.CycleReport` (populated inside `CollectAll`), add the column to `poll_runs` via a migration, extend `telemetry.PollRun` + the `InsertPollRun` query, and map it in `RunWriter.RecordRun`. The caller in `internal/app` passes the whole `CycleReport` — it gains no pool and no table.
- **Read `poll_runs`:** there is **no read port yet**. Direct SQL is the only way to see a row today; adding a `Reader`-style method is deferred backlog work, not an existing surface.

## Conventions & gotchas

- **One writer, many readers.** Only `telemetry.Collector` (via `NewService`) writes; every other module reads through `Reader`/`SuperchargerReader`. No user-facing request ever writes telemetry. _Source: `internal/telemetry/telemetry.go` package doc; `internal/telemetry/AGENTS.md`._
- **Reads are hot-path — keep them indexed and bounded.** The workload profile is read-heavy (dashboards) vs one nightly write batch; every new read method must be bounded (limit or `[start,end]` window). _Source: `ai/architecture.md` §7, `ai/go-conventions.md` §"Read optimization"._
- **NEVER import `internal/telemetry/db` outside the module** — consumers take the port interface; pgtype never escapes. _Source: `internal/gateway/AGENTS.md`, `internal/app/AGENTS.md` → allowed imports._
- **`EffectiveDate` vs `CapturedDate` vs `CapturedAt`** — three distinct time fields on `Snapshot` (the day the data describes / dedupe-UNIQUE day / precise read instant); mixing them up is the classic bug. `EffectiveDate` is read-derived, never persisted. _Source: `internal/telemetry/telemetry.go` `Snapshot` doc._
- **Same-day captures dedupe** — `UNIQUE (account_id, tesla_id, captured_date)`, latest wins; repeated same-day collection is not duplicate data. _Source: `telemetry-dedupe-daily-snapshots` design D1/D2._
- **The scheduler lives in `internal/app`, not telemetry** (RM29 RD8) — `Processor` never consults a clock; `Scheduler` is a peer adapter holding a `Processor`. _Source: `internal/app/AGENTS.md`._
- **`internal/app` owns NO data** — `poll_attempts` (incl. `run_id`/`triggered_by`) stays telemetry's. _Source: `internal/app/AGENTS.md` → Data Ownership._

- **A poll run is recorded exactly once, and a duplicate is an error — never an upsert.** A second summary for a run identity that already has one is rejected and leaves the first record unchanged; recording a run twice is never a legitimate outcome. _Source: spec telemetry — Requirement: Run-Level Poll Summary Storage._
- **A run that fails before touching a single vehicle still records a summary.** Its account and vehicle counts are all zero, but its start/finish times and duration are real. This is the whole point of the table: before it, a failed run left no trace at all, because zero `poll_attempts` rows were written. _Source: spec telemetry — Requirement: Run-Level Poll Summary Storage._
- **Every Tesla Fleet API request counts, including the ones that fail.** A rejected request still consumes a request against the vendor's quota, so the counter increments before the error is checked — never after. _Source: spec telemetry — Requirement: Tesla API Call Counting._
- **"Whole-account failure" has exactly two causes.** An account counts as failed only when it cannot obtain a usable Tesla access token, or when its account-wide vehicle-list request is rejected as unauthorized. No other failure mode marks an account failed — per-vehicle failures never do. _Source: spec telemetry — Requirement: Account-Level Attempt And Outcome Counts._
- **Succeeded accounts is derived, not counted:** attempted minus failed. Do not increment it independently or the two will drift. _Source: spec telemetry — Requirement: Account-Level Attempt And Outcome Counts._
- **The cycle log line mixes two grains, so every label says which.** Vehicle-grain counts are labelled as vehicles (`vehicles_attempted`/`vehicles_succeeded`); account-grain counts and the Tesla API call count are reported alongside them. The unlabelled `attempted`/`succeeded` pair was the exact ambiguity MAG-35 was filed about. _Source: spec telemetry — Requirement: Nightly Cycle Log Summary._

## Related KB

- Features: (none yet)
- Workflows: `workflows/manual-charge-crud.md` (analytics recalc after writes), `workflows/supercharger-stats-read.md` (read-only page over the `charge_sessions` mirror)
- Architecture: `architecture/nightly-cycle.md` (the 3-step `ProcessVehicleData` cycle that drives telemetry's write path)
