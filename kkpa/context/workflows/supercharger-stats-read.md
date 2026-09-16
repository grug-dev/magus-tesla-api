# Supercharger Stats read + battery-edit write path (charging.SessionReader / SessionVerifier) — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
>
> **File name note (RM31):** this guide keeps its `-read` filename by explicit project rule
> (`ai/architecture.md`'s "docs track structural change" + the RM31 dispatch's non-negotiable:
> the file is NOT renamed) even though the concept now ALSO carries a write path — see
> "Write path" below. Read `-read` as "the guide for the Supercharger Stats concept," not as a
> claim that the concept itself is read-only.

## Glossary

- **Known as:** `supercharger stats`, `Supercharger session`, `charge session log`, `Supercharger Stats page`, `/supercharger-stats`, `fast charging stats`, `session battery edit`, `verify session battery`, `battery percentage correction`, `session battery percentages`, `supercharger chart axes`, `session status`, `session lifecycle status`, `done calculated`
- **Internal name:** `SuperchargerStatsPage` / `SuperchargerStatsFragment` / `SuperchargerRowStatic` / `SuperchargerRowEditFragment` / `SuperchargerRowUpdate` (gateway handlers) → `charging.SessionReader` (`ListSessionsByVehicleBetween`) / `charging.SessionVerifier` (`VerifySession`) / `charging.SessionWriter` — table `charging.supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3), owned by the `charging` module. **Changed by RM30** (was `telemetry.SuperchargerReader` over telemetry's own, still-`public`, `supercharger_sessions`, the raw ingestion buffer). **Extended by RM31 tier 4**: the chart carries `YYYY-MM` bar labels + reused kWh y-axis ticks, and the session table carries the record's battery-percentage columns. **Write path added by RM31 tier 5** (`RM31-gateway-add-session-battery-edit`): a signed-in user can now correct a session's `start_battery_pct`/`end_battery_pct` inline through `charging.SessionVerifier.VerifySession` — still NO Create and NO Delete (see "Write path" below). **Narrowed by RM41 tier 2** (`RM41-charging-drop-estimate-columns`, 2026-09-03): the record now carries **two** percentages plus a provenance — `start_battery_pct`, `end_battery_pct`, `battery_pct_source` — not four. The frozen estimated pair `start_battery_pct_est` / `end_battery_pct_est` no longer exists in the table, in `charging.Session`, or on the page. **Extended by RM41 tier 4** (`RM41-charging-add-session-status`, MAG-45, 2026-09-04): the record also carries a **lifecycle status** — `charging.SessionStatus` on `charging.Session.Status`, backed by `charging.supercharger_sessions.status` (TEXT + CHECK, DEFAULT `IN_PROGRESS`) — with three codes `IN_PROGRESS` / `DONE_CALCULATED` / `DONE`, computed by `VerifySession` alone. **Rendered by RM41 tier 5** (`RM41-gateway-add-session-status-column`, MAG-45, 2026-09-04): the session table shows it as a `ui.Badge` in the 2nd column — `primary` for `DONE`, `neutral` for `DONE_CALCULATED`, `ghost` for `IN_PROGRESS`, never `success`/`warning` — plus a second page-level `ui.Alert{Kind:"info"}` (`supercharger.status_help`). The gateway only reads it and adds no new query.

- **Inferred pack capacity:** `charging.Session.InferredCapacityKWhCalc` (`*float64`), backed by the DB-generated column `charging.supercharger_sessions.inferred_capacity_kwh_calc` (renamed from `charge_sessions.inferred_capacity_kwh_calc`, RM39 tier 3). Also known as `inferred capacity`.

## Component map

Files involved, grouped by layer. Each row: the file's role in this concept.

### Gateway — routes & handlers

| File | Role |
|---|---|
| `internal/gateway/gateway.go` | Registers FIVE routes: two reads — `GET /supercharger-stats` (page), `GET /ui/supercharger-stats` (fragment) — plus, since RM31, three row-level routes: `GET /ui/supercharger-stats/row/:id` (static row, e.g. Cancel target), `GET /ui/supercharger-stats/row/:id/edit` (edit-row fragment), `PATCH /ui/supercharger-stats/row/:id` (the write). Still **no DELETE route** for this concept. |
| `internal/gateway/handlers/supercharger.go` | The whole slice. Read side: `SuperchargerStatsPage` / `SuperchargerStatsFragment` (entry points), `superchargerStatsViewFor` (parse → 400-empty-no-selector → `resolveSelectedVehicle` → build), `parseSuperchargerRange` (the `?start=&end=` parser), `buildSuperchargerStatsView` (gin-free core: ONE `ListSessionsByVehicleBetween` read), `buildSuperchargerTiles` / `buildSuperchargerChart` / `buildSuperchargerRows` → `superchargerRowVMFromSession` (the one row-mapper, RM31 D11), `monthsBackFrom` / `startOfMonth` (month-aligned window math), `superchargerRangeDefaultMonths` (6) and `superchargerRangeMaxDays` (400). Write side (RM31): `SuperchargerRowStatic`, `SuperchargerRowEditFragment`, `SuperchargerRowUpdate` (the `PATCH` handler), `fetchSuperchargerRowVM` (list-and-match single-row resolve — see "Write path" below), `superchargerWindowStrs`, `recalculateAfterSessionVerify`, `csrfSuperchargerKey`. |
| `internal/gateway/handlers/handlers.go` | `Deps` wiring: `SuperchargerReader charging.SessionReader` (field NAME kept from the telemetry era; the TYPE is the charging port since RM30) and, since RM31, `SuperchargerVerifier charging.SessionVerifier` — the write port `SuperchargerRowUpdate` calls. |

### Gateway — templates (view)

| File | Role |
|---|---|
| `internal/gateway/templates/pages/supercharger_stats.templ` | Page shell (`layouts.BaseAuth`). The `#supercharger-stats-content` region carries `hx-get="/ui/supercharger-stats" hx-trigger="vehicle-changed from:body"` — vehicle-scoped refresh. |
| `internal/gateway/templates/fragments/supercharger_stats.templ` | `SuperchargerStatsView` VM + the region body: preset selector (each button's `hx-get` is a SERVER-RENDERED absolute `?start=&end=` href from `RangePreset.StartStr/EndStr`), KPI tiles, chart (delegates to the shared `historyBarChart` SVG renderer), session table rows. |
| `internal/gateway/templates/fragments/supercharger_vm.go` | `SuperchargerStatsView` (adds `CSRFToken`/`WindowStartStr`/`WindowEndStr`, RM31) and `SuperchargerRowVM` (adds `ID`/`RawStartBatteryPct`/`RawEndBatteryPct`, RM31), and `RawStatus` (RM41 tier 5) struct definitions. |
| `internal/gateway/templates/fragments/supercharger_row.templ` | `SuperchargerRow` — the static `<tr>`; its only action is Edit (`hx-get .../row/:id/edit?start=&end=`). No delete button, no `hx-confirm` — this concept has no delete affordance. Also `SuperchargerRowError`. |
| `internal/gateway/templates/fragments/supercharger_row_edit.templ` | `SuperchargerRowEdit` (RM31) — the inline edit `<tr><form hx-patch=...>`: date/site/energy/cost/both `_est` fields render read-only; ONLY `start_battery_pct`/`end_battery_pct` are `ui.Input type="number"` fields (neither `Required` — blank is a valid clear). Save (`type="submit"`) + Cancel (`hx-get` to the static row) buttons. |

### Charging module — read port (RM30 swap target)

| File | Role |
|---|---|
| `internal/charging/charging.go` | `SessionReader` interface: `ListSessionsByVehicleBetween` (bounded window, `to` INCLUSIVE of its whole day — translated to a `to+1day` half-open bound in Go, never in SQL; no limit param — the window bounds the read); `NewSessionReader(pool)`. Also `SessionVerifier` interface: `VerifySession(ctx, ref vehicleref.Ref, id, startBatteryPct, endBatteryPct *int) (Session, error)` (shipped tier 1, `RM31-charging-add-session-verification-port`; wired into the gateway by `RM31-gateway-add-session-battery-edit`) — SET clause reaches EXACTLY `start_battery_pct`, `end_battery_pct`, `battery_pct_source` and `status` (both computed, never caller-supplied) plus `updated_at` — `status` added by RM41 tier 4 (MAG-45), auto-set from the two percentages' presence and whether this write derived the start; `WHERE id = @id AND tesla_id = @tesla_id` — the caller names the vehicle it believes the session belongs to; `NewSessionVerifier(pool)`. |
| `internal/charging/session_reader.go` | `sessionReader` impl; pgtype→domain mapping stays inside the module. |
| `internal/charging/session_verifier.go` | `sessionVerifier` impl backing `VerifySession` — the query the gateway's write path calls. |
| `internal/charging/db/migrations/20260823000001_add_charge_sessions.sql` | `charging.supercharger_sessions` DDL (renamed from `charge_sessions`, RM39 tier 3) — the table the page reads (mirrored Supercharger sessions, incl. `battery_pct_source` 'user_verified'/'polled' trust labels). |
| `internal/charging/db/queries.sql` → `query.sql.go` | sqlc source of truth for the session queries; regenerate via `make sqlc`, never hand-edit. |

### Upstream — how sessions reach `charging.supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3; NOT reachable from the page)

| File | Role |
|---|---|
| `internal/telemetry/service.go` | `UpsertSuperchargerHistory` — the ONLY writer of the RAW `telemetry.supercharger_history` table (nightly collector, never the gateway). Renamed + moved out of `public` by RM39 tier 4. |
| `internal/app/processor.go` | Step 2 of `ProcessVehicleData`: `SuperchargerSessionsByVehicleUpdatedSince` (telemetry read) → mirrors new sessions into `charging` via `charging.SessionWriter` (`UpsertSession`). The page reads the mirror, not the raw table. |
| `internal/charging/charging.go` | `SessionWriter` interface + `NewSessionWriter(pool)` — the mirror's write port. |

### Wiring / composition root

| File | Role |
|---|---|
| `cmd/web/main.go` | Injects `charging.NewSessionReader(pool)` into `gateway.Deps.SuperchargerReader`, and, since RM31, `charging.NewSessionVerifier(pool)` into `gateway.Deps.SuperchargerVerifier`. |

## How maintenance works

- **Read (page load / fragment swap):** `GET /supercharger-stats` or `GET /ui/supercharger-stats?start=YYYY-MM-DD&end=YYYY-MM-DD` → auth guard (`currentUID`) → `superchargerStatsViewFor` (shared by both entry points; returns `(view, httpStatus)`): `parseSuperchargerRange` → on `ok=false` return the empty view with `Presets: nil` at HTTP 400 → `resolveSelectedVehicle` (account read; no vehicle → empty state, not an error) → `buildSuperchargerStatsView`: ONE `ListSessionsByVehicleBetween(ctx, teslaID, start, end)` call → reverse the oldest-first slice once for newest-first display → tiles + chart + rows VM. The page renders via `render` / `renderError`; the fragment via `renderFragment` / `renderFragmentError` (`"supercharger-stats"`).
- **Change the window contract (default, cap, presets):** `superchargerRangeDefaultMonths` (6), `superchargerRangeMaxDays` (400) and `superchargerMonthPresets` (`{3, 6, 12}`) are constants in `internal/gateway/handlers/supercharger.go`; `monthsBackFrom` computes month-aligned starts and `buildSuperchargerPresets` builds the selector's absolute `?start=&end=` hrefs. The template iterates `[]RangePreset`, so only the constants + tests change.
- **Add a KPI tile / table column:** `internal/gateway/handlers/supercharger.go` (`buildSuperchargerTiles` / `buildSuperchargerRows` — all formatting here) → `internal/gateway/templates/fragments/supercharger_stats.templ` (present the pre-formatted string) → i18n keys in `internal/gateway/i18n/catalog.go` (ES + EN) → `make templ && make css`. A column only exists if `charging.Session` carries the field — RM30 dropped the Country and Billing-type columns precisely because it does not.
- **Widen the readable window:** raise `superchargerRangeMaxDays` in the gateway — the port is already time-bounded, so no `charging` change is needed. Size the cap from the source table's row density, per `internal/gateway/AGENTS.md` §"HTTP date-filter convention".
- **Create a session:** NO user-facing path exists, and none is planned — sessions are ingested only by the nightly telemetry/`app` pipeline (see "Upstream" above). Do not add a gateway create handler for this concept.
- **Delete a session:** NO user-facing path exists, and none is planned (design.md Non-Goals, `RM31-gateway-add-session-battery-edit`) — `SuperchargerRow` has exactly one action button (Edit) and no `hx-confirm`. Do not add a gateway delete handler for this concept.
- **Update (write path — added by RM31, `RM31-gateway-add-session-battery-edit`):** a signed-in user CAN correct exactly two fields — `start_battery_pct` and `end_battery_pct` — inline on `/supercharger-stats`, through `charging.SessionVerifier.VerifySession`. This is the ONLY mutation this concept supports: no other column, no `battery_pct_source` and no `status` (both always computed by `VerifySession` itself, never gateway-supplied), no `*_est` column, is reachable through this or any gateway route. Three new routes (all in `internal/gateway/handlers/supercharger.go`):
  - `GET /ui/supercharger-stats/row/:id/edit` (`SuperchargerRowEditFragment`) — swaps the static row for `SuperchargerRowEdit`.
  - `GET /ui/supercharger-stats/row/:id` (`SuperchargerRowStatic`) — the Cancel target; swaps back to the static row.
  - `PATCH /ui/supercharger-stats/row/:id` (`SuperchargerRowUpdate`) — the save.
  All three resolve their target row via `fetchSuperchargerRowVM`: `resolveSelectedVehicle` → `parseSuperchargerRange(c, today)` on the REQUEST's own `?start=&end=` (not a fresh default) → `ListSessionsByVehicleBetween` → linear match on `id`. There is still NO `charging.SessionReader` by-id method — this mirrors `external_charges.go`'s `fetchEntryVM` shape exactly (design.md D1). Every row's Edit/Cancel/Save URL carries the SAME `?start=&end=` the table was rendered under (`SuperchargerStatsView.WindowStartStr`/`WindowEndStr`, design.md D2) so this resolve stays deterministic; an `id` outside that window is indistinguishable from a nonexistent one and returns `404`.
  - **Strict body semantics (design.md D3):** `SuperchargerRowUpdate` reads `start_battery_pct`/`end_battery_pct` via `c.GetPostForm` (not `c.PostForm`) specifically to distinguish "absent" from "present but empty." EITHER key absent → `HTTP 400` (`KeySuperchargerErrorMalformedBody`), `VerifySession` never called. A present key with an EMPTY value is an explicit clear (passed as `nil` to `VerifySession`) — NOT an error and NOT "leave unchanged." A present, non-empty value is range/type-validated to `[0,100]` integer BEFORE calling `VerifySession`; a violation is `HTTP 422` with a field-level error (`start_battery_pct`/`end_battery_pct`), the submitted raw value echoed back, and the port never called. Clearing BOTH percentages in one call also clears `battery_pct_source` to NULL — this is `VerifySession`'s own behavior, not a separate gateway write. NO ordering validation (`end < start` is accepted).
  - **CSRF:** a NEW session key, `csrfSuperchargerKey = "csrf_supercharger"` — distinct from `external_charges.go`'s `csrfExternalChargeKey`. Issued ONLY by `SuperchargerStatsPage`/`SuperchargerStatsFragment`; the three row-level handlers only READ it, never re-issue it (issuing a fresh token per row would invalidate any other row's already-rendered edit form in the same session).
  - **Tenant boundary:** `SuperchargerRowUpdate` now DOES carry a separate ownership check, like `external_charges.go`'s write path — after body validation it reads the session-selected vehicle (`currentVehicle`, not `resolveSelectedVehicle`'s auto-select) and proves it with `h.authorizeVehicle`, reusing the same `RegisteredVehicles` call `resolveSelectedVehicle` makes elsewhere on this page (one query, not two). `VerifySession`'s own `WHERE id = @id AND tesla_id = @tesla_id` still does the actual row match — a mismatch still surfaces as an unknown id, not a 403 — but the `tesla_id` it is given can no longer be an unproven value.
- **The recalculation hop — `recalculateAfterSessionVerify`:** on every successful `VerifySession` call, `SuperchargerRowUpdate` calls `h.recalculateAfterSessionVerify(ctx, uid, teslaID, updated.ChargeStopDateTime)`, which calls `analytics.Recalculator.Recalculate` over a **`±1 day`** window: `[day-1, day+1]` where `day = startOfDay(ChargeStopDateTime.UTC())`. This is a SEPARATE function from `external_charges.go`'s `recalculateAfterExternalChargeWrite` (single-day window) — it is NOT reused and NOT widened for this tier; the two write paths keep independent recalculation logic (design.md D4).
  - **Why `±1 day` and not a single day:** `analytics.sumSuperchargerPctBetween` buckets a session into the `vehicle_metrics` row whose `effectiveDay = CapturedDate - 1 day` (the nightly poller "describes the prior day"), so a session stopping between local midnight and ~03:30 poller-local can belong to the metric row for the day BEFORE its own `ChargeStopDateTime`'s calendar day. A naive single-day `Recalculate(day, day)` would miss that row entirely. The window is derived from `ChargeStopDateTime` (not `ChargeStartDateTime` — the aggregation itself filters on stop time) in plain UTC, not the browser-local `browser_tz` cookie (this page already commits to plain UTC for its own date math, and the gateway has no access to the poller's configured zone anyway). `±1 day` in UTC is proven (design.md D5) to always contain the true metric day even though the poller's own `effectiveDay` computation runs in ITS OWN local zone, not UTC — the two possible ±1 shifts (poller-local-vs-UTC zone offset, and the poller's own prior-day attribution) cannot stack to ±2 on the same instant. `Recalculate` is idempotent, so the two extra recomputed days cost nothing beyond one harmless extra UPSERT/DELETE pass.
  - A `Recalculate` error is logged only, never surfaced to the user — the save has already committed.

- **Change the chart's month labels or kWh axis:** everything lives in `buildSuperchargerChart` in `internal/gateway/handlers/supercharger.go`, which populates `HistoryBar.Label` (`YYYY-MM`) and calls the shared `buildYAxisTicks` with a kWh formatter. There is no Supercharger-specific tick algorithm and no chart library — the shared `HistoryChart` / `HistoryBar` / `historyBarChart` contract renders both. A zero tallest bucket keeps the bars and the vertical-label selection but yields **no** ticks; do not special-case it in the template.
- **Add or change a battery column:** `buildSuperchargerRows` maps each nullable `charging.Session` percentage through the handler's single display formatter (present ⇒ `NN%`, nil ⇒ `—`) onto `SuperchargerRowVM`, then `internal/gateway/templates/fragments/supercharger_stats.templ` renders the pre-formatted string, with the header key in `internal/gateway/i18n/catalog.go` (ES **and** EN) → `make templ`. The values come from the read the page already performs — adding a column must not add a read.

## Conventions & gotchas

- **The page reads `charging.supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3) through `charging.SessionReader` — never `telemetry`, never a live Fleet API call.** RM30 moved this path off `telemetry.SuperchargerReader`/telemetry's own, still-`public`, `supercharger_sessions` (the raw ingestion buffer) onto the record the `charging` capability owns. _Source: spec gateway — Requirement: Supercharger Stats page._
- **NEVER import `internal/charging/db` (`chargingdb`) from the gateway** — all access through the `charging.SessionReader` interface only. _Source: spec gateway — Requirement: Supercharger Stats page._
- **One bounded read per render, not a row limit.** A single `ListSessionsByVehicleBetween` call bounded by the requested window; the pre-RM30 "500-row read then filter in memory" pattern is gone. Do not reintroduce an in-memory window filter. _Source: spec gateway — Requirement: Supercharger Stats page._
- **The port returns oldest-first; the table displays newest-first.** The gateway reverses the slice once before building tiles/chart/rows. Do not "fix" the port to `DESC` — the ascending order on `charge_stop_date_time` is what lets `idx_supercharger_sessions_vehicle_stop` (renamed from `idx_charge_sessions_vehicle_stop`, RM39 tier 3, D16) serve the read with no sort. _Source: spec gateway — Requirement: Supercharger Stats page._
- **The HTTP contract is `?start=YYYY-MM-DD&end=YYYY-MM-DD`, never `?months=N`.** The 3/6/12-month presets survive as buttons, but each emits a server-computed absolute `?start=&end=` href. This is the platform's single closed date-filter vocabulary — the endpoint previously violated it. _Source: spec gateway — Requirement: Supercharger Stats month-window selector._
- **Default window is month-aligned, so a preset always renders exactly N bars.** Both params absent ⇒ `end` = today, `start` = the 1st of the month 5 months back. Each preset is computed the same way; an unaligned start would render N+1 partial monthly bars. _Source: spec gateway — Requirement: Supercharger Stats month-window selector._
- **400 rules (closed set):** missing partner, malformed non-ISO date, `end` before `start`, `end` after today, or a window wider than **400 days**. On a 400 the region renders its empty state with **no window selector**, and **no read is performed**. _Source: spec gateway — Requirement: Supercharger Stats month-window selector._
- **`end` = today is ACCEPTED here — unlike the dashboard history endpoint, which rejects it.** Charge sessions are readable the day they end; history has a nightly capture lag. This endpoint also frames "today" as plain UTC (`startOfDay(time.Now().UTC())`) and does not use the `browser_tz` cookie. Do not copy history's boundary by reflex. _Source: spec gateway — Requirement: Supercharger Stats month-window selector._
- **The 400-day cap is this endpoint's own, not a platform constant.** Caps are per-endpoint, sized by the source table's row density: `charging.supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3) is sparse (400 days), `vehicle_snapshots` is dense (90 days). A new date-filtered endpoint measures its own density rather than copying either number. _Source: spec gateway — Requirement: Supercharger Stats month-window selector._
- **Multi-currency costs are NEVER summed across currencies** — one pre-formatted cost line per currency; sessions missing cost or currency still count toward Sessions/Energy but are excluded from cost. _Source: spec gateway — Requirement: Supercharger Stats cost aggregation never sums across currencies._
- **Reader errors degrade to the empty state, never a 500.** _Source: spec gateway — Requirement: Supercharger Stats page._
- **The window drives tiles, chart and table identically** — all three reflect the same filtered slice; changing a preset re-fetches the whole region without a full page reload. _Source: spec gateway — Requirement: Supercharger Stats month-window selector._
- **The region is vehicle-scoped and refreshes on switch** — `#supercharger-stats-content` subscribes to `vehicle-changed from:body`; reads go through `resolveSelectedVehicle`, never `registered[0]`. _Source: spec gateway — Requirement: Supercharger Stats page._
- **One template tree serves both entry points** — `SuperchargerStatsPage(v)` is rendered whole for the page and via `renderFragment(…, "supercharger-stats")` for the swap; the `@templ.Fragment("supercharger-stats")` id must match. _Source: spec gateway — Requirement: Supercharger Stats page._
- **No business logic in templates** — charts are hand-rolled SVG reusing `historyBarChart`; all heights, labels and tooltip strings are pre-computed in the handler. _Source: spec gateway — Requirement: Supercharger Stats charts and tables contain no business logic in templates._
- **The sessions table is unpaginated** — the window is the bound, not a page size. _Source: spec gateway — Requirement: Supercharger Stats sessions table is unpaginated._
- **The write path is narrow by construction — only `start_battery_pct`/`end_battery_pct` are ever writable from the gateway.** No delete route, no create route, no ordering validation (`end < start` accepted), no `battery_pct_source` gateway-side write, no new `charging.SessionReader`/`SessionVerifier` method, no database object added by this write path (RM31). _Source: spec gateway — Requirement: Supercharger session battery percentages are correctable inline._
- **Row action URLs MUST carry the table's rendered `?start=&end=` window** (`SuperchargerStatsView.WindowStartStr`/`WindowEndStr`) — this is what keeps the row-level handlers' list-and-match resolve deterministic; an id outside the request's window is indistinguishable from a nonexistent one (`HTTP 404`). Do not build a row action URL without threading these two values through. _Source: spec gateway — Requirement: A Supercharger session row not in the currently requested window is treated as not found._
- **The `PATCH` body is STRICT: both keys must be present.** Use `c.GetPostForm`, never `c.PostForm`, to tell "absent" from "present but empty" apart — this is the one load-bearing distinction the whole write path hinges on. _Source: spec gateway — Requirement: Supercharger session battery edit uses a strict PATCH body._
- **`recalculateAfterSessionVerify` is a SEPARATE function from `recalculateAfterExternalChargeWrite`** — do not merge them or widen the manual-charge path's single-day window to accommodate this one. _Source: spec gateway — Requirement: A successful battery-percentage save immediately recalculates the affected vehicle's derived metrics._
- **The gateway does not import `pgx` to distinguish a 404 from a 500 on a `VerifySession` write error** — it re-resolves the row via `fetchSuperchargerRowVM` instead of inspecting the (possibly `pgx.ErrNoRows`-wrapping) error, keeping the gateway's import surface unchanged (design.md D9). _Source: `internal/gateway/handlers/supercharger.go` (`SuperchargerRowUpdate`)._
- **This write path's CSRF token (`csrf_supercharger`) and tenant-boundary check are its OWN, distinct from `external_charges.go`'s `csrf_externalcharge`/`RegisteredVehicles` shape** — see `internal/gateway/AGENTS.md` §"Exception: Supercharger session battery verification (D8 amendment)" for the full guard-order contract before touching CSRF or ownership logic here. _Source: spec gateway — Requirement: Supercharger session battery edit is CSRF-protected and account-scoped._
- **Durable Supercharger record with unalterable site name** — `charging.supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3) maintains a durable record per account and session ID. The charging site name is write-once and frozen upon creation, whereas energy delivered, total cost, currency, and payment settlement status track and update from the source on every sync pass. _Source: spec charge-session-log — Requirement: Supercharger Charge Session Record & Requirement: The Charging Site Is Fixed On Record; Energy, Cost, Currency And Payment Status Track The Source._
- **VIN is write-once; vehicle_id is refreshed per sync** — VIN is durable and write-once, while `vehicle_id` refreshes to match the account's currently registered vehicle registry (set to absent if the vehicle is no longer registered). Unregistered vehicle sessions remain retained in storage but are excluded from vehicle-scoped retrievals. _Source: spec charge-session-log — Requirement: Registered Vehicle Identifier Is Refreshed, VIN Is Not._
- **Synchronization never overwrites battery percentage verification** — `charging.supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3) carries optional verified battery percentages (start/stop), provenance ("user verified" or "polled"), and frozen estimates. Sync passes never write, clear, or overwrite these 5 verification fields. Percentage values must be between 0 and 100 inclusive and require a provenance when present. _Source: spec charge-session-log — Requirement: Battery Percentage Verification On A Charge Session._
- **One-time legacy import defaults missing provenance to "user verified"** — Previously collected sessions with verified battery percentages but missing provenance are imported with provenance set to "user verified". Sessions without percentages receive no provenance. Import is repeatable and idempotent. _Source: spec charge-session-log — Requirement: One-Time Import Of Previously Collected Sessions._
- **Human battery percentage corrections derive provenance and preserve session facts** — Human corrections to start/end battery percentages set provenance automatically to "user verified" (or clear percentages and provenance if neither percentage is supplied). Corrections touch ONLY start %, end %, and provenance, preserving all other session facts (site, energy, cost, currency, payment, frozen estimates). Out-of-bounds percentages (outside 0-100) or wrong-account attempts are rejected cleanly. _Source: spec charge-session-log — Requirement: A Charge Session's Battery Percentages Are Correctable By A Human._
- **Session retrieval interfaces support window, recency-of-update, and count queries** — Sessions can be retrieved by whole-calendar-day window (inclusive bounds, ordered earliest stop first), by recency of update/creation (ordered earliest stop first, including battery % edits), or by recency of occurrence up to a bounded count (ordered newest stop first). Unregistered vehicles are omitted from vehicle-scoped retrievals, and empty matches return empty results rather than errors. _Source: spec charge-session-log — Requirement: Charge Sessions Are Retrievable For A Vehicle Within A Time Window, Requirement: Charge Sessions Are Retrievable For A Vehicle By Recency Of Update, & Requirement: Charge Sessions Are Retrievable For A Vehicle By Recency Of Occurrence, Bounded By Count._

- **The chart reuses the shared history rendering contract — never a second tick algorithm.** `HistoryChart` / `HistoryBar` / `historyBarChart` and the existing `buildYAxisTicks` render the Supercharger chart; a second tick algorithm, chart renderer, chart library, client-side chart code, or template arithmetic is forbidden. _Source: spec gateway — Requirement: Supercharger Stats monthly chart has readable month and kWh axes._
- **Every calendar-month bucket in the window gets a `YYYY-MM` label, zero-energy months included.** Months with no sessions are zero-filled bars, never skipped — a skipped month silently misaligns the axis. _Source: spec gateway — Requirement: Supercharger Stats monthly chart has readable month and kWh axes._
- **Labels render vertically.** `YYYY-MM` is wider than the dashboard's `MM-DD`, and the labels must stay legible across the 3-, 6-, and 12-month selector windows. _Source: spec gateway — Requirement: Supercharger Stats monthly chart has readable month and kWh axes._
- **A zero tallest bucket renders NO y-axis ticks — the chart must not fabricate a positive kWh maximum.** Bars and the vertical-label selection survive; only the ticks vanish. This is the shared chart's own behavior (`buildYAxisTicks(0, …)` returns nil), not a Supercharger special case, so do not "fix" it by inventing a floor. _Source: spec gateway — Requirement: Supercharger Stats monthly chart has readable month and kWh axes._
- **The two battery columns are start % and end % — both from `charging.Session`, pre-formatted in the handler.** A present value renders as its integer percentage plus `%`; a nil value renders **exactly** `"—"`. The template presents strings only. _Source: spec gateway — Requirement: Supercharger Stats session table displays battery percentages._
- **The battery columns add NO read.** They are served from the existing `charging.SessionReader` result — no additional read, write, Tesla API call, or database query may be introduced to display them. _Source: spec gateway — Requirement: Supercharger Stats session table displays battery percentages._
- **There is no longer an estimate column in the gateway.** `start_battery_pct_est`/`end_battery_pct_est` were removed from the rendered table by `RM41-gateway-revise-battery-pct-ui` (2026-09-03) — they rendered `"—"` on every row since they shipped (no writer ever populated them) and are now gone from the sessions table, the row VM, and the i18n catalogue. `charging.Session` no longer carries either field either, since tier 2 (`RM41-charging-drop-estimate-columns`, 2026-09-03) dropped both columns from `charging.supercharger_sessions`; the gateway had already stopped naming them one tier earlier. _Source: spec gateway — Requirement: Supercharger Stats session table displays battery percentages._
- **The session table's 2nd column is a Status badge, mirroring `/external-charges`.** `ui.Badge` Kind is `primary` for `DONE`, `neutral` for `DONE_CALCULATED`, `ghost` for `IN_PROGRESS` — never `success`/`warning` (same restriction `external_charge_row.templ` follows). The badge adds no read: `Status` is already present on every `charging.Session` the existing `ListSessionsByVehicleBetween` call returns. A second page-level `ui.Alert{Kind:"info"}` (`supercharger.status_help`) tells the user to fill in an `IN_PROGRESS` session's end percentage. _Source: spec gateway — Requirement: Supercharger Stats session status badge._
- **A page-level info alert explains the battery-percentage gap.** `/supercharger-stats` renders one bilingual `ui.Alert{Kind:"info"}` (key `supercharger.battery_pct_help`), unconditionally, above the tiles/chart/table — telling the user Tesla does not supply the percentages and that the system derives the start percentage from the end when only the end is known. _Source: spec gateway — Requirement: Supercharger Stats battery percentage guidance._
- **A missing percentage is `"—"`, never `0%` and never an empty cell.** `0%` is a real, meaningful reading; conflating it with "unknown" would misreport a fully-drained arrival. _Source: spec gateway — Requirement: Supercharger Stats session table displays battery percentages._
- **Country stays gone — do not restore the column, the cell, or a Country i18n key.** The table retains Date, Site, Energy and Cost only; `charging.Session` carries no `CountryCode` (RM29 D1) and RM30 removed the column deliberately. _Source: spec gateway — Requirement: Supercharger Stats session table displays battery percentages._
- **Every new header resolves through the i18n catalogue with non-empty ES *and* EN.** A hardcoded or Spanish-only header is incomplete work here exactly as everywhere else in the gateway. _Source: spec gateway — Requirement: Supercharger Stats session table displays battery percentages._
- **A saved battery-percentage edit must NOT refresh the KPI tiles or the chart.** The swap replaces the row and nothing else: session count, energy, cost and average kWh/session, and every chart bar, are derived from energy and cost — never from a battery percentage — so re-rendering the region on save would be pure churn presented to the user as a change. _Source: spec gateway — Requirement: Supercharger session battery percentages are correctable inline._

- **The inferred pack capacity is derived by the database, never by Go.** It is a
  `GENERATED ALWAYS AS (…) STORED` column, so it is correct on every write path with no caller
  action. Do not add a Go-side computation and do not name the column in any write.
  _Source: spec charge-session-log — Requirement: Inferred Pack Capacity Is Recorded On Every Charge Session._
- **Two independent paths keep it fresh, and neither one names it.** The nightly mirror's
  `ON CONFLICT DO UPDATE SET` refresh of `energy_kwh` as Tesla's fees settle, and a human
  correcting the percentages through `charging.SessionVerifier.VerifySession`. The value
  recomputes from opposite directions without either path knowing it exists — including on the
  `charging.Session` that `VerifySession` itself returns, because the query is `RETURNING *`.
  _Source: spec charge-session-log — Requirement: Inferred Pack Capacity Is Recorded On Every Charge Session._
- **`SessionMirror` deliberately has no field for it, exactly as it has none for the verified
  percentages.** That is not an omission to "fix" — the mirror must not be able to write either.
  _Source: spec charge-session-log — Requirement: Inferred Pack Capacity Is Recorded On Every Charge Session._
- **Expect mostly `nil` on this table.** The value needs the verified percentages, which only
  exist for sessions a human has verified, plus a non-`NULL` `energy_kwh` (a session with no kWh
  fee has none). An unverified session legitimately records no capacity.
  _Source: spec charge-session-log — Requirement: Inferred Pack Capacity Is Recorded On Every Charge Session._
- **A recorded absence must never fail a synchronization pass.** The guard (all three inputs
  present **and** end % strictly greater than start %) exists partly to protect the nightly
  mirror: an equal delta would be a division by zero, and because one bad row rejects the whole
  `MirrorSessions` call, that error would abort the entire night's sync for that batch. The
  column's type is unconstrained `NUMERIC` for the same reason — `energy_kwh` is vendor-controlled
  `DOUBLE PRECISION` with no `CHECK`, so any fixed precision could overflow on data the project
  does not own and take down the sync.
  _Source: spec charge-session-log — Requirement: Inferred Pack Capacity Is Recorded On Every Charge Session._
- **Do not narrow the column's type.** If a test asserting a very large capacity (energy `1e9`
  over a 1-point delta) ever starts failing, someone added a precision constraint and reintroduced
  the abort-the-nightly-mirror risk.
  _Source: spec charge-session-log — Requirement: Inferred Pack Capacity Is Recorded On Every Charge Session._

- **A charge session record carries exactly two battery percentages plus one provenance — never a frozen estimated pair.** `start_battery_pct`, `end_battery_pct` and `battery_pct_source` are the whole verification channel. The frozen estimated pair a prior revision also stored was removed because no code path in the platform ever wrote it, and the estimator originally meant to populate it (`derivedStartBatteryPct`) writes the real verified start percentage instead. Do not re-add an estimate column believing it is merely unimplemented — it was deliberately reversed.
  _Source: spec charge-session-log — Requirement: Battery Percentage Verification On A Charge Session._
- **Any recorded percentage is 0–100 inclusive, provenance is "user verified" or "polled", and a record carrying either percentage must also carry a provenance.** The constraint is enforced in the same statement that sets or clears the percentages, so no intermediate row state can violate it.
  _Source: spec charge-session-log — Requirement: Battery Percentage Verification On A Charge Session._
- **Synchronizing charge session records never writes, clears or overwrites the verification channel** — not even when the same sync pass updates that record's registered vehicle identifier or its energy, cost, currency or payment facts. The nightly sync's own type has no field to bind one to, so a reversal of this rule would fail to compile rather than corrupt data silently.
  _Source: spec charge-session-log — Requirement: Battery Percentage Verification On A Charge Session._
- **A retrieved record carries every fact the capability holds, so a caller needs no second request to describe the session** — site, energy, cost, currency, payment status, the two verified percentages, their provenance, and the lifecycle status. When adding a fact to the table, add it to the read port too, or you reintroduce the follow-up request this rule exists to prevent.
  _Source: spec charge-session-log — Requirement: Charge Sessions Are Retrievable For A Vehicle Within A Time Window._
- **A correction changes the start percentage, the end percentage, the provenance and the lifecycle status, and nothing else** — identity, time window, site, energy, cost, currency and payment facts are unaffected however many times a correction runs, and whether the start percentage was supplied or derived. This list no longer names a frozen estimated pair.
  _Source: spec charge-session-log — Requirement: A Charge Session's Battery Percentages Are Correctable By A Human._
- **A correction naming a non-existent record and one naming another account's record are rejected identically** — the two cases are indistinguishable to the caller by design, so a probe cannot use the error to discover whether a session id exists.
  _Source: spec charge-session-log — Requirement: A Charge Session's Battery Percentages Are Correctable By A Human._
- **The lifecycle status is a function of the two percentages, and the ONLY place the derived/supplied distinction survives.** Either percentage absent ⇒ `IN_PROGRESS` — including a record carrying only a start, and one carrying only an end whose start could not be derived. Both present ⇒ `DONE_CALCULATED` when *this write* derived the start, `DONE` in every other case. Provenance deliberately does NOT record that distinction (a derived start is still `user_verified`), so the status is the one signal that tells the two apart. Do not try to recover it from `battery_pct_source`.
  _Source: spec charge-session-log — Requirement: A Charge Session Carries A Lifecycle Status._
- **The status is recomputed on EVERY correction, never left at its prior value — including a correction that clears both percentages, which resets it to `IN_PROGRESS`.** A `DONE_CALCULATED` record whose start is later supplied directly becomes `DONE`. Treat the column as a cache of the current percentages, not as a historical fact about the record: nothing in it is meant to survive a correction that contradicts it.
  _Source: spec charge-session-log — Requirement: A Charge Session Carries A Lifecycle Status._
- **The status is never an input.** No caller — gateway route, mirror, or sync pass — may supply it; `VerifySession` computes it, and `MirrorSuperchargerSession` deliberately excludes it from both its INSERT column list and its `ON CONFLICT DO UPDATE SET`, so a freshly mirrored session takes `IN_PROGRESS` from the column DEFAULT. Adding it to the mirror to "complete the pattern" would let ingestion overwrite a human's verified state.
  _Source: spec charge-session-log — Requirement: A Charge Session Carries A Lifecycle Status._

- **The Status column pairs a `ui.Dot` WITH the text `ui.Badge`, and both survive on a phone.** The spec as written (`RM41-gateway-add-session-status-column`) forbade the Dot, on the grounds that the status already is the completeness signal and a Dot would restate it in a second colour vocabulary. **MAG-46 step 4.2 reversed that**: the badge shrinks (`CompactOnMobile`) instead of being hidden, and the Dot carries the meaning that colour alone cannot — `title` tooltips never fire on touch, so a phone user would otherwise get colour with no label. Note the two vocabularies differ on purpose: the **Dot** is `success`/`neutral`/`warning`, the **Badge** is `primary`/`neutral`/`ghost`. Do not "align" them, and do not delete the Dot on the authority of the spec sentence — it is superseded.
  _Source: `internal/gateway/templates/fragments/supercharger_row.templ` (doc comment, MAG-46 step 4.2); supersedes spec gateway — Requirement: Supercharger Stats session status badge._
- **`Status` is read-only from the gateway's side — no route may set or change it.** It is always computed by `charging.SessionVerifier.VerifySession`. The inline edit form carries no status field and must never gain one: the gateway supplies the two percentages, and the status follows from them inside `charging`. A gateway-supplied status would let the UI contradict the record.
  _Source: spec gateway — Requirement: Supercharger Stats session status badge._
- **The edit row must span the FULL table width, so its `colspan` tracks the column count.** `SuperchargerRowEdit` renders the whole form in one cell, and `SuperchargerRowError` does the same for errors — both are `colspan="8"` since the Status column landed. Adding or removing a column means updating BOTH, in two different files (`supercharger_row.templ` and `supercharger_row_edit.templ`). RM41 tier 5 nearly shipped with only one of them fixed; the edit row would have rendered one column narrow on every Edit click.
  _Source: spec gateway — Requirement: Supercharger Stats in-progress session guidance & the change's design.md "Finding: a second colspan"._

- **`updated_at` on a charge session record means "this row's data changed", not "the last
  sync touched this row".** A synchronization pass that writes the same energy, cost,
  currency, payment status and registered vehicle identifier the record already holds does
  NOT advance it. Before MAG-48 every pass advanced it, which made `internal/analytics`
  recalculate the whole history every night.
  _Source: spec charge-session-log — Requirement: Charge Sessions Are Retrievable For A Vehicle By Recency Of Update._

- **A human battery-% correction and a sync pass can never be mistaken for each other, in
  either direction.** The comparison neither reads nor is influenced by the record's verified
  percentages or its lifecycle status. So a human correction is never read as a sync change,
  and a sync pass never looks like a human correction.
  _Source: spec charge-session-log — Requirement: The Charge Session Log Is Synchronized From The Source._

- **The registered vehicle identifier going from absent to present DOES count as a change.**
  That is how a session becomes visible again once its vehicle is re-registered. If you ever
  exclude this field from the comparison, the orphan-recovery path breaks silently.
  _Source: spec charge-session-log — Requirement: The Registered Vehicle Identifier Is Refreshed On Every Synchronization._

- **A source disagreement on the charging site is NOT a change — on any night, not just the
  first.** The record keeps its original site name, and the disagreement is not treated as
  new information. This matters because the site name is never updated after first recording:
  if it were compared, the mismatch could never resolve, and the record would look modified
  every single night forever.
  _Source: spec charge-session-log — Requirement: The Charging Site Is Fixed On Record._

- **Rule to keep this correct when you change the sync:** the change comparison covers exactly
  the columns the synchronization writes, and nothing else. Add a column to what the sync
  writes → include it in the comparison. Add a column the sync does not write → exclude it.
  `internal/charging/db_mirror_schema_selfcheck_integration_test.go` fails the moment a live
  column belongs to neither list, and names the column plus which list to fix.
  _Source: spec charge-session-log — Requirement: The Charge Session Log Is Synchronized From The Source._

- **The mirror reads a BOUNDED window, not the whole history.** It asks the source only for
  sessions modified at or after this vehicle's watermark, widened slightly to tolerate a source
  write that commits just after the previous run read. Before MAG-48 it re-read every session
  every night, which is what defeated every downstream cursor.
  _Source: spec charge-session-log — Requirement: Supercharger Mirror Synchronization Is Bounded By A Vehicle Watermark._

- **The watermark is per VEHICLE, and the per-account cursor it replaced is retired.** An account
  with two cars held one cursor and needed two — the stored instant could not say how far each
  car had progressed. Synchronization now runs once per distinct vehicle across the platform, not
  once per account, so a car with two registered drivers is mirrored once per run, not twice.
  The requirement was renamed to match (MAG-70): "An Account Watermark" → "A Vehicle Watermark".
  _Source: spec charge-session-log — Requirement: Supercharger Mirror Synchronization Is Bounded By A Vehicle Watermark._

- **THE most important rule in this capability: a run whose bounded read returns nothing leaves
  the watermark completely untouched.** Never advance it to "now". A session the source commits
  moments after the read would fall permanently behind the cursor and never be picked up again.
  The loss is silent and undetectable — nothing errors, nothing logs, the row simply never
  arrives. If you change this code, this is the line to protect.
  _Source: spec charge-session-log — Requirement: Supercharger Mirror Synchronization Is Bounded By A Vehicle Watermark._

- **A run that DOES return sessions advances the watermark to the highest last-modified instant
  actually observed — never to the run's own instant.** Same reason: advancing to "now" skips
  anything the source commits between the read and the advance.
  _Source: spec charge-session-log — Requirement: Supercharger Mirror Synchronization Is Bounded By A Vehicle Watermark._

- **The watermark advances only AFTER the mirroring step succeeds.** A failed run leaves it
  where it was, so the next run's bounded read still covers what the failed one did not write.
  That repeats work; it never loses a row. Prefer that trade every time.
  _Source: spec charge-session-log — Requirement: Supercharger Mirror Synchronization Is Bounded By A Vehicle Watermark._

- **A vehicle with no watermark backfills its whole history once**, then advances normally.
  So the bounded read costs nothing on first deploy and needs no migration or manual seeding.
  The cursor is per vehicle, so a car with two registered drivers still backfills once, not twice.
  _Source: spec charge-session-log — Requirement: Supercharger Mirror Synchronization Is Bounded By A Vehicle Watermark._

- **A session whose vehicle is not registered is NEVER stored — it is skipped and counted.**
  `tesla_id` is `NOT NULL`, so there is no row to write. The nightly cycle reports the skip in
  its `CycleReport` rather than swallowing it. The older advice here said the opposite: it told
  you to keep an account-wide updated-since port so such sessions could be recovered. That port
  no longer exists, and recovery through it is not a behaviour to restore.
  _Source: spec charging — Requirement: Supercharger Session Vehicle Keying._

- **A session is keyed on the vehicle, never on an account.** `charging.supercharger_sessions`
  carries `tesla_id NOT NULL` and no `account_id` column. Which cars a user may see is recorded
  once, by the account module's vehicle registry. Do not add an account column back to scope a
  read — scope it by `tesla_id`.
  _Source: spec charging — Requirement: Supercharger Session Vehicle Keying._

- **One `session_id` is one stored row, store-wide.** Uniqueness is `UNIQUE (session_id)`, not a
  pair. A Supercharger session happened to exactly one car, so two rows for one `session_id`
  would be two records of one event. Re-mirroring the same `session_id` under a different
  vehicle updates the row to the newest vehicle; it never inserts a second one.
  _Source: spec charging — Requirement: Supercharger Session Vehicle Keying._

- **When the re-key had to collapse a duplicated pair, the copy with human-entered percentages
  wins.** Every other column is re-derived from the mirrored source on the next sync, so the
  hand-entered battery percentages are the only value a delete could destroy.
  _Source: spec charging — Requirement: Supercharger Session Vehicle Keying._

- **Every public Supercharger port takes `teslaID int64` and no account id.** This covers the
  mirror write, the three session reads, and the verification write. A port that still asks for
  an account id is stale code, not a second scoping style.
  _Source: spec charging — Requirement: Supercharger Port Vehicle Scoping._

- **`VerifySession` keeps a scope — it did not lose one.** It matches on BOTH `id` AND
  `tesla_id`. Naming a vehicle the session does not belong to changes nothing and returns the
  same error an unknown id returns, so "not yours" and "does not exist" stay indistinguishable
  from outside. Never replace that predicate with an id-only match.
  _Source: spec charging — Requirement: Supercharger Port Vehicle Scoping._

- **The mirror write validates no owning account, and an empty set stays a successful no-op.**
  No session carries an account, so there is nothing to check across the batch.
  _Source: spec charging — Requirement: Supercharger Port Vehicle Scoping._

## Related KB

- Features: (none yet)
- Workflows: `workflows/manual-charge-crud.md` (a sibling user-write path — full Create/Update/Delete over `manual_charge_entries`, vs. this concept's single narrow correction of two fields over an existing `charging.supercharger_sessions` row, with no Create and no Delete)
- Architecture: `architecture/telemetry-ingest-only.md` (the raw, telemetry-owned `telemetry.supercharger_history` upstream + the nightly mirror into `charging.supercharger_sessions`, renamed from `charge_sessions`, RM39 tier 3)
- Architecture: `architecture/charge-record-mutation.md` — the contract this concept's battery-percentage write shares with the manual charge write path (affected period → centralized recalculation → persist → gaps), and the documented divergences between the two implementations
- Use cases: `use-case/charging/verify-session-battery.md`
- Input ports: `input-port/charging/supercharger-stats.md`
