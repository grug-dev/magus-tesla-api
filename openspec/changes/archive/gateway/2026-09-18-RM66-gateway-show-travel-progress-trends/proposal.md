Source: MAG-59 — https://linear.app/magus-monitor/issue/MAG-59/travel-progress-drive-the-updown-arrows-from-the-previous-day
Roadmap: openspec/roadmaps/RM66-travel-progress-trends.md
Tier: 3 of 3 (`gateway`, depends on tier 2 `analytics` — the three
`_delta_calc` fields must exist on `analytics.VehicleStatus` before this tier
can read them)

## Why

The dashboard's Travel Progress tiles (Distance travelled, Battery used,
Efficiency) show a hardcoded arrow today: `dashboard.templ:81-83` writes
`Trend: "up"` on the first tile and `Trend: "down"` on the second, no matter
what the vehicle actually did. Tier 2 added the three real day-over-day
deltas the arrows should follow — `DistanceTraveledKmDeltaCalc`,
`ConsumedPctDeltaCalc`, `KmPerPctDeltaCalc` on `analytics.VehicleStatus` — but
nothing reads them yet. This tier wires the three tiles to those real values,
the same way the Tire pressure tiles already read their own four deltas.

Roadmap decision **D-C** adds one rule the tyre tiles did not need: only
Efficiency gets a good/bad colour (more km per battery percent is clearly
good). Distance travelled and Battery used must still point the real
direction — driving more is not automatically good news — but in a neutral
colour, never green or red.

This dispatch, like tier 1 and tier 2's, produces **OpenSpec artifacts
only** — no Go edit, no `.templ` edit, no `make templ`/`make css`.

## What Changes

- **`ui.StatTileProps.Trend`** (`internal/gateway/templates/ui/stat_tile.templ`)
  grows from a 2-value vocabulary (`"up"`/`"down"`) to a 4-value one, adding
  `"up-neutral"`/`"down-neutral"`. Both new values draw the same two arrow
  glyphs the coloured values already draw, in a neutral semantic colour
  instead of `text-success`/`text-error`. The empty string still renders no
  icon. This is the "third value" the component's own doc comment already
  reserved a place for — extended to two, because direction and colour are
  two separate facts here, and both new tiles still need a real direction.
- **`fragments.DashboardData`** (`internal/gateway/templates/fragments/dashboard_vm.go`)
  replaces its three flat `DistanceTraveled`/`BatteryUsed`/`Efficiency`
  string fields with a new `TravelStatVM` struct per tile (`Value`, `Trend`,
  `Delta`), mirroring the existing `TireWheelVM` shape.
- **`mapDashboardSnapshot`** (`internal/gateway/handlers/handlers.go`) builds
  the three `TravelStatVM` values from the matching `*DeltaCalc` field on
  `analytics.VehicleStatus`, reusing (and slightly generalising) the helpers
  `dashTireWheel`'s four call sites already established — no second trend
  mechanism is introduced.
- **`dashboard.templ:81-83`** drops the two hardcoded `Trend:` literals and
  reads `.Trend`/`.Desc` off the three new `TravelStatVM` values, the same
  way the four tyre tiles already read `.Trend`/`.Delta` off `TireWheelVM`.
- **i18n**: the tyre section's existing generic delta-format key is reused
  (renamed to a section-neutral name, since three more tiles now use it) —
  no new catalogue key is needed for the delta line's wording. Full
  before/after key list is in design.md.
- **Two stale docs corrected**: `openspec/specs/gateway/spec.md`'s "Dashboard
  Travel Progress Subsection" requirement, which today documents the fixed
  icon rule as deliberate, is replaced by this change's spec delta (below)
  with the real delta-driven behaviour. The KB guide
  `kkpa/context/use-case/gateway/read-dashboard-bento.md:242`, which restates
  the same now-false claim, gets a matching correction task (implementation
  wave, not this dispatch).

## Breaking / modules affected

- **Breaking for gateway-internal callers only.** `DashboardData`'s three
  field types change from `string` to `TravelStatVM`; the only two callers —
  `mapDashboardSnapshot` (writer) and `dashboard.templ` (reader) — are both
  inside this same change. No other module or external consumer reads
  `fragments.DashboardData`.
- **Modules affected:** `gateway` only. `analytics` is read-only here
  (`analytics.VehicleStatus`'s three tier-2 fields, already shipped).
- **Read path affected:** none newly added. The three deltas ride on the same
  `analytics.Reader.LatestMetricsForVehicles` call `mapDashboardSnapshot`
  already consumes for every other dashboard field — no new query, no new
  Tesla Fleet API call. This is a projection the read path already returns;
  tier 2's design.md proved the widened `SELECT` needs no index change.

## No database gate

This tier touches no database object — no migration, no schema, no index.
The three values it reads were already added to `analytics.vehicle_metrics`
and projected by `LatestVehicleMetricsByVehicles` in tier 2. `CLAUDE.md`'s
`Design-Gates` therefore does not apply to this change.
