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
  whole calendar year, over `analytics.vehicle_monthly_metrics`. Two halves: seven KPIs that
  state the answer, then three blocks that explain it. **Read-only:** no create, no edit, no
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
| `internal/gateway/handlers/vehicle_stats_tiles.go` | `buildVehicleStatsTiles` and `buildVehicleStatsWhy` — all roll-up maths, over the SAME months slice |
| `internal/gateway/templates/ui/dropdown.templ` | `ui.Dropdown`, the kit component the period control composes |
| `internal/gateway/templates/ui/stat_tile.templ` | `StatTileProps.Size` — the `"lg"` headline variant this page added to the kit |
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
| Ledger | Energy, Sessions, Cost, Cost per km | standard tiles, four across |
| Footnote | Range at full battery | a quiet line under a rule — it is a battery-health reading from the latest month, NOT a period total, and must not sit among things that are |

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

## Related KB

- `workflows/vehicle-monthly-metrics.md` — the table, how it is written, and its three traps
- `entities/vehicle-metrics/guide.md` — the per-day table the monthly rollup is derived from
- `input-port/charging/supercharger-stats.md` — a sibling date-filtered page, different rules
- `architecture/nightly-cycle.md` — step 5 is what writes the rows this page reads
