# Supercharger Stats read path (charging.SessionReader) — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.

## Glossary

- **Known as:** `supercharger stats`, `Supercharger session`, `Supercharger Stats page`, `/supercharger-stats`, `fast charging stats`
- **Internal name:** `SuperchargerStatsPage` / `SuperchargerStatsFragment` (gateway handlers) → `charging.SessionReader` port — table `charge_sessions` (charging DB). RM30 (MAG-30) moved this read from `telemetry.SuperchargerReader`/`supercharger_sessions` to the charging module's mirrored `charge_sessions`.

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

- **Read (page load / fragment swap):** `GET /supercharger-stats` or `GET /ui/supercharger-stats?start=YYYY-MM-DD&end=YYYY-MM-DD` → auth guard (`currentUID`) → `superchargerStatsViewFor`: `parseSuperchargerRange` (both absent → month-aligned default: `end=today`, `start=monthsBackFrom(end, 6)`; malformed/one-sided/`end<start`/future `end`/wider than 400 days → HTTP 400, empty state, no preset selector) → `resolveSelectedVehicle` (no vehicle → empty state, not an error) → `buildSuperchargerStatsView`: ONE `ListSessionsByVehicleBetween(ctx, uid, teslaID, start, end)` call → tiles + chart + rows VM → render page / `renderFragment(…, "supercharger-stats")`.
- **Add a KPI tile / table column:** `internal/gateway/handlers/supercharger.go` (`buildSuperchargerTiles` / `buildSuperchargerRows` — all formatting here) → `internal/gateway/templates/fragments/supercharger_stats.templ` (present the pre-formatted string) → i18n keys in `internal/gateway/i18n/catalog.go` (ES + EN) → `make templ && make css`.
- **Change the window presets/default:** `superchargerRangeDefaultMonths` / preset construction in `internal/gateway/handlers/supercharger.go`; presets are rendered as absolute `?start=&end=` dates server-side, so only the constants and tests change.
- **Create / Update / Delete a session:** NO user-facing path exists — by design. `charge_sessions` rows are written only by the nightly mirror (`app.processor` step 2 via `charging.SessionWriter`); raw `supercharger_sessions` only by the telemetry collector. Do not add a gateway write handler for this concept.
- **Add a new field from the raw session:** the mirror copies a fixed column set — extend `charge_sessions` DDL + `SessionWriter.UpsertSession` + the mirror in `internal/app/processor.go`, then surface it through `SessionReader`/`Session`. The page never reads `supercharger_sessions` directly.

## Conventions & gotchas

- **`?start=&end=` is the date-filter contract** — absolute dates, both-or-neither, `end` ≤ today, window ≤ `superchargerRangeMaxDays` (400); the old `?months=N` count-based param was removed by RM30. Follow `internal/gateway/AGENTS.md` §"HTTP date-filter convention" (canonical rationale: `ai/architecture.md` §7 pattern 5). _Source: `internal/gateway/handlers/supercharger.go` (`parseSuperchargerRange`), `internal/gateway/AGENTS.md`._
- **Month presets render as absolute dates server-side** — each preset button's `hx-get` carries pre-formatted `?start=&end=` (from `RangePreset.StartStr/EndStr`); `monthsBackFrom` keeps 3/6/12 presets rendering exactly n calendar buckets (never n+1). _Source: `supercharger_stats.templ`, `supercharger.go` D5._
- **The page reads the CHARGING mirror, not telemetry** — `Deps.SuperchargerReader` is typed `charging.SessionReader` over `charge_sessions`; `telemetry.SuperchargerReader` remains for analytics + the nightly mirror only. NEVER re-point the page at telemetry; the boundary rule stands (import the port, never `internal/charging/db` or `internal/telemetry/db`). _Source: RM30 design, `internal/gateway/AGENTS.md` → allowed imports._
- **Read-only page — no edit API.** The gateway registers only the two GETs. This concept has NO user-write exception (contrast with manual charges — see `workflows/manual-charge-crud.md`). _Source: `internal/gateway/gateway.go`._
- **Multi-currency costs are NEVER summed across currencies** — `buildSuperchargerTiles` produces one pre-formatted cost line per currency (sorted by code); sessions missing cost or currency count toward Sessions/Energy but are excluded from cost. `charging.Session` carries no CountryCode/BillingType. _Source: `internal/gateway/handlers/supercharger.go`._
- **Reader errors degrade to the empty state, never a 500** — `buildSuperchargerStatsView` logs and returns an empty view (mirrors `buildHistoryView`). A malformed range gets HTTP 400, the empty-state placeholder, and NO preset selector. _Source: `internal/gateway/handlers/supercharger.go`._
- **The region is vehicle-scoped and refreshes on switch** — `#supercharger-stats-content` subscribes to `vehicle-changed from:body`; reads must go through `resolveSelectedVehicle`, never `registered[0]`. _Source: `internal/gateway/AGENTS.md` → "Vehicle-scoped reads", `pages/supercharger_stats.templ`._
- **One template tree serves both entry points** — `SuperchargerStatsPage(v)` is rendered whole for the page and via `renderFragment(…, "supercharger-stats")` for the swap; the `@templ.Fragment("supercharger-stats")` id must match. _Source: `internal/gateway/AGENTS.md` → "Two render entry points"._
- **Charts are hand-rolled SVG** (reuses `historyBarChart`) — no chart library; all heights/tooltip strings pre-computed in the handler, no math in templates, semantic fill tokens only. _Source: `internal/gateway/AGENTS.md` → "Charts / data-viz (RD7)"._

## Related KB

- Features: (none yet)
- Workflows: `workflows/manual-charge-crud.md` (the contrast case: the ONE concept with a user write path)
- Architecture: `architecture/telemetry-data-hub.md` (the raw `supercharger_sessions` upstream + the nightly mirror into `charge_sessions`)
