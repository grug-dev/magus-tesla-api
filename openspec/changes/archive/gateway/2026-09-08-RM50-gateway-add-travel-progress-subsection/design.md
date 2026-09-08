# Design — RM50-gateway-add-travel-progress-subsection

> Roadmap decisions RD1–RD8 and RD-fixed (`openspec/roadmaps/RM50-vehicle-status-subsections.md`)
> are binding and not re-opened here. This file adds the implementation-level decisions
> (D1–D9) needed to build them.

## Overview

Two files change shape (`dashboard.templ` markup, `dashboard_vm.go` fields), two files
grow a small addition (`icon.templ`, `stat_tile.templ`), `handlers.go` gains two format
helpers and two mapping lines, `catalog.go` gains six keys, and two KB guides get
corrected. No database object, no new module port, no new route.

## D1 — Panel layout (RD9 Option B, as Templ/htmx)

Inside the existing `vehicle-status` `ui.Card`, below the badge row, the current
`flex items-center justify-center` image wrapper + `grid-cols-2 lg:grid-cols-4` tile
grid is replaced by one inner 12-column grid:

```
<div class="grid grid-cols-1 md:grid-cols-12 gap-6">
  <div class="md:col-span-4 flex flex-col items-center gap-4">
    <img ... class="w-full max-w-[220px] object-contain"/>
    <div class="w-full flex flex-col gap-3">
      @ui.StatTile(Odometer)
      @ui.StatTile(MaxRangeCharges)
    </div>
  </div>
  <div class="md:col-span-8 flex flex-col gap-6">
    <section id="travel-progress">
      @ui.SectionHeader(Title, Desc)
      <div class="grid grid-cols-2 gap-3">
        @ui.StatTile(DistanceTraveled, Trend: "up")
        @ui.StatTile(BatteryUsed, Trend: "down")
      </div>
    </section>
    <!-- tier 4 inserts the Tire pressure (PSI) subsection HERE -->
    <section id="interior-exterior">
      @ui.SectionHeader(Title, Desc)
      <div class="grid grid-cols-2 gap-3">
        @ui.StatTile(InsideTemp)
        @ui.StatTile(OutsideTemp)
      </div>
    </section>
  </div>
</div>
```

The "Last updated" footnote stays exactly where it is today — after this grid, still
inside the card. The badge row (stale/locked/sentry) stays above it, unchanged.

**Breakpoint choice — `md:`, not the module's default `sm:`.** `internal/gateway/AGENTS.md`
§Mobile R2 names `sm:` (640px) as the module's default content breakpoint, but RD9's own
text names `md:col-span-4` / `md:col-span-8` explicitly, and the outer bento grid this
card already sits inside uses `md:grid-cols-12` (`dashboard.templ`, unchanged by this
tier). Nesting the inner split at `sm:` would give one card two different breakpoints —
worse for an agent reading this file later than matching the outer grid's own line. This
tier follows RD9's explicit breakpoint, consistent with the existing sibling grid.

**Mobile stacking needs no order utility.** Below `md` both grids collapse to one column.
DOM order already puts image + the two lifetime tiles first, then the three subsections
in order — exactly the "image and its two tiles first, then the subsections" rule RD9
states, for free, from source order (R1: CSS-only responsiveness, no device branch).

**Subsection tile grids stay a fixed `grid-cols-2`, no responsive variant.** Below `md`
the right column is the same full card width the old flat 4-tile grid used at 2 columns
(the MAG-46 width budget the `stat_tile.templ` doc comment records: ~117px/tile, "45,678
km" needs ~108px) — unchanged budget, so no new mobile-fit risk.

**Section ids.** `travel-progress` and `interior-exterior` are bare `<section>` elements
(not `ui.Card`), so per `internal/gateway/AGENTS.md` §"Identifiable cards & sections" the
page sets `id` directly on the element — kebab-case, page-unique, semantic, matching the
existing `vehicle-status`/`battery-info`/`vital-stats` precedent on this same page.

**Tire pressure (PSI) placeholder.** Tier 2 renders nothing in that slot — no
`SectionHeader`, no empty grid, no id. A `//` comment marks exactly where tier 4
(`RM50-gateway-add-tire-pressure-subsection`, depends on tiers 2 and 3) inserts its
`<section id="tire-pressure">` block. Rendering an empty section now would need its own
Title/Desc (AGENTS.md "Every section is titled and described" has no exception for "not
built yet"), and an empty grid with no tiles is worse than no markup at all — so nothing
renders, per the roadmap's own tier-2 scope note.

## D2 — Travel Progress data mapping (RD7)

Tier 1 already added `DistanceTraveledKmCalc *float64` and `ConsumedPct *float64` to
`analytics.VehicleStatus`, selected by `LatestMetricsByAccount`
(`internal/analytics/analytics.go`, `internal/analytics/reader.go`). This tier only
reads them — no new port method, no new query, no new module dependency.

`fragments.DashboardData` (`dashboard_vm.go`) gains two pre-formatted display strings:

```go
// --- Travel Progress subsection (RM50 tier 2) ---
// DistanceTraveled is the latest computed day's driven distance ("45 km"), or "—"
// when the day has no predecessor (nil DistanceTraveledKmCalc) or there is no
// snapshot at all. Never a fabricated 0.
DistanceTraveled string
// BatteryUsed is the latest computed day's battery percent used ("12.3%"), or "—"
// under the same nil rule as DistanceTraveled.
BatteryUsed string
```

`handlers.go` gains two formatters, mirroring the existing `dashTempOrDash`/
`dashCountOrDash` shape exactly (nil-safe, never fabricate):

```go
// dashDistanceOrDash formats the latest day's driven distance. nil -> "—" (no
// predecessor day, or the row predates RM50 tier 1). Reuses formatKm — the same
// formatter the Odometer tile already uses — so a raw/negative correction-day
// value (see analytics.VehicleStatus.DistanceTraveledKmCalc's own doc comment:
// "never averaged", never clamped) renders exactly as the history chart already
// renders the same field (formatKmRaw in history.go) — not a new edge case.
func dashDistanceOrDash(v *float64) string {
	if v == nil {
		return "—"
	}
	return formatKm(*v)
}

// dashBatteryUsedOrDash formats the latest day's battery percent used. nil -> "—",
// same rule as dashDistanceOrDash. Reuses formatPctRaw — the consumed chart's own
// one-decimal formatter — so the tile and the chart agree on precision.
func dashBatteryUsedOrDash(v *float64) string {
	if v == nil {
		return "—"
	}
	return formatPctRaw(*v) + "%"
}
```

Two new lines in `mapDashboardSnapshot`:

```go
vm.DistanceTraveled = dashDistanceOrDash(vs.DistanceTraveledKmCalc)
vm.BatteryUsed = dashBatteryUsedOrDash(vs.ConsumedPct)
```

**Known, accepted edge case — not fixed here.** `ConsumedPct` can be negative (a day
with more charging than driving; see `internal/analytics/consumed_test.go`'s own
documented -45.0 case). This tier displays it as-is, same as `DistanceTraveledKmCalc`'s
raw/negative case — no clamping, no threshold, per the project's "never fabricate, never
clamp" convention and RD3's explicit rejection of threshold logic for the sibling tyre
metric. The icon stays fixed red-down regardless (D3/RD-fixed) — this is a deliberate
simplification the roadmap already settled, not an oversight to correct in this tier.

The template renders both tiles through the existing `dashStat(d.HasSnapshot, …)`
wrapper, exactly like every other stat tile on this card — no new placeholder logic in
the template.

## D3 — `StatTileProps.Trend` (RD11 part 2)

```go
type StatTileProps struct {
	Label string
	Value string
	Desc  string
	Class string
	// Trend renders a small up/down trend glyph beside the value: "up"
	// (text-success) or "down" (text-error). Empty (the zero value) renders no
	// icon — every existing call site is unaffected. A third value is not
	// modeled; add it here, not as a raw ui.Icon call at a page, if a future
	// caller needs one (RM50-gateway-add-travel-progress-subsection D3).
	Trend string
}
```

Rendering: wrap the existing `stat-value` div and a new icon in a `flex items-center
gap-1` row. The icon selection is a small closed-vocabulary switch colocated in
`stat_tile.templ` (mirrors `iconMarkup`'s own closed-switch shape):

```go
templ statTrendIcon(trend string) {
	switch trend {
	case "up":
		@Icon(IconProps{Name: "trending_up", Class: "h-5 w-5 text-success shrink-0"})
	case "down":
		@Icon(IconProps{Name: "trending_down", Class: "h-5 w-5 text-error shrink-0"})
	default:
	}
}
```

**Why a `Trend string`, not a `bool` + separate up/down props, and not two bools.** Two
values plus "none" is exactly the shape `ui.BadgeProps.Kind` and `ui.DotProps.Variant`
already use elsewhere in this kit (a small closed string vocabulary an agent looks up,
not a `HasTrend bool` + `TrendUp bool` pair that can represent an invalid state like
`HasTrend: false, TrendUp: true`). Consistent with the kit's existing pattern
(AI-efficiency: one shape to learn, reused).

**Why this doesn't need a size variant or its own responsive class.** The icon sits
beside `stat-value`, which already has its own mobile-sized text (`text-xl
sm:text-[2rem]`, R5). A fixed `h-5 w-5` reads fine at both the mobile and desktop value
size — this is a small glyph next to text, not a text element itself, so R5's "fix the
size once in `ui/`" concern (overlapping text) does not apply here.

**Existing call sites are unaffected.** Three pages render `ui.StatTile` today
(`/dashboard`, `/external-charges`, `/supercharger-stats`); none passes `Trend`, so all
three keep rendering with zero icon — verified by reading every `ui.StatTile(` call site
in `templates/pages` and `templates/fragments` before writing this design.

## D4 — `ui.Icon` gains `trending_up` / `trending_down` (RD11 part 1)

Two new `case` branches in `iconMarkup` (`icon.templ`), same shape as every existing
glyph — 24×24 viewBox, `fill="currentColor"`, one solid path, `aria-hidden="true"`
(decorative, exactly like the existing `battery`/`car`/`speed` glyphs — the tile's
`Label` text already carries the accessible meaning, so no new a11y requirement). Paths
are the standard Material Symbols `trending_up`/`trending_down` glyphs (single path,
matching this file's existing convention of using the well-known Material path data for
`settings`/`logout`/`ev_station`/etc.):

- `trending_up`: `M16 6l2.29 2.29-4.88 4.88-4-4L2 16.59 3.41 18l6-6 4 4 6.3-6.29L22 12V6z`
- `trending_down`: `M16 18l2.29-2.29-4.88-4.88-4 4L2 7.41 3.41 6l6 6 4-4 6.3 6.29L22 12v6z`

`IconProps.Name`'s doc-comment vocabulary list gains both names.

**Colour comes from the surrounding text token, never the icon (RD6).** `fill=
"currentColor"` is unchanged from every other glyph — `statTrendIcon` (D3) supplies
`text-success`/`text-error` via `Class`, never a hex/raw value. `make ui-guard` covers
this the same way it already covers every other semantic-token use in this file.

## D5 — No `Desc` line on the Travel Progress tiles (RD10, answered)

RD10's general rule — a tile with a delta shows the number on its `Desc` line
("+0.4 vs prev. day") — applies to tier 4's tyre tiles, which show a **current value**
(this reading) alongside a **computed change** (vs. yesterday): two numbers, so the
second belongs in `Desc`.

Travel Progress's two tiles are different in shape: `DistanceTraveled` and
`BatteryUsed` are **already** single-day deltas (the day's driven distance, the day's
battery used) — there is no second, further "change vs. previous day" number to show
underneath, and RD-fixed already forbids deriving one for the icon. Adding a `Desc` here
would either restate the same number the `Value` already shows, or invent a second
metric the roadmap never asked for. Both tiles pass `Desc: ""` (the existing zero-value
behaviour — `StatTile` already renders no `stat-desc` line when `Desc` is empty).

## D6 — i18n keys (six new, `catalog.go`)

One `Key` constant + one `catalog` entry per key, `ES`/`EN` on the same line, following
the file's existing `dashboard.*` namespace:

| Key | ES | EN |
|---|---|---|
| `KeyDashboardTravelProgressTitle` (`dashboard.travel_progress_title`) | Progreso de viaje | Travel Progress |
| `KeyDashboardTravelProgressDesc` (`dashboard.travel_progress_desc`) | Distancia recorrida y batería usada en el último día calculado. | Distance travelled and battery used on the last computed day. |
| `KeyDashboardInteriorExteriorTitle` (`dashboard.interior_exterior_title`) | Interior / Exterior | Interior / Exterior |
| `KeyDashboardInteriorExteriorDesc` (`dashboard.interior_exterior_desc`) | Temperatura dentro y fuera del vehículo. | Temperature inside and outside the vehicle. |
| `KeyDashboardDistanceTraveled` (`dashboard.distance_traveled`) | Distancia recorrida | Distance travelled |
| `KeyDashboardBatteryUsed` (`dashboard.battery_used`) | Batería usada | Battery used |

No Tire-pressure-subsection key is added here (D1) — tier 4 adds its own when it builds
that subsection.

## D7 — Database (checked, not skipped)

**This change touches no database object.** No migration, no new column, no new query,
no new index. `analytics.vehicle_metrics`, `distance_traveled_km_calc` and
`consumed_pct` all already exist (tier 1, archived). This tier's only analytics contact
is reading two already-public pointer fields off a struct the existing
`LatestMetricsByAccount` call already returns.

## D8 — Makefile / guards check (checked, not skipped)

No new `make` target and no Makefile edit. The two existing guards that apply:

- **`make ui-guard`** — the new markup (inner grid, two `<section>`s, `statTrendIcon`)
  uses only `ui.*` components, Tailwind layout utilities (`grid`, `gap-*`, `flex`,
  `md:col-span-*`), and semantic tokens (`text-success`, `text-error`) — no raw colour,
  no inline DaisyUI component class. Passes.
- **`make i18n-guard`** — all six new strings resolve through `i18n.T`; none is a bare
  text node or a hardcoded Go-side literal. Passes.

`make templ` and `make css` are required after this tier's `.templ` edits (new markup +
new Tailwind classes: `md:col-span-4`, `md:col-span-8`, `shrink-0`, none of which are new
utilities the scanner hasn't seen before, but the regen step is still required per the
module's own regeneration cheatsheet).

## D9 — Docs to update in this same change

- `internal/gateway/AGENTS.md` — one short addition: the Vehicle Status panel now has
  three named subsections (Travel Progress, a tier-4 Tire pressure placeholder,
  Interior/Exterior) instead of one flat tile row, plus one line noting
  `StatTileProps.Trend` and the two new `Icon` glyphs, so a future agent finds them by
  reading this file instead of re-deriving them from `stat_tile.templ`/`icon.templ`.
- `kkpa/context/use-case/gateway/read-dashboard-bento.md` — its "Triggered by" /
  "flow" section currently says the stat row is "four tiles: odometer, interior temp,
  exterior temp, and the lifetime 100%-charge count" and that `mapDashboardSnapshot`
  reads "nine pointer fields" — both go stale. Update the tile-row description to the
  new subsection layout and note `mapDashboardSnapshot` now reads eleven pointer fields
  (the nine from RM38 plus `DistanceTraveledKmCalc`/`ConsumedPct`), with the four TPMS
  fields still unread until tier 4. This file already has a forward-pointer to this
  tier — see its own "Nine is what this mapper reads, not what the type holds" note.
- `kkpa/context/input-port/gateway/dashboard.md` — its page "Description" row lists
  "four stat tiles — Odometer / Interior / Exterior / 100% Charges" in one flat
  sentence; update it to describe the subsection layout (left column lifetime tiles,
  right column named subsections).
- No other KB file names this page's tile layout (checked:
  `grep -rl "dashboard.*tile\|stat.tile" kkpa/context/`).

## Test Contract

Authored before implementation, per `ai/go-conventions.md` §Testing. Per RD8, this is
the whole test surface for this tier — no markup-coupled test.

### `mapDashboardSnapshot` — extend the two existing fixture tests

`internal/gateway/handlers/handlers_test.go` already has
`TestMapDashboardSnapshot_FixtureFull` and `TestMapDashboardSnapshot_FixtureNil`. Extend
both — do not add new dedicated tests for the two new formatters, mirroring how every
other pointer field on this fixture is already covered by these same two tests rather
than by one test per field.

**`TestMapDashboardSnapshot_FixtureFull`** — add to the fixture:
`DistanceTraveledKmCalc: ptrF64(45.2)`, `ConsumedPct: ptrF64(12.3)`.

**Expected:** `vm.DistanceTraveled == "45 km"` (`formatKm(45.2)` rounds to 45);
`vm.BatteryUsed == "12.3%"` (`formatPctRaw(12.3)` + `"%"`).

**`TestMapDashboardSnapshot_FixtureNil`** — fixture already leaves every pointer field
nil (a pre-migration row); no change to the fixture itself.

**Expected:** `vm.DistanceTraveled == "—"`, `vm.BatteryUsed == "—"` — added assertions,
same "—" placeholder every other nil pointer field in this test already asserts.

### Not tested (RD8)

- `statTrendIcon`'s branch selection — presence of a decoration, banned by
  `internal/gateway/AGENTS.md` §"Do not test what the page looks like".
- The new panel layout, section ids, grid classes — layout, verified by eye at 375px and
  desktop per the module's existing convention (no automated test for how a page looks).
- `TestCatalog_AllKeysHaveBothLanguages` already covers the six new keys automatically
  (existing test, no new test needed) — confirm by inspection that every new line has
  both `ES` and `EN` non-empty (mirrors `RM42-gateway-add-theme-selector` T2.2's
  precedent for the identical situation).

## Files touched (for the implementing worker)

| File | Change |
|---|---|
| `internal/gateway/templates/ui/icon.templ` | +2 `case` branches, doc-comment vocabulary list |
| `internal/gateway/templates/ui/stat_tile.templ` | `+Trend string` field, `statTrendIcon`, wrap value+icon in a flex row |
| `internal/gateway/templates/pages/dashboard.templ` | Replace the image+flat-tile-grid block with the Option B inner grid (D1) |
| `internal/gateway/templates/fragments/dashboard_vm.go` | +2 fields (`DistanceTraveled`, `BatteryUsed`) |
| `internal/gateway/handlers/handlers.go` | +2 formatters (`dashDistanceOrDash`, `dashBatteryUsedOrDash`), +2 lines in `mapDashboardSnapshot` |
| `internal/gateway/handlers/handlers_test.go` | Extend `TestMapDashboardSnapshot_FixtureFull`/`FixtureNil` |
| `internal/gateway/i18n/catalog.go` | +6 keys |
| `internal/gateway/AGENTS.md` | Short note (D9) |
| `kkpa/context/use-case/gateway/read-dashboard-bento.md` | Corrected per D9 |
| `kkpa/context/input-port/gateway/dashboard.md` | Corrected per D9 |

Regeneration after the `.templ` edits: `make templ && make css` (or `make generate`).
