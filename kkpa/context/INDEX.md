# Knowledge Base — INDEX

> Entry point for the `context-fetch` skill. Resolve a UI/business term to its internal name
> and KB guide before touching any concept. One row per term **and per alias**.

## Glossary & routing — entities

| Term (UI / business) | Internal name | Type | KB path |
|---|---|---|---|
| `manual charge` | `charging.Writer` / `manual_charge_entries` | entity | `workflows/manual-charge-crud.md` |
| `charge row` | `ExternalChargeRowUpdate` / `ExternalChargeRowDelete` | entity | `workflows/manual-charge-crud.md` |
| `charges form` | `ExternalChargeCreate` / `parseExternalChargeForm` | entity | `workflows/manual-charge-crud.md` |
| `External charges` | `/external-charges` page (`ExternalChargesPage`) | entity | `workflows/manual-charge-crud.md` |
| `supercharger stats` | `SuperchargerStatsPage` / `charging.SessionReader` (read) + `charging.SessionVerifier` (write, RM31) (`supercharger_sessions`) | entity | `workflows/supercharger-stats-read.md` |
| `Supercharger session` | `charging.Session` / mirrored into `supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3) by the nightly sync; its `start_battery_pct`/`end_battery_pct` are correctable by the gateway via `charging.SessionVerifier` (RM31), which also auto-sets a three-state `status` (`IN_PROGRESS`/`DONE_CALCULATED`/`DONE`, RM41 tier 4) that no caller may supply — still no gateway Create or Delete | entity | `workflows/supercharger-stats-read.md` |
| `fast charging stats` | synonym of `supercharger stats` | entity | `workflows/supercharger-stats-read.md` |
| `charge session log` | `charging.SessionReader` / `charging.SessionWriter` (`supercharger_sessions`) | entity | `workflows/supercharger-stats-read.md` |
| `charge session record` | synonym of `charge session log` | entity | `workflows/supercharger-stats-read.md` |
| `session battery edit` | `SuperchargerRowUpdate` / `charging.SessionVerifier.VerifySession` | entity | `use-case/charging/verify-session-battery.md` |
| `verify session battery` | synonym of `session battery edit` | entity | `use-case/charging/verify-session-battery.md` |
| `account settings` (the one-row-per-account record: `language` + `theme` + `analysis_start_date`, PK `account_id`) | `account.Settings` — table `account.settings` | entity | `entities/account-settings/guide.md` |
| `user preferences` | synonym of `account settings` → `entities/account-settings/guide.md` | entity | `entities/account-settings/guide.md` |
| `theme preference` | `account.settings.theme` — closed vocabulary `apex` / `graphite` / `halloween`, default `graphite` | entity | `entities/account-settings/guide.md` |
| `language preference` | `account.settings.language` — moved off `accounts.language` by RM42 tier 1, which dropped that column | entity | `entities/account-settings/guide.md` |
| `analysis start date` | `account.settings.analysis_start_date` — read-only, set at signup from `internal/clock`; `account.Service.AnalysisStartDateFor` (RM49 tier 1, MAG-55) | entity | `entities/account-settings/guide.md` |
| `battery percentage correction` | synonym of `session battery edit` | entity | `use-case/charging/verify-session-battery.md` |
| `vehicle metrics` | `analytics.Recalculator` / `vehicle_metrics` | entity | `entities/vehicle-metrics/guide.md` |
| `calc fields` | the `_calc` columns of `vehicle_metrics` | entity | `entities/vehicle-metrics/guide.md` |
| `calculated fields` | synonym of `calc fields` | entity | `entities/vehicle-metrics/guide.md` |
| `metrics reconciliation` | `Recalculator.Reconcile` / `vehicle_metric_watermarks` | entity | `entities/vehicle-metrics/guide.md` |
| `watermark source` | `vehicle_metric_watermarks.source` (`vehicle_snapshots` / `supercharger_sessions` / `manual_charge_entries`) | entity | `entities/vehicle-metrics/guide.md` |
| `vehicle status` | the eight raw status observations on `vehicle_metrics`, read via `analytics.Reader.LatestMetricsForVehicles` → `analytics.VehicleStatus` (RM38) | entity | `entities/vehicle-metrics/guide.md` |
| `latest vehicle status` | `analytics.Reader.LatestMetricsForVehicles` / `LatestVehicleMetricsByVehicles` (`DISTINCT ON (tesla_id)` over `vehicle_metrics`) | entity | `entities/vehicle-metrics/guide.md` |
| `session inferred capacity` | `charging.Session.InferredCapacityKWhCalc` / `supercharger_sessions.inferred_capacity_kwh_calc` (DB-generated) | entity | `workflows/supercharger-stats-read.md` |
| `derived start battery` | `charging.SessionVerifier.VerifySession` derivation (`start_battery_pct` computed from `energy_kwh` + end %) | entity | `use-case/charging/verify-session-battery.md` |
| `calculated start battery` | synonym of `derived start battery` | entity | `use-case/charging/verify-session-battery.md` |
| `inferred capacity` | `charging.Entry.InferredCapacityKWhCalc` / `manual_charge_entries.inferred_capacity_kwh_calc` (DB-generated) | entity | `workflows/manual-charge-crud.md` |
| `inferred pack capacity` | synonym of `inferred capacity` | entity | `workflows/manual-charge-crud.md` |
| `entry status` | `charging.Status` (`IN_PROGRESS` / `DONE`) / `charging.RequiredFieldsFor` — the status-to-required-fields rule | entity | `workflows/manual-charge-crud.md` |
| `charge status` | synonym of `entry status` | entity | `workflows/manual-charge-crud.md` |
| `energy source` | `charging.EnergySource` (`USER` / `ESTIMATED`) / `manual_charge_entries.energy_source` — module-computed, never caller-supplied | entity | `workflows/manual-charge-crud.md` |
| `energy provenance` | synonym of `energy source` | entity | `workflows/manual-charge-crud.md` |
| `price source` | `charging.PriceSource` (`USER` / `UNCONFIRMED`) / `manual_charge_entries.price_source` — module-computed, never caller-supplied | entity | `workflows/manual-charge-crud.md` |
| `price provenance` | synonym of `price source` | entity | `workflows/manual-charge-crud.md` |
| `zero price confirmation` | synonym of `price source` | entity | `workflows/manual-charge-crud.md` |
| `start battery source` | `charging.StartBatterySource` (`USER` / `ESTIMATED`) / `manual_charge_entries.start_battery_source` — module-computed, nullable, never caller-supplied | entity | `workflows/manual-charge-crud.md` |
| `start battery provenance` | synonym of `start battery source` | entity | `workflows/manual-charge-crud.md` |
| `battery level by day` | `analytics.Reader.BatteryLevelByDay` / `analytics.DayBattery` (`vehicle_metrics.battery_level_pct`, `battery_range_km`) | entity | `entities/vehicle-metrics/guide.md` |
| `per-day battery level` | synonym of `battery level by day` | entity | `entities/vehicle-metrics/guide.md` |
| `battery history` | synonym of `battery level by day` | entity | `entities/vehicle-metrics/guide.md` |
| `battery chart` | `buildBatteryChart` / `analytics.Reader.BatteryLevelByDay` (since RM40; previously `telemetry.Reader`) | entity | `use-case/gateway/read-dashboard-history.md` |
| `battery history chart` | synonym of `battery chart` | entity | `use-case/gateway/read-dashboard-history.md` |
| `watermark vocabulary` | the closed set of table names `vehicle_metric_watermarks.source` may hold, and how it is migrated → `entities/vehicle-metrics/guide.md` |
| `vocabulary migration` | retiring a watermark source value when another module renames its table → `entities/vehicle-metrics/guide.md` |
| `supercharger history change detection` | when a nightly sync counts as a change to a `telemetry.supercharger_history` row (`updated_at`) | entity | `architecture/telemetry-ingest-only.md` |
| `why does updated_at change every night` | the MAG-48 symptom, at the telemetry layer | entity | `architecture/telemetry-ingest-only.md` |
| `per-vehicle updated-since read` | `telemetry.SuperchargerHistoryReader.SuperchargerHistoryByVehicleUpdatedSince` — the only updated-since port; the account-wide one was removed when the table dropped `account_id` | entity | `architecture/telemetry-ingest-only.md` |
| `orphaned supercharger session` | a session whose VIN is not a registered vehicle; it is never stored, and the nightly cycle counts the skip | entity | `architecture/telemetry-ingest-only.md` |
| `session change detection` | when a nightly sync counts as a modification of a charge session record (`updated_at`) | entity | `workflows/supercharger-stats-read.md` |
| `why did every session recalculate` | the MAG-48 symptom — `updated_at` used to advance on every sync pass | entity | `workflows/supercharger-stats-read.md` |
| `bounded mirror read` | the watermark-bounded Supercharger sync (RM44 tier 4); replaced the full-history read | entity | `workflows/supercharger-stats-read.md` |
| `why is the mirror slow` | it used to read all history every night — MAG-48, fixed by the bounded read | entity | `workflows/supercharger-stats-read.md` |
| `tire pressure` | the four `tpms_pressure_*_psi` columns of `vehicle_metrics` (RM50 tier 1), raw per-day observations read via `analytics.Reader.LatestMetricsForVehicles` | entity | `entities/vehicle-metrics/guide.md` |
| `tyre pressure` | synonym of `tire pressure` | entity | `entities/vehicle-metrics/guide.md` |
| `TPMS` | synonym of `tire pressure` (tire-pressure monitoring system) | entity | `entities/vehicle-metrics/guide.md` |
| `tyre pressure delta` | the four `tpms_pressure_*_psi_calc` columns of `vehicle_metrics` (RM50 tier 3) — each day's wheel pressure minus the previous day's, NULL without a predecessor or a raw reading | entity | `entities/vehicle-metrics/guide.md` |
| `tyre pressure variance` | synonym of `tyre pressure delta` | entity | `entities/vehicle-metrics/guide.md` |
| `pressure change` | synonym of `tyre pressure delta` | entity | `entities/vehicle-metrics/guide.md` |
| `travel progress` | UI name for `vehicle_metrics.distance_traveled_km_calc`, exposed on `analytics.VehicleStatus.DistanceTraveledKmCalc` (RM50) | entity | `entities/vehicle-metrics/guide.md` |
| `battery drain` | UI name for `vehicle_metrics.consumed_pct`, exposed on `analytics.VehicleStatus.ConsumedPct` (RM50) | entity | `entities/vehicle-metrics/guide.md` |
| `charge authorship` | `charging.Entry.CreatedByAccountID` / `manual_charge_entries.created_by_account_id` — stored and returned, never a read filter | entity | `workflows/manual-charge-crud.md` |
| `entry author` | synonym of `charge authorship` | entity | `workflows/manual-charge-crud.md` |

## Input ports — pages & endpoints

| Page / endpoint | Route or URI | Module | KB path |
|---|---|---|---|
| `External charges page` | `/external-charges` | `charging` | `input-port/charging/external-charges.md` |
| `Manual Records` (former label, renamed MAG/CH44) | `/external-charges` | `charging` | `input-port/charging/external-charges.md` |
| `Registros manuales` (former label) | `/external-charges` | `charging` | `input-port/charging/external-charges.md` |
| `charge form layout` | `ExternalChargeCreateForm` / `ExternalChargeRowEdit` field set and order | `charging` | `input-port/charging/external-charges.md` |
| `charge form fields` | synonym of `charge form layout` | `charging` | `input-port/charging/external-charges.md` |
| `location label toggle` | RD14 — `applyChargeLocationLabelToggle` (`static/app.js`) | `charging` | `input-port/charging/external-charges.md` |
| `status required toggle` | RD13 — `applyChargeStatusRequiredToggle` (`static/app.js`) | `charging` | `input-port/charging/external-charges.md` |
| `charge date time sync` | RD12 — the `charged_on` → `started_at`/`ended_at` date splice | `charging` | `input-port/charging/external-charges.md` |
| `one in-progress per day` | `handlers.inProgressConflictOn` — one `IN_PROGRESS` entry per (vehicle, `charged_on`) | `charging` | `input-port/charging/external-charges.md` |
| `charge date before analysis start` | `handlers.parseExternalChargeForm`'s `AnalysisStartDateFor` check — rejects a `charged_on` before the account's analysis start date (RM49 tier 2, MAG-55) | `charging` | `input-port/charging/external-charges.md` |
| `Externas` | `/external-charges` | `charging` | `input-port/charging/external-charges.md` |
| `charges page` | `/external-charges` | `charging` | `input-port/charging/external-charges.md` |
| `Supercharger Stats page` | `/supercharger-stats` | `charging` | `input-port/charging/supercharger-stats.md` |
| `fast charging stats page` | `/supercharger-stats` | `charging` | `input-port/charging/supercharger-stats.md` |
| `Dashboard page` | `/dashboard` | `gateway` | `input-port/gateway/dashboard.md` |
| `dashboard` | `/dashboard` | `gateway` | `input-port/gateway/dashboard.md` |
| `Tablero` | `/dashboard` | `gateway` | `input-port/gateway/dashboard.md` |
| `Vehicle Status panel` | `/dashboard` | `gateway` | `input-port/gateway/dashboard.md` |
| `Estado del vehículo` | `/dashboard` | `gateway` | `input-port/gateway/dashboard.md` |
| `dashboard history charts` | `/ui/dashboard/history` | `gateway` | `input-port/gateway/dashboard.md` |
| `manual rerun endpoint` | `POST /internal/rerun/<token>` | `n/a` | `input-port/manual-rerun-endpoint.md` |
| `rerun endpoint` | `POST /internal/rerun/<token>` | `n/a` | `input-port/manual-rerun-endpoint.md` |
| `/internal/rerun` | `POST /internal/rerun/<token>` | `n/a` | `input-port/manual-rerun-endpoint.md` |
| `Travel Progress` | `/dashboard` | `gateway` | `input-port/gateway/dashboard.md` |
| `Progreso de viaje` | `/dashboard` | `gateway` | `input-port/gateway/dashboard.md` |
| `Tire pressure panel` | `/dashboard` | `gateway` | `input-port/gateway/dashboard.md` |
| `Presión de llantas` | `/dashboard` | `gateway` | `input-port/gateway/dashboard.md` |

## Use cases

| Use case | Entry point | Module | KB path |
|---|---|---|---|
| `edit a manual charge record` | `PUT /ui/external-charges/row/:id` | `charging` | `use-case/charging/update-manual-charge.md` |
| `update manual charge` | `PUT /ui/external-charges/row/:id` | `charging` | `use-case/charging/update-manual-charge.md` |
| `derived start battery percentage` | `PUT /ui/external-charges/row/:id` | `charging` | `use-case/charging/update-manual-charge.md` |
| `optional start battery percentage` | `PUT /ui/external-charges/row/:id` | `charging` | `use-case/charging/update-manual-charge.md` |
| `delete a manual charge record` | `DELETE /ui/external-charges/row/:id` | `charging` | `use-case/charging/delete-manual-charge.md` |
| `edit a Supercharger session` | `PATCH /ui/supercharger-stats/row/:id` | `charging` | `use-case/charging/verify-session-battery.md` |
| `read dashboard bento` | `GET /dashboard` | `gateway` | `use-case/gateway/read-dashboard-bento.md` |
| `render the dashboard` | `GET /dashboard` | `gateway` | `use-case/gateway/read-dashboard-bento.md` |
| `dashboard vehicle status` | `GET /ui/dashboard` | `gateway` | `use-case/gateway/read-dashboard-bento.md` |
| `read dashboard history charts` | `GET /ui/dashboard/history` | `gateway` | `use-case/gateway/read-dashboard-history.md` |
| `dashboard charts` | `GET /ui/dashboard/history` | `gateway` | `use-case/gateway/read-dashboard-history.md` |
| `trigger a manual rerun` | `POST /internal/rerun/<token>` | `n/a` | `use-case/trigger-manual-rerun.md` |
| `manual rerun` | `POST /internal/rerun/<token>` | `n/a` | `use-case/trigger-manual-rerun.md` |
| `rerun the nightly cycle` | `POST /internal/rerun/<token>` | `n/a` | `use-case/trigger-manual-rerun.md` |
| `force a collection cycle` | `POST /internal/rerun/<token>` | `n/a` | `use-case/trigger-manual-rerun.md` |
| `on-demand poll` | `POST /internal/rerun/<token>` | `n/a` | `use-case/trigger-manual-rerun.md` |
| `POLLER_RERUN_TOKEN` | `POST /internal/rerun/<token>` | `n/a` | `use-case/trigger-manual-rerun.md` |
| `why can't I edit this session` | the write is scoped to the session-selected vehicle; select that vehicle first | `charging` | `use-case/charging/verify-session-battery.md` |

## Workflows

| Workflow | KB path |
|---|---|
| `manual charge CRUD` (create/edit/delete a manual charge, incl. analytics recalc hook) | `workflows/manual-charge-crud.md` |
| `supercharger stats read` (page/fragment read flow, plus the narrow `session battery edit` write path over two fields — RM31) | `workflows/supercharger-stats-read.md` |
| `supercharger stats date filter` (`?start=&end=`, 400-day cap) | `workflows/supercharger-stats-read.md` |
| `supercharger stats chart axes` (`YYYY-MM` bar labels + reused kWh y-axis ticks) | `workflows/supercharger-stats-read.md` |
| `supercharger session battery percentages` (the two `charging.Session` % columns + `battery_pct_source`; nil ⇒ `—`) | `workflows/supercharger-stats-read.md` |
| `supercharger session status` (the three-state `charging.Session.Status`: `IN_PROGRESS` / `DONE_CALCULATED` / `DONE`, recomputed on every correction) | `workflows/supercharger-stats-read.md` |
| `supercharger status badge` (2nd-column `ui.Badge` + a `ui.Dot`; Badge Kind `primary`/`neutral`/`ghost`, Dot Variant `success`/`neutral`/`warning`; gateway-read-only) | `workflows/supercharger-stats-read.md` |
| `vehicle monthly metrics` (the monthly per-vehicle measurement story; today one metric, the measured pack capacity) | `workflows/vehicle-monthly-metrics.md` |
| `monthly effective capacity` (measured pack capacity per vehicle per month; median of reliable charge records, absent when evidence is thin) | `workflows/vehicle-monthly-metrics.md` |
| `effective pack capacity` | synonym of `monthly effective capacity` → `workflows/vehicle-monthly-metrics.md` |
| `measured pack capacity` | synonym of `monthly effective capacity` → `workflows/vehicle-monthly-metrics.md` |
| `monthly capacity` | synonym of `monthly effective capacity` → `workflows/vehicle-monthly-metrics.md` |
| `effective capacity` | synonym of `monthly effective capacity` → `workflows/vehicle-monthly-metrics.md` |
| `pack capacity` | `charging`'s `packCapacityKWh` + its `62.0` fallback → `workflows/vehicle-monthly-metrics.md`. The `analytics` `car_type` table was DELETED by RM61 — there is only one definition now. |
| `capacity backfill` | re-run one month — `cmd/monthly-capacity` / `make cmd-monthly-capacity` → `workflows/vehicle-monthly-metrics.md` |
| `cmd/monthly-capacity` | synonym of `capacity backfill` → `workflows/vehicle-monthly-metrics.md` |
| `monthly_effective_capacity` | the table → `workflows/vehicle-monthly-metrics.md` |

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
| `supercharger history estimate columns` | **dropped** — RM41 tier 3 removed `start_battery_pct_est` / `end_battery_pct_est`; reverses RM27 D6 → `architecture/telemetry-ingest-only.md` |
| `verification-time snapshot pair` | synonym of `supercharger history estimate columns` → `architecture/telemetry-ingest-only.md` |
| `nightly cycle` (the 4-step `ProcessVehicleData` orchestration: sync fleet data → mirror charging data → recalculate analytics → measure monthly capacity, the last one only on the 1st of the month) | `architecture/nightly-cycle.md` |
| `nightly collection` | synonym of `nightly cycle` → `architecture/nightly-cycle.md` |
| `nightly poll` | synonym of `nightly cycle` → `architecture/nightly-cycle.md` |
| `nightly batch` | synonym of `nightly cycle` → `architecture/nightly-cycle.md` |
| `monthly capacity step` | synonym of the nightly cycle's step 4 → `architecture/nightly-cycle.md` |
| `step 4` | the nightly cycle's monthly-capacity step → `architecture/nightly-cycle.md` |
| `client-side JS` (the gateway's zero-JS rule RD8 and its numbered sanctioned exceptions) | `architecture/gateway-client-side-js.md` |
| `zero-JS rule` | synonym of `client-side JS` → `architecture/gateway-client-side-js.md` |
| `app.js` | `internal/gateway/static/app.js` → `architecture/gateway-client-side-js.md` |
| `sanctioned exception` | synonym of `client-side JS` → `architecture/gateway-client-side-js.md` |
| `confirm modal` | RD10 — `ui.ConfirmDialog` / the `htmx:confirm` listener → `architecture/gateway-client-side-js.md` |
| `confirmation dialog` | synonym of `confirm modal` → `architecture/gateway-client-side-js.md` |
| `browser_tz cookie` | RD9 — the inline script in `layouts.BaseAuth` → `architecture/gateway-client-side-js.md` |
| `timezone cookie` | synonym of `browser_tz cookie` → `architecture/gateway-client-side-js.md` |
| `theme instant apply` | RD15 — the `click` + `htmx:afterRequest` listener pair → `architecture/gateway-client-side-js.md` |
| `install hint` | RD16 — `ui.InstallHint`, the iOS-only "Add to Home Screen" hint → `architecture/gateway-client-side-js.md` |
| `iOS install hint` | synonym of `install hint` → `architecture/gateway-client-side-js.md` |
| `gateway theming` (the palettes, the self-hosted fonts, and the `static/themes/` file layout) | `architecture/gateway-theming.md` |
| `palette` | synonym of `gateway theming` → `architecture/gateway-theming.md` |
| `theme file` | synonym of `gateway theming` → `architecture/gateway-theming.md` |
| `_shared.css` | what every theme inherits — fonts, font tokens, battery scale, divider reset → `architecture/gateway-theming.md` |
| `apex` / `graphite` | the two palettes → `architecture/gateway-theming.md` |
| `halloween` | a corrected DaisyUI *builtin*, not a palette (MAG-49) → `architecture/gateway-theming.md` |
| `data-theme` | resolved per request by `baseShell` from `ui.ThemeFromContext(ctx)` → `architecture/gateway-theming.md` |
| `adding a theme` | the four steps + `make theme-guard` → `architecture/gateway-theming.md` |
| `theme-guard` | synonym of `adding a theme` → `architecture/gateway-theming.md` |
| `ui.Themes` | the closed theme vocabulary — `internal/gateway/templates/ui/theme.go` → `architecture/gateway-theming.md` |
| `self-hosted fonts` | RD11 — Inter + JetBrains Mono under `internal/gateway/static/fonts/` → `architecture/gateway-theming.md` |
| `Inter` / `JetBrains Mono` | synonym of `self-hosted fonts` → `architecture/gateway-theming.md` |
| `font-mono` | the Tailwind utility that resolves to JetBrains Mono → `architecture/gateway-theming.md` |
| `battery scale` | the red/orange/yellow/green band — `ui.BatteryBandClass`, never per-theme → `architecture/gateway-theming.md` |
| `battery band` | synonym of `battery scale` → `architecture/gateway-theming.md` |
| `status colours` | `error`/`warning`/`success` are fixed in every theme; `info` is the sole exception → `architecture/gateway-theming.md` |
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
| `gateway reader/writer ports` (which ports a gateway handler may call: `Reader` always, `Writer` only in six named places, with their guard sets and why three deliberately differ) | `architecture/gateway-reader-writer-ports.md` |
| `read-only rule` | the gateway default — a handler calls `Reader` ports only → `architecture/gateway-reader-writer-ports.md` |
| `read-only at request time` | synonym of `read-only rule` → `architecture/gateway-reader-writer-ports.md` |
| `Reader port` / `Writer port` (in the gateway) | which handler may call which → `architecture/gateway-reader-writer-ports.md` |
| `gateway write exceptions` | synonym — the six places allowed to call a `Writer` → `architecture/gateway-reader-writer-ports.md` |
| `write aperture` | synonym of `gateway write exceptions` → `architecture/gateway-reader-writer-ports.md` |
| `CSRF` | the four session keys + `checkCSRFKey`'s fail-closed contract → `architecture/gateway-reader-writer-ports.md` |
| `csrf_theme` / `csrf_supercharger` / `csrf_externalcharge` / `csrf_vehicle_select` | the four CSRF session keys, one per form → `architecture/gateway-reader-writer-ports.md` |
| `tenant ownership check` | `RegisteredVehicles` on the submitted `(TeslaID, VIN)` — and the one aperture (theme switch) that deliberately skips it → `architecture/gateway-reader-writer-ports.md` |
| `language switch` | `LangSwitch` — no CSRF by user-approved decision; `SameSite=Lax` is the defence → `architecture/gateway-reader-writer-ports.md` |
| `theme switch` | `ThemeSwitch` — auth + CSRF, cookie written only after the persist → `architecture/gateway-reader-writer-ports.md` |
| `SameSite` | why the `lang` cookie's `SameSite=Lax` is mandatory → `architecture/gateway-reader-writer-ports.md` |
| `poll run summary` (one row per `ProcessVehicleData` invocation, recorded on every exit path incl. whole-cycle failure) | `architecture/nightly-cycle.md` |
| `SEO metatags` (the one `seoHead` component that renders every search-engine and share tag; public pages indexable, `BaseAuth` pages `noindex`) | `architecture/seo-metadata.md` |
| `metatags` | synonym of `SEO metatags` → `architecture/seo-metadata.md` |
| `meta tags` | synonym of `SEO metatags` → `architecture/seo-metadata.md` |
| `Open Graph` | synonym of `SEO metatags` → `architecture/seo-metadata.md` |
| `og tags` | synonym of `SEO metatags` → `architecture/seo-metadata.md` |
| `link preview` | synonym of `SEO metatags` → `architecture/seo-metadata.md` |
| `share card` | synonym of `SEO metatags` → `architecture/seo-metadata.md` |
| `og:image` | `seoImagePath` → `internal/gateway/static/img/magus-logo.png` → `architecture/seo-metadata.md` |
| `canonical URL` | `ui.Site.Canonical` (absolute, built from `BASE_URL`, never from the `Host` header) → `architecture/seo-metadata.md` |
| `noindex` | what every `layouts.BaseAuth` page emits → `architecture/seo-metadata.md` |
| `robots tag` | synonym of `noindex` → `architecture/seo-metadata.md` |
| `why does the shared link show the login page` | `/` 302-redirects to `/login`, so the domain's card is `/login`'s → `architecture/seo-metadata.md` |
| `robots.txt` | `handlers.RobotsTxt` / `robotsDisallow` — a route at the domain root, never a static file → `architecture/seo-metadata.md` |
| `sitemap.xml` | `handlers.SitemapXML` / `sitemapPaths` — lists `/login` only; `/` redirects → `architecture/seo-metadata.md` |
| `JSON-LD` | `seoJSONLD` (`layouts/jsonld.go`) rendered by `templ.JSONScript(...).WithType("application/ld+json")` → `architecture/seo-metadata.md` |
| `structured data` | synonym of `JSON-LD` → `architecture/seo-metadata.md` |
| `schema.org` | synonym of `JSON-LD` → `architecture/seo-metadata.md` |
| `favicon` | `layouts.faviconLinks` + `internal/gateway/static/img/favicon/` (3 files) → `architecture/seo-metadata.md` |
| `app icon` | synonym of `favicon` → `architecture/seo-metadata.md` |
| `web app manifest` | `handlers.WebManifest` (`/site.webmanifest`) — generated, so the install prompt is translated → `architecture/seo-metadata.md` |
| `site.webmanifest` | synonym of `web app manifest` → `architecture/seo-metadata.md` |
| `PWA` | synonym of `web app manifest` → `architecture/seo-metadata.md` |
| `add to home screen` | RD16 — the iOS hint that names this Share-sheet row → `architecture/gateway-client-side-js.md`; for the manifest that makes the app installable see `architecture/seo-metadata.md` |
| `run duration` | `ProcessVehicleData`'s clock-measured start-to-finish span → `architecture/nightly-cycle.md` |
| `poll run` (one `poll_runs` row per collection-cycle invocation: trigger, timing, account/vehicle outcome counts, Tesla API call count) | `architecture/telemetry-ingest-only.md` |
| `run summary` | synonym of `poll run` → `architecture/telemetry-ingest-only.md` |
| `poll_runs` | `telemetry.PollRun` / `telemetry.RunWriter.RecordRun` → `architecture/telemetry-ingest-only.md` |
| `Tesla API call count` | `CycleReport.TeslaAPICalls` (counting decorator inside `internal/telemetry`) → `architecture/telemetry-ingest-only.md` |
| `schema per module` (one PostgreSQL schema per persistence-owning `internal/` module, named after the module; RM39) | `architecture/schema-per-module.md` |
| `module schema` | synonym of `schema per module` → `architecture/schema-per-module.md` |
| `account schema` | the `account` schema holding `accounts`, `tesla_tokens`, `vehicles`, `settings` (added RM42 tier 1 — language + theme preferences) → `architecture/schema-per-module.md` |
| `charging tables` (every column, CHECK, generated column and index decision on the four tables `internal/charging` owns) | `architecture/charging-tables.md` |
| `telemetry tables` (every column, dropped column, and the units history on the four tables `internal/telemetry` owns) | `architecture/telemetry-tables.md` |
| `vehicle_snapshots` | the nightly per-vehicle snapshot — one row per (vehicle, `captured_date`) → `architecture/telemetry-tables.md` |
| `poll_attempts` | append-only audit row per (vehicle, run) → `architecture/telemetry-tables.md` |
| `supercharger_history` | the raw vendor Supercharger upsert + the human-owned battery-% trio → `architecture/telemetry-tables.md` |
| `captured_date` | Go-computed calendar day, never a DB expression → `architecture/telemetry-tables.md` |
| `raw_data` | the lossless Fleet API payload — stays in native units (miles, bar) → `architecture/telemetry-tables.md` |
| `battery_pct_source` | why a human-verified percentage trio exists; never stores `"estimated"` → `architecture/telemetry-tables.md` |
| `poll account election` (one account elected per car per cycle — prefer OWNER, never skip a car) | `architecture/telemetry-ingest-only.md` |
| `elected polling account` | synonym of `poll account election` → `architecture/telemetry-ingest-only.md` |
| `who polls a car registered to two accounts` | synonym of `poll account election` → `architecture/telemetry-ingest-only.md` |
| `why is my car polled twice` | the problem the election fixed → `architecture/telemetry-ingest-only.md` |
| `polled_by_account_id` | the `poll_attempts` column that records whose token paid for the call (was `account_id`) → `architecture/telemetry-tables.md` |
| `manual_charge_entries` | the user-asserted charge table — columns, CHECKs, the Go-side required-field set → `architecture/charging-tables.md` |
| `supercharger_sessions` | the Supercharger mirror + the human-owned percentage columns → `architecture/charging-tables.md` |
| `monthly_effective_capacity` | the measured per-vehicle pack capacity, one row per month → `architecture/charging-tables.md` |
| `mirror_watermarks` | the per-vehicle mirror cursor (`tesla_id`); never advances to `now()` → `architecture/charging-tables.md` |
| `inferred_capacity_kwh_calc` | the `GENERATED ALWAYS AS … STORED` capacity column on both charge tables → `architecture/charging-tables.md` |
| `energy_source` / `price_source` / `start_battery_source` | module-computed provenance columns, ignored when supplied by a caller → `architecture/charging-tables.md` |
| `_calc` suffix | the `<what>_<unit>_calc` naming rule for a stored derived column → `architecture/charging-tables.md` |
| `schema-qualified query` | why every `query.sql` table reference carries its schema (sqlc codegen requirement) → `architecture/schema-per-module.md` |
| `gen.go.rename` | the `sqlc.yaml` block that keeps generated Go type names stable across a schema move → `architecture/schema-per-module.md` |
| `vehicles table schema` | `account.vehicles` — the registry lives in its owning module's schema → `architecture/schema-per-module.md` |
| `analytics schema` | the `analytics` schema holding `vehicle_metrics`, `vehicle_metric_watermarks`, `charge_gaps` → `architecture/schema-per-module.md` |
| `watermark source values` | why `vehicle_metric_watermarks.source` strings are never schema-qualified → `architecture/schema-per-module.md` |
| `charging schema` | the `charging` schema holding `manual_charge_entries` and `supercharger_sessions` → `architecture/schema-per-module.md` |
| `table rename` | renaming a table and every catalog object that carries its name → `architecture/schema-per-module.md` |
| `charge_sessions rename` | why `charge_sessions` became `supercharger_sessions` (RM39 tier 3) → `architecture/schema-per-module.md` |
| `supercharger history` (`telemetry.supercharger_history` — the RAW vendor upsert; NOT charging's `supercharger_sessions` mirror) | `architecture/telemetry-ingest-only.md` |
| `telemetry schema` | the module's own Postgres schema — all four telemetry tables live there, never `public` → `architecture/telemetry-ingest-only.md` |
| `supercharger_history vs supercharger_sessions` | two different tables — telemetry's raw upsert vs charging's mirror → `architecture/telemetry-ingest-only.md` |
| `telemetry query logging` (every live query + Fleet API call logs its arguments; credentials and raw payloads never logged) | `architecture/telemetry-ingest-only.md` |
| `fleet api logging` | synonym of `telemetry query logging` → `architecture/telemetry-ingest-only.md` |
| `why is my query not logged` | the four decorated ports + the callCounter seam → `architecture/telemetry-ingest-only.md` |
| `analytics query logging` (the nightly path only — `Recalculator`, `GapWriter`, and `Reader.ConsumedByDay`; dashboard reads stay silent) | `architecture/nightly-cycle.md` |
| `analytics query:` | the log topic of `analytics query logging` → `architecture/nightly-cycle.md` |
| `mirror watermark` (the per-vehicle cursor bounding step 2 of the nightly cycle; holds telemetry's `updated_at`, owned by `internal/charging`) | `architecture/nightly-cycle.md` |
| `mirror cursor` | synonym of `mirror watermark` → `architecture/nightly-cycle.md` |
| `charging.mirror_watermarks` | the table behind `mirror watermark` → `architecture/nightly-cycle.md` |
| `who owns the mirror cursor` | the module that READS, not the one that is read → `architecture/nightly-cycle.md` |
| `charge gap ledger` | synonym of `charge gaps` — the reconcile contract → `architecture/charge-record-mutation.md` |
| `gap reconciliation` | `analytics.GapWriter.ReconcileWindow`'s own contract → `architecture/charge-record-mutation.md` |
| `deployment stack` (the five-service Docker Compose stack, its startup order, and its hardening) | `architecture/deployment-stack.md` |
| `docker compose stack` | synonym of `deployment stack` → `architecture/deployment-stack.md` |
| `container stack` | synonym of `deployment stack` → `architecture/deployment-stack.md` |
| `deploy stack` | synonym of `deployment stack` → `architecture/deployment-stack.md` |
| `deploy logs` (named, rotated log files for `web`, `poller`, and `caddy` on the VPS, and the host `logrotate` job) | `architecture/deploy-log-files.md` |
| `magus-logs` | the `~/magus-logs` host folder → `architecture/deploy-log-files.md` |
| `log rotation` | the host `logrotate` job for `web.log`/`poller.log`, and Caddy's own rotation for `caddy.log` → `architecture/deploy-log-files.md` |
| `logrotate` | synonym of `log rotation` → `architecture/deploy-log-files.md` |
| `vehicle ownership proof` (the gateway proves the signed-in user owns a vehicle once and passes the proof down; modules below do not re-check tenancy) | `architecture/vehicle-ownership-proof.md` |
| `authorization seam` | synonym of `vehicle ownership proof` → `architecture/vehicle-ownership-proof.md` |
| `vehicleref` | `internal/vehicleref` (`Ref` / `Authorize` / `All` / `TeslaIDs`) → `architecture/vehicle-ownership-proof.md` |
| `can a module check tenancy itself` | no — the gateway proves ownership, modules trust it → `architecture/vehicle-ownership-proof.md` |
| `charging_skipped_unregistered` | the cycle-log label for a session skipped because its VIN is not a registered vehicle → `architecture/telemetry-ingest-only.md` |
| `cycle report charging counters` | the three independent charging counters (upserted · fetch failures · skipped unregistered) → `architecture/telemetry-ingest-only.md` |
| `session vehicle keying` | why a Supercharger session carries `tesla_id` and no `account_id`, and why `session_id` is unique store-wide → `architecture/charging-tables.md` |
| `supercharger port scoping` | the mirror write and the three reads take `teslaID` alone; `VerifySession` takes a proof value and matches id AND vehicle → `workflows/supercharger-stats-read.md` |
| `wrong vehicle on a session write` | returns the same error an unknown id returns, on purpose → `workflows/supercharger-stats-read.md` |
| `which supercharger ports need a proof` | only the verification write; the mirror write and the three reads take a bare `teslaID` → `use-case/charging/verify-session-battery.md` |
| `why not retype the session read ports` | `internal/analytics` calls them and cannot legally build a `Ref` → `use-case/charging/verify-session-battery.md` |

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
| `bulk ownership proof` | synonym of `vehicle ownership proof` → `architecture/vehicle-ownership-proof.md` |
| `fleet-wide read` | a read scoped to every vehicle the caller owns → `architecture/vehicle-ownership-proof.md` |
| `schema migrations` (one self-contained baseline per module + a ledger private to each module; MAG-83) | `architecture/schema-migrations.md` |
| `goose per module` | synonym of `schema migrations` → `architecture/schema-migrations.md` |
| `migration baseline` | the squashed file that defines a module's whole schema — recorded as applied on existing databases, never run there → `architecture/schema-migrations.md` |
| `baseline` | synonym of `migration baseline` → `architecture/schema-migrations.md` |
| `migration ledger` | `<module>.goose_db_version` — one per module, inside that module's schema → `architecture/schema-migrations.md` |
| `goose_db_version` | synonym of `migration ledger`; `public.goose_db_version` is pre-squash history only → `architecture/schema-migrations.md` |
| `how do I add a migration` | add a numbered .sql to the owning module's `db/migrations/`; never edit a baseline → `architecture/schema-migrations.md` |
| `why did my baseline fail` | the database already holds the objects and was not registered → `architecture/schema-migrations.md` |
