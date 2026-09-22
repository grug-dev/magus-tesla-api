# RM68 — Gasoline equivalent (km per gallon)

Source ticket: MAG-96 — https://linear.app/magus-monitor/issue/MAG-96/show-gasoline-equivalent-km-per-gallon-on-the-vehicle-stats-page

In Colombia a car is measured in km per gallon. This platform measures efficiency in
km per 1% of battery. That number says nothing to a person who drives a gasoline car.

Every input is already stored except one: the gasoline price. A new `internal/reference`
module holds it, and `/vehicle-stats` turns the money the car actually cost into
"gallons of money" and prints km per gallon.

This is **cost parity**, not MPGe. It answers "what would this month have cost me in
gasoline terms", not "how efficient is the motor".

## Tier table

Status legend: `[ ]` pending (change not created) · `[~]` in progress (change exists, not archived) · `[x]` done (archived)

| Status | Change | Module | Scope | depends_on | Proposal prompt |
|---|---|---|---|---|---|
| `[ ]` | `RM68-reference-add-fuel-prices` | `internal/reference` | New module. New `reference.fuel_prices` table (`period` date with the month-start CHECK, `currency` text, `price numeric(14,2)`, UNIQUE on `period`, plus the module's normal `id`/`created_at`/`updated_at`). One migration creating the table, one seeding `2026-08 = 16331 COP`. Read port `PricesForMonths(ctx, start, end)` resolving the "newest row at or before" fallback in SQL, one query for the whole window. sqlc wiring. Add `reference` to `MIGRATION_MODULES` in the Makefile. Module `AGENTS.md` + `README.md`. Root `README.md` structure tree and architecture table. | — | Create the OpenSpec artifacts for `RM68-reference-add-fuel-prices`. Binding decisions D1–D6 and D10 in this roadmap. design.md MUST carry the full schema, the rationale (including why `region` and `product` columns were rejected and why the result is not stored in `analytics.vehicle_monthly_metrics`), and the index plan against the read pattern — the `database` design gate applies. Mirror `analytics.vehicle_monthly_metrics` for the `period` month-start CHECK (`EXTRACT(day FROM period) = 1`) and for the money type (`numeric(14,2)` paired with a `currency` column, no unit suffix). This is the project's FIRST new module in a while: verify `MIGRATIONS_DIRS` order, `make boundary-guard`, `make naming-guard`, `sqlc`, and `db-setup`/`db-reset` role-and-ownership assumptions all still hold, and record what you checked — not only what you changed. Unit tests are excluded. |
| `[ ]` | `RM68-gateway-add-km-per-gallon-tile` | `internal/gateway` | Eighth tile inside the existing summary card on `/vehicle-stats`. New field on `VehicleStatsTiles`, rendered beside the other seven, hidden when there is no figure. Ratio-of-sums computed per request in the view model from `analytics.MonthlyReader` output plus the new `reference` port. New port field on the gateway `Deps`, wired from `cmd/web`. ES + EN catalogue entries. KB under `kkpa/context/` updated. | tier 1 | Create the OpenSpec artifacts for `RM68-gateway-add-km-per-gallon-tile`. Binding decisions D1, D6–D10 in this roadmap. Mirror the seven existing tiles in `internal/gateway/templates/fragments/vehicle_stats_vm.go` and `vehicle_stats.templ` — the page already speaks `ui.StatTile`, do NOT invent an eighth pattern. Read `internal/reference` only through its public Go interface; the gateway never touches its database. The gasoline price is never rendered. Nothing is stored. Unit tests are excluded. |

## Decisions (binding on every tier)

Settled with the user on 2026-09-22, before any artifact was written. Workers treat
these as given and never re-open them. D1–D6 come from the ticket itself; D7–D10 were
settled in the comprehension interview.

- **D1 — Owning modules.** The price lives in a NEW `internal/reference` module. Not
  `analytics`, not `charging`. It holds external reference values that belong to no
  vehicle and no user. The tile lives in `internal/gateway`.

- **D2 — Table shape.** One table, one row per month:

  ```sql
  reference.fuel_prices (
    period    date            NOT NULL,   -- first day of the month
    currency  text            NOT NULL,   -- 'COP'
    price     numeric(14,2)   NOT NULL,   -- price of one gallon
    UNIQUE (period)
  )
  ```

  `period` carries the same month-start CHECK `analytics.vehicle_monthly_metrics`
  uses (`EXTRACT(day FROM period) = 1`). Money has no unit suffix and is paired with
  a `currency` column — the project rule. The type mirrors the three `*_cost` columns
  already in `vehicle_monthly_metrics`, which are `numeric(14,2)`.

- **D3 — No `region` column and no `product` column.** One price per month, regular
  gasoline only. A DEFAULT adds either one later if it is ever needed. Adding them now
  costs a wider UNIQUE key and a discriminator every query must carry, for a
  distinction the platform cannot yet make.

- **D4 — Prices are loaded by one migration per month.** No command, no UI form, no
  poller. A price changes a few times a year and is copied from a government figure by
  hand; a whole input surface for twelve rows a year is not worth building.

- **D5 — The gasoline price is never shown in the UI.** Only the resulting km per
  gallon appears.

- **D6 — The result is NOT stored in `analytics.vehicle_monthly_metrics`.** The gateway
  computes it per request. Storing it would couple `analytics` to `reference`, and
  correcting one price would force a re-sync of every month that fell back to it.

- **D7 — The read port takes a window, not a month.** `PricesForMonths(ctx, start, end)`
  returns one resolved price per month in the range, with the "newest row at or before"
  fallback applied **in SQL**. The ticket described a per-month signature mirroring
  `charging.packCapacityKWh`, but the page asks for up to 12 months, so that shape means
  12 round trips on every render and every period change. The declared
  `Performance-Profile` is read-heavy with reads mandatory-fast. Resolving the whole
  window in one query keeps the fallback rule inside the owning module, which the
  alternative — returning the whole table and picking in Go — would not.

- **D8 — No default price.** The ticket's reference to `packCapacityKWh` is loose: that
  function falls back to a hardcoded `62.0` when no row exists. A gasoline price has no
  sensible default. Only the "newest row at or before" lookup is reused. A month with no
  price is left out of the sums, and the tile is hidden when no month in the window has
  one — exactly what the ticket's own Catches section says.

- **D9 — A month with zero charging cost is excluded from BOTH sums.** Its distance does
  not count either. `ext_ac_cost`, `ext_dc_cost` and `sc_cost` all default to 0, so a
  month synced with no charge data would otherwise add real kilometres against zero
  gallons and report a figure that is too good. When nothing is left, the divisor is 0
  and the tile hides.

- **D10 — No currency check.** `vehicle_monthly_metrics.currency` and
  `fuel_prices.currency` are both `'COP'` today, and the month column is
  `NOT NULL DEFAULT 'COP'`. The code divides without comparing them. The user accepted
  the risk: if a second currency is ever stored, the tile reports a wrong number
  quietly, and no guard or test would catch it.

- **D11 — Ratio of sums, never an average of monthly figures.**

  ```
  km_per_gallon = SUM(month distance) / SUM(month cost / month price)
  ```

  Each month converts its own cost at its own price, then the totals divide. This is the
  same rule the monthly table already follows. Averaging the monthly figures would weigh
  a 300 km month the same as a 1200 km month.

- **D12 — The tile is the eighth entry in the existing summary card.** Not its own card,
  and it replaces nothing. `VehicleStatsTiles` already holds seven formatted KPI values
  rendered by `vehicleStatsTiles`; this adds one field to that struct. Reusing the
  `ui.StatTile` vocabulary gives the worker seven examples to mirror instead of a new
  pattern to invent.

- **D13 — Unit tests are excluded.** Stated in the ticket.

## Accepted consequences

- **A later price row changes older figures.** Months with no row use the previous
  price. Adding an October row shifts every month between the last row and October.
  The user accepted this. The price moved about 1% in nine months — 16,491 COP in
  January 2026, 16,331 COP in September 2026 — so the drift is small.

- **Only 2026-08 is seeded.** The government froze the price on 29 August 2026, so
  September falls back to that same row. Those are the only two months with data. For
  reference if older months are ever added: Bogota was 16,491 COP in January 2026.

## Out of scope

- No price shown in the UI, no `region` or `product` column, no command or form to load
  prices, no storage of the result in `vehicle_monthly_metrics`, no unit tests, no tile
  on any page other than `/vehicle-stats`.

## Future work

None recorded. Nothing was descoped into `openspec/roadmaps/backlog.md` for this
roadmap.
