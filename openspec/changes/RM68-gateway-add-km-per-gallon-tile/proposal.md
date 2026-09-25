Source: MAG-96 — https://linear.app/magus-monitor/issue/MAG-96/show-gasoline-equivalent-km-per-gallon-on-the-vehicle-stats-page
Roadmap: openspec/roadmaps/RM68-gasoline-equivalent.md
Tier: 2 of 2 (`gateway`, depends on tier 1 `reference` — the `fuel_prices`
table and `PricesForMonths` port must exist before this tier can read them.
Tier 1 is archived: `openspec/changes/archive/reference/2026-09-25-RM68-reference-add-fuel-prices/`.)

## Why

In Colombia a car's fuel use is judged in kilometres per gallon. This
platform's own efficiency figure is kilometres per 1% of battery, which
means nothing to someone who thinks in gasoline terms. Every number the
comparison needs is already stored on `/vehicle-stats` except one — the
price of gasoline — and tier 1 added that as a new `internal/reference`
module. This tier turns the two into one more figure on the page: what the
period's charging actually cost, expressed as "kilometres per gallon of
gasoline it would have taken to go the same distance."

This is **cost parity, not MPGe.** It answers "what would this month have
cost me in gasoline", not "how efficient is the motor."

## What Changes

- **`fragments.VehicleStatsTiles`** (`internal/gateway/templates/fragments/vehicle_stats_vm.go`)
  gains one new field, `KmPerGallon string`, holding the already-formatted
  figure or the empty string when it cannot be computed.
- **`vehicle_stats_tiles.go`** gains the roll-up math: a small
  `gasolineAccumulator` that sums a period's ELIGIBLE months' distance and
  gasoline-equivalent gallons, and a `monthKey`/`monthKeyOf` helper that
  looks a resolved price up by calendar month regardless of day-of-month or
  time zone. `sumVehicleStatsMonths` takes one more parameter, a
  `map[monthKey]float64` of resolved prices; `buildVehicleStatsTiles` sets
  `tiles.KmPerGallon` from the accumulator, or leaves it empty.
- **`vehicle_stats.go`** reads the new `reference.Reader` port once per
  render — the same shape as its existing second read, `ConsumedByDay` — and
  builds the price lookup map before calling `sumVehicleStatsMonths`.
- **`vehicle_stats.templ`** renders the new tile as a conditional fifth entry
  in the existing four-tile "ledger" row (Energy, Sessions, Cost, Cost per
  km), using the page's own `ui.StatTile` — no new visual pattern.
- **`gateway.Deps` / `handlers.Deps`** gain one new field,
  `ReferenceReader reference.Reader`, wired from `cmd/web` (leader-owned
  task, outside this module).
- **i18n**: one new catalogue key for the tile's label, both `ES` and `EN`.
- **Docs**: `internal/gateway/AGENTS.md` gets the new port documented under
  "Public interface" (mirrors the `AnalyticsMonthlyReader` bullet); the KB
  guides for `/vehicle-stats` and for `fuel_prices` get the new consumer
  and the eligibility rule recorded; the root `README.md` dependency graph
  gets the new `gateway → reference` edge.

## Breaking / modules affected

- **Not breaking.** `fragments.VehicleStatsTiles` gains a field; nothing
  removes or retypes an existing one. `sumVehicleStatsMonths` gains a
  parameter, but its only two callers are both inside this same change.
- **Modules affected:** `gateway` (all the edits above) and `cmd/web`
  (wiring only, one line, leader-owned). `internal/reference` is read-only
  here — its `Reader` port and schema already exist from tier 1.
- **Read path affected:** `GET /vehicle-stats` and `GET /ui/vehicle-stats`
  (`VehicleStatsPage` / `VehicleStatsFragment`). One new read per render,
  `reference.Reader.PricesForMonths(ctx, start, end)`, over the SAME
  `[start, end]` window the page's other two reads already use — no new
  query shape, no unbounded scan. The declared `Performance-Profile` is
  read-heavy with reads mandatory-fast; this stays inside that budget
  because the window is already capped by the page's own 366-day rule.

## No database gate

This tier touches no database object — no migration, no schema, no index.
It reads an existing port (`reference.Reader.PricesForMonths`) built and
archived in tier 1. `CLAUDE.md`'s `Design-Gates` therefore does not apply
to this change.

## Grill-me

The comprehension interview for this roadmap ran with the user at roadmap
level on 2026-09-22 (recorded in `openspec/roadmaps/RM68-gasoline-equivalent.md`
§Decisions, D1–D13). This tier's dispatch treats D1, D6–D10 (and D5, D11,
D12, D13) as given and does not reopen them; the six local decisions this
tier still had to make are recorded as D1–D6 in `design.md`.
