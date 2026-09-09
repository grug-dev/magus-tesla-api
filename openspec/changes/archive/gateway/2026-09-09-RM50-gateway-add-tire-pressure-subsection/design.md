# Design — RM50-gateway-add-tire-pressure-subsection

> Roadmap decisions RD1–RD13 (`openspec/roadmaps/RM50-vehicle-status-subsections.md`)
> are binding and not re-opened here. This file adds the implementation-level
> decisions (D1–D8) needed to build tier 4.

## Overview

One file changes shape (`dashboard.templ` — the placeholder comment becomes a real
`<section>`), one file grows a struct + four fields (`dashboard_vm.go`), one file
grows three formatters + one builder + four mapping lines (`handlers.go`), one file
grows two formatters (`format.go`), `catalog.go` gains seven keys, and two KB guides
get one line each. No database object, no new module port, no new route, no change to
`ui/icon.templ` or `ui/stat_tile.templ` — this tier reuses tier 2's `Trend` mechanism
verbatim.

## D1 — Database (checked, not skipped)

**This tier touches no database.** No migration, no new column, no new query, no new
index. `analytics.vehicle_metrics`'s four raw TPMS columns (tier 1) and four `_calc`
delta columns (tier 3) already exist and are already archived. This tier's only
analytics contact is reading eight already-public pointer fields off
`analytics.VehicleStatus`, a struct the existing `LatestMetricsByAccount` call already
returns to `mapDashboardSnapshot`. Per `CLAUDE.md`'s Design-Gates config, the database
gate applies only to changes that touch a database object — this tier does not, so it
does not apply.

## D2 — `TireWheelVM` (one struct, four named fields — not a slice)

```go
// TireWheelVM is one wheel's tire-pressure tile: the current reading, its trend
// direction, and its formatted day-over-day delta. Mirrors SuperchargerTiles'
// "one struct per repeated shape" pattern (internal/gateway/templates/fragments/
// supercharger_vm.go) rather than four separate flat string fields per wheel.
type TireWheelVM struct {
	// Value is the wheel's current pressure ("42.1 PSI"), or "—" when the raw
	// reading is absent (the vehicle did not report TPMS at capture, or the row
	// predates the RM50 tier 1 migration — analytics.VehicleStatus's own doc
	// comment on TpmsPressureFLPSI). Never a fabricated value.
	Value string
	// Trend is "up", "down", or "" for ui.StatTileProps.Trend (tier 2's
	// mechanism, reused verbatim — no second trend mechanism is introduced).
	// "" covers two distinct cases: the delta is absent (unknown), or the delta
	// is exactly 0.0 (a real "no change" reading) — see D5.
	Trend string
	// Delta is the tile's stat-desc line ("+0.4 vs prev. day", RD10), or "" when
	// the delta is absent (StatTile renders no stat-desc line at all when Desc
	// is empty — never a fabricated "0.0 vs prev. day" for an unknown delta).
	Delta string
}
```

`fragments.DashboardData` gains:

```go
// --- Tire pressure (PSI) subsection (RM50 tier 4) ---
TirePressureFL TireWheelVM
TirePressureFR TireWheelVM
TirePressureRL TireWheelVM
TirePressureRR TireWheelVM
```

**Why four named fields, not `TirePressure [4]TireWheelVM` or `map[string]TireWheelVM`.**
Every other multi-value group on this VM (`SuperchargerTiles`, `ExternalChargeTiles`) uses
named fields, not an array or map — a named field is self-documenting at the call site
(`d.TirePressureFL` vs. `d.TirePressure[0]`) and the set is fixed at exactly four wheels
(RD1's own rationale for four scalar `_calc` columns applies identically here: no
flexibility is needed, so no indirection is bought).

## D3 — Three pure formatters + one builder (`handlers.go`)

```go
// dashPSIOrDash formats a raw tyre-pressure reading. nil -> "—" (the vehicle did
// not report TPMS at capture, or the row predates the RM50 tier 1 migration).
// Same nil-placeholder rule as dashTempOrDash.
func dashPSIOrDash(v *float64) string {
	if v == nil {
		return "—"
	}
	return formatPSI(*v)
}

// dashTireTrend maps a tyre-pressure delta to a StatTile Trend value. nil (no
// predecessor day, or either day's raw wheel reading missing — analytics.
// VehicleStatus.TpmsPressureFLPSICalc's own doc comment) and exactly 0.0 (a
// real "no change" reading, RD13) both render no icon: ui.StatTileProps.Trend
// only models "up"/"down"/"", and the roadmap explicitly forbids inventing a
// third, neutral glyph. Positive -> "up", negative -> "down".
func dashTireTrend(v *float64) string {
	if v == nil || *v == 0 {
		return ""
	}
	if *v > 0 {
		return "up"
	}
	return "down"
}

// dashTireDelta formats a tyre-pressure delta as the tile's stat-desc line
// (RD10), e.g. "+0.4 vs prev. day". nil -> "" (no line at all) — never a
// fabricated "0.0 vs prev. day" for an unknown delta. A real 0.0 DOES render
// ("0.0 vs prev. day"), because it is a known value, not an absent one (RD13)
// — distinct from dashTireTrend's own "0.0 gets no icon" rule; the two
// functions answer different questions from the same input.
func dashTireDelta(ctx context.Context, v *float64) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyDashboardTireDeltaDesc), formatSignedPSI(*v))
}

// dashTireWheel builds one wheel's TireWheelVM from its raw reading and delta.
// Small builder so mapDashboardSnapshot's four call sites (D6) are one line
// each instead of three.
func dashTireWheel(ctx context.Context, raw, delta *float64) fragments.TireWheelVM {
	return fragments.TireWheelVM{
		Value: dashPSIOrDash(raw),
		Trend: dashTireTrend(delta),
		Delta: dashTireDelta(ctx, delta),
	}
}
```

## D4 — Two formatters (`format.go`)

```go
// formatPSI renders a tyre-pressure value to one decimal place with a " PSI"
// suffix — e.g. 42.06 -> "42.1 PSI". Mirrors formatKm's "round once, unit-
// suffix here" shape; PSI needs sub-integer precision (a ~1 PSI leak is
// meaningful), unlike formatKm's whole-number odometer.
func formatPSI(psi float64) string {
	return strconv.FormatFloat(psi, 'f', 1, 64) + " PSI"
}

// formatSignedPSI renders a signed tyre-pressure delta to one decimal place,
// no unit suffix (the caller's i18n format string supplies the "vs prev. day"
// context) — e.g. 0.4 -> "+0.4", -0.4 -> "-0.4", 0.0 -> "0.0". Explicit "+"
// only for a strictly positive value; strconv's own "-" already covers a
// negative one; zero gets neither sign, matching dashTireTrend's own "0.0 is
// neither up nor down" rule.
func formatSignedPSI(v float64) string {
	s := strconv.FormatFloat(v, 'f', 1, 64)
	if v > 0 {
		return "+" + s
	}
	return s
}
```

Both live in `internal/gateway/handlers/format.go`, next to `formatKm`/`formatPctRaw`
— no new file (closed vocabulary; one formatters file).

## D5 — Zero vs. nil: why they render differently (answers a gap RD3/RD10/RD13 leave open)

The roadmap explicitly settles the NULL case ("never a fabricated zero, never a
neutral arrow") and the fact that a real `0.0` "must not also mean 'unknown'" (RD13),
but does not name which icon a real `0.0` gets — there are only two glyphs
(`trending_up`/`trending_down`), and neither means "unchanged". This tier's answer:
**`0.0` renders no icon, exactly like `nil`, but DOES render its delta line** — the
two behaviors are independent (D3's doc comments state this explicitly, since the
symmetry is easy to miss on a re-read).

**Why not fabricate a third glyph.** `ui.StatTileProps.Trend` (tier 2, D3 of that
tier's design) models exactly `"up"`/`"down"`/`""` — a closed, three-value
vocabulary matching `ui.BadgeProps.Kind` / `ui.DotProps.Variant`'s own shape. Adding a
fourth `"flat"` value here would be a second trend mechanism sitting beside the first,
which the roadmap dispatch explicitly rules out ("reuse it... do not add a second way
to render a trend").

**Why the delta line still renders at 0.0.** `Value`/`Trend`/`Delta` answer three
different questions from the same input: "what is the reading" (raw), "did it go up or
down" (icon), and "what changed" (number). A `0.0` delta has a real answer to the third
question — "nothing" — the same way a `0` `MaxRangeChargeCounter` is a real answer
elsewhere on this card (`dashCountOrDash`'s own precedent: a reported `0` is never
collapsed to the nil placeholder).

## D6 — `mapDashboardSnapshot` mapping (four new lines)

```go
vm.TirePressureFL = dashTireWheel(ctx, vs.TpmsPressureFLPSI, vs.TpmsPressureFLPSICalc)
vm.TirePressureFR = dashTireWheel(ctx, vs.TpmsPressureFRPSI, vs.TpmsPressureFRPSICalc)
vm.TirePressureRL = dashTireWheel(ctx, vs.TpmsPressureRLPSI, vs.TpmsPressureRLPSICalc)
vm.TirePressureRR = dashTireWheel(ctx, vs.TpmsPressureRRPSI, vs.TpmsPressureRRPSICalc)
```

Added next to the existing `vm.DistanceTraveled = ...` / `vm.BatteryUsed = ...` lines
tier 2 added, same function, same "eight new pointer fields read" pattern the
function's own doc comment already tracks incrementally (RM38 → RM50 tier 1 → this
tier).

**When `HasSnapshot` is false, these four lines never run** — `mapDashboardSnapshot`
is only called from the `HasSnapshot` branch in `dashboardFor` (unchanged control
flow). Each `TireWheelVM` field then keeps its Go zero value (`Value: "", Trend: "",
Delta: ""`); the template's existing `dashStat(d.HasSnapshot, ...)` wrapper (D7) turns
the empty `Value` into the "—" placeholder, and the empty `Trend`/`Delta` already mean
"no icon, no line" — no extra branching needed for this case.

## D7 — Panel layout (RD9's 2x2 grid, filling tier 2's placeholder)

Replaces the `//` placeholder comment tier 2 left in `dashboard.templ`, between the
`travel-progress` and `interior-exterior` `<section>`s:

```templ
<section id="tire-pressure">
	@ui.SectionHeader(ui.SectionHeaderProps{Title: i18n.T(ctx, i18n.KeyDashboardTirePressureTitle), Desc: i18n.T(ctx, i18n.KeyDashboardTirePressureDesc)})
	<div class="grid grid-cols-2 gap-3">
		@ui.StatTile(ui.StatTileProps{Label: i18n.T(ctx, i18n.KeyDashboardTireFL), Value: dashStat(d.HasSnapshot, d.TirePressureFL.Value), Trend: d.TirePressureFL.Trend, Desc: d.TirePressureFL.Delta})
		@ui.StatTile(ui.StatTileProps{Label: i18n.T(ctx, i18n.KeyDashboardTireFR), Value: dashStat(d.HasSnapshot, d.TirePressureFR.Value), Trend: d.TirePressureFR.Trend, Desc: d.TirePressureFR.Delta})
		@ui.StatTile(ui.StatTileProps{Label: i18n.T(ctx, i18n.KeyDashboardTireRL), Value: dashStat(d.HasSnapshot, d.TirePressureRL.Value), Trend: d.TirePressureRL.Trend, Desc: d.TirePressureRL.Delta})
		@ui.StatTile(ui.StatTileProps{Label: i18n.T(ctx, i18n.KeyDashboardTireRR), Value: dashStat(d.HasSnapshot, d.TirePressureRR.Value), Trend: d.TirePressureRR.Trend, Desc: d.TirePressureRR.Delta})
	</div>
</section>
```

**Why `grid-cols-2` alone gives a 2x2, not a row of four (RD9).** Four items in a
two-column grid wrap to two rows by construction — the same `grid grid-cols-2 gap-3`
class tier 2 already uses for `travel-progress` and `interior-exterior` (both
two-tile subsections). No new grid class is introduced; this is the third call site of
an existing pattern, not a new one.

**Wheel order — FL, FR, RL, RR.** Reads top-left, top-right, bottom-left,
bottom-right — the natural reading order for a 2x2 grid representing a vehicle's four
corners, and matches the column order tier 1/tier 3 already use everywhere
(`analytics.VehicleStatus` field order, the migration's column order).

**Every value still passes through `dashStat(d.HasSnapshot, ...)`.** This mirrors
`InsideTemp`/`OutsideTemp`'s own call sites exactly (D2 of tier 2's design): the field
itself already carries "—" when the raw reading is absent (`dashPSIOrDash`), and
`dashStat` additionally collapses it to "—" when there is no stored row at all — the
same two-layer placeholder shape every other tile on this card already uses.
`Trend`/`Desc` are NOT wrapped in `dashStat` — an empty string already means "no icon"
/ "no line" for both, so no extra placeholder logic is needed (unlike `Value`, which
would otherwise render a bare empty cell instead of "—").

**Section id.** `tire-pressure` — a bare `<section>`, so per `internal/gateway/AGENTS.md`
§"Identifiable cards & sections" the page sets `id` directly on the element, matching
`travel-progress`/`interior-exterior`'s own precedent and the id tier 2's own
placeholder comment already named.

## D8 — i18n keys (seven new, `catalog.go`)

One `Key` constant + one `catalog` entry per key, `ES`/`EN` on the same line,
following the file's existing `dashboard.*` namespace (mirrors tier 2's D6 table
exactly):

| Key | ES | EN |
|---|---|---|
| `KeyDashboardTirePressureTitle` (`dashboard.tire_pressure_title`) | Presión de llantas (PSI) | Tire pressure (PSI) |
| `KeyDashboardTirePressureDesc` (`dashboard.tire_pressure_desc`) | Presión actual de cada llanta y su cambio respecto al día anterior. | Current pressure per wheel and its change versus the previous day. |
| `KeyDashboardTireFL` (`dashboard.tire_fl`) | Delantera izquierda | Front left |
| `KeyDashboardTireFR` (`dashboard.tire_fr`) | Delantera derecha | Front right |
| `KeyDashboardTireRL` (`dashboard.tire_rl`) | Trasera izquierda | Rear left |
| `KeyDashboardTireRR` (`dashboard.tire_rr`) | Trasera derecha | Rear right |
| `KeyDashboardTireDeltaDesc` (`dashboard.tire_delta_desc`) | %s vs. día anterior | %s vs prev. day |

`KeyDashboardTireDeltaDesc`'s `%s` carries `formatSignedPSI`'s output (`"+0.4"`,
`"-0.3"`, `"0.0"`) — the sign is baked into the Go-formatted number, not into the
catalogue string, so one format string serves every sign case in both languages
(mirrors `KeyDashboardChargeLimit`'s own "%s inside the sentence" shape).

## D9 — Makefile / guards check (checked, not skipped)

No new `make` target and no Makefile edit. The two existing guards that apply:

- **`make ui-guard`** — the new markup (one `<section>`, four `ui.StatTile` calls, one
  `ui.SectionHeader` call) uses only `ui.*` components and Tailwind layout utilities
  (`grid`, `grid-cols-2`, `gap-3`) already used by the sibling subsections — no raw
  colour, no inline DaisyUI component class, no new utility. Passes.
- **`make i18n-guard`** — all seven new strings resolve through `i18n.T`; none is a
  bare text node or a hardcoded Go-side literal. Passes.

`make templ` and `make css` are required after the `.templ` edit — no new Tailwind
utility is introduced (the tile grid reuses `grid grid-cols-2 gap-3`, already compiled
for the sibling subsections), but the module's own regeneration cheatsheet requires
the step after any `.templ` edit regardless.

## D10 — Docs to update in this same change

- `internal/gateway/AGENTS.md` — one short addition: the Vehicle Status panel's three
  named subsections (Travel Progress, Tire pressure, Interior/Exterior) are now all
  built — the tier-4 placeholder note tier 2 left is superseded. No new `ui/` kit
  addition to document (this tier reuses `StatTileProps.Trend` verbatim).
- `kkpa/context/use-case/gateway/read-dashboard-bento.md` — its stat-row description
  (corrected by tier 2 to note a gap for tier 4) is corrected again to say the gap is
  filled, and `mapDashboardSnapshot`'s pointer-field count grows from eleven to
  nineteen (the eleven tier 2 left it at, plus the eight TPMS fields this tier reads).
- `kkpa/context/input-port/gateway/dashboard.md` — its page "Description" row is
  updated to name all three subsections instead of two plus a gap.
- No other KB file names this page's tile layout (checked:
  `grep -rl "dashboard.*tile\|stat.tile\|tire.pressure" kkpa/context/`).

## Test Contract

Authored before implementation, per `ai/go-conventions.md` §Testing. Per RD8, this is
the whole test surface for this tier — no markup-coupled test. The testable surface is
the pure view-model mapping: `dashPSIOrDash`, `dashTireTrend`, `dashTireDelta`,
`formatPSI`, `formatSignedPSI`, and their composition inside `mapDashboardSnapshot`.

### `mapDashboardSnapshot` — extend the two existing fixture tests

`internal/gateway/handlers/handlers_test.go` already has
`TestMapDashboardSnapshot_FixtureFull` and `TestMapDashboardSnapshot_FixtureNil`.
Extend both — do not add a new dedicated test per formatter, mirroring tier 2's own
"extend, don't multiply" precedent.

**`TestMapDashboardSnapshot_FixtureFull`** — add to the fixture, one wheel per branch
so all four `dashTireTrend`/`dashTireDelta` cases are covered in a single fixture:

| Field | Raw (`TpmsPressure**PSI`) | Delta (`TpmsPressure**PSICalc`) |
|---|---|---|
| FL | `ptrF64(42.06)` | `ptrF64(0.4)` (positive) |
| FR | `ptrF64(40.6)` | `ptrF64(-0.3)` (negative) |
| RL | `ptrF64(39.2)` | `ptrF64(0.0)` (real zero) |
| RR | `nil` | `nil` (absent) |

**Expected:**

| Field | `.Value` | `.Trend` | `.Delta` |
|---|---|---|---|
| `vm.TirePressureFL` | `"42.1 PSI"` | `"up"` | `"+0.4 vs prev. day"` |
| `vm.TirePressureFR` | `"40.6 PSI"` | `"down"` | `"-0.3 vs prev. day"` |
| `vm.TirePressureRL` | `"39.2 PSI"` | `""` | `"0.0 vs prev. day"` |
| `vm.TirePressureRR` | `"—"` | `""` | `""` |

**`TestMapDashboardSnapshot_FixtureNil`** — fixture already leaves every pointer field
nil (a pre-migration row); no change to the fixture itself.

**Expected:** all four wheels equal `fragments.TireWheelVM{Value: "—", Trend: "", Delta: ""}`.

### Not tested (RD8)

- The panel layout, section id, grid classes — layout, verified by eye at 375px and
  desktop per the module's existing convention (no automated test for how a page
  looks).
- `TestCatalog_AllKeysHaveBothLanguages` already covers the seven new keys
  automatically (existing test, no new test needed) — confirm by inspection that
  every new line has both `ES` and `EN` non-empty.

## Files touched (for the implementing worker)

| File | Change |
|---|---|
| `internal/gateway/templates/fragments/dashboard_vm.go` | +`TireWheelVM` struct, +4 fields on `DashboardData` |
| `internal/gateway/handlers/handlers.go` | +3 formatters (`dashPSIOrDash`, `dashTireTrend`, `dashTireDelta`), +1 builder (`dashTireWheel`), +4 lines in `mapDashboardSnapshot` |
| `internal/gateway/handlers/format.go` | +2 formatters (`formatPSI`, `formatSignedPSI`) |
| `internal/gateway/templates/pages/dashboard.templ` | Replace the tier-4 placeholder comment with the `tire-pressure` section (D7) |
| `internal/gateway/handlers/handlers_test.go` | Extend `TestMapDashboardSnapshot_FixtureFull`/`FixtureNil` per the Test Contract |
| `internal/gateway/i18n/catalog.go` | +7 keys |
| `internal/gateway/AGENTS.md` | Short note (D10) |
| `kkpa/context/use-case/gateway/read-dashboard-bento.md` | Corrected per D10 |
| `kkpa/context/input-port/gateway/dashboard.md` | Corrected per D10 |

Regeneration after the `.templ` edit: `make templ && make css` (or `make generate`).
