# Supercharger Stats read path (charging.SessionReader) — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.

## Glossary

- **Known as:** `supercharger stats`, `Supercharger session`, `charge session log`, `Supercharger Stats page`, `/supercharger-stats`, `fast charging stats`
- **Internal name:** `SuperchargerStatsPage` / `SuperchargerStatsFragment` (gateway handlers) → `charging.SessionReader` (`ListSessionsByVehicleBetween`) / `charging.SessionWriter` — table `charge_sessions`, owned by the `charging` module. **Changed by RM30** (was `telemetry.SuperchargerReader` over `supercharger_sessions`, the raw ingestion buffer).

## Component map

Files involved, grouped by layer. Each row: the file's role in this concept.

### Gateway — routes & handlers

| File | Role |
|---|---|
| `internal/gateway/gateway.go` | Registers the ONLY two routes: `GET /supercharger-stats` (page) and `GET /ui/supercharger-stats` (fragment). No write routes exist. |
| `internal/gateway/handlers/supercharger.go` | The whole slice: `SuperchargerStatsPage` / `SuperchargerStatsFragment` (entry points), `superchargerStatsViewFor` (parse → 400-empty-no-selector → `resolveSelectedVehicle` → build), `parseSuperchargerRange` (the `?start=&end=` parser), `buildSuperchargerStatsView` (gin-free core: ONE `ListSessionsByVehicleBetween` read), `buildSuperchargerTiles` / `buildSuperchargerChart` / `buildSuperchargerRows`, `monthsBackFrom` / `startOfMonth` (month-aligned window math), `superchargerRangeDefaultMonths` (6) and `superchargerRangeMaxDays` (400). |
| `internal/gateway/handlers/handlers.go` | `Deps` wiring: `SuperchargerReader charging.SessionReader` (field NAME kept from the telemetry era; the TYPE is the charging port since RM30). |

### Gateway — templates (view)

| File | Role |
|---|---|
| `internal/gateway/templates/pages/supercharger_stats.templ` | Page shell (`layouts.BaseAuth`). The `#supercharger-stats-content` region carries `hx-get="/ui/supercharger-stats" hx-trigger="vehicle-changed from:body"` — vehicle-scoped refresh. |
| `internal/gateway/templates/fragments/supercharger_stats.templ` | `SuperchargerStatsView` VM + the region body: preset selector (each button's `hx-get` is a SERVER-RENDERED absolute `?start=&end=` href from `RangePreset.StartStr/EndStr`), KPI tiles, chart (delegates to the shared `historyBarChart` SVG renderer), session table rows. |

### Charging module — read port (RM30 swap target)

| File | Role |
|---|---|
| `internal/charging/charging.go` | `SessionReader` interface: `ListSessionsByVehicleBetween` (bounded window, `to` INCLUSIVE of its whole day — translated to a `to+1day` half-open bound in Go, never in SQL; no limit param — the window bounds the read); `NewSessionReader(pool)`. |
| `internal/charging/session_reader.go` | `sessionReader` impl; pgtype→domain mapping stays inside the module. |
| `internal/charging/db/migrations/20260823000001_add_charge_sessions.sql` | `charge_sessions` DDL — the table the page reads (mirrored Supercharger sessions, incl. `battery_pct_source` 'user_verified'/'polled' trust labels). |
| `internal/charging/db/queries.sql` → `query.sql.go` | sqlc source of truth for the session queries; regenerate via `make sqlc`, never hand-edit. |

### Upstream — how sessions reach `charge_sessions` (NOT reachable from the page)

| File | Role |
|---|---|
| `internal/telemetry/service.go` | `UpsertSuperchargerSession` — the ONLY writer of the RAW `supercharger_sessions` table (nightly collector, never the gateway). |
| `internal/app/processor.go` | Step 2 of `ProcessVehicleData`: `SuperchargerSessionsByVehicleUpdatedSince` (telemetry read) → mirrors new sessions into `charging` via `charging.SessionWriter` (`UpsertSession`). The page reads the mirror, not the raw table. |
| `internal/charging/charging.go` | `SessionWriter` interface + `NewSessionWriter(pool)` — the mirror's write port. |

### Wiring / composition root

| File | Role |
|---|---|
| `cmd/web/main.go` | Injects `charging.NewSessionReader(pool)` into `gateway.Deps.SuperchargerReader`. |

## How maintenance works

- **Read (page load / fragment swap):** `GET /supercharger-stats` or `GET /ui/supercharger-stats?start=YYYY-MM-DD&end=YYYY-MM-DD` → auth guard (`currentUID`) → `superchargerStatsViewFor` (shared by both entry points; returns `(view, httpStatus)`): `parseSuperchargerRange` → on `ok=false` return the empty view with `Presets: nil` at HTTP 400 → `resolveSelectedVehicle` (account read; no vehicle → empty state, not an error) → `buildSuperchargerStatsView`: ONE `ListSessionsByVehicleBetween(ctx, uid, teslaID, start, end)` call → reverse the oldest-first slice once for newest-first display → tiles + chart + rows VM. The page renders via `render` / `renderError`; the fragment via `renderFragment` / `renderFragmentError` (`"supercharger-stats"`).
- **Change the window contract (default, cap, presets):** `superchargerRangeDefaultMonths` (6), `superchargerRangeMaxDays` (400) and `superchargerMonthPresets` (`{3, 6, 12}`) are constants in `internal/gateway/handlers/supercharger.go`; `monthsBackFrom` computes month-aligned starts and `buildSuperchargerPresets` builds the selector's absolute `?start=&end=` hrefs. The template iterates `[]RangePreset`, so only the constants + tests change.
- **Add a KPI tile / table column:** `internal/gateway/handlers/supercharger.go` (`buildSuperchargerTiles` / `buildSuperchargerRows` — all formatting here) → `internal/gateway/templates/fragments/supercharger_stats.templ` (present the pre-formatted string) → i18n keys in `internal/gateway/i18n/catalog.go` (ES + EN) → `make templ && make css`. A column only exists if `charging.Session` carries the field — RM30 dropped the Country and Billing-type columns precisely because it does not.
- **Widen the readable window:** raise `superchargerRangeMaxDays` in the gateway — the port is already time-bounded, so no `charging` change is needed. Size the cap from the source table's row density, per `internal/gateway/AGENTS.md` §"HTTP date-filter convention".
- **Create / Update / Delete a session:** NO user-facing path exists — by design. Sessions are written by the analytics recalculation path into `charge_sessions`; the gateway registers only the two GETs. Do not add a gateway write handler for this concept.

## Conventions & gotchas

- **The page reads `charge_sessions` through `charging.SessionReader` — never `telemetry`, never a live Fleet API call.** RM30 moved this path off `telemetry.SuperchargerReader`/`supercharger_sessions` (the raw ingestion buffer) onto the record the `charging` capability owns. _Source: spec gateway — Requirement: Supercharger Stats page._
- **NEVER import `internal/charging/db` (`chargingdb`) from the gateway** — all access through the `charging.SessionReader` interface only. _Source: spec gateway — Requirement: Supercharger Stats page._
- **One bounded read per render, not a row limit.** A single `ListSessionsByVehicleBetween` call bounded by the requested window; the pre-RM30 "500-row read then filter in memory" pattern is gone. Do not reintroduce an in-memory window filter. _Source: spec gateway — Requirement: Supercharger Stats page._
- **The port returns oldest-first; the table displays newest-first.** The gateway reverses the slice once before building tiles/chart/rows. Do not "fix" the port to `DESC` — the ascending order on `charge_stop_date_time` is what lets `idx_charge_sessions_vehicle_stop` serve the read with no sort. _Source: spec gateway — Requirement: Supercharger Stats page._
- **The HTTP contract is `?start=YYYY-MM-DD&end=YYYY-MM-DD`, never `?months=N`.** The 3/6/12-month presets survive as buttons, but each emits a server-computed absolute `?start=&end=` href. This is the platform's single closed date-filter vocabulary — the endpoint previously violated it. _Source: spec gateway — Requirement: Supercharger Stats month-window selector._
- **Default window is month-aligned, so a preset always renders exactly N bars.** Both params absent ⇒ `end` = today, `start` = the 1st of the month 5 months back. Each preset is computed the same way; an unaligned start would render N+1 partial monthly bars. _Source: spec gateway — Requirement: Supercharger Stats month-window selector._
- **400 rules (closed set):** missing partner, malformed non-ISO date, `end` before `start`, `end` after today, or a window wider than **400 days**. On a 400 the region renders its empty state with **no window selector**, and **no read is performed**. _Source: spec gateway — Requirement: Supercharger Stats month-window selector._
- **`end` = today is ACCEPTED here — unlike the dashboard history endpoint, which rejects it.** Charge sessions are readable the day they end; history has a nightly capture lag. This endpoint also frames "today" as plain UTC (`startOfDay(time.Now().UTC())`) and does not use the `browser_tz` cookie. Do not copy history's boundary by reflex. _Source: spec gateway — Requirement: Supercharger Stats month-window selector._
- **The 400-day cap is this endpoint's own, not a platform constant.** Caps are per-endpoint, sized by the source table's row density: `charge_sessions` is sparse (400 days), `vehicle_snapshots` is dense (90 days). A new date-filtered endpoint measures its own density rather than copying either number. _Source: spec gateway — Requirement: Supercharger Stats month-window selector._
- **Sessions with a `NULL` `TeslaID` are invisible here, by construction and by design** — the single read is scoped by `TeslaID`, so they can never match. Never add a second account-wide read to discover or disclose them, and show no "N sessions hidden" notice. _Source: spec gateway — Requirement: Unattributed Supercharger sessions are out of scope._
- **Multi-currency costs are NEVER summed across currencies** — one pre-formatted cost line per currency; sessions missing cost or currency still count toward Sessions/Energy but are excluded from cost. _Source: spec gateway — Requirement: Supercharger Stats cost aggregation never sums across currencies._
- **Reader errors degrade to the empty state, never a 500.** _Source: spec gateway — Requirement: Supercharger Stats page._
- **The window drives tiles, chart and table identically** — all three reflect the same filtered slice; changing a preset re-fetches the whole region without a full page reload. _Source: spec gateway — Requirement: Supercharger Stats month-window selector._
- **The region is vehicle-scoped and refreshes on switch** — `#supercharger-stats-content` subscribes to `vehicle-changed from:body`; reads go through `resolveSelectedVehicle`, never `registered[0]`. _Source: spec gateway — Requirement: Supercharger Stats page._
- **One template tree serves both entry points** — `SuperchargerStatsPage(v)` is rendered whole for the page and via `renderFragment(…, "supercharger-stats")` for the swap; the `@templ.Fragment("supercharger-stats")` id must match. _Source: spec gateway — Requirement: Supercharger Stats page._
- **No business logic in templates** — charts are hand-rolled SVG reusing `historyBarChart`; all heights, labels and tooltip strings are pre-computed in the handler. _Source: spec gateway — Requirement: Supercharger Stats charts and tables contain no business logic in templates._
- **The sessions table is unpaginated** — the window is the bound, not a page size. _Source: spec gateway — Requirement: Supercharger Stats sessions table is unpaginated._
- **Durable Supercharger record with unalterable site name** — `charge_sessions` maintains a durable record per account and session ID. The charging site name is write-once and frozen upon creation, whereas energy delivered, total cost, currency, and payment settlement status track and update from the source on every sync pass. _Source: spec charge-session-log — Requirement: Supercharger Charge Session Record & Requirement: The Charging Site Is Fixed On Record; Energy, Cost, Currency And Payment Status Track The Source._
- **VIN is write-once; vehicle_id is refreshed per sync** — VIN is durable and write-once, while `vehicle_id` refreshes to match the account's currently registered vehicle registry (set to absent if the vehicle is no longer registered). Unregistered vehicle sessions remain retained in storage but are excluded from vehicle-scoped retrievals. _Source: spec charge-session-log — Requirement: Registered Vehicle Identifier Is Refreshed, VIN Is Not._
- **Synchronization never overwrites battery percentage verification** — `charge_sessions` carries optional verified battery percentages (start/stop), provenance ("user verified" or "polled"), and frozen estimates. Sync passes never write, clear, or overwrite these 5 verification fields. Percentage values must be between 0 and 100 inclusive and require a provenance when present. _Source: spec charge-session-log — Requirement: Battery Percentage Verification On A Charge Session._
- **One-time legacy import defaults missing provenance to "user verified"** — Previously collected sessions with verified battery percentages but missing provenance are imported with provenance set to "user verified". Sessions without percentages receive no provenance. Import is repeatable and idempotent. _Source: spec charge-session-log — Requirement: One-Time Import Of Previously Collected Sessions._
- **Human battery percentage corrections derive provenance and preserve session facts** — Human corrections to start/end battery percentages set provenance automatically to "user verified" (or clear percentages and provenance if neither percentage is supplied). Corrections touch ONLY start %, end %, and provenance, preserving all other session facts (site, energy, cost, currency, payment, frozen estimates). Out-of-bounds percentages (outside 0-100) or wrong-account attempts are rejected cleanly. _Source: spec charge-session-log — Requirement: A Charge Session's Battery Percentages Are Correctable By A Human._
- **Session retrieval interfaces support window, recency-of-update, and count queries** — Sessions can be retrieved by whole-calendar-day window (inclusive bounds, ordered earliest stop first), by recency of update/creation (ordered earliest stop first, including battery % edits), or by recency of occurrence up to a bounded count (ordered newest stop first). Unregistered vehicles are omitted from vehicle-scoped retrievals, and empty matches return empty results rather than errors. _Source: spec charge-session-log — Requirement: Charge Sessions Are Retrievable For A Vehicle Within A Time Window, Requirement: Charge Sessions Are Retrievable For A Vehicle By Recency Of Update, & Requirement: Charge Sessions Are Retrievable For A Vehicle By Recency Of Occurrence, Bounded By Count._

## Related KB

- Features: (none yet)
- Workflows: `workflows/manual-charge-crud.md` (the contrast case: the ONE concept with a user write path)
- Architecture: `architecture/telemetry-data-hub.md` (the raw `supercharger_sessions` upstream + the nightly mirror into `charge_sessions`)
