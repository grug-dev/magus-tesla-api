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
| `supercharger stats` | `SuperchargerStatsPage` / `charging.SessionReader` (read) + `charging.SessionVerifier` (write, RM31) (`charge_sessions`) | entity | `workflows/supercharger-stats-read.md` |
| `Supercharger session` | `charging.Session` / mirrored into `charge_sessions` by the analytics recalculation path; its `start_battery_pct`/`end_battery_pct` are correctable by the gateway via `charging.SessionVerifier` (RM31) — still no gateway Create or Delete | entity | `workflows/supercharger-stats-read.md` |
| `fast charging stats` | synonym of `supercharger stats` | entity | `workflows/supercharger-stats-read.md` |
| `charge session log` | `charging.SessionReader` / `charging.SessionWriter` (`charge_sessions`) | entity | `workflows/supercharger-stats-read.md` |
| `charge session record` | synonym of `charge session log` | entity | `workflows/supercharger-stats-read.md` |
| `session battery edit` | `SuperchargerRowUpdate` / `charging.SessionVerifier.VerifySession` | entity | `workflows/supercharger-stats-read.md` |
| `verify session battery` | synonym of `session battery edit` | entity | `workflows/supercharger-stats-read.md` |
| `battery percentage correction` | synonym of `session battery edit` | entity | `workflows/supercharger-stats-read.md` |
| `vehicle metrics` | `analytics.Recalculator` / `vehicle_metrics` | entity | `entities/vehicle-metrics/guide.md` |
| `calc fields` | the `_calc` columns of `vehicle_metrics` | entity | `entities/vehicle-metrics/guide.md` |
| `calculated fields` | synonym of `calc fields` | entity | `entities/vehicle-metrics/guide.md` |
| `metrics reconciliation` | `Recalculator.Reconcile` / `vehicle_metric_watermarks` | entity | `entities/vehicle-metrics/guide.md` |
| `watermark source` | `vehicle_metric_watermarks.source` (`vehicle_snapshots` / `charge_sessions` / `manual_charge_entries`) | entity | `entities/vehicle-metrics/guide.md` |

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
| `telemetry hub` (module purpose + consumer map: who reads telemetry data) | `architecture/telemetry-data-hub.md` |
| `telemetry module` | synonym of `telemetry hub` → `architecture/telemetry-data-hub.md` |
| `vehicle snapshots` | `telemetry.Snapshot` / `vehicle_snapshots` → `architecture/telemetry-data-hub.md` |
| `nightly cycle` (the 3-step `ProcessVehicleData` orchestration: sync fleet data → mirror charging data → recalculate analytics) | `architecture/nightly-cycle.md` |
| `nightly collection` | synonym of `nightly cycle` → `architecture/nightly-cycle.md` |
| `nightly poll` | synonym of `nightly cycle` → `architecture/nightly-cycle.md` |
| `nightly batch` | synonym of `nightly cycle` → `architecture/nightly-cycle.md` |
| `the poller run` | synonym of `nightly cycle` → `architecture/nightly-cycle.md` |
| `ProcessVehicleData` | `app.Processor.ProcessVehicleData` → `architecture/nightly-cycle.md` |
| `session mirror` | step 2 of the cycle — `charging.SessionWriter.MirrorSessions` → `architecture/nightly-cycle.md` |

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
