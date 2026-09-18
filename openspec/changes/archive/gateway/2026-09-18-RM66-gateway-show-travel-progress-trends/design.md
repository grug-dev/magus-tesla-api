# Design — RM66-gateway-show-travel-progress-trends

## Verified against the code — do not trust the roadmap blindly

The roadmap's "Facts verified against the code" section is binding and is not
repeated here except where this dispatch needed one more level of detail to
write the exact diff. Checked again while writing this design, all still
hold, no drift:

1. **`ui.StatTileProps.Trend`** (`internal/gateway/templates/ui/stat_tile.templ:6-17`)
   is exactly the 2-value vocabulary the roadmap says: `"up"` → `text-success`,
   `"down"` → `text-error`, `""` → no icon. `statTrendIcon` (lines 77-85)
   switches on the same two strings plus a `default:` that renders nothing.
2. **`dashTireTrend`** (`internal/gateway/handlers/handlers.go:516-524`) already
   implements exactly the nil/zero-vs-real-direction rule D-C needs: `nil` or
   `*v == 0` → `""`, else `"up"`/`"down"` by sign. This is the function to
   generalise for Efficiency's colour, not reinvent.
3. **`dashTireDelta`** (`handlers.go:532-537`) already implements the "nil ⇒ no
   line, real zero ⇒ a rendered zero line" rule the test contract below needs
   for all three new tiles, via `i18n.KeyDashboardTireDeltaDesc`
   (`"%s vs prev. day"`) and `formatSignedPSI` (`format.go:96-103`).
4. **`dashTireWheel`** (`handlers.go:542-548`) is the exact shape to mirror: a
   small builder combining a value formatter, `dashTireTrend`, and
   `dashTireDelta` into one `TireWheelVM`.
5. **`mapDashboardSnapshot`** (`handlers.go:563-608`) sets the three current
   flat fields at lines 577-579:
   `vm.DistanceTraveled = dashDistanceOrDash(vs.DistanceTraveledKmCalc)`,
   `vm.BatteryUsed = dashBatteryUsedOrDash(vs.ConsumedPct)`,
   `vm.Efficiency = dashEfficiencyOrDash(vs.KmPerPctCalc)` — all three value
   formatters (`dashDistanceOrDash`, `dashBatteryUsedOrDash`,
   `dashEfficiencyOrDash`, `handlers.go:467-497`) stay exactly as they are;
   this tier adds trend/delta alongside them, it does not touch how the
   value itself is computed or rounded.
6. **`dashboard.templ:81-83`** is the three hardcoded-`Trend` call sites named
   by the roadmap, confirmed unchanged.
7. **`analytics.VehicleStatus`** (`internal/analytics/analytics.go:370-382`)
   already carries `DistanceTraveledKmDeltaCalc *float64`,
   `ConsumedPctDeltaCalc *float64`, `KmPerPctDeltaCalc *float64` — tier 2,
   archived. Their doc comment's absence rule (nil on a predecessor-less day,
   nil when either side of the subtraction is itself nil, nil on the first
   day a recalculation pass considers) is the rule this tier's `nil` test
   cases below exercise; this tier does not re-derive or restate it, only
   consumes it.
8. **`internal/gateway/i18n/catalog.go:134,564`** — `KeyDashboardTireDeltaDesc`
   has exactly one Go usage site (`handlers.go:536`, confirmed by a
   whole-repo grep) besides its own declaration and catalogue entry. Renaming
   it is a 3-line change, not a sweep.

## Database

**None.** This tier reads three columns tier 2 already added and projected
(`analytics.vehicle_metrics.distance_traveled_km_delta_calc`,
`consumed_pct_delta_calc`, `km_per_pct_delta_calc`, via
`LatestVehicleMetricsByVehicles`). No migration, no schema change, no index
change, no new query.

## D1 — The third `Trend` value — one field, four non-empty states, not one

`stat_tile.templ`'s own comment reserved space for "a third value" in
`StatTileProps.Trend`. Read literally, D-C needs **two** new values, not one:
Distance travelled and Battery used still need a *real* direction (D-C: "every
tile shows an up or down arrow") — the only thing D-C removes is their
*colour*. Direction and colour are two separate facts here, so a single
`"neutral"` value could not carry both a direction and "not coloured" at once.

The vocabulary stays a **single closed string field** — not a second bool
prop — matching this component's own stated pattern ("a small closed string
list, not a bool pair", `internal/gateway/AGENTS.md` §"ui.StatTileProps.Trend").
Final vocabulary, five states counting the existing empty one:

| Value | Icon | Colour class | Used by |
|---|---|---|---|
| `""` | none | — | any tile with an absent delta (unchanged) |
| `"up"` | `trending_up` | `text-success` | Efficiency, positive delta (unchanged rule, new caller) |
| `"down"` | `trending_down` | `text-error` | Efficiency, negative delta (unchanged rule, new caller) |
| `"up-neutral"` | `trending_up` | `text-neutral` | Distance travelled / Battery used, positive delta |
| `"down-neutral"` | `trending_down` | `text-neutral` | Distance travelled / Battery used, negative delta |

`text-neutral` is DaisyUI's existing semantic "neutral" token, already used
by `ui.Dot`/`ui.Badge`'s own `"neutral"` variant (`dot.go`, `badge.go`) — not
a new token, not a hex value.

### `internal/gateway/templates/ui/stat_tile.templ`

- Update `StatTileProps.Trend`'s doc comment to the five-state table above
  (replace the "third value is not modeled" sentence, since it now is).
- Extend `statTrendIcon`'s `switch` with two more cases:

  ```go
  case "up-neutral":
      @Icon(IconProps{Name: "trending_up", Class: "h-5 w-5 text-neutral shrink-0"})
  case "down-neutral":
      @Icon(IconProps{Name: "trending_down", Class: "h-5 w-5 text-neutral shrink-0"})
  ```

  No new glyph — `trending_up`/`trending_down` already exist in
  `ui/icon.templ`. Every existing call site (`""`, `"up"`, `"down"`) is
  byte-for-byte unaffected.

## D5 — View model — `internal/gateway/templates/fragments/dashboard_vm.go`

Add a new struct next to `TireWheelVM`, same three fields, same shape,
different name because it is not a wheel:

```go
// TravelStatVM is one Travel Progress tile: its current value, trend
// direction, and formatted day-over-day delta line. Same shape as
// TireWheelVM (Value/Trend/Delta) — the two subsections share one pattern,
// not two.
type TravelStatVM struct {
	Value string
	Trend string
	Delta string
}
```

Replace `DashboardData`'s three flat fields:

```go
DistanceTraveled string
BatteryUsed      string
Efficiency       string
```

with:

```go
DistanceTraveled TravelStatVM
BatteryUsed      TravelStatVM
Efficiency       TravelStatVM
```

Keep the existing doc comments on what each value means and its nil rule;
add one sentence to each noting `.Trend`/`.Delta` now follow the same rule
`TireWheelVM`'s own fields already document, so the comment does not repeat
the rule a second time.

## D2 — Handler changes — `internal/gateway/handlers/handlers.go`

### Rename `dashTireTrend` → `dashColoredTrend` (body unchanged)

The function's logic was never tyre-specific — it maps any delta to a
coloured direction. Efficiency needs the exact same rule. Renaming it once,
here, keeps the mechanism reused rather than duplicated (the roadmap's own
constraint: "never add a second way to draw an arrow"). Update its four
existing call sites (`dashTireWheel`) and its doc comment's "shared by
[wheels] and Efficiency" line.

### Add `dashNeutralTrend`

```go
// dashNeutralTrend is dashColoredTrend's direction rule with a neutral
// colour instead of success/error: nil or an exact zero still renders no
// icon, but a real positive or negative delta renders "up-neutral" /
// "down-neutral" instead of "up"/"down". Distance travelled and Battery
// used both use this — driving more or less is not itself good or bad
// news, so neither earns a colour, but both still point a real direction.
func dashNeutralTrend(v *float64) string {
	if v == nil || *v == 0 {
		return ""
	}
	if *v > 0 {
		return "up-neutral"
	}
	return "down-neutral"
}
```

### Generalise `dashTireDelta` → `dashDeltaDesc`

```go
// dashDeltaDesc formats any day-over-day delta as a tile's stat-desc line
// ("+0.4 vs prev. day"), using format to render the signed number. nil ->
// "" (no line at all) — never a fabricated "0 vs prev. day" for an unknown
// delta. A real zero DOES render its line, because it is a known value,
// not an absent one — distinct from the trend functions' own "zero gets no
// icon" rule; this function and they answer different questions from the
// same input. Shared by every tile that shows a day-over-day change.
func dashDeltaDesc(ctx context.Context, v *float64, format func(float64) string) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyDashboardDeltaDesc), format(*v))
}
```

`dashTireWheel` becomes:

```go
func dashTireWheel(ctx context.Context, raw, delta *float64) fragments.TireWheelVM {
	return fragments.TireWheelVM{
		Value: dashPSIOrDash(raw),
		Trend: dashColoredTrend(delta),
		Delta: dashDeltaDesc(ctx, delta, formatSignedPSI),
	}
}
```

Behaviour of the four tyre tiles is unchanged — this is a rename plus a
parameter, not a logic change.

### Add one small builder, mirroring `dashTireWheel`'s own shape

```go
// dashTravelStat builds one Travel Progress tile's TravelStatVM from its
// own day's value and its day-over-day delta. formatValue is the tile's
// existing value formatter; formatDelta is its signed-delta formatter;
// trend picks dashNeutralTrend or dashColoredTrend per D-C. Mirrors
// dashTireWheel's own "one small builder, several thin call sites" shape.
func dashTravelStat(ctx context.Context, value, delta *float64, formatValue func(*float64) string, formatDelta func(float64) string, trend func(*float64) string) fragments.TravelStatVM {
	return fragments.TravelStatVM{
		Value: formatValue(value),
		Trend: trend(delta),
		Delta: dashDeltaDesc(ctx, delta, formatDelta),
	}
}
```

### `mapDashboardSnapshot` (lines 577-579 today)

```go
vm.DistanceTraveled = dashTravelStat(ctx, vs.DistanceTraveledKmCalc, vs.DistanceTraveledKmDeltaCalc, dashDistanceOrDash, formatSignedKm, dashNeutralTrend)
vm.BatteryUsed = dashTravelStat(ctx, vs.ConsumedPct, vs.ConsumedPctDeltaCalc, dashBatteryUsedOrDash, formatSignedPct, dashNeutralTrend)
vm.Efficiency = dashTravelStat(ctx, vs.KmPerPctCalc, vs.KmPerPctDeltaCalc, dashEfficiencyOrDash, formatSignedKmPerPct, dashColoredTrend)
```

Each tile's own *value* still comes from the same field it reads today
(`DistanceTraveledKmCalc`, `ConsumedPct`, `KmPerPctCalc`) — only the new
*trend*/*delta* arguments are new, read from the matching `*DeltaCalc`
field tier 2 added.

## D4 — The zero/nil delta rule

`nil` and an exact `0.0` delta are different facts and must never render the
same way. `dashNeutralTrend` and `dashColoredTrend` (D2) both treat them
alike for the icon: nil or an exact zero renders no trend icon. `dashDeltaDesc`
(D2) tells them apart on the numeric line: nil renders no line at all — never
a fabricated "0 vs prev. day" — while a real zero still renders its line,
because it is a known value, not an absent one. This is the same rule
`dashTireTrend`/`dashTireDelta` already applied to the four tyre tiles; this
tier extends it, unchanged, to the three Travel Progress tiles. See the Test
contract below for every input this rule governs.

## New format helpers — `internal/gateway/handlers/format.go`

One shared one-decimal signed formatter, factored out of the logic
`formatSignedPSI` already has (its own body moves into the shared helper; its
signature and behaviour are unchanged for every existing caller):

```go
// formatSigned1 renders v to one decimal place with an explicit "+" for a
// strictly positive value; strconv's own "-" covers negative; zero gets
// neither sign. The shared core behind every one-decimal signed delta
// formatter below.
func formatSigned1(v float64) string {
	s := strconv.FormatFloat(v, 'f', 1, 64)
	if v > 0 {
		return "+" + s
	}
	return s
}

func formatSignedPSI(v float64) string { return formatSigned1(v) }

// formatSignedPct mirrors formatSignedPSI's shape for a battery-percent
// delta — one decimal, no "%" suffix (the caller's format string supplies
// the "vs prev. day" context, exactly like formatSignedPSI's own doc note).
func formatSignedPct(v float64) string { return formatSigned1(v) }

// formatSignedKmPerPct mirrors formatSignedPSI's shape for an efficiency
// delta (kilometres per battery percent) — one decimal, no unit suffix (the
// tile's main value already shows "km/%").
func formatSignedKmPerPct(v float64) string { return formatSigned1(v) }

// formatSignedKm renders a signed distance delta as a whole kilometre count
// with an explicit "+" — e.g. 42 -> "+42", -17 -> "-17", 0 -> "0". Rounds
// the same way formatKm does (math.Round), so a tile's delta line and its
// main value never round to a different number for the same input.
func formatSignedKm(v float64) string {
	whole := int(math.Round(v))
	s := strconv.Itoa(whole)
	if whole > 0 {
		return "+" + s
	}
	return s
}
```

`formatSignedPSI`'s existing four call sites and its own doc comment need no
change beyond the body move — its behaviour for every existing input is
identical before and after.

## Template wiring — `internal/gateway/templates/pages/dashboard.templ:81-83`

Before:

```go
@ui.StatTile(ui.StatTileProps{Label: i18n.T(ctx, i18n.KeyDashboardDistanceTraveled), Value: dashStat(d.HasSnapshot, d.DistanceTraveled), Trend: "up"})
@ui.StatTile(ui.StatTileProps{Label: i18n.T(ctx, i18n.KeyDashboardBatteryUsed), Value: dashStat(d.HasSnapshot, d.BatteryUsed), Trend: "down"})
@ui.StatTile(ui.StatTileProps{Label: i18n.T(ctx, i18n.KeyDashboardEfficiency), Value: dashStat(d.HasSnapshot, d.Efficiency)})
```

After (mirrors the four tyre tiles two sections below, which already read
`.Value`/`.Trend`/`.Delta` off a per-tile VM):

```go
@ui.StatTile(ui.StatTileProps{Label: i18n.T(ctx, i18n.KeyDashboardDistanceTraveled), Value: dashStat(d.HasSnapshot, d.DistanceTraveled.Value), Trend: d.DistanceTraveled.Trend, Desc: d.DistanceTraveled.Delta})
@ui.StatTile(ui.StatTileProps{Label: i18n.T(ctx, i18n.KeyDashboardBatteryUsed), Value: dashStat(d.HasSnapshot, d.BatteryUsed.Value), Trend: d.BatteryUsed.Trend, Desc: d.BatteryUsed.Delta})
@ui.StatTile(ui.StatTileProps{Label: i18n.T(ctx, i18n.KeyDashboardEfficiency), Value: dashStat(d.HasSnapshot, d.Efficiency.Value), Trend: d.Efficiency.Trend, Desc: d.Efficiency.Delta})
```

`dashStat(d.HasSnapshot, ...)` is unchanged — it already collapses to `"—"`
when `HasSnapshot` is false, and `TravelStatVM.Value` already collapses to
`"—"` on its own nil rule, so the two never disagree (same double-guard
shape the tyre tiles already have with `dashStat(d.HasSnapshot,
d.TirePressureFL.Value)`).

## D3 — i18n

**No new catalogue key.** The tyre section's existing delta-format key is
generic already (`"%s vs prev. day"` / `"%s vs. día anterior"` — it names no
tyre-specific word), so it is renamed, not duplicated, now that three more
tiles use it:

- `internal/gateway/i18n/catalog.go:134`:
  `KeyDashboardTireDeltaDesc Key = "dashboard.tire_delta_desc"` →
  `KeyDashboardDeltaDesc Key = "dashboard.delta_desc"`
- `internal/gateway/i18n/catalog.go:564`:
  `KeyDashboardTireDeltaDesc: {ES: "%s vs. día anterior", EN: "%s vs prev. day"}` →
  `KeyDashboardDeltaDesc: {ES: "%s vs. día anterior", EN: "%s vs prev. day"}`
  (same two strings, unchanged — only the Go name and key string move)
- `handlers.go:536` (inside what becomes `dashDeltaDesc`): update the one
  reference from `i18n.KeyDashboardTireDeltaDesc` to `i18n.KeyDashboardDeltaDesc`.

`TestCatalog_AllKeysHaveBothLanguages` and `make i18n-guard` both still pass:
the key still has both `ES` and `EN` non-empty, and it is still reached only
through `i18n.T(ctx, ...)`.

## Test contract — authored before implementation

Every case below is what `internal/gateway/handlers/handlers_test.go` (or a
new `_test.go` file in the same package) must assert, verbatim. These are
the values a later implementation is checked against — not values read back
from the code after it is written.

### `dashNeutralTrend` (Distance travelled, Battery used)

| Input `*float64` | Output |
|---|---|
| `42.0` (positive) | `"up-neutral"` |
| `-17.0` (negative) | `"down-neutral"` |
| `0.0` (exact zero) | `""` |
| `nil` | `""` |

### `dashColoredTrend` (Efficiency; also the four tyre wheels, unchanged)

| Input `*float64` | Output |
|---|---|
| `0.4` (positive) | `"up"` |
| `-0.6` (negative) | `"down"` |
| `0.0` (exact zero) | `""` |
| `nil` | `""` |

### `formatSignedKm`

| Input | Output |
|---|---|
| `42.0` | `"+42"` |
| `-17.0` | `"-17"` |
| `0.0` | `"0"` |
| `42.6` | `"+43"` (rounds like `formatKm`) |

### `formatSignedPct` / `formatSignedKmPerPct` (both one decimal, same rule as the existing `formatSignedPSI`)

| Input | Output |
|---|---|
| `3.2` | `"+3.2"` |
| `-1.5` | `"-1.5"` |
| `0.0` | `"0.0"` |

### `dashDeltaDesc` (using `formatSignedKm` as the example `format`)

| Input `*float64` | Output |
|---|---|
| `42.0` | `"+42 vs prev. day"` |
| `-17.0` | `"-17 vs prev. day"` |
| `0.0` | `"0 vs prev. day"` (rendered — a known zero, not an absent delta) |
| `nil` | `""` (no line at all) |

### `mapDashboardSnapshot` — full per-tile `TravelStatVM`, all three tiles

Distance travelled (`vs.DistanceTraveledKmCalc = 128.4`, so `Value` = `"128 km"` in every row below):

| `DistanceTraveledKmDeltaCalc` | `.Trend` | `.Delta` |
|---|---|---|
| `42.0` | `"up-neutral"` | `"+42 vs prev. day"` |
| `-17.0` | `"down-neutral"` | `"-17 vs prev. day"` |
| `0.0` | `""` | `"0 vs prev. day"` |
| `nil` | `""` | `""` |

Battery used (`vs.ConsumedPct = 12.3`, so `Value` = `"12.3%"` in every row below):

| `ConsumedPctDeltaCalc` | `.Trend` | `.Delta` |
|---|---|---|
| `3.2` | `"up-neutral"` | `"+3.2 vs prev. day"` |
| `-1.5` | `"down-neutral"` | `"-1.5 vs prev. day"` |
| `0.0` | `""` | `"0.0 vs prev. day"` |
| `nil` | `""` | `""` |

Efficiency (`vs.KmPerPctCalc = 2.8`, so `Value` = `"2.8 km/%"` in every row below):

| `KmPerPctDeltaCalc` | `.Trend` | `.Delta` |
|---|---|---|
| `0.4` | `"up"` | `"+0.4 vs prev. day"` |
| `-0.6` | `"down"` | `"-0.6 vs prev. day"` |
| `0.0` | `""` | `"0.0 vs prev. day"` |
| `nil` | `""` | `""` |

### `ui.StatTile` / `statTrendIcon` (`internal/gateway/templates/ui/stat_tile_test.go` or equivalent)

Assert what the markup DOES, not how it looks (`ai/go-conventions.md`
§Testing): that a given `Trend` value renders exactly one `<svg>` with the
right class list, or none. Never assert path data, colour hex, or element
order.

| `Trend` | Rendered |
|---|---|
| `""` | no `<svg>` |
| `"up"` | one `<svg class="h-5 w-5 text-success shrink-0">` |
| `"down"` | one `<svg class="h-5 w-5 text-error shrink-0">` |
| `"up-neutral"` | one `<svg class="h-5 w-5 text-neutral shrink-0">` |
| `"down-neutral"` | one `<svg class="h-5 w-5 text-neutral shrink-0">` |

## D6 — Spec and KB corrections

- **`openspec/specs/gateway/spec.md`**: this change's spec delta (below)
  **MODIFIES** the "Dashboard Travel Progress Subsection" requirement,
  replacing every fixed-icon sentence and scenario with the real
  delta-driven behaviour, matching the shape the neighbouring "Dashboard Tire
  Pressure Subsection" requirement already uses. On archive/sync this
  replaces the stale text at `spec.md:3652` in place.
- **`kkpa/context/use-case/gateway/read-dashboard-bento.md:242`** states the
  same now-false claim ("the two Travel Progress trend icons are fixed,
  never computed"). This file is not part of the OpenSpec artifact set, so
  it is not edited by this dispatch — it is an implementation-wave task (see
  tasks.md), per `CLAUDE.md`'s "Docs track structural change" rule, which
  requires the KB correction to land in the same change that invalidates it.

## Cross-module work

**None.** Unlike tier 2, this tier's changes are entirely inside
`internal/gateway/`. It reads `analytics.VehicleStatus`'s three already-shipped
fields through the existing `analytics.Reader` interface; it adds no new
field to that port and calls no write path. No leader-owned fix is needed in
this tier's wave.
