Source: MAG-19 — https://linear.app/magus-monitor/issue/MAG-19/allow-editing-supercharger-sessions
Roadmap: openspec/roadmaps/RM31-supercharger-session-verification.md
Tier: 4 of 5 (gateway display-only slice). This tier is independent of tiers 1–3; tier 5's
inline edit depends on it.

Design gate: **not tripped.** This change adds no database object and does not alter an
existing one: no migration, table, column, index, constraint, view, query, or DB package is
touched. It reads the already-exposed `charging.Session` fields through the existing
`charging.SessionReader` call, so the database design gate has nothing to review.

Grill-me: the only material product decisions are already owner-set in the RM31 roadmap:
roadmap Decision 3 keeps both `_est` fields deliberately NULL until a real estimator exists,
and Decision 5 confirms that Country remains absent. The chart implementation is additionally
fixed by the tier prompt: reuse `HistoryChart` / `HistoryBar` and `buildYAxisTicks`; no separate
chart library or tick algorithm is an available design choice.

## Why

The Supercharger Stats page already owns a display-only read of `charging.Session`, but it
omits data that is already part of that record: the verified start/end battery percentages and
their frozen estimate counterparts. It also reuses the shared history chart renderer without
filling the renderer's existing per-bar labels or y-axis tick fields. As a result, the chart
has less readable time context than the dashboard history charts even though its view-model
vocabulary already supports it.

This tier closes ticket items 1 and 3 without creating a write path. Tier 5 will add the
separate inline verification edit flow after the charging and analytics dependencies are ready.

## Breaking change

**No.** Routes, query parameters, ports, persistence, and existing table columns retain their
current behavior. The rendered table gains four additive columns; Country is not restored.

**Modules affected:** `gateway` only. `charging` is consumed solely through its existing public
`SessionReader` / `Session` contract. No other module, `cmd/`, or database package changes.

## Read paths affected (Performance-Profile: read-heavy)

`GET /supercharger-stats` and `GET /ui/supercharger-stats` retain their one bounded,
vehicle-scoped `charging.SessionReader.ListSessionsByVehicleBetween` read. This change performs
only in-memory presentation mapping of fields already returned by that read; it introduces no
additional query, write, Tesla API call, runtime aggregation, index, or cache.

Performance profile (binding): `read-heavy — read performance is mandatory over write performance; writes are mostly done by pollers at midnight, so denormalizing, indexing aggressively, and precomputing for reads is acceptable — never at the cost of the modular-monolith boundaries or module data ownership.`

## What Changes

- **CHANGED** — `internal/gateway/handlers/supercharger.go`: populate each monthly
  `HistoryBar.Label` as `YYYY-MM`; populate `HistoryChart.YAxisTicks` with the existing
  `buildYAxisTicks(maxKWh, kWhFormatter)` helper; select vertical labels for the wider
  `YYYY-MM` labels; and map all four existing battery percentage fields to display-ready row
  labels, using `"—"` for nil.
- **CHANGED** — `internal/gateway/templates/fragments/supercharger_vm.go`: add four
  preformatted battery display labels to `SuperchargerRowVM`; no domain types or formatting are
  exposed to markup.
- **CHANGED** — `internal/gateway/templates/fragments/supercharger_stats.templ`: append four
  translated headers and matching cells to the existing Supercharger sessions table. It continues
  to delegate drawing to `historyBarChart`; it does not add chart markup or a chart library.
- **CHANGED** — `internal/gateway/i18n/catalog.go`: add one bilingual ES/EN key per new table
  header in the single existing catalogue.
- **CHANGED** — `internal/gateway/handlers/supercharger_test.go`: add pure/offline coverage for
  monthly labels, y-axis ticks, vertical labels, populated battery values, and nil-to-em-dash
  mapping; add a template/render assertion for the translated headers and row cells.

## Non-Goals

- No inline edit UI, PATCH route, verifier-port call, analytics recalculation, CSRF change, or
  write path (tier 5 only).
- No SOC estimator. `start_battery_pct_est` and `end_battery_pct_est` remain NULL and visibly
  degrade to `"—"` (RM31 roadmap Decision 3).
- No Country column, Country i18n key, billing column, or other previously removed field
  (RM31 roadmap Decision 5; RM30 gateway design D4).
- No database object, migration, index, SQL query, or direct database access.
- No second chart implementation, tick algorithm, client-side chart dependency, or template
  arithmetic.

## AI-efficiency rationale

Reusing the existing `HistoryChart` / `HistoryBar`, `historyBarChart`, `buildYAxisTicks`,
`SuperchargerRowVM`, and single i18n catalogue keeps the change local and deterministic. An
agent can mirror one established vocabulary instead of rediscovering rendering rules or
maintaining two chart paths; typed view models and the existing tests provide cheap, immediate
signals without adding indirection.
