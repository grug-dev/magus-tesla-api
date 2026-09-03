# Knowledge Base — INDEX

> Entry point for the `context-fetch` skill. Resolve a UI/business term to its internal name
> and KB guide before touching any concept. One row per term **and per alias**.

## Glossary & routing — entities

| Term (UI / business) | Internal name | Type | KB path |
|---|---|---|---|
| `manual charge` | `charging.Writer` / `manual_charge_entries` | entity | `workflows/manual-charge-crud.md` |
| `charge row` | `ChargeRowUpdate` / `ChargeRowDelete` | entity | `workflows/manual-charge-crud.md` |
| `charges form` | `ChargeCreate` / `parseChargeForm` | entity | `workflows/manual-charge-crud.md` |
| `Manual Records` | `/charges` page (`ChargePage`) | entity | `workflows/manual-charge-crud.md` |
| `supercharger stats` | `SuperchargerStatsPage` / `charging.SessionReader` (read) + `charging.SessionVerifier` (write, RM31) (`supercharger_sessions`) | entity | `workflows/supercharger-stats-read.md` |
| `Supercharger session` | `charging.Session` / mirrored into `supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3) by the nightly sync; its `start_battery_pct`/`end_battery_pct` are correctable by the gateway via `charging.SessionVerifier` (RM31) — still no gateway Create or Delete | entity | `workflows/supercharger-stats-read.md` |
| `fast charging stats` | synonym of `supercharger stats` | entity | `workflows/supercharger-stats-read.md` |
| `charge session log` | `charging.SessionReader` / `charging.SessionWriter` (`supercharger_sessions`) | entity | `workflows/supercharger-stats-read.md` |
| `charge session record` | synonym of `charge session log` | entity | `workflows/supercharger-stats-read.md` |
| `session battery edit` | `SuperchargerRowUpdate` / `charging.SessionVerifier.VerifySession` | entity | `use-case/charging/verify-session-battery.md` |
| `verify session battery` | synonym of `session battery edit` | entity | `use-case/charging/verify-session-battery.md` |
| `battery percentage correction` | synonym of `session battery edit` | entity | `use-case/charging/verify-session-battery.md` |
| `vehicle metrics` | `analytics.Recalculator` / `vehicle_metrics` | entity | `entities/vehicle-metrics/guide.md` |
| `calc fields` | the `_calc` columns of `vehicle_metrics` | entity | `entities/vehicle-metrics/guide.md` |
| `calculated fields` | synonym of `calc fields` | entity | `entities/vehicle-metrics/guide.md` |
| `metrics reconciliation` | `Recalculator.Reconcile` / `vehicle_metric_watermarks` | entity | `entities/vehicle-metrics/guide.md` |
| `watermark source` | `vehicle_metric_watermarks.source` (`vehicle_snapshots` / `supercharger_sessions` / `manual_charge_entries`) | entity | `entities/vehicle-metrics/guide.md` |
| `vehicle status` | the eight raw status observations on `vehicle_metrics`, read via `analytics.Reader.LatestMetricsByAccount` → `analytics.VehicleStatus` (RM38) | entity | `entities/vehicle-metrics/guide.md` |
| `latest vehicle status` | `analytics.Reader.LatestMetricsByAccount` / `LatestVehicleMetricsByAccount` (`DISTINCT ON (tesla_id)` over `vehicle_metrics`) | entity | `entities/vehicle-metrics/guide.md` |
| `session inferred capacity` | `charging.Session.InferredCapacityKWhCalc` / `supercharger_sessions.inferred_capacity_kwh_calc` (DB-generated) | entity | `workflows/supercharger-stats-read.md` |
| `derived start battery` | `charging.SessionVerifier.VerifySession` derivation (`start_battery_pct` computed from `energy_kwh` + end %) | entity | `use-case/charging/verify-session-battery.md` |
| `calculated start battery` | synonym of `derived start battery` | entity | `use-case/charging/verify-session-battery.md` |
| `inferred capacity` | `charging.Entry.InferredCapacityKWhCalc` / `manual_charge_entries.inferred_capacity_kwh_calc` (DB-generated) | entity | `workflows/manual-charge-crud.md` |
| `inferred pack capacity` | synonym of `inferred capacity` | entity | `workflows/manual-charge-crud.md` |
| `entry status` | `charging.Status` (`IN_PROGRESS` / `DONE`) / `charging.RequiredFieldsFor` — the status-to-required-fields rule | entity | `workflows/manual-charge-crud.md` |
| `charge status` | synonym of `entry status` | entity | `workflows/manual-charge-crud.md` |
| `energy source` | `charging.EnergySource` (`USER` / `ESTIMATED`) / `manual_charge_entries.energy_source` — module-computed, never caller-supplied | entity | `workflows/manual-charge-crud.md` |
| `energy provenance` | synonym of `energy source` | entity | `workflows/manual-charge-crud.md` |
| `battery level by day` | `analytics.Reader.BatteryLevelByDay` / `analytics.DayBattery` (`vehicle_metrics.battery_level_pct`, `battery_range_km`) | entity | `entities/vehicle-metrics/guide.md` |
| `per-day battery level` | synonym of `battery level by day` | entity | `entities/vehicle-metrics/guide.md` |
| `battery history` | synonym of `battery level by day` | entity | `entities/vehicle-metrics/guide.md` |
| `battery chart` | `buildBatteryChart` / `analytics.Reader.BatteryLevelByDay` (since RM40; previously `telemetry.Reader`) | entity | `use-case/gateway/read-dashboard-history.md` |
| `battery history chart` | synonym of `battery chart` | entity | `use-case/gateway/read-dashboard-history.md` |

## Input ports — pages & endpoints

| Page / endpoint | Route or URI | Module | KB path |
|---|---|---|---|
| `Manual Records page` | `/charges` | `charging` | `input-port/charging/charges.md` |
| `Registros manuales` | `/charges` | `charging` | `input-port/charging/charges.md` |
| `charges page` | `/charges` | `charging` | `input-port/charging/charges.md` |
| `Supercharger Stats page` | `/supercharger-stats` | `charging` | `input-port/charging/supercharger-stats.md` |
| `fast charging stats page` | `/supercharger-stats` | `charging` | `input-port/charging/supercharger-stats.md` |
| `Dashboard page` | `/dashboard` | `gateway` | `input-port/gateway/dashboard.md` |
| `dashboard` | `/dashboard` | `gateway` | `input-port/gateway/dashboard.md` |
| `Tablero` | `/dashboard` | `gateway` | `input-port/gateway/dashboard.md` |
| `Vehicle Status panel` | `/dashboard` | `gateway` | `input-port/gateway/dashboard.md` |
| `Estado del vehículo` | `/dashboard` | `gateway` | `input-port/gateway/dashboard.md` |
| `dashboard history charts` | `/ui/dashboard/history` | `gateway` | `input-port/gateway/dashboard.md` |

## Use cases

| Use case | Entry point | Module | KB path |
|---|---|---|---|
| `edit a manual charge record` | `PUT /ui/charges/row/:id` | `charging` | `use-case/charging/update-manual-charge.md` |
| `update manual charge` | `PUT /ui/charges/row/:id` | `charging` | `use-case/charging/update-manual-charge.md` |
| `delete a manual charge record` | `DELETE /ui/charges/row/:id` | `charging` | `use-case/charging/delete-manual-charge.md` |
| `edit a Supercharger session` | `PATCH /ui/supercharger-stats/row/:id` | `charging` | `use-case/charging/verify-session-battery.md` |
| `read dashboard bento` | `GET /dashboard` | `gateway` | `use-case/gateway/read-dashboard-bento.md` |
| `render the dashboard` | `GET /dashboard` | `gateway` | `use-case/gateway/read-dashboard-bento.md` |
| `dashboard vehicle status` | `GET /ui/dashboard` | `gateway` | `use-case/gateway/read-dashboard-bento.md` |
| `read dashboard history charts` | `GET /ui/dashboard/history` | `gateway` | `use-case/gateway/read-dashboard-history.md` |
| `dashboard charts` | `GET /ui/dashboard/history` | `gateway` | `use-case/gateway/read-dashboard-history.md` |

## Workflows

| Workflow | KB path |
|---|---|
| `manual charge CRUD` (create/edit/delete a manual charge, incl. analytics recalc hook) | `workflows/manual-charge-crud.md` |
| `supercharger stats read` (page/fragment read flow, plus the narrow `session battery edit` write path over two fields — RM31) | `workflows/supercharger-stats-read.md` |
| `supercharger stats date filter` (`?start=&end=`, 400-day cap) | `workflows/supercharger-stats-read.md` |
| `supercharger stats chart axes` (`YYYY-MM` bar labels + reused kWh y-axis ticks) | `workflows/supercharger-stats-read.md` |
| `supercharger session battery percentages` (the four `charging.Session` % columns; nil ⇒ `—`) | `workflows/supercharger-stats-read.md` |

## Architecture topics

| Topic | KB path |
|---|---|
| `telemetry module` (ingest-only: fetches the Fleet API and writes what it fetched — plus the consumer map of who may read it) | `architecture/telemetry-ingest-only.md` |
| `telemetry hub` | **misnomer** — telemetry is a source, not a hub → `architecture/telemetry-ingest-only.md` |
| `who reads telemetry` | synonym of `telemetry module` → `architecture/telemetry-ingest-only.md` |
| `telemetry vs analytics` | the module split (telemetry ingests · charging mirrors + owns manual · analytics derives) → `architecture/telemetry-ingest-only.md` |
| `can the gateway read telemetry` | no — forbidden by `make boundary-guard` → `architecture/telemetry-ingest-only.md` |
| `ingest module` | synonym of `telemetry module` → `architecture/telemetry-ingest-only.md` |
| `vehicle snapshots` | `telemetry.Snapshot` / `vehicle_snapshots` → `architecture/telemetry-ingest-only.md` |
| `nightly cycle` (the 3-step `ProcessVehicleData` orchestration: sync fleet data → mirror charging data → recalculate analytics) | `architecture/nightly-cycle.md` |
| `nightly collection` | synonym of `nightly cycle` → `architecture/nightly-cycle.md` |
| `nightly poll` | synonym of `nightly cycle` → `architecture/nightly-cycle.md` |
| `nightly batch` | synonym of `nightly cycle` → `architecture/nightly-cycle.md` |
| `the poller run` | synonym of `nightly cycle` → `architecture/nightly-cycle.md` |
| `ProcessVehicleData` | `app.Processor.ProcessVehicleData` → `architecture/nightly-cycle.md` |
| `session mirror` | step 2 of the cycle — `charging.SessionWriter.MirrorSessions` → `architecture/nightly-cycle.md` |
| `charge record mutation` (the contract every charge write shares: source write → affected period → centralized recalc → persist → gaps, plus the documented divergences between the manual and Supercharger implementations) | `architecture/charge-record-mutation.md` |
| `edit charging records` | synonym of `charge record mutation` → `architecture/charge-record-mutation.md` |
| `charge record lifecycle` | synonym of `charge record mutation` → `architecture/charge-record-mutation.md` |
| `charge recalculation` | synonym of `charge record mutation` → `architecture/charge-record-mutation.md` |
| `charge write side effects` | synonym of `charge record mutation` → `architecture/charge-record-mutation.md` |
| `charge gaps` (`analytics.GapWriter` / `charge_gaps` — written only by the nightly cycle, never by an edit) | `architecture/charge-record-mutation.md` |
| `platform time zone` (the single default zone `America/Bogota` + calendar-day normalization) | `architecture/platform-time-zone.md` |
| `default time zone` | synonym of `platform time zone` → `architecture/platform-time-zone.md` |
| `America/Bogota` | synonym of `platform time zone` → `architecture/platform-time-zone.md` |
| `clock` | `internal/clock` (`Zone` / `Now` / `LoadOrDefault` / `CalendarDay`) → `architecture/platform-time-zone.md` |
| `calendar day` | `clock.CalendarDay` — normalize a moment to its day in a given zone → `architecture/platform-time-zone.md` |
| `account activation gate` (the cross-module Active/Inactive rule: the account module hides Inactive rows from reads, the gateway refuses them a session at login) | `architecture/account-activation-gate.md` |
| `inactive account` | synonym of `account activation gate` → `architecture/account-activation-gate.md` |
| `account status` | `account.Account.Status` (`Active` / `Inactive`) → `architecture/account-activation-gate.md` |
| `blocked login` | synonym of `account activation gate` → `architecture/account-activation-gate.md` |
| `deactivated account` | synonym of `account activation gate` → `architecture/account-activation-gate.md` |
| `poll run summary` (one row per `ProcessVehicleData` invocation, recorded on every exit path incl. whole-cycle failure) | `architecture/nightly-cycle.md` |
| `run duration` | `ProcessVehicleData`'s clock-measured start-to-finish span → `architecture/nightly-cycle.md` |
| `poll run` (one `poll_runs` row per collection-cycle invocation: trigger, timing, account/vehicle outcome counts, Tesla API call count) | `architecture/telemetry-ingest-only.md` |
| `run summary` | synonym of `poll run` → `architecture/telemetry-ingest-only.md` |
| `poll_runs` | `telemetry.PollRun` / `telemetry.RunWriter.RecordRun` → `architecture/telemetry-ingest-only.md` |
| `Tesla API call count` | `CycleReport.TeslaAPICalls` (counting decorator inside `internal/telemetry`) → `architecture/telemetry-ingest-only.md` |

<!--
Notes for the curator:
- One row per concept and its true synonyms — words that mean the SAME thing (e.g. a UI label,
  a multilingual name, and a back-end name all pointing to the same guide).
- Do NOT add a row per attribute/field of a concept. An attribute is not an alias; a question
  about it routes to the owning concept's guide anyway. Attribute detail lives in that guide's
  glossary, not here — otherwise INDEX must be maintained per field.
- `Internal name` is the actual symbol/table behind the term — the thing CodeGraph knows.
- Keep paths relative to kkpa/context/.
-->
