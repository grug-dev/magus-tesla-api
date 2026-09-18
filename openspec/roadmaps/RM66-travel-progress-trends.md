# RM66 — Travel Progress trends

Source ticket: MAG-59 — https://linear.app/magus-monitor/issue/MAG-59/travel-progress-drive-the-updown-arrows-from-the-previous-day

## Goal

The Travel Progress subsection on `/dashboard` shows three tiles. Their up/down
arrows never change today. They are hardcoded in the template. We make them real:
each arrow compares the day against the day before, the same way the Tire pressure
arrows already do.

The same work also renames the four tyre-pressure delta columns so every delta
column in the database carries one clear suffix, and writes that naming rule where
an AI assistant will read it.

## Decisions (settled with the user before the roadmap was written)

These bind every tier. A worker must not re-open them.

- **D-A. Scope stays whole.** The arrows, the tyre-pressure column rename, and the
  naming rule ship together as this one roadmap.
- **D-B. Store the deltas; do not compute them at read time.** New `_delta_calc`
  columns hold the day-over-day change. `deriveVehicleMetrics` computes each one
  from the row the loop built one step earlier (`out[len(out)-1]`). It does NOT
  take a third snapshot, and it does NOT fetch a second preceding snapshot.
  Accepted cost: the first day of a recalculation window has no in-loop
  predecessor, so its delta is NULL for that pass. `Reconcile` recalculates full
  history, so the next full pass fills it. The `LAG` window-function option was
  rejected: Postgres runs window functions before `DISTINCT ON`, so `LAG` would
  read each vehicle's whole history on a page load. That loses the one-row read
  the read-heavy profile depends on.
- **D-C. Direction on all three tiles; colour only on Efficiency.** Every tile
  shows an up or down arrow against the previous day. Only Efficiency carries a
  good/bad colour, because more km per percent is clearly good. Distance travelled
  and Battery used show a neutral arrow. Driving more is neither good nor bad, and
  a green arrow would praise it.
- **D-D. Column names use `_delta_calc`.** `distance_traveled_km_delta_calc`,
  `consumed_pct_delta_calc`, `km_per_pct_delta_calc`. The four tyre columns are
  renamed to match (`tpms_pressure_fl_psi_delta_calc` and siblings). One rule for
  the whole database.
- **D-E. The naming rule goes in `ai/go-conventions.md` AND a `make` guard.** The
  prose sits beside the existing column unit-suffix rule. The guard makes the
  mistake fail fast, with no human review round.

## Facts verified against the code before this roadmap was written

Use these. Do not re-derive them.

- The read query is `LatestVehicleMetricsByVehicles` (`internal/analytics/db/query.sql:96`).
  The ticket calls it `LatestVehicleMetricsByAccount`; that name is stale since MAG-69
  re-keyed it to `tesla_id`.
- `internal/gateway/templates/pages/dashboard.templ:81-83` hardcodes `Trend: "up"`
  and `Trend: "down"`. The Efficiency tile passes no `Trend` at all.
- `ui.StatTileProps.Trend` is a **closed vocabulary of two values**: `"up"` renders
  `text-success`, `"down"` renders `text-error`. There is no neutral value, so D-C
  needs a third one. The component's own comment (`stat_tile.templ:13`) already says
  a third value belongs in the component, never as a raw `ui.Icon` call at a page.
- Tyre pressure needs only two snapshots, because raw PSI lives on
  `telemetry.Snapshot` (`consumption.go:86`). The three Travel Progress figures are
  themselves built from a `(prev, cur)` pair, so their deltas are deltas of deltas.
  That is why D-B exists.
- `internal/analytics/db/migrations/` holds exactly ONE file,
  `20260917000001_baseline.sql`. The rename needs a NEW migration. Never edit the
  applied baseline. Production already holds the old column names.
- Renaming the port fields breaks four `gateway` call sites
  (`handlers.go:580-583`) plus `handlers_test.go`. That is cross-module integration
  and the **leader** fixes it, not a module worker.
- **CORRECTED after tier 1 measured it.** This roadmap first said only four
  `_calc` columns exist. That was wrong. A repo-wide grep finds **ten** `_calc`
  columns in **two** modules — `analytics` and `charging`
  (`inferred_capacity_kwh_calc`) — and about fifteen Go identifiers. Tier 2
  renames only four of them, so the guard's baseline shrinks from 10 to 6, never
  to empty. Six of the ten were never day-over-day deltas: `days_spanned_calc` is
  a count, `km_per_pct_calc` and `estimated_range_km_calc` are same-row
  derivations. Source: tier 1 `design.md` §"Repo-wide measurement".
- `openspec/specs/gateway/spec.md:3652` and
  `kkpa/context/use-case/gateway/read-dashboard-bento.md:242` both state the
  fixed-icon rule as deliberate. Both must be corrected.

## Tiers

Status legend: `[ ]` pending — the tier's OpenSpec change does not exist yet.
`[~]` in progress — the change exists but is not archived. `[x]` done — archived.

| Status | Change | Module | Scope | depends_on | Proposal prompt |
|---|---|---|---|---|---|
| `[~]` | `RM66-platform-add-delta-column-naming-rule` | platform | Write the delta-column naming rule in `ai/go-conventions.md`, beside the existing unit-suffix rule. Add a `make delta-guard` target that fails when a new derived delta column or Go field is named `_calc` without `_delta`. Mirror `naming-guard`'s baseline shape (Makefile:892-908): every pre-existing `_calc` name WARNs, so `make check` stays green until tier 2 renames the tyre columns. | — | Add the naming rule and its guard. No database change. No behaviour change. The guard's baseline only ever shrinks; it does NOT reach empty. |
| `[ ]` | `RM66-analytics-add-travel-progress-deltas` | analytics | One migration: add `distance_traveled_km_delta_calc`, `consumed_pct_delta_calc`, `km_per_pct_delta_calc`; rename the four `tpms_pressure_*_psi_calc` columns to `*_psi_delta_calc`. Compute the three new values in `deriveVehicleMetrics` per D-B. Widen `LatestVehicleMetricsByVehicles` to project them. Add and rename the matching `analytics.VehicleStatus` fields. Delete the tier-1 guard baseline entries. | tier 1 | DATABASE GATE: the user must confirm the schema, the rationale, and the index plan before Apply. The three new columns are projected only — never a WHERE, JOIN or ORDER BY — so `idx_vehicle_metrics_latest` should still serve the read unchanged; prove that in design.md. State whether a backfill is needed and what it costs. |
| `[ ]` | `RM66-gateway-show-travel-progress-trends` | gateway | Add the third, neutral `Trend` value to `ui.StatTileProps` (in the component, per its own comment). Map the three deltas to a `Trend` and a `Desc` in `mapDashboardSnapshot`, reusing `dashTireWheel`'s shape. Wire all three tiles in `dashboard.templ`. Add the i18n keys in both ES and EN. Correct the gateway spec requirement and the KB guide. | tier 2 | Reuse the existing trend mechanism; never add a second way to draw an arrow. No hardcoded colours — semantic tokens only. A NULL delta renders no arrow at all, never a flat or neutral one: unknown and "no change" must stay different. |

## Cross-module work owned by the leader

The tier-2 port-field rename breaks `internal/gateway/handlers/handlers.go:580-583`
and `handlers_test.go`. The leader fixes those call sites in tier 2's wave commit.
A module worker never edits another module.

## Future work

None deferred yet.
