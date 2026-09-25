# Design — RM68-gateway-add-km-per-gallon-tile

## Verified against the code

1. **`fragments.VehicleStatsTiles`** (`internal/gateway/templates/fragments/vehicle_stats_vm.go:109-146`)
   already holds seven formatted string fields plus per-tile `*Trend`/`*Delta`
   pairs. `RangeFull` is the one existing field with NO trend pair — the
   precedent this tile partly follows (see D5).
2. **`sumVehicleStatsMonths`** (`internal/gateway/handlers/vehicle_stats_tiles.go:115-134`)
   loops `[]analytics.VehicleMonthlyMetrics` once, summing `t.cost +=
   m.ExtACCost + m.ExtDCCost + m.SCCost` per month. This is the exact per-month
   cost figure the roadmap's D9 test (`ext_ac_cost + ext_dc_cost + sc_cost = 0`)
   needs — no new cost computation, only a place to also feed it to the new
   gasoline accumulator.
3. **`buildVehicleStatsTiles`** (`vehicle_stats_tiles.go:151-183`) is called
   with a `vehicleStatsTotals` and an optional `prev *vehicleStatsTotals`, and
   is the single place that turns raw totals into `fragments.VehicleStatsTiles`.
4. **`vehicleStatsViewFor`** (`internal/gateway/handlers/vehicle_stats.go:74-160`)
   already makes a SECOND read past the primary `MonthlyMetricsBetween` call —
   `h.analyticsReader.ConsumedByDay` — whose failure is logged and degrades
   only the Gaps section; the figures already built keep rendering. This is
   the posture the new `reference.Reader` read follows (see D6), not the
   primary read's posture (which sets `v.Error` and returns before any tile
   is built).
5. **`analytics.VehicleMonthlyMetrics.Period`** (`internal/analytics/analytics.go:528`)
   and **`reference.MonthPrice.Period`** (`internal/reference/reference.go:34-38`)
   are both `time.Time`, doc-commented "first day of the month" — but they
   come from two different modules' own DB→domain mapping code, so nothing
   guarantees they carry the identical `time.Time` representation (location,
   wall-vs-monotonic reading). See D2.
6. **`reference.Reader.PricesForMonths`** (`internal/reference/reference.go:19-30`)
   is genuinely sparse: a month with nothing to fall back to is simply absent
   from the result, never a zero-price entry. The eligibility rule below (D3)
   depends on treating "absent from the map" as "no price", never as "price
   zero".
7. **`gateway.Deps`/`handlers.Deps`** already carry `AnalyticsMonthlyReader
   analytics.MonthlyReader`, wired from `cmd/web` via
   `analytics.NewMonthlyReader(pool)` (`gateway.go:82-89`,
   `handlers.go:89-93`, `cmd/web/main.go:81-84`) — the exact shape to mirror
   for `ReferenceReader reference.Reader` / `reference.NewReader(pool)`.

## Database

**None.** This tier reads a port tier 1 already built and archived
(`reference.Reader.PricesForMonths`, over `reference.fuel_prices`). No
migration, no schema change, no index change, no new query on either side.

## D1 — Where the new port is called: the handler, not the view-model builder

`h.referenceReader.PricesForMonths(ctx, start, end)` is called from
`vehicleStatsViewFor` in `vehicle_stats.go`, in the same place and the same
style as the existing `ConsumedByDay` second read — right after the primary
`MonthlyMetricsBetween` months are confirmed non-empty, before the totals are
built.

**Why not inside `sumVehicleStatsMonths` or another pure builder in
`vehicle_stats_tiles.go`.** Every function in that file is a pure formatter
over data the handler already fetched — none of them touches a port, a
context deadline, or an error. Calling a port from inside one would be the
first exception to that shape, and would also make the previous-period trend
call site (`sumVehicleStatsMonths(prevMonths, ...)`, see D5) trigger a second,
unwanted `PricesForMonths` read. Fetching once in the handler and passing the
resolved prices in as plain data keeps the builder file exactly as pure as it
is today.

## D2 — Joining a monthly row to its price: by (year, month) integer, not `time.Time` equality

```go
// monthKey identifies a calendar month independent of day-of-month, time
// zone, or which module's DB mapping produced the time.Time — two different
// modules' "first day of the month" values are not guaranteed to compare
// equal with time.Time.Equal, even when they name the same month.
type monthKey int

func monthKeyOf(t time.Time) monthKey {
	return monthKey(t.Year()*100 + int(t.Month()))
}
```

Built once per render into `map[monthKey]float64` from the `[]reference.MonthPrice`
slice `PricesForMonths` returns. `analytics.VehicleMonthlyMetrics.Period` is
looked up through the same `monthKeyOf`, so the join never depends on the two
modules' `Period` values being byte-identical `time.Time`s — only on both
correctly naming the calendar month, which their own doc comments already
guarantee.

## D3 — Eligibility rule: a month counts only with BOTH a resolved price AND positive cost

Combines roadmap D8 (no default price) and D9 (zero-cost months excluded) into
one gate a month must pass before it contributes to either sum:

```go
// gasolineAccumulator sums an eligible month's distance and its
// gasoline-equivalent gallons, so the period total is SUM(distance) /
// SUM(gallons) — a plain ratio of sums (roadmap D11), never an average of
// monthly ratios.
type gasolineAccumulator struct {
	distanceKm float64
	gallons    float64
}

// add folds one month in, ONLY when it is eligible: cost > 0 (roadmap D9 —
// a month with no charging cost recorded contributes nothing, not a free
// distance) AND a price was resolved for it (roadmap D8 — no default price,
// ever). An ineligible month's distance is also excluded, per D9 ("its
// distance does not count either").
func (g *gasolineAccumulator) add(distanceKm, cost, price float64) {
	if cost <= 0 || price <= 0 {
		return
	}
	g.distanceKm += distanceKm
	g.gallons += cost / price
}

// value returns the period's km-per-gallon, or 0 when no month was
// eligible — the caller's signal to hide the tile.
func (g gasolineAccumulator) value() float64 {
	if g.gallons <= 0 {
		return 0
	}
	return g.distanceKm / g.gallons
}
```

`price <= 0` in `add` is defensive (a stored price is never zero or negative
in practice), not a second lookup path — the caller only invokes `add` with a
price it already resolved from the map; a month with no map entry never calls
`add` at all (see the wiring in `sumVehicleStatsMonths` below).

`vehicleStatsTotals` (the existing raw-totals struct) gains one field:

```go
type vehicleStatsTotals struct {
	// ... existing fields unchanged ...
	gasoline gasolineAccumulator
}
```

`sumVehicleStatsMonths` gains one parameter and one call inside its existing
loop, reusing the `cost` value it already computes for `t.cost`:

```go
func sumVehicleStatsMonths(months []analytics.VehicleMonthlyMetrics, priceByMonth map[monthKey]float64) vehicleStatsTotals {
	t := vehicleStatsTotals{currency: "COP"}
	for _, m := range months {
		t.distanceKm += m.AllDistanceKm
		t.eff.add(m.AllKmPerPctCalc, m.AllDistanceKm)
		t.energyKWh += m.ExtACEnergyKWh + m.ExtDCEnergyKWh + m.SCEnergyKWh
		cost := m.ExtACCost + m.ExtDCCost + m.SCCost
		t.cost += cost
		t.sessions += m.ExtACEntryCount + m.ExtDCEntryCount + m.SCSessionCount
		if price, ok := priceByMonth[monthKeyOf(m.Period)]; ok {
			t.gasoline.add(m.AllDistanceKm, cost, price)
		}
		// ... TeslaRange100PctKmCalc / Currency lines unchanged ...
	}
	return t
}
```

A `nil` `priceByMonth` (the previous-period call site, D5) makes every lookup
miss — Go's `map[k]` read on a `nil` map is a safe zero-value/`false`, so no
extra `if priceByMonth != nil` guard is needed.

## D4 — Ratio shape: plain ratio of sums, no distance-weighting

Unlike `effAccumulator` (which combines each month's already-STORED ratio
and must distance-weight the combination because the table stores no
restricted-day sums to divide directly — see that type's own doc comment),
`gasolineAccumulator` has every input it needs per month: distance, cost, and
price. So `distanceKm` and `gallons` are summed independently across eligible
months and divided exactly once, at the end — precisely roadmap D11's
formula, with no approximation and no weighting step. This mirrors
`costPerKm`'s existing "plain ratio of sums, and correctly so" case in the
same file, not the efficiency case.

## D5 — No trend/delta pair for this tile

`fragments.VehicleStatsTiles.KmPerGallon` is a single `string` field, with no
`KmPerGallonTrend`/`KmPerGallonDelta` pair. Two reasons, both sufficient
alone:

- **Scope.** Neither the ticket nor roadmap D12 asks for one — D12 says the
  change "adds one field to that struct" (singular). Every other trend pair
  on this page was requested explicitly (MAG-87); none was here.
- **Cost.** A trend would need a second `PricesForMonths` read for the
  previous period, on every render, for a comparison nobody asked for.

This is the same "no trend" shape `RangeFull` already carries, though for a
different reason — `RangeFull` has none because it is a battery-health
reading, not a period accumulation; `KmPerGallon` has none because it is out
of scope. Both empty `Trend`/`Desc` values already render correctly with
today's `ui.StatTile` (proven by every existing tile whenever `prev == nil`).
If a trend is wanted later, it is a small, additive follow-up — nothing here
forecloses it.

## D6 — `PricesForMonths` failure degrades only this tile

```go
priceByMonth := map[monthKey]float64{}
if prices, err := h.referenceReader.PricesForMonths(ctx, start, end); err != nil {
	logging.Note("Handler", "vehicleStatsViewFor", "reference price read failed for account %s, vehicle %d: %v", uid, teslaID, err)
} else {
	for _, p := range prices {
		priceByMonth[monthKeyOf(p.Period)] = p.Price
	}
}
totals := sumVehicleStatsMonths(months, priceByMonth)
```

Placed right after the existing `len(months) == 0` early return, before
`totals := sumVehicleStatsMonths(months)` (which becomes the line above). On
error, `priceByMonth` stays empty, so `gasolineAccumulator.add` is never
called (every lookup misses) and `tiles.KmPerGallon` ends up empty — the tile
hides. This mirrors `ConsumedByDay`'s existing posture exactly: log and
continue, degrade one part of the page, never set `v.Error` or abort — that
posture is reserved for `MonthlyMetricsBetween`, the page's primary read,
whose failure means the page has nothing to show at all.

The previous-period call (`sumVehicleStatsMonths(prevMonths, nil)`, D5) needs
no price read and passes `nil`.

## Template — the eighth tile's placement

Added as a **conditionally-rendered fifth entry** in the existing four-tile
"ledger" row in `vehicleStatsTiles` (`vehicle_stats.templ`):

```templ
<div class="grid grid-cols-2 md:grid-cols-4 gap-3 mt-2">
	@ui.StatTile(ui.StatTileProps{Label: i18n.T(ctx, i18n.KeyVehicleStatsEnergy), Value: t.Energy, Trend: t.EnergyTrend, Desc: t.EnergyDelta})
	@ui.StatTile(ui.StatTileProps{Label: i18n.T(ctx, i18n.KeyVehicleStatsSessions), Value: t.Sessions, Trend: t.SessionsTrend, Desc: t.SessionsDelta})
	@ui.StatTile(ui.StatTileProps{Label: i18n.T(ctx, i18n.KeyVehicleStatsCost), Value: t.Cost, Trend: t.CostTrend, Desc: t.CostDelta})
	@ui.StatTile(ui.StatTileProps{Label: i18n.T(ctx, i18n.KeyVehicleStatsCostPerKm), Value: t.CostPerKm, Trend: t.CostPerKmTrend, Desc: t.CostPerKmDelta})
	if t.KmPerGallon != "" {
		@ui.StatTile(ui.StatTileProps{Label: i18n.T(ctx, i18n.KeyVehicleStatsKmPerGallon), Value: t.KmPerGallon})
	}
</div>
```

**Why the ledger row, not the headline pair or the footnote line.** Distance
and Efficiency are the headline tier because they answer the page's question
on their own (`AGENTS.md`'s own tier table); this tile is supporting detail
like Energy/Sessions/Cost/CostPerKm, not a second headline. The footnote line
(`RangeFull`) is reserved for a reading that is not a period total at all —
this tile IS a period total, so it belongs with the other period totals.
`grid-cols-2 md:grid-cols-4` already wraps a fifth item onto its own row with
no class change: two rows of 2 on a phone, one row of 4 plus a lone fifth on
desktop.

**Why conditional rendering, not an em dash.** Every other tile in this row
always renders, showing `emDash` when it cannot be computed — that is this
page's normal "no data" convention. This tile is the one exception, per the
roadmap's own wording ("hidden when there is no figure") and per D8/D9
treating "no eligible month" as "nothing to show", not "a knowable zero".
`fragments.VehicleStatsTiles.KmPerGallon` is therefore `""` (empty string,
never `emDash`) when `gasolineAccumulator.value()` is `0`, and the template
checks `!= ""` rather than always rendering the tile.

## New formatter — `formatKmPerGallon`

```go
// formatKmPerGallon renders the Colombian gasoline cost-parity figure: how
// many kilometres the period's charging cost would have bought at gasoline
// prices, one decimal place — mirrors formatKmPerPct's shape.
func formatKmPerGallon(kmPerGallon float64) string {
	return strconv.FormatFloat(kmPerGallon, 'f', 1, 64) + " km/gal"
}
```

Added to `internal/gateway/handlers/format.go`, next to `formatKmPerPct`.

## New i18n key

One key, `vehicle_stats.km_per_gallon`, added to
`internal/gateway/i18n/catalog.go` next to the existing `KeyVehicleStatsCostPerKm`
/ `KeyVehicleStatsRangeFull` entries, both `ES` and `EN` non-empty:

| Key | ES | EN |
|---|---|---|
| `KeyVehicleStatsKmPerGallon` | `Km por galón (equivalente en gasolina)` | `Km per gallon (gasoline equivalent)` |

The gasoline PRICE itself is never rendered (roadmap D5) — only this label
and the computed figure.

## Worked example — the test contract (unit tests excluded, per roadmap D13)

A four-month period, `[2026-07-01, 2026-10-31]`, worked by hand. This is what
an implementation is checked against, since no `_test.go` exercises it.

| Month | Distance (km) | Charging cost (COP) | Resolved price (COP/gal) | Eligible (D3)? | Gallons (cost ÷ price) |
|---|---|---|---|---|---|
| 2026-07 | 750 | 45,000 | none — before the first stored row (D8) | No | — |
| 2026-08 | 900 | 80,000 | 16,000 (exact row for August) | Yes | 5.0 |
| 2026-09 | 1,000 | 0 | 16,331 (exact row for September) | No — zero cost (D9) | — |
| 2026-10 | 1,100 | 97,986 | 16,331 (fallback: newest row at or before, September's) | Yes | 6.0 |

Only 2026-08 and 2026-10 are eligible. Their distance and gallons are the only
ones summed:

```
SUM(distance) = 900 + 1,100        = 2,000 km
SUM(gallons)  = 5.0 + 6.0          = 11.0
km_per_gallon = 2,000 / 11.0       = 181.818...
formatKmPerGallon(181.818...)      = "181.8 km/gal"
```

Note 2026-07's 750 km and 2026-09's 1,000 km are **excluded from the
numerator too** (D9's "its distance does not count either"), not just their
cost — the period's `KmPerGallon` is not "2,750 km / 11.0 gal".

**All-excluded case (tile hidden):** a period covering only 2026-07 and
2026-09 (no eligible month in range) leaves `gasolineAccumulator.gallons`
at `0`, so `value()` returns `0` and `tiles.KmPerGallon` is `""` — the
template renders no fifth tile at all, and the other seven tiles render
exactly as they do today.

## Docs to update (implementation wave, not this dispatch)

- `internal/gateway/AGENTS.md` — add a `Deps.ReferenceReader reference.Reader`
  bullet under "Public interface", same shape as the `AnalyticsMonthlyReader`
  bullet immediately above it in that file.
- `kkpa/context/input-port/analytics/vehicle-stats.md` — add the new port to
  the front-end component map, add `KmPerGallon` to the ledger-tier table,
  and record the D3 eligibility rule under "Gotchas" (a month can have a
  resolved price and a real distance and still not count, if its charging
  cost is zero).
- `kkpa/context/entities/fuel-price/guide.md` — add `internal/gateway`'s
  `/vehicle-stats` page as this port's first consumer (today the guide only
  describes the module in isolation).
- Root `README.md` — the dependency graph (§"Dependency graph") gains a
  `reference` edge on the `gateway`, `handlers`, and `cmd/web` lines. This
  file is outside `internal/gateway/`; the leader grants it explicitly to
  whichever worker implements this tier (see `tasks.md` T11).
