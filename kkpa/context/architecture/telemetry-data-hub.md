# Telemetry as the data hub — consumer map — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.

## Glossary

- **Known as:** `telemetry module`, `vehicle snapshots`, `nightly collection`, `telemetry hub`, `who reads telemetry`
- **Internal name:** `internal/telemetry` — ports `telemetry.Reader`, `telemetry.SuperchargerReader` (reads), `telemetry.Collector` (write) — tables `vehicle_snapshots`, `supercharger_sessions`, `poll_attempts`

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

## Conventions & gotchas

- **One writer, many readers.** Only `telemetry.Collector` (via `NewService`) writes; every other module reads through `Reader`/`SuperchargerReader`. No user-facing request ever writes telemetry. _Source: `internal/telemetry/telemetry.go` package doc; `internal/telemetry/AGENTS.md`._
- **Reads are hot-path — keep them indexed and bounded.** The workload profile is read-heavy (dashboards) vs one nightly write batch; every new read method must be bounded (limit or `[start,end]` window). _Source: `ai/architecture.md` §7, `ai/go-conventions.md` §"Read optimization"._
- **NEVER import `internal/telemetry/db` outside the module** — consumers take the port interface; pgtype never escapes. _Source: `internal/gateway/AGENTS.md`, `internal/app/AGENTS.md` → allowed imports._
- **`EffectiveDate` vs `CapturedDate` vs `CapturedAt`** — three distinct time fields on `Snapshot` (the day the data describes / dedupe-UNIQUE day / precise read instant); mixing them up is the classic bug. `EffectiveDate` is read-derived, never persisted. _Source: `internal/telemetry/telemetry.go` `Snapshot` doc._
- **Same-day captures dedupe** — `UNIQUE (account_id, tesla_id, captured_date)`, latest wins; repeated same-day collection is not duplicate data. _Source: `telemetry-dedupe-daily-snapshots` design D1/D2._
- **The scheduler lives in `internal/app`, not telemetry** (RM29 RD8) — `Processor` never consults a clock; `Scheduler` is a peer adapter holding a `Processor`. _Source: `internal/app/AGENTS.md`._
- **`internal/app` owns NO data** — `poll_attempts` (incl. `run_id`/`triggered_by`) stays telemetry's. _Source: `internal/app/AGENTS.md` → Data Ownership._

## Related KB

- Features: (none yet)
- Workflows: `workflows/manual-charge-crud.md` (analytics recalc after writes), `workflows/supercharger-stats-read.md` (read-only page over the `charge_sessions` mirror)
- Architecture: `architecture/nightly-cycle.md` (the 3-step `ProcessVehicleData` cycle that drives telemetry's write path)
