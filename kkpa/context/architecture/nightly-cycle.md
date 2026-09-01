# The nightly cycle — `app.ProcessVehicleData` — maintenance guide

> The map for changing the nightly cycle without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `nightly cycle`, `nightly collection`, `nightly poll`, `nightly batch`, `the poller run`, `poll run summary`
- **Internal name:** `app.Processor.ProcessVehicleData` — the 3-step orchestration; since RM36 it also measures its own span and records one `telemetry.PollRun` per invocation through the `telemetry.RunWriter` port

## Component map

### Orchestration — `internal/app`

| File | Role |
|---|---|
| `internal/app/app.go` | `Processor` port + `NewProcessor` — seven **public ports** plus a `*time.Location`. There is no `*pgxpool.Pool` parameter and there must never be one. |
| `internal/app/processor.go` | The three steps: `ProcessVehicleData` (step 1 + the short-circuit), `processChargingData` (step 2), `recalculateAnalytics` (step 3). **Change the cycle here.** |
| `internal/app/scheduler.go` | `Scheduler`/`NewScheduler`/`Run` + pure `nextRun` — the daily driving adapter that CALLS `Processor` from the outside; it is NOT inside it. |
| `cmd/poller/main.go` | Composition root: constructs every port and injects it. Thin — zero business logic. |

### Step 1 — sync fleet data (`internal/telemetry`, the only writer of fleet data)

| File | Role |
|---|---|
| `internal/telemetry/service.go` | `Collector.CollectAll` — per-account token resolve, per-vehicle wake/read, charging-history fetch. |
| `internal/telemetry/telemetry.go` | Ports `Collector`, `Reader`, `SuperchargerReader`; domain types (`Snapshot`, `SuperchargerSession`, `CycleReport`, `RunContext`). |
| `internal/telemetry/reader.go` | Read implementations (`NewReader`, `newSuperchargerReaderImpl`); pgtype→domain mapping stays here. |
| `internal/tesla/vehicles.go` | The paid Fleet API calls (`ListVehicles`, `WakeUp`, `VehicleData`, `ChargingHistory`). |
| `internal/account/service.go` | Vehicle registry (`AllRegisteredVehicles`), per-user token refresh (`AccessTokenFor`), config write-back (`SetVehicleConfigIfEmpty`). |

### Step 2 — mirror charging data (`internal/charging`)

| File | Role |
|---|---|
| `internal/charging/session_writer.go` | `SessionWriter.MirrorSessions` — upsert-only, one transaction per account, rejects a mis-scoped `AccountID`. |
| `internal/charging/charging.go` | `SessionMirror` — the mirror's payload type. It has **no battery-percentage field**; that is the structural protection, not a convention. |

### Step 3 — recalculate analytics (`internal/analytics`)

| File | Role |
|---|---|
| `internal/analytics/recalculate.go` | `Recalculator.Reconcile` (watermark-driven) and `Recalculate` (window re-derivation) — the module's write path onto `vehicle_metrics`. |
| `internal/analytics/reader.go` | `Reader.ConsumedByDay` — read of the model step 3a just advanced. |
| `internal/analytics/gap_writer.go` | `GapWriter.ReconcileWindow` — UPSERT+DELETE over `charge_gaps` for the trailing window. |
| `internal/charging/session_reader.go` | `SuperchargerSessionAnalyticsReader` — **analytics' Supercharger source since RM31** (`charge_sessions`), replacing `telemetry.SuperchargerReader`. |
| `internal/analytics/db/migrations/20260828000001_migrate_vehicle_metric_watermarks_source.sql` | Retires the `'supercharger_sessions'` watermark label by DELETING those cursor rows (forcing a backfill), not renaming them. |

### Port map — who calls whom in one cycle

| Caller | Port | Callee | Methods used |
|---|---|---|---|
| `app` | `telemetry.Collector` | `telemetry` | `CollectAll` |
| `app` | `telemetry.SuperchargerReader` | `telemetry` | `SuperchargerSessionsByAccount` — **the port's only remaining caller repo-wide** |
| `app` | `charging.SessionWriter` | `charging` | `MirrorSessions` |
| `app` | `analytics.Recalculator` | `analytics` | `Reconcile` |
| `app` | `analytics.Reader` | `analytics` | `ConsumedByDay` |
| `app` | `analytics.GapWriter` | `analytics` | `ReconcileWindow` |
| `app` | `account.Service` | `account` | `AllRegisteredVehicles` (×3 — once per step) |
| `telemetry` | `account.Service` | `account` | `AllRegisteredVehicles`, `AccessTokenFor`, `SetVehicleConfigIfEmpty` |
| `telemetry` | `tesla.VehicleService` | `tesla` | `ListVehicles`, `WakeUp`, `VehicleData`, `ChargingHistory` |
| `analytics` | `telemetry.Reader` | `telemetry` | `SnapshotsByVehicleUpdatedSince`, `SnapshotsByVehicleBetween`, `SnapshotPrecedingDay` — **unchanged by RM31** |
| `analytics` | `charging.SuperchargerSessionAnalyticsReader` | `charging` | `ListSessionsByVehicleUpdatedSince`, `ListSessionsByVehicleBetween` |
| `analytics` | `charging.Reader` | `charging` | `ListEntriesByVehicleUpdatedSince`, `ListEntriesByVehicleBetween` |

### Table effects — which step touches what

| Table | Owner | Steps | Operations |
|---|---|---|---|
| `vehicles` | account | 1, 2, 3 | R (×3, `ListAllVehicles`) + U (`UpdateVehicleConfigIfEmpty`, step 1) |
| `tesla_tokens` | account | 1 | R+U, row-locked, in one transaction; UPDATE only when the token needed refreshing |
| `accounts` | account | — | untouched — OAuth and language are user paths |
| `vehicle_snapshots` | telemetry | 1 write · 3 read | C+U (`InsertVehicleSnapshot`, on-conflict per `(account_id, tesla_id, captured_date)`), R by analytics |
| `poll_attempts` | telemetry | 1 | C only, one row per vehicle per cycle, stamped `run_id`/`triggered_by` |
| `supercharger_sessions` | telemetry | 1 write · 2 read | C+U (`UpsertSuperchargerSession`), R by step 2's mirror (`SuperchargerSessionsByAccount`). **No longer read in step 3.** |
| `charge_sessions` | charging | 2 write · 3 read | C+U (`MirrorChargeSession`), R by analytics (`ListSessionsByVehicle{Between,UpdatedSince}`) |
| `manual_charge_entries` | charging | 3 read | R only — the nightly job never writes manual entries |
| `vehicle_metrics` | analytics | 3 | C+R+U+D — the only table the cycle touches with all four, in one transaction |
| `vehicle_metric_watermarks` | analytics | 3 | C+R+U — one cursor per source |
| `charge_gaps` | analytics | 3 | C+R+U+D — window is the reconciliation unit |

## How maintenance works

- **Change what the cycle does, or the order of its steps:** `internal/app/processor.go` only. Never re-add orchestration to `cmd/poller`, and never give `Processor` a `Scheduler` field or a clock.
- **Add a step:** add a private method on `processor` in `processor.go`, call it from `ProcessVehicleData`, and add its port to `NewProcessor` in `app.go` + the injection site in `cmd/poller/main.go`. The new argument must be another module's **public port** — never a pool.
- **Add a new derivation source to step 3:** add the read port to `analytics.NewRecalculator` (`recalculate.go`), add a watermark source constant, and extend the `vehicle_metric_watermarks.source` CHECK by migration. A **new** source starts at epoch and backfills the vehicle's whole history on first run — that is intended, not a bug.
- **Change what the mirror carries:** add the field to `charging.SessionMirror` (`charging.go`), the `MirrorChargeSession` query (`internal/charging/db/query.sql` → `make sqlc`), and the mapping in `processor.go`'s `processChargingData`. Field-name-for-field-name, no renames, no derivation.
- **Change the schedule:** `internal/app/scheduler.go` + the `PollerScheduleHour`/`PollerScheduleMinute` config read in `cmd/poller/main.go`. Keep `nextRun` unexported and pure.
- **Change failure containment:** the isolation shape is per-account in step 2 and per-vehicle in step 3; see the gotchas below before loosening either.

- **Add a fact to the recorded run summary:** the value must first exist on `telemetry.CycleReport` (populated inside `telemetry.CollectAll`). Then extend `telemetry.PollRun` and the `poll_runs` schema on the telemetry side, and map the new field in `internal/app`'s `buildPollRun`. `internal/app` gains no pool and no table — it only maps and calls the port.
- **Change what the cycle measures:** `start`/`finish` are read in `ProcessVehicleData` via `internal/clock`, bracketing all three steps. Anything that needs its own timing is a separate measurement, not a widening of these two.

## Conventions & gotchas

- **Exactly one failure stops the cycle: step 1's.** A non-nil error from `Collector.CollectAll` returns immediately and steps 2 and 3 never run — both later steps read data step 1 was supposed to have just written. Every other failure is logged and isolated, never fatal. _Source: `internal/app/processor.go` `ProcessVehicleData` doc; `internal/app/AGENTS.md` §Responsibility._
- **Step 2 before step 3 is load-bearing since RM31 — it used to be only a convention.** Analytics now derives its Supercharger figures from `charge_sessions`, which step 2 writes. A mirror that fails leaves step 3 deriving that account's consumed-percent from the previous night's sessions. _Source: `RM31-analytics-read-sessions-from-charging`; `internal/analytics/recalculate.go`._
- **Analytics reads `charging.charge_sessions`, NOT `telemetry.supercharger_sessions`.** RM31 moved it to `charging.SuperchargerSessionAnalyticsReader`. `telemetry.SuperchargerReader` still exists and is still correct — but `internal/app`'s step 2 is now its **only** caller. _Source: `internal/analytics/recalculate.go`, `internal/charging/charging.go`._
- **`telemetry.Reader` is unchanged by that move.** Analytics still reads snapshot history straight from telemetry (`SnapshotsByVehicleUpdatedSince`, `SnapshotsByVehicleBetween`, `SnapshotPrecedingDay`). Do not "finish the migration" by moving snapshot reads too — telemetry owns snapshots. _Source: `internal/analytics/recalculate.go`._
- **The watermark source label is `charge_sessions`, not `supercharger_sessions`.** Migration `20260828000001` DELETED the old cursor rows rather than renaming them, so every affected vehicle backfills its whole Supercharger history on the first run after deploy. That is deliberate: the two tables' `updated_at` columns do not carry the same meaning. _Source: `internal/analytics/db/migrations/20260828000001_migrate_vehicle_metric_watermarks_source.sql`._
- **The nightly mirror can never overwrite a human's verified battery percentage — structurally.** `charging.SessionMirror` has no percentage field, so a nightly pass that clobbered one would not compile, and `MirrorChargeSession` never names the five battery-percentage columns. Only `charging.SessionVerifier.VerifySession` (a gateway user path, RM31) writes them. _Source: `internal/charging/charging.go`; `internal/charging/db/migrations/20260823000001_add_charge_sessions.sql`._
- **The mirror's upsert has no WHERE predicate, on purpose.** `updated_at` must keep advancing on every nightly pass so downstream watermarks see the row. _Source: `internal/charging/db/query.sql` `MirrorChargeSession`._
- **Step 2 enumerates per ACCOUNT, step 3 per VEHICLE.** The session read is account-wide, so looping per vehicle would re-mirror the same rows once per vehicle; the derivation is per vehicle. _Source: `internal/app/processor.go` `processChargingData` doc._
- **A failing `Reconcile` skips that vehicle's gap step too.** Reconciling gaps against metrics you just failed to refresh would delete gap rows on stale evidence. Keep the `continue`. _Source: `internal/app/processor.go` `recalculateAnalytics`._
- **The gap window's "yesterday" resolves in the POLLER'S zone, not UTC.** `time.Now().UTC()` here asks for the wrong day for 5 hours out of every 24. `internal/analytics` stays zone-free; the zone lives in this composition. _Source: `internal/app/processor.go` `recalculateAnalytics`; roadmap D6/D18._
- **`internal/app` owns no data and takes no pool.** `poll_attempts` (incl. `run_id`/`triggered_by`) stays telemetry's. Every `NewProcessor` argument is a public port. _Source: `internal/app/AGENTS.md` §Data ownership._
- **A `poll_attempts` insert failure is swallowed on purpose** — it must never abort the cycle, and `CycleReport` still reflects the true outcome. _Source: `internal/telemetry/service.go`._

- **Every cycle records exactly one summary — including a cycle that fails outright.** A whole-cycle synchronization failure short-circuits steps 2 and 3 but still records a row, with all counts zero and timings reflecting how fast the failure was. Before this, a failed cycle wrote zero `poll_attempts` rows and so left no trace of having run at all. _Source: spec process-vehicle-data — Requirement: Every Cycle Records A Poll Run Summary._
- **Recording the summary can never change the cycle's reported outcome.** A failed summary write is logged and swallowed; `ProcessVehicleData` returns exactly what its three steps produced. In `internal/app` this is enforced structurally — `recordRun` returns nothing, so the compiler prevents it, not a convention. _Source: spec process-vehicle-data — Requirement: Every Cycle Records A Poll Run Summary._
- **The record point is a fall-through, not a second call site.** Both the success path and the failure short-circuit fall through to one measurement/record tail. Adding an early `return` anywhere in `ProcessVehicleData` silently reintroduces the untraced-run bug this design exists to prevent. _Source: spec process-vehicle-data — Requirement: Every Cycle Records A Poll Run Summary._
- **Both poller entry points are covered by construction.** The nightly schedule and `cmd/poller --once` both call `ProcessVehicleData`, so neither can diverge from the other. Never record a run from `cmd/`. _Source: spec process-vehicle-data — Requirement: Every Cycle Records A Poll Run Summary._

## Rendered view (visual map)

A published Artifact renders this same cycle as a diagram — tier map, per-step call traces, the
CRUD matrix, and the failure blast-radius table:

**https://claude.ai/code/artifact/22187391-35c9-4ad3-a71c-8aae03a3a5e9** — *Nightly Cycle Map*

**Precedence, when they disagree:** the **code** is the source of truth, then **this guide**, then
the artifact. The artifact is a rendering for humans, not an input to implementation — never
implement from it, and never treat it as evidence that a fact is current.

**It does not self-update.** Any change that touches this guide's port map, table effects, or
failure table must refresh the artifact in the same change, exactly as `CLAUDE.md`'s
"Docs track structural change" rule requires of the structure docs. Republish over the same URL
so the link above stays valid.

## Related KB

- Features: (none)
- Use cases: (none — the cycle has no external HTTP trigger; the tier-8 manual-rerun API is parked)
- Workflows: `workflows/manual-charge-crud.md` (the other trigger of `Recalculator`, on user write), `workflows/supercharger-stats-read.md` (the page that reads the `charge_sessions` mirror this cycle writes)
- Architecture: `architecture/telemetry-data-hub.md` (telemetry's own consumer map)
- Entities: `entities/vehicle-metrics/guide.md` (what step 3 derives and stores)
