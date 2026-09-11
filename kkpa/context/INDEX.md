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
| `price source` | `charging.PriceSource` (`USER` / `UNCONFIRMED`) / `manual_charge_entries.price_source` — module-computed, never caller-supplied | entity | `workflows/manual-charge-crud.md` |
| `price provenance` | synonym of `price source` | entity | `workflows/manual-charge-crud.md` |
| `zero price confirmation` | synonym of `price source` | entity | `workflows/manual-charge-crud.md` |
| `battery level by day` | `analytics.Reader.BatteryLevelByDay` / `analytics.DayBattery` (`vehicle_metrics.battery_level_pct`, `battery_range_km`) | entity | `entities/vehicle-metrics/guide.md` |
| `per-day battery level` | synonym of `battery level by day` | entity | `entities/vehicle-metrics/guide.md` |
| `battery history` | synonym of `battery level by day` | entity | `entities/vehicle-metrics/guide.md` |
| `battery chart` | `buildBatteryChart` / `analytics.Reader.BatteryLevelByDay` (since RM40; previously `telemetry.Reader`) | entity | `use-case/gateway/read-dashboard-history.md` |
| `battery history chart` | synonym of `battery chart` | entity | `use-case/gateway/read-dashboard-history.md` |
| `watermark vocabulary` | the closed set of table names `vehicle_metric_watermarks.source` may hold, and how it is migrated → `entities/vehicle-metrics/guide.md` |
| `vocabulary migration` | retiring a watermark source value when another module renames its table → `entities/vehicle-metrics/guide.md` |
| `supercharger history change detection` | when a nightly sync counts as a change to a `telemetry.supercharger_history` row (`updated_at`) | entity | `architecture/telemetry-ingest-only.md` |
| `why does updated_at change every night` | the MAG-48 symptom, at the telemetry layer | entity | `architecture/telemetry-ingest-only.md` |
| `account-wide updated-since read` | `telemetry.SuperchargerHistoryReader.SuperchargerHistoryByAccountUpdatedSince` — the only updated-since port that returns sessions with no registered vehicle | entity | `architecture/telemetry-ingest-only.md` |
| `orphaned supercharger session` | a session whose vehicle is not currently registered; reachable only through the account-wide updated-since port | entity | `architecture/telemetry-ingest-only.md` |
| `session change detection` | when a nightly sync counts as a modification of a charge session record (`updated_at`) | entity | `workflows/supercharger-stats-read.md` |
| `why did every session recalculate` | the MAG-48 symptom — `updated_at` used to advance on every sync pass | entity | `workflows/supercharger-stats-read.md` |
| `bounded mirror read` | the watermark-bounded Supercharger sync (RM44 tier 4); replaced the full-history read | entity | `workflows/supercharger-stats-read.md` |
| `why is the mirror slow` | it used to read all history every night — MAG-48, fixed by the bounded read | entity | `workflows/supercharger-stats-read.md` |
| `tire pressure` | the four `tpms_pressure_*_psi` columns of `vehicle_metrics` (RM50 tier 1), raw per-day observations read via `analytics.Reader.LatestMetricsByAccount` | entity | `entities/vehicle-metrics/guide.md` |
| `tyre pressure` | synonym of `tire pressure` | entity | `entities/vehicle-metrics/guide.md` |
| `TPMS` | synonym of `tire pressure` (tire-pressure monitoring system) | entity | `entities/vehicle-metrics/guide.md` |
| `tyre pressure delta` | the four `tpms_pressure_*_psi_calc` columns of `vehicle_metrics` (RM50 tier 3) — each day's wheel pressure minus the previous day's, NULL without a predecessor or a raw reading | entity | `entities/vehicle-metrics/guide.md` |
| `tyre pressure variance` | synonym of `tyre pressure delta` | entity | `entities/vehicle-metrics/guide.md` |
| `pressure change` | synonym of `tyre pressure delta` | entity | `entities/vehicle-metrics/guide.md` |
| `travel progress` | UI name for `vehicle_metrics.distance_traveled_km_calc`, exposed on `analytics.VehicleStatus.DistanceTraveledKmCalc` (RM50) | entity | `entities/vehicle-metrics/guide.md` |
| `battery drain` | UI name for `vehicle_metrics.consumed_pct`, exposed on `analytics.VehicleStatus.ConsumedPct` (RM50) | entity | `entities/vehicle-metrics/guide.md` |

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
| `pack capacity` | `charging`'s `packCapacityKWh` + its `62.0` fallback → `workflows/vehicle-monthly-metrics.md` (the `analytics` `car_type` table is a different thing — see that guide's last section) |
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
| `client-side JS` (the gateway's zero-JS rule RD8 and its six sanctioned exceptions) | `architecture/gateway-client-side-js.md` |
| `zero-JS rule` | synonym of `client-side JS` → `architecture/gateway-client-side-js.md` |
| `app.js` | `internal/gateway/static/app.js` → `architecture/gateway-client-side-js.md` |
| `sanctioned exception` | synonym of `client-side JS` → `architecture/gateway-client-side-js.md` |
| `confirm modal` | RD10 — `ui.ConfirmDialog` / the `htmx:confirm` listener → `architecture/gateway-client-side-js.md` |
| `confirmation dialog` | synonym of `confirm modal` → `architecture/gateway-client-side-js.md` |
| `browser_tz cookie` | RD9 — the inline script in `layouts.BaseAuth` → `architecture/gateway-client-side-js.md` |
| `timezone cookie` | synonym of `browser_tz cookie` → `architecture/gateway-client-side-js.md` |
| `theme instant apply` | RD15 — the `click` + `htmx:afterRequest` listener pair → `architecture/gateway-client-side-js.md` |
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
| `gateway write exceptions` (the closed list of handlers allowed to break the gateway's read-only rule, their guard sets, and why three of them deliberately differ) | `architecture/gateway-write-exceptions.md` |
| `write aperture` | synonym of `gateway write exceptions` → `architecture/gateway-write-exceptions.md` |
| `read-only rule` | the gateway default — handlers call `Reader` ports only → `architecture/gateway-write-exceptions.md` |
| `CSRF` | the four session keys + `checkCSRFKey`'s fail-closed contract → `architecture/gateway-write-exceptions.md` |
| `csrf_theme` / `csrf_supercharger` / `csrf_externalcharge` / `csrf_vehicle_select` | the four CSRF session keys, one per form → `architecture/gateway-write-exceptions.md` |
| `tenant ownership check` | `RegisteredVehicles` on the submitted `(TeslaID, VIN)` — and the two apertures that deliberately skip it → `architecture/gateway-write-exceptions.md` |
| `language switch` | `LangSwitch` — no CSRF by user-approved decision; `SameSite=Lax` is the defence → `architecture/gateway-write-exceptions.md` |
| `theme switch` | `ThemeSwitch` — auth + CSRF, cookie written only after the persist → `architecture/gateway-write-exceptions.md` |
| `SameSite` | why the `lang` cookie's `SameSite=Lax` is mandatory → `architecture/gateway-write-exceptions.md` |
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
| `add to home screen` | synonym of `web app manifest` → `architecture/seo-metadata.md` |
| `run duration` | `ProcessVehicleData`'s clock-measured start-to-finish span → `architecture/nightly-cycle.md` |
| `poll run` (one `poll_runs` row per collection-cycle invocation: trigger, timing, account/vehicle outcome counts, Tesla API call count) | `architecture/telemetry-ingest-only.md` |
| `run summary` | synonym of `poll run` → `architecture/telemetry-ingest-only.md` |
| `poll_runs` | `telemetry.PollRun` / `telemetry.RunWriter.RecordRun` → `architecture/telemetry-ingest-only.md` |
| `Tesla API call count` | `CycleReport.TeslaAPICalls` (counting decorator inside `internal/telemetry`) → `architecture/telemetry-ingest-only.md` |
| `schema per module` (one PostgreSQL schema per persistence-owning `internal/` module, named after the module; RM39) | `architecture/schema-per-module.md` |
| `module schema` | synonym of `schema per module` → `architecture/schema-per-module.md` |
| `account schema` | the `account` schema holding `accounts`, `tesla_tokens`, `vehicles`, `settings` (added RM42 tier 1 — language + theme preferences) → `architecture/schema-per-module.md` |
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
| `mirror watermark` (the per-account cursor bounding step 2 of the nightly cycle; holds telemetry's `updated_at`, owned by `internal/charging`) | `architecture/nightly-cycle.md` |
| `mirror cursor` | synonym of `mirror watermark` → `architecture/nightly-cycle.md` |
| `charging.mirror_watermarks` | the table behind `mirror watermark` → `architecture/nightly-cycle.md` |
| `who owns the mirror cursor` | the module that READS, not the one that is read → `architecture/nightly-cycle.md` |
| `deployment stack` (the five-service Docker Compose stack, its startup order, and its hardening) | `architecture/deployment-stack.md` |
| `docker compose stack` | synonym of `deployment stack` → `architecture/deployment-stack.md` |
| `container stack` | synonym of `deployment stack` → `architecture/deployment-stack.md` |
| `deploy stack` | synonym of `deployment stack` → `architecture/deployment-stack.md` |

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
