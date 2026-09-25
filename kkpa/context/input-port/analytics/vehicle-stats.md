# Vehicle Stats page — /vehicle-stats

> **Adapter side:** what the outside world calls, and where it forwards to. **No backend flow
> here** — that lives in the linked workflow files.
>
> "Input port" in this KB means the *driving adapter* — the page, route, or endpoint that
> triggers a use case. (Strict hexagonal reserves "port" for the interface the core exposes;
> this KB deliberately uses the looser sense. Do not go looking for a `Port` interface in the
> code because of this filename.)
>
> All KB links below are relative to `kkpa/context/`.

## Page

- **Route / URI:** `/vehicle-stats`
- **Description:** How the selected vehicle performed over one whole calendar month or one
  whole calendar year, over `analytics.vehicle_monthly_metrics`. Seven KPIs with
  period-over-period arrows, a month-by-month chart, a warning for days whose charge records are
  missing, then three blocks explaining the figures. **Read-only:** no create, no edit, no
  delete, no CSRF token.
- **Module:** `analytics`
- **Nav label:** `Estadísticas` / `Vehicle Stats`, in the *Insights* section
  (`templates/layouts/nav.go`). It was a `Placeholder: true` "Soon" entry until MAG-87.

## Front-end component map

| File | Role |
|---|---|
| `internal/gateway/templates/pages/vehicle_stats.templ` | Page shell — header + the `#vehicle-stats-content` region |
| `internal/gateway/templates/fragments/vehicle_stats.templ` | The swappable region: period control, the three KPI tiers, the "why" blocks, empty state |
| `internal/gateway/templates/fragments/vehicle_stats_vm.go` | `VehicleStatsView` / `VehicleStatsPeriod` / `VehicleStatsTiles` / `VehicleStatsWhy` + its three row types |
| `internal/gateway/handlers/vehicle_stats.go` | Both handlers and the shared `vehicleStatsViewFor` |
| `internal/gateway/handlers/vehicle_stats_period.go` | `parseVehicleStatsRange` + `buildVehicleStatsPeriods` — the window rules |
| `internal/gateway/handlers/vehicle_stats_tiles.go` | `sumVehicleStatsMonths` (raw totals), `buildVehicleStatsTiles` (formats + trends), `buildVehicleStatsWhy` — all roll-up maths, over the SAME months slice. Also `monthKey`/`monthKeyOf` and `gasolineAccumulator` — the gasoline cost-parity tile's month-keyed price join and roll-up |
| `internal/reference` | `Reader.PricesForMonths` — the per-month gasoline price the gasoline cost-parity tile joins against; see `entities/fuel-price/guide.md` |
| `internal/gateway/handlers/vehicle_stats_chart.go` | `buildVehicleStatsMonthChart` (the month-by-month bars) and `vehicleStatsPrevWindow` (the period the trends compare against) |
| `internal/gateway/handlers/vehicle_stats_gaps.go` | `buildVehicleStatsGaps` + `gapCopyFor` — the missing-charge-records warning and its per-kind routing |
| `internal/gateway/templates/ui/dropdown.templ` | `ui.Dropdown`, the kit component the period control composes |
| `internal/gateway/templates/ui/stat_tile.templ` | `StatTileProps.Size` — the `"lg"` headline variant this page added to the kit — and the `"up-error"` / `"down-success"` Trend values it added for cost |
| `internal/gateway/i18n/catalog.go` | Every string on this page, ES + EN, including the 12 `month.NN` names |

## Endpoints

| Method + path | Purpose | Use case |
|---|---|---|
| `GET /vehicle-stats` | Full page load (`VehicleStatsPage`) | — read path, see `workflows/vehicle-monthly-metrics.md` |
| `GET /ui/vehicle-stats` | Re-render the region — period change and vehicle switch (`VehicleStatsFragment`) | — read path |

**There is deliberately no write route of any kind.** The rows are derived nightly by
`analytics.MonthlySyncer`; nothing on this page can change them.

## Adapter-side conventions

- **The period control is a `ui.Dropdown`, not a native select — and that is load-bearing.**
  Each menu row carries its OWN complete `hx-get="/ui/vehicle-stats?start=…&end=…"`. One option
  of a native select holds a **single** value, so a control that must set two coupled query
  params from one click would need JavaScript. The dropdown is the CSS-only DaisyUI `:focus`
  pattern `ui.LangSwitcher` already uses — no new client behaviour. **This is what let the page
  keep the platform's one date-filter vocabulary (`?start=&end=`) with no exception.**
- **Both bounds must be month-aligned** — `start` a 1st, `end` a month's last day — or it is a
  400. The page reads a per-month rollup, so a window starting mid-month cannot be answered by
  the underlying rows; it would silently return whole months and misreport what was asked. This
  rule is unique to this endpoint among the four date-filtered pages.
- **"Today" is `browserToday(c)` here**, the `browser_tz` cookie — unlike
  `/supercharger-stats`, which uses plain UTC.
- **The "future" frame is the CURRENT MONTH'S END, not today**, so the current month stays
  selectable for its whole duration. That is a deliberate requirement, not an oversight.
- **A second lower bound applies:** `start` before the account's `analysis_start_date` month is
  a 400. Read through `account.Service.AnalysisStartDateFor`; a lookup failure falls back to
  today's month, offering the current month alone — too narrow is recoverable, too wide would
  offer periods the account may not read.
- **Every rule is enforced twice**: the builder never offers an out-of-range period, and the
  parser rejects one anyway. Only a hand-typed URL reaches the second check.
- **Cap: 366 days** (`vehicleStatsRangeMaxDays`), one leap year — a whole year is the widest
  window the control can express. Deliberately not copied from the 400 the two charging pages
  use.
- **A malformed window renders 400 with no chrome** — no period control at all, matching every
  other date-filtered page in the module.
- **i18n:** every string resolves through `i18n.T(ctx, key)` with both ES and EN non-empty.
  Spanish month names are lowercase by orthography; that is correct, not a typo.

## Page structure — the two halves

The ticket's own shape: **state the answer, then explain it.** Do not flatten these back into
one grid of equal cards; that is the thing the ticket explicitly asked against.

**The answer** — seven KPIs in THREE tiers of weight, because they do not answer equally:

| Tier | Tiles | Treatment |
|---|---|---|
| Headline | Distance, Efficiency | `ui.StatTile` with `Size: "lg"` |
| Ledger | Energy, Sessions, Cost, Cost per km, Km per gallon (gasoline) | standard tiles, four across, plus a fifth shown only when at least one month is eligible (see Gotchas) |
| Footnote | Range at full battery | a quiet line under a rule — it is a battery-health reading from the latest month, NOT a period total, and must not sit among things that are |

**Between the two halves** sit two blocks that are neither a figure nor an explanation:

| Block | When it renders | Why there |
|---|---|---|
| `Distancia por mes` — one bar per calendar month | only when the period spans **more than one month** | It is the SHAPE of the headline distance. A year otherwise collapses twelve months into one number and loses everything about how they differed. One bar is not a chart, so a single-month period skips it. |
| `Registros de carga faltantes` | only when the period has flagged days | It is a **caveat on the figures**, not an explanation of them — a missing charge under-counts consumption, pushing energy and cost down and efficiency up. A reader who has seen the numbers must see this before trusting them. |

**The why** — three blocks, each answering one headline number, in that order:

| Block | Answers | Shape |
|---|---|---|
| How you drove (weekday vs weekend) | Efficiency | a comparison, with each side's day count so the two are read fairly |
| Where the energy came from | Cost | three meters + **price per kWh per source**, which is the figure that actually explains a costly period |
| How full you leave it | — | the five ending-battery bands as meters |

**None of the three is a stat tile, on purpose.** A tile states a value; these state a
comparison, a share and a distribution — the shapes that explain a value. Reusing tiles here
would flatten the hierarchy the section above exists to build.

**The period control lives inside the swappable region, not in the page header.** That is
forced, not preferred: the header is outside `#vehicle-stats-content`, so an htmx swap would
leave its button naming the previous period. It is right-aligned directly under the header to
read as though it belonged there.

## How the charging figures are composed

Every charging KPI is the sum of **three** stored sources, in this fixed order:

| Source | Column trio | Comes from |
|---|---|---|
| External AC | `ext_ac_energy_kwh` / `ext_ac_cost` / `ext_ac_entry_count` | `charging.manual_charge_entries` with `charging_type = 'AC'` |
| External DC | `ext_dc_*` | same table, `charging_type = 'DC'` |
| Supercharger | `sc_energy_kwh` / `sc_cost` / `sc_session_count` | `charging.supercharger_sessions` |

So the `Charging sessions` KPI is `ext_ac_entry_count + ext_dc_entry_count + sc_session_count`,
and the three rows in the "Where the energy came from" block are that same split — they must
always add up to the tile above them. Worked example from this repo's own data, 2026-08,
vehicle `3744327027802250`: **7 AC + 0 DC + 3 Supercharger = 10**.

Two rules that make a count smaller or larger than a person expects — both decided in
`analytics`, not here, so the page cannot change them:

- **A manual entry with no `charging_type` is skipped entirely, not even counted.** It lands in
  no bucket and appears nowhere. A forgotten type silently removes a charge from every figure.
- **`IN_PROGRESS` entries ARE counted.** There is no status filter. In that same August, 2 of
  the 7 AC entries are `IN_PROGRESS`, so "10" includes two unfinished charges and their partial
  energy and cost. The count does **not** mean "completed charges".

## The missing-charge-records warning

Lists the days in the period whose battery use the stored charge records cannot account for, each
linking to the page that fixes that specific day with the day already applied as the filter.

**It reads `analytics.Reader.ConsumedByDay`, NOT `analytics.charge_gaps`. Do not "improve" this
by switching to the gaps table.**

`charge_gaps` is the module's WORKLIST, not live truth. A row is deleted only by
`GapWriter.ReconcileWindow`, and only inside its 30-day window; the manual-charge write path
calls `Recalculate` (which clears `vehicle_metrics.flagged`) but never `ReconcileWindow`. So a
gap fixed more than 30 days ago keeps its `charge_gaps` row forever. Measured on this repo's data
for vehicle `3744327027802250`: `charge_gaps` held six days, of which **2026-07-19 and 2026-08-03
were already resolved** — 08-03 has two AC entries and a normal 4.0% / 11.2 km day, and its gap
row was last touched 2026-09-02 before falling out of the window. Warning about those two would
send the reader to redo work they had already done.

`ConsumedByDay` carries the live `Flagged` and `MissingChargingType` per day, and the gateway
already holds that port — so this costs no new dependency. It is a **second read**, day-grain,
because the monthly rollup stores no per-day flag; a failure degrades the warning only and the
figures still render.

**The kind decides the destination**, and the two need different words because they need
different work:

| `MissingChargingType` | Means | Badge | Action | Links to |
|---|---|---|---|---|
| `MANUAL` | Charged where the Fleet API does not report it — home, work, third-party AC | `Carga manual` | `Agregar registro` | `/external-charges?start=D&end=D` |
| `SUPERCHARGER` | The session IS stored; only its two battery percentages are NULL | `Supercharger` | `Completar batería` | `/supercharger-stats?start=D&end=D` |

A flagged day whose type is neither value is **skipped**: the row's whole value is the link, and
without the type there is no page to send the reader to.

Capped at `vehicleStatsGapsMax` (10) rows, with the remainder counted rather than dropped.

**Layout:** a grid of day cells, not a list of rows. The first version was a list carrying a
date, a full sentence and a button per line — three competing elements that wrapped badly, and
the sentence was identical in every row. What varies is the date and the kind, so the date is the
cell's anchor, the kind is a badge, the explanation is said once in the card description, and the
whole cell is the link.

## Period-over-period trends

Each of the six comparable tiles carries an arrow and a signed percentage against the
**immediately preceding period of the same length in whole months** — September compares with
August, a three-month window with the three months before it. `vehicleStatsPrevWindow`
(`vehicle_stats_chart.go`) resolves it; it is a **second call** to `MonthlyMetricsBetween`.

**Polarity is per metric, not one rule for all:**

| Tiles | Polarity | Rising renders as |
|---|---|---|
| Distance, Energy, Sessions | neutral | grey ↑ (`up-neutral`) — driving more is neither good nor bad, and a reader who drove more on purpose must not be shown a red arrow |
| Efficiency | higher is better | green ↑ (`up`) |
| Cost, Cost per km | lower is better | **red ↑ (`up-error`)** |
| Range at full battery | **no trend at all** | — |

`up-error` and `down-success` did not exist in `ui.StatTileProps.Trend` before this page; it had
only up-is-good and down-is-bad. Without them a rising cost could only be drawn as a green
up-arrow (wrong meaning) or a grey one (no meaning). They complete the direction × meaning grid
the prop's own doc comment already claimed to separate.

**Range at full battery deliberately carries no arrow.** One month's reading against another's is
mostly weather and driving style, and an arrow there would invite a reader to see battery
degradation in noise.

**No trend is shown at all** when the previous period would start before the account's
`analysis_start_date`, when it has no stored month, when either value is zero, or when the change
rounds to 0%. The summary card then says so in words — silence would look identical to a period
that simply did not change.

## The month chart's axis is calendar-driven, not data-driven

Every month in the window gets one slot, in order, whether or not a row exists. A month with no
stored row renders at zero height with its label kept and `Present=false`.

**That matters more here than on the other charts.** This table is sparse by month, so a
data-driven axis would quietly relabel the gaps away and draw a tidy chart of a year that was
never recorded. **The empty slots ARE the information.** Worked example from this repo:
`analysis_start_date` is 2026-07-09 and only 2026-08 and 2026-09 are stored, so the whole-year
period renders three bars — `2026-07` empty, then the two real months.

It reuses `historyBarChart` (`fragments/history.templ`), like the Supercharger month chart does.
Do not write a second chart renderer.

**The bars are vertical on purpose — do not switch them to horizontal to match the two blocks
below.** Those two are categorical distributions (three charge sources, five battery bands),
where order is arbitrary or ordinal and you read them by comparing lengths. This is a TIME
series: months advance, time reads left-to-right, and the point is the shape of a rise or fall
rather than a ranking. Vertical is also already the module's language for time — `/dashboard`'s
history charts and the Supercharger month chart are both vertical.

**Two or three months look narrow, and that is the fix, not the bug.** `historyBarChart` caps its
width at 4rem per bar (`historyBarSlotRem`), so bars are ~51px wide everywhere in the app and
only the chart's total width reflects how much data exists. Before that cap, three bars stretched
to ~240px wide against a 96px height. Do not widen it back.

## Gotchas

- **The table is sparse BY MONTH, and no backfill is coming.** The nightly poller writes only
  the current and previous month, and nothing filled the months before the table shipped
  (2026-09-18). A whole-year period legitimately returns **fewer than twelve rows**. The owner
  decided against a backfill (MAG-87) — prod has no older data to recover — so this is the
  permanent shape. Roll up whatever comes back; never index by month number or assume a count.
- **NEVER compute efficiency as `all_distance_km ÷ all_consumed_pct`.** This is the bug this
  page shipped with and the reason to read this bullet. The two sum columns cover **every
  computable day**; the stored `all_km_per_pct_calc` covers **only days whose `consumed_pct` is
  positive**. A day that charged more than it drove has a negative `consumed_pct`, so dividing
  the sums puts its kilometres in the numerator *and* shrinks the denominator — both errors push
  the ratio up. On real data it returned **5.8 km/% against a stored 2.94**, and for a vehicle
  whose month is net-charging `all_consumed_pct` is negative outright, which the naive formula
  cannot render at all. The stored ratio is the only correct per-month figure; combine those,
  never the sums. Same trap on `weekday_*` and `weekend_*`.
- **Combining months is distance-weighted, and approximate on purpose.** `effAccumulator`
  weights each month's stored ratio by its distance. For a **single month** that is exact — the
  weight cancels — which is why there is no special case. For a year it is an approximation: the
  exact combined ratio needs each month's restricted sums (distance and consumed % over positive
  days only) and the table stores **neither**, so they cannot be recovered. Making a year exact
  means adding those two columns; that schema change was considered and declined (MAG-87).
- **Cost per km IS a plain ratio of sums, and correctly so.** `all_distance_km` over every
  computable day is exactly the distance the period's money bought — its denominator has no
  positive-only restriction to respect. Do not "fix" it to match the efficiency rule above.
- **`RangeFull` is the exception: it is the latest month, not a roll-up.**
  `tesla_range_100_pct_km_calc` is a battery-health reading, not a quantity that accumulates.
  Summing it is meaningless and averaging it would be a mean of ratios whose weights this table
  does not expose. A stored `0` means "no day that month had a reading", so zeros are skipped.
- **A value that cannot be computed is an em dash, never `0`** — a zero would claim a
  measurement nobody took. This matters here because the table is `NOT NULL DEFAULT 0`.
- **"Avg. End Battery" is not a KPI here and cannot be.** The stored
  `*_ending_battery_dist` columns are five bucket **counts**, not an average; no true average
  was ever stored. They render as the distribution block in the "why" half instead.
- **The bucket share divides by charges that REPORTED an ending battery**, not by every charge
  in the period. A charge with no reading is in no bucket, so counting it in the divisor would
  shrink all five bars against a total they do not belong to.
- **The three sources always render, even at zero energy.** Hiding an empty row would hide the
  very fact that explains a cheap period — that nothing went through the Supercharger.
- **A month can have a resolved gasoline price and real distance and still not count.** The
  Km-per-gallon tile only counts a month when BOTH a price resolves for it AND its charging cost
  (AC + DC + Supercharger) is greater than zero. A month with a price but zero charging cost is
  excluded — its distance too, not only its cost. When no month in the period is eligible, the
  tile does not render at all: no em dash, no placeholder.
- **The gasoline price is never shown.** Only the km-per-gallon figure appears on the page, in
  either language. Do not add the price as a tile description or tooltip.
  _Source: spec gateway — Requirement: Vehicle Stats Page Shows A Gasoline Cost-Parity Tile._
- **The Km-per-gallon tile has no trend.** It carries no arrow and no period-over-period
  comparison, and the previous-period roll-up reads no prices. Adding a trend means a second
  `PricesForMonths` read per render.
  _Source: spec gateway — Requirement: Vehicle Stats Page Shows A Gasoline Cost-Parity Tile._
- **A failed price read hides only this tile.** The handler logs the error and continues. The
  other seven figures still render from their own reads; the page does not show its error state.
  _Source: spec gateway — Requirement: Vehicle Stats Page Shows A Gasoline Cost-Parity Tile._

## Related KB

- `workflows/vehicle-monthly-metrics.md` — the table, how it is written, and its three traps
- `entities/vehicle-metrics/guide.md` — the per-day table the monthly rollup is derived from
- `input-port/charging/supercharger-stats.md` — a sibling date-filtered page, different rules
- `architecture/nightly-cycle.md` — step 5 is what writes the rows this page reads
