# Telemetry is ingest-only — consumer map — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.

> **RESOLVED — RM39 finished both renames.** `charging.charge_sessions` →
> `charging.supercharger_sessions` (D5b) landed in RM39 tier 3 (`charging-move-to-own-schema`,
> MAG-31). `telemetry.supercharger_sessions` → `telemetry.supercharger_history` (D5a) landed in
> RM39 tier 4 (`telemetry-move-to-own-schema`), which also moved this module's four tables out
> of `public` into schema `telemetry`. Roadmap D25 retired the "separate blocked boundary
> ticket" (D6) this banner used to point at — no such ticket ever existed.
>
> **The two tables no longer share a base name**, so a bare `supercharger_sessions` below is
> always **charging's** mirror; telemetry's is `telemetry.supercharger_history`. One thing is
> deliberately still half-renamed: the port `telemetry.SuperchargerReader` and its four
> `SuperchargerSessions*` methods keep their old names until RM39 tier 5. That is designed —
> do not "fix" it. See `openspec/roadmaps/RM39-schema-per-module.md`.

## What this module is (read this before the map)

`internal/telemetry` **fetches Tesla Fleet API data and writes what it fetched. Nothing more.**
It is a *source*, not a hub — it holds no read model, serves no page, and answers no
user-facing request. Anything the app shows a user comes from a module that mirrors or derives
telemetry's rows, never from telemetry itself.

| Module | Role | Who may read it |
|---|---|---|
| `telemetry` | **Ingest only.** Nightly Fleet API collection → schema `telemetry`: `vehicle_snapshots`, `supercharger_history`, `poll_attempts`, `poll_runs`. | `internal/analytics` (snapshots only) and `internal/app` (the Supercharger mirror step). **Never `internal/gateway`.** |
| `charging` | Mirrors telemetry's Supercharger rows into `supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3), **and originates** `manual_charge_entries` (a human types those). Part mirror, part owner — not a pure mirror. | gateway, analytics |
| `analytics` | Derived read model. Recomputes `vehicle_metrics` from three independent watermark sources (`vehicle_snapshots`, `supercharger_history`, `manual_charge_entries` — `supercharger_sessions` reused from before RM31 by `RM39-analytics-fix-watermark-vocabulary`, roadmap tier 3b; see that change's `design.md` §6). | gateway |

The gateway therefore reads **`charging` and `analytics` only**. That is enforced, not merely
documented — see the boundary gotcha below.

## Glossary

- **Known as:** `telemetry module`, `vehicle snapshots`, `nightly collection`, `who reads telemetry`, `telemetry vs analytics`, `can the gateway read telemetry`, `ingest module`, `poll run`, `run summary`
- **Internal name:** `internal/telemetry` — ports `telemetry.Reader`, `telemetry.SuperchargerReader` (reads), `telemetry.Collector` (write), `telemetry.RunWriter` (run summary write) — tables (all in schema `telemetry`) `vehicle_snapshots`, `supercharger_history`, `poll_attempts`, `poll_runs`

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
| ~~`internal/gateway/**`~~ — **NO LONGER A CONSUMER, and now forbidden** | — | The gateway's four snapshot call sites were repointed onto `analytics.Reader` by **RM38** (`LatestMetricsByAccount` — dashboard, vehicle cards, nav header, charges battery suggestion) and **RM40** (`BatteryLevelByDay` — the history battery chart). The supercharger page had already moved to `charging.SessionReader` in RM30. `make boundary-guard` now fails the build on any `internal/telemetry` import under `internal/gateway/`, with **zero** `// boundary:allow:` escape hatches. See the boundary gotcha below. |
| `internal/analytics/analytics.go` + `reader.go` | `SnapshotsByVehicleSince` | Derived metrics: `ConsumedByDay`, `OdometerDeltaByDay` over `vehicle_metrics`. Telemetry supplies **snapshots only** — `NewReader`'s `supercharger` argument is `charging.SuperchargerSessionAnalyticsReader`, not a telemetry port (**changed by RM31**). Verified: no file under `internal/analytics/` names `telemetry.SuperchargerReader`. |
| `internal/analytics/recalculate.go` | `SnapshotPrecedingDay`, `SnapshotsByVehicleUpdatedSince`, `SnapshotsByVehicleBetween` | `Recalculator` re-derives `vehicle_metrics` rows (nightly + after manual-charge writes — see `entities/vehicle-metrics/guide.md`). **Snapshot reads only since RM31** — its Supercharger source moved to `charging.SuperchargerSessionAnalyticsReader` over `charging.supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3). |
| `internal/app/processor.go` | `SuperchargerSessionsByAccount` (limit 0 = every session) | Step 2 of `ProcessVehicleData`: mirrors Supercharger sessions into `charging.supercharger_sessions` (renamed from `charging.charge_sessions`, RM39 tier 3) via `charging.SessionWriter` — what the Supercharger Stats page reads (RM30) **and, since RM31, what `internal/analytics` derives from**. This is now the **only remaining caller of `telemetry.SuperchargerReader` repo-wide**. See `architecture/nightly-cycle.md`. |

### Driving adapters (the write side's callers)

| File | Role |
|---|---|
| `internal/app/scheduler.go` | Daily timer; calls `Processor.ProcessVehicleData` (the scheduler is a peer adapter, NOT inside the Processor). |
| `cmd/poller/main.go` | Constructs `telemetry.NewService(...)`, `app.NewScheduler(...)`; thin composition only. |
| `cmd/web/main.go` | Builds **one** `telemetry.NewReader(pool)` and passes it to `analytics.NewReader` / `analytics.NewRecalculator` — **never into `gateway.Deps`**. Its own comment at the call site says "gateway never calls that". |

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
- **The gateway may not depend on `telemetry` AT ALL — not even `telemetry.Reader`.** Stronger than the usual "ports only" rule: the whole module is outside the gateway's vocabulary, and `telemetry.*` types must not appear in gateway code. `make boundary-guard` enforces it repo-wide — it **fails** on a non-test file, warns on a `_test.go` — and the gateway carries **zero** `// boundary:allow:` escape hatches. A new `internal/telemetry` import in the gateway is a **regression, not known debt**. Route the read through `analytics` or `charging` instead. _Source: `ai/architecture.md` §"Exception: the gateway may not depend on `telemetry` at all"; `internal/gateway/AGENTS.md`._
- **Telemetry is a source, not a hub — it serves no user-facing read.** If a page needs telemetry data, the correct move is to add it to `analytics`' derived model or `charging`'s mirror, never to open a telemetry port to the gateway. RM38 and RM40 exist precisely because that shortcut was taken once and had to be undone. _Source: `ai/architecture.md`; roadmaps RM38/RM40._
- **`charging` is NOT a pure mirror.** It mirrors telemetry's Supercharger rows into `supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3), but `manual_charge_entries` originates in the module — a human types those, and telemetry never sees them. Treating `charging` as read-only-derived will lose the manual half. _Source: `workflows/manual-charge-crud.md`; `internal/charging/db/migrations/20260823000001_add_charge_sessions.sql` header._

- **A poll run is recorded exactly once, and a duplicate is an error — never an upsert.** A second summary for a run identity that already has one is rejected and leaves the first record unchanged; recording a run twice is never a legitimate outcome. _Source: spec telemetry — Requirement: Run-Level Poll Summary Storage._
- **A run that fails before touching a single vehicle still records a summary.** Its account and vehicle counts are all zero, but its start/finish times and duration are real. This is the whole point of the table: before it, a failed run left no trace at all, because zero `poll_attempts` rows were written. _Source: spec telemetry — Requirement: Run-Level Poll Summary Storage._
- **Every Tesla Fleet API request counts, including the ones that fail.** A rejected request still consumes a request against the vendor's quota, so the counter increments before the error is checked — never after. _Source: spec telemetry — Requirement: Tesla API Call Counting._
- **"Whole-account failure" has exactly two causes.** An account counts as failed only when it cannot obtain a usable Tesla access token, or when its account-wide vehicle-list request is rejected as unauthorized. No other failure mode marks an account failed — per-vehicle failures never do. _Source: spec telemetry — Requirement: Account-Level Attempt And Outcome Counts._
- **Succeeded accounts is derived, not counted:** attempted minus failed. Do not increment it independently or the two will drift. _Source: spec telemetry — Requirement: Account-Level Attempt And Outcome Counts._
- **The cycle log line mixes two grains, so every label says which.** Vehicle-grain counts are labelled as vehicles (`vehicles_attempted`/`vehicles_succeeded`); account-grain counts and the Tesla API call count are reported alongside them. The unlabelled `attempted`/`succeeded` pair was the exact ambiguity MAG-35 was filed about. _Source: spec telemetry — Requirement: Nightly Cycle Log Summary._

## Related KB

- Features: (none yet)
- Workflows: `workflows/manual-charge-crud.md` (analytics recalc after writes), `workflows/supercharger-stats-read.md` (read-only page over the `supercharger_sessions` mirror)
- Architecture: `architecture/nightly-cycle.md` (the 3-step `ProcessVehicleData` cycle that drives telemetry's write path)
