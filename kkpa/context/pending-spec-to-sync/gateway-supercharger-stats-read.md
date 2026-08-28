# Sync proposal — gateway (Supercharger Stats read path)

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `workflows/supercharger-stats-read.md`
Source spec:  `openspec/specs/gateway/spec.md` (scoped to the Supercharger Stats requirements
              re-synced by `RM30-gateway-read-supercharger-stats-from-charging`, archived
              2026-08-27 as `openspec/changes/archive/gateway/2026-08-27-RM30-gateway-read-supercharger-stats-from-charging`)
Generated:    2026-08-27
Status: PENDING REVIEW

---

> **Scope note.** `openspec/specs/gateway/spec.md` is the whole gateway capability (~2170 lines,
> many unrelated requirements). This proposal is deliberately scoped to the requirements the
> archived change actually modified — the Supercharger Stats ones — which are the requirements
> that resolve to this target guide. It does not restate the rest of the gateway capability.

> ⚠️ **TWO STALE AREAS THIS PROPOSAL CANNOT FIX — read before applying.**
>
> 1. **`## Component map` is NOT proposed and will stay stale after apply.** `spec.md` carries
>    behavior, not file paths, so `from-spec` never touches a Component map without the opt-in
>    `--with-filemap` flag. The live map still lists `internal/telemetry/telemetry.go`,
>    `reader.go`, `db/queries.sql`, `clampSuperchargerMonths` and `superchargerReadLimit` as
>    part of this concept — **all of which are wrong for this path now.** Fix by re-running
>    `/kkpa-context-curate from-spec openspec/specs/gateway/spec.md --with-filemap`, or by a full
>    re-curate of the concept.
> 2. **The three `INDEX.md` rows keep their stale "internal name" cells.** `[index] … ADD ROWS`
>    skips any row whose first cell already exists, and the existing rows say
>    `telemetry.SuperchargerReader` / `supercharger_sessions`. The routing still lands on the
>    right guide, so nothing breaks — but the cell text misleads. Correct rows are proposed below
>    under a REPLACE-style note for a human to apply by hand.

---

## [guide] ## Glossary — REPLACE

- **Known as:** `supercharger stats`, `Supercharger session`, `Supercharger Stats page`, `/supercharger-stats`, `fast charging stats`
- **Internal name:** `SuperchargerStatsPage` / `SuperchargerStatsFragment` (gateway handlers) → `charging.SessionReader` port (`ListSessionsByVehicleBetween`) — table `charge_sessions`, owned by the `charging` module. **Changed by RM30** (was `telemetry.SuperchargerReader` over `supercharger_sessions`, the raw ingestion buffer).

## [guide] ## How maintenance works — REPLACE

Order of operations for the common changes. Reference the files above by path.

- **Read (page load / fragment swap):** `GET /supercharger-stats` or `GET /ui/supercharger-stats?start=YYYY-MM-DD&end=YYYY-MM-DD` → auth guard (`currentUID`) → `superchargerStatsViewFor` (shared by both entry points; returns `(view, httpStatus)`): `parseSuperchargerRange` → on `ok=false` return the empty view with `Presets: nil` at HTTP 400 → `resolveSelectedVehicle` (account read; no vehicle → empty state, not an error) → `buildSuperchargerStatsView`: ONE `ListSessionsByVehicleBetween(ctx, uid, teslaID, start, end)` call → reverse the oldest-first slice once for newest-first display → tiles + chart + rows VM. The page renders via `render` / `renderError`; the fragment via `renderFragment` / `renderFragmentError` (`"supercharger-stats"`).
- **Change the window contract (default, cap, presets):** `superchargerRangeDefaultMonths` (6), `superchargerRangeMaxDays` (400) and `superchargerMonthPresets` (`{3, 6, 12}`) are constants in `internal/gateway/handlers/supercharger.go`; `monthsBackFrom` computes month-aligned starts and `buildSuperchargerPresets` builds the selector's absolute `?start=&end=` hrefs. The template iterates `[]RangePreset`, so only the constants + tests change.
- **Add a KPI tile / table column:** `internal/gateway/handlers/supercharger.go` (`buildSuperchargerTiles` / `buildSuperchargerRows` — all formatting here) → `internal/gateway/templates/fragments/supercharger_stats.templ` (present the pre-formatted string) → i18n keys in `internal/gateway/i18n/catalog.go` (ES + EN) → `make templ && make css`. A column only exists if `charging.Session` carries the field — RM30 dropped the Country and Billing-type columns precisely because it does not.
- **Widen the readable window:** raise `superchargerRangeMaxDays` in the gateway — the port is already time-bounded, so no `charging` change is needed. Size the cap from the source table's row density, per `internal/gateway/AGENTS.md` §"HTTP date-filter convention".
- **Create / Update / Delete a session:** NO user-facing path exists — by design. Sessions are written by the analytics recalculation path into `charge_sessions`; the gateway registers only the two GETs. Do not add a gateway write handler for this concept.

## [guide] ## Conventions & gotchas — REPLACE

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

## [index] ## Workflows — ADD ROWS

| `supercharger stats date filter` (`?start=&end=`, 400-day cap) | `workflows/supercharger-stats-read.md` |

---

## Manual INDEX.md correction (apply by hand — `ADD ROWS` cannot rewrite existing rows)

The three existing rows resolve correctly but their internal-name cells are stale. Replace:

| Term | Internal name (CURRENT — stale) | Internal name (PROPOSED) |
|---|---|---|
| `supercharger stats` | `SuperchargerStatsPage` / `telemetry.SuperchargerReader` (`supercharger_sessions`) | `SuperchargerStatsPage` / `charging.SessionReader` (`charge_sessions`) |
| `Supercharger session` | `SuperchargerSession` / `UpsertSuperchargerSession` (collector-only writes) | `charging.Session` / written by the analytics recalculation path (no gateway write path) |
| `fast charging stats` | synonym of `supercharger stats` | *(unchanged)* |
