## ADDED Requirements

### Requirement: Supercharger Stats page

The gateway SHALL render a "Supercharger Stats" page for the currently session-selected
vehicle, replacing the sidebar's "Soon" placeholder with a live route. The page SHALL show
that vehicle's Tesla-billed Supercharger / DC fast-charging session history sourced
exclusively from the `telemetry.SuperchargerReader` port; the gateway SHALL NOT read
telemetry tables directly and SHALL NOT make a live Tesla Fleet API call to render the page.

The page SHALL be served by two authenticated routes: `GET /supercharger-stats` (the full
page, initial load) and `GET /ui/supercharger-stats` (the htmx fragment, swapped by the
month-preset selector) — mirroring the `/charges` + `/ui/charges` pairing. Anonymous requests
to either route SHALL be redirected to `/login` with no Supercharger data served.

Both routes SHALL resolve the target vehicle from the session-selected vehicle (never from the
URL or a request parameter), scoping the read to that vehicle's Tesla id and the caller's
account — the same resolution mechanism used by the dashboard and history-chart fragments.

The read SHALL be a single call to `SuperchargerSessionsByVehicle`, capped at a named limit of
500 rows, ordered newest-first. If the reader returns an error, the page SHALL degrade to its
empty state (empty tiles, empty chart, empty table) rather than returning a 500.

#### Scenario: Stats page renders for the selected vehicle

- **GIVEN** a signed-in user whose selected vehicle has stored Supercharger sessions
- **WHEN** `GET /supercharger-stats` is requested
- **THEN** the response renders the KPI tile row, the kWh-per-month chart, and the sessions
  table for the selected vehicle's sessions
- **AND** no live Tesla Fleet API call is made and no telemetry table is read directly
- **AND** the read is scoped to the selected vehicle's Tesla id and the caller's account, not
  to any other vehicle on the account

#### Scenario: Stats fragment is not served to anonymous callers

- **GIVEN** an unauthenticated request to `GET /supercharger-stats` or
  `GET /ui/supercharger-stats`
- **WHEN** the handler resolves the session
- **THEN** the request is redirected to `/login`
- **AND** no Supercharger session data is served

#### Scenario: No selected vehicle renders the empty state

- **GIVEN** a signed-in user whose account has no registered or selected vehicle
- **WHEN** `GET /supercharger-stats` is requested
- **THEN** the page renders in its empty state (no tiles, no chart, no table rows)
- **AND** the request does not error

#### Scenario: Reader error degrades to the empty state, never a 500

- **GIVEN** a signed-in user with a selected vehicle
- **WHEN** the `SuperchargerReader` call returns an error
- **THEN** the page renders its empty state (empty tiles, empty chart, empty table)
- **AND** the response is not a 500
- **AND** the error is logged server-side

#### Scenario: Zero sessions in the selected window renders the empty state

- **GIVEN** a signed-in user with a selected vehicle that has no Supercharger sessions within
  the selected month window
- **WHEN** the stats page or fragment is rendered
- **THEN** the tiles, chart, and table all show the empty state
- **AND** no fabricated data is shown

### Requirement: Unattributed Supercharger sessions are out of scope

The gateway SHALL NOT show or count, anywhere on the Supercharger Stats page, a
`SuperchargerSession` whose `TeslaID` is `NULL` (the session's VIN does not match any
currently-registered vehicle on the account) — not in the tiles, not in the chart, not in the
table. This is a deliberate, specified limitation: the page's single read is scoped by
`TeslaID` (`SuperchargerSessionsByVehicle`), so such sessions can never match the filter by
construction. The gateway SHALL NOT perform a second, account-wide read to discover or
disclose these sessions.

#### Scenario: A session with no matching registered vehicle is silently excluded

- **GIVEN** a Supercharger session stored for the account whose VIN does not match any of the
  account's currently-registered vehicles (its `TeslaID` is `NULL`)
- **AND** the account also has at least one Supercharger session that DOES match the selected
  vehicle
- **WHEN** the Supercharger Stats page is rendered for the selected vehicle
- **THEN** the unattributed session does not appear in the sessions table
- **AND** its energy and cost are not included in any KPI tile or chart bucket
- **AND** no error, warning, or "N sessions hidden" notice is shown for it
- **AND** only one read (`SuperchargerSessionsByVehicle`) is made — no second, account-wide
  read is performed to check for unattributed sessions

### Requirement: Supercharger Stats month-window selector

The page SHALL offer a month-window selector with a fixed, closed preset set of **{3, 6, 12}**
months, defaulting to **6**. A `months` query parameter value that is missing, non-numeric, or
not one of the allowed presets SHALL fall back to the default of 6. The selected window SHALL
drive the tiles, the chart, and the table identically — all three sections SHALL reflect the
same filtered set of sessions.

Selecting a different preset SHALL re-fetch and re-render the whole Supercharger Stats region
without a full page reload.

#### Scenario: Month window is validated and defaults to 6

- **GIVEN** a signed-in user on the Supercharger Stats page
- **WHEN** the fragment is requested with a `months` value that is missing, non-numeric, or not
  one of the allowed presets (3, 6, or 12)
- **THEN** the page renders using the default window of 6 months
- **AND** when `months` is one of the allowed presets, the page renders using that window
- **AND** the tiles, the chart, and the table all reflect the same window

#### Scenario: Changing the month preset re-fetches the whole region

- **GIVEN** a signed-in user viewing the Supercharger Stats page with the default window
- **WHEN** the user selects a different month preset
- **THEN** the region re-fetches `GET /ui/supercharger-stats?months=N` for the newly-selected
  window
- **AND** the tiles, chart, and table all re-render for that window without a full page reload
- **AND** the selected preset is marked active in the month-selector control

### Requirement: Supercharger Stats KPI tiles

The page SHALL show four KPI tiles computed from the single filtered session slice for the
selected window: **Sessions** (count of sessions in the window), **Energy** (sum of
`EnergyKWh` across sessions where it is non-nil), **Cost** (per-currency totals — see the cost
aggregation requirement below), and **Avg kWh per session** (energy divided by the count of
sessions with a non-nil `EnergyKWh`, guarded against division by zero). All four tiles, the
chart, and the table SHALL derive from the same filtered slice per render, so their counts
SHALL NOT diverge.

#### Scenario: Tiles reflect the filtered session slice

- **GIVEN** a selected vehicle with several Supercharger sessions inside the selected window,
  some with a nil `EnergyKWh`
- **WHEN** the Supercharger Stats page is rendered
- **THEN** the Sessions tile shows the count of sessions in the window
- **AND** the Energy tile shows the sum of `EnergyKWh` over sessions where it is non-nil
- **AND** the Avg kWh per session tile divides that energy sum by the count of sessions with a
  non-nil `EnergyKWh`
- **AND** the number of rows in the sessions table equals the Sessions tile's count

#### Scenario: Avg kWh per session guards against division by zero

- **GIVEN** a selected vehicle whose sessions in the window all have a nil `EnergyKWh`
- **WHEN** the Supercharger Stats page is rendered
- **THEN** the Avg kWh per session tile shows an empty/placeholder value, not a division error
  or a fabricated number

### Requirement: Supercharger Stats cost aggregation never sums across currencies

Session cost SHALL be aggregated into a map keyed by currency, built only from sessions where
both `TotalCost` and `Currency` are non-nil; a session with a nil cost or nil currency SHALL be
excluded from the cost aggregation but SHALL still count toward the Sessions and Energy tiles.
The Cost tile SHALL render one line per currency present in the aggregation and SHALL NOT sum
values across different currencies into a single combined figure.

#### Scenario: Sessions in two currencies render as two cost lines, never summed

- **GIVEN** a selected vehicle with Supercharger sessions in the window billed in two different
  currencies (e.g. some in USD, some in COP), each with a non-nil `TotalCost` and `Currency`
- **WHEN** the Supercharger Stats page is rendered
- **THEN** the Cost tile shows one line per currency with that currency's summed total
- **AND** no line combines amounts from different currencies into a single number

#### Scenario: Sessions with nil cost or currency are excluded from the cost tile only

- **GIVEN** a selected vehicle with some sessions in the window that have a nil `TotalCost` or
  a nil `Currency` (no fee data), alongside other sessions that have both
- **WHEN** the Supercharger Stats page is rendered
- **THEN** the Cost tile's per-currency totals include only the sessions with a non-nil
  `TotalCost` AND a non-nil `Currency`
- **AND** the Sessions tile still counts the nil-cost sessions
- **AND** the Energy tile still includes the nil-cost sessions' `EnergyKWh` when it is non-nil

### Requirement: Supercharger Stats kWh-per-month chart

The page SHALL render a responsive, zero-JavaScript inline SVG bar chart showing kWh charged
per calendar month within the selected window, one bar per month, following the project's
existing hand-rolled-SVG chart pattern (`internal/gateway/AGENTS.md` §"Charts are hand-rolled
SVG"). All bar heights and tooltip strings SHALL be pre-computed by the Go handler; the
template SHALL perform no arithmetic, no unit conversion, and no method calls on domain types.
Bar colors SHALL use DaisyUI semantic tokens, never hardcoded hex.

#### Scenario: Chart buckets sessions by month with pre-computed bar data

- **GIVEN** a selected vehicle with Supercharger sessions spanning multiple calendar months
  within the selected window
- **WHEN** the kWh-per-month chart is rendered
- **THEN** each bar represents one calendar month's summed `EnergyKWh` (nil-`EnergyKWh`
  sessions excluded from the sum)
- **AND** each bar's height and tooltip string arrive on the view model already computed — the
  template performs no arithmetic or domain-method calls
- **AND** bar colors are DaisyUI semantic tokens, not hardcoded hex

### Requirement: Supercharger Stats sessions table is unpaginated

The page SHALL render every session in the selected window in a single `ui.Table` — no
pagination and no "show more" control. The window (month preset) and the read cap already
bound the row count.

#### Scenario: All sessions in the window appear in the table

- **GIVEN** a selected vehicle with a number of Supercharger sessions within the selected
  window that is smaller than the read cap
- **WHEN** the Supercharger Stats page is rendered
- **THEN** every one of those sessions appears as a row in the sessions table
- **AND** no pagination control or "show more" affordance is present

### Requirement: Supercharger Stats charts and tables contain no business logic in templates

The Supercharger Stats Templ components SHALL perform presentation only — conditionals, loops,
and rendering of already-computed strings/numbers. All aggregation (tile sums, currency
grouping, month bucketing, averages) and all formatting (unit suffixes, currency labels, date
formatting) SHALL happen in the Go handler before the view model reaches the template.

#### Scenario: Templates receive fully-computed view model values

- **GIVEN** the Supercharger Stats page and fragment templates
- **WHEN** they render
- **THEN** every tile value, bar height, tooltip string, and table cell has already been
  computed by the Go handler
- **AND** the templates perform no arithmetic, no currency summation, no unit conversion, and
  no method calls on domain types
