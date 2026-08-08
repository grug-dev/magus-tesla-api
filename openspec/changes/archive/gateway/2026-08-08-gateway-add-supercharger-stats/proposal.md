## Why

The nav sidebar has pointed to a "Supercharger Stats" item as a `Href="#"` placeholder with a
"Soon" badge since `gateway-add-stitch-design-handoff` (Stitch design decision **D9**, user,
2026-07-26). `internal/telemetry` already stores Tesla-billed Supercharger / DC fast-charging
sessions (`supercharger_sessions`, upserted from `dx/charging/history`, Source B) and already
exposes a read-only `SuperchargerReader` port (`SuperchargerSessionsByVehicle` /
`SuperchargerSessionsByAccount`) — no telemetry-module change is needed. This change (backlog
item #4) makes the nav entry a live route + page: per-vehicle Supercharger stats built from the
existing port, mirroring the `charges` gold-standard slice.

## What Changes

Primary module: **`internal/gateway/`**. Consumes the existing, unmodified
`telemetry.SuperchargerReader` port.

### (a) New page + fragment routes

`GET /supercharger-stats` (full page) and `GET /ui/supercharger-stats` (htmx fragment region,
swapped by the month-preset selector) — the same page/fragment pairing as `/charges` +
`/ui/charges`. Both auth-guard (anonymous → `/login`, no data served) and resolve the
**session-selected vehicle** via `h.resolveSelectedVehicle(ctx, c, uid)` (D1).

### (b) New `SuperchargerReader` dependency wired through `Deps`

`gateway.Deps` and `handlers.Deps` each gain a `SuperchargerReader telemetry.SuperchargerReader`
field, threaded through `NewEngine` → `handlers.New` (same shape as the existing
`TelemetryReader` / `ManualChargeReader` fields). `cmd/web/main.go` passes
`telemetry.NewSuperchargerReader(pool)` — a one-line, zero-business-logic DI wiring change
(explicitly granted path for this change).

### (c) Page composition — three sections (D3)

1. A `ui.StatTile` KPI row: **Sessions · Energy · Cost · Avg kWh/session** (D7).
2. A zero-JS SVG bar chart of **kWh per month**, following the existing
   `historyBarChart`/`HistoryBar` pattern (`internal/gateway/templates/fragments/history.templ`):
   the handler pre-computes `HeightPct` + `<title>` tooltip strings; the template does no
   arithmetic.
3. A `ui.Table` of every session in the selected window (no pagination — D7).

### (d) Month-preset selector

A closed vocabulary `{3, 6, 12}` months, default 6 (D5), mirroring `historyDayPresets` /
`clampHistoryDays` exactly. Selecting a preset re-fetches `GET /ui/supercharger-stats?months=N`
and swaps the whole region — mirroring the dashboard history selector.

### (e) Nav entry goes live

`internal/gateway/templates/layouts/nav.go`: the `{Label: "Supercharger Stats", Icon:
"analytics", Placeholder: true}` entry becomes `{Label: "Supercharger Stats", Href:
"/supercharger-stats", Active: active == "/supercharger-stats", Icon: "analytics"}`. The
"Settings" placeholder is untouched.

## Breaking

No. Additive only: two new routes, one new `Deps` field on both `gateway.Deps` and
`handlers.Deps` (Go struct-literal construction by field name, so existing callers are
unaffected), new Templ components, and one nav entry flipped from placeholder to live. No
existing route, view model, or template contract changes.

## Modules affected

- **`internal/gateway/`** — primary: new route, handler, view model, Templ page/fragment/chart
  components, `Deps` wiring, and the nav entry.
- **`cmd/web/main.go`** — explicitly granted for this change: one added constructor call
  (`telemetry.NewSuperchargerReader(pool)`) passed into `gateway.Deps`. No business logic.
- No other `internal/` module changes. Reads only through the existing
  `telemetry.SuperchargerReader` public interface — never `telemetrydb`.

## Database Changes

**None.** This change consumes the existing `telemetry.SuperchargerReader` port over the
existing `supercharger_sessions` table (schema owned and already shipped by a prior telemetry
change). It adds no table, column, index, constraint, view, or migration. The `database` design
gate does not trigger; design.md states this explicitly rather than omitting a schema section.

## Read Paths Affected

One read per page/fragment render: `telemetry.SuperchargerReader.SuperchargerSessionsByVehicle(
ctx, accountID, teslaID, limit=500)`, ordered `charge_start_date_time DESC`, scoped to the
session-selected vehicle (D1, D6). All month-window filtering, KPI aggregation (including the
per-currency cost split, D4), and chart bucketing happen in Go in the handler, in memory, after
the single bounded read — no N+1, no per-request `GROUP BY` in SQL. This keeps the page on one
indexed range scan per render, consistent with the project's read-heavy performance profile and
the precedent set by `DashboardHistoryFragment` / `SnapshotsByVehicleSince`.

## Capabilities

### Added Capabilities

- **`gateway`** — new "Supercharger Stats" requirement (delta:
  `openspec/changes/gateway-add-supercharger-stats/specs/gateway/spec.md`): the stats page +
  fragment endpoints, per-selected-vehicle scope, month-preset validation + default, the read
  cap, the KPI tiles (including multi-currency cost aggregation and nil-cost/nil-currency
  handling), the kWh/month chart, the sessions table, the D2 unattributed-session limitation, and
  the inherited empty/anonymous/degrade invariants.

### Consumed Capabilities (no change to their specs)

- **`telemetry` — Supercharger Session Read Port** (`SuperchargerReader`) — the sole data source;
  already built and shipped, unmodified by this change.

## Resolved decisions

Resolved with the user in the leader↔user grill-me pass (2026-08-08) — decisions **D1–D7**, each
with rationale and rejected alternatives, are recorded in `design.md`. The inherited precedent
(anonymous redirect, no-vehicle empty state, reader-error degradation, zero-sessions empty state,
no-business-logic-in-templates, semantic-tokens-only) is also recorded there rather than
re-litigated.
