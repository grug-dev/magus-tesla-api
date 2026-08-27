## MODIFIED Requirements

### Requirement: Supercharger Stats page

The gateway SHALL render a "Supercharger Stats" page for the currently session-selected
vehicle. The page SHALL show that vehicle's Tesla-billed Supercharger / DC fast-charging
session history sourced exclusively from the `charging.SessionReader` port; the gateway SHALL
NOT read `charge_sessions` or any other table directly and SHALL NOT make a live Tesla Fleet
API call to render the page. **This is a CHANGE from the prior revision of this requirement,
under which the page's source was `telemetry.SuperchargerReader` over `supercharger_sessions`
— the raw ingestion buffer, not the record the platform's `charging` capability owns.**

The page SHALL be served by two authenticated routes: `GET /supercharger-stats` (the full
page, initial load) and `GET /ui/supercharger-stats` (the htmx fragment, swapped by the
window selector) — mirroring the `/charges` + `/ui/charges` pairing. Anonymous requests to
either route SHALL be redirected to `/login` with no Supercharger data served.

Both routes SHALL resolve the target vehicle from the session-selected vehicle (never from the
URL or a request parameter), scoping the read to that vehicle's Tesla id and the caller's
account — the same resolution mechanism used by the dashboard and history-chart fragments.

The read SHALL be a single call to `ListSessionsByVehicleBetween`, bounded by the requested
date window (see the date-range requirement below) rather than a fixed row limit — **a CHANGE
from the prior revision, which capped a single row-limited read at 500 rows and then filtered
to the selected window in memory.** The underlying port returns sessions oldest-first; the
sessions table SHALL continue to display sessions newest-first regardless of the order the
read port returns them in — the gateway SHALL reorder them for display, not rely on or expose
the port's own ordering. If the reader returns an error, the page SHALL degrade to its empty
state (empty tiles, empty chart, empty table) rather than returning a 500.

#### Scenario: Stats page renders for the selected vehicle

- **GIVEN** a signed-in user whose selected vehicle has stored charge sessions
- **WHEN** `GET /supercharger-stats` is requested
- **THEN** the response renders the KPI tile row, the kWh-per-month chart, and the sessions
  table for the selected vehicle's sessions
- **AND** no live Tesla Fleet API call is made and no table is read directly
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
- **WHEN** the `charging.SessionReader` call returns an error
- **THEN** the page renders its empty state (empty tiles, empty chart, empty table)
- **AND** the response is not a 500
- **AND** the error is logged server-side

#### Scenario: Zero sessions in the selected window renders the empty state

- **GIVEN** a signed-in user with a selected vehicle that has no charge sessions within
  the selected date window
- **WHEN** the stats page or fragment is rendered
- **THEN** the tiles, chart, and table all show the empty state
- **AND** no fabricated data is shown

#### Scenario: The sessions table displays newest-first despite the port's oldest-first order

- **GIVEN** a selected vehicle with several charge sessions within the selected window
- **WHEN** the Supercharger Stats page is rendered
- **THEN** the sessions table's first row is the session with the most recent charge date
- **AND** the sessions table's last row is the session with the least recent charge date
  within the window

### Requirement: Unattributed Supercharger sessions are out of scope

The gateway SHALL NOT show or count, anywhere on the Supercharger Stats page, a charge session
whose `TeslaID` is `NULL` (the session's VIN does not match any currently-registered vehicle on
the account) — not in the tiles, not in the chart, not in the table. This is a deliberate,
specified limitation: the page's single read is scoped by `TeslaID` (`ListSessionsByVehicleBetween`),
so such sessions can never match the filter by construction. The gateway SHALL NOT perform a
second, account-wide read to discover or disclose these sessions. **This requirement's
mechanism is unchanged from the prior revision — only the underlying port name changed, from
`telemetry.SuperchargerReader.SuperchargerSessionsByVehicle` to
`charging.SessionReader.ListSessionsByVehicleBetween` — both exclude a `NULL`-`TeslaID` row by
the identical SQL-equality mechanism.**

#### Scenario: A session with no matching registered vehicle is silently excluded

- **GIVEN** a charge session stored for the account whose VIN does not match any of the
  account's currently-registered vehicles (its `TeslaID` is `NULL`)
- **AND** the account also has at least one charge session that DOES match the selected
  vehicle
- **WHEN** the Supercharger Stats page is rendered for the selected vehicle
- **THEN** the unattributed session does not appear in the sessions table
- **AND** its energy and cost are not included in any KPI tile or chart bucket
- **AND** no error, warning, or "N sessions hidden" notice is shown for it
- **AND** only one read (`ListSessionsByVehicleBetween`) is made — no second, account-wide
  read is performed to check for unattributed sessions

### Requirement: Supercharger Stats month-window selector

The page SHALL offer a window selector with a fixed, closed preset set of **{3, 6, 12}**
months, each rendered as a button whose `hx-get` is a server-computed absolute
`?start=YYYY-MM-DD&end=YYYY-MM-DD` href — **never a `?months=N` query parameter, a CHANGE from
the prior revision of this requirement, which accepted `?months=N` directly.** The underlying
HTTP contract SHALL be `?start=YYYY-MM-DD&end=YYYY-MM-DD` (both whole UTC calendar days, `end`
inclusive), following the platform's single closed `?start=&end=` date-filter vocabulary
(`internal/gateway/AGENTS.md` §"HTTP date-filter convention") — this brings the endpoint into
compliance with a convention it previously violated.

When both `start` and `end` are absent, the endpoint SHALL apply the default **6-month**
window: `end` = today, `start` = the 1st of the month 5 months before today's month (a
month-aligned window, preserving the pre-existing chart-bucket behavior of exactly 6 monthly
bars). Each of the 3/6/12-month preset buttons SHALL be computed the same way, so selecting any
preset always renders exactly that many monthly chart bars, never one more or fewer.

A missing partner (`start` without `end` or vice versa), a malformed non-ISO date, an `end`
earlier than `start`, an `end` later than today, or a window wider than **400 days** SHALL be
rejected with HTTP 400; on a
400 the region SHALL render its empty state with NO window selector — mirroring the dashboard
history endpoint's malformed-request degradation. The 400-day cap (wider than the dashboard
history endpoint's 90-day cap) reflects the sparser row density of charge-session data compared
to per-day telemetry snapshots.

The selected window SHALL drive the tiles, the chart, and the table identically — all three
sections SHALL reflect the same filtered set of sessions. Selecting a different preset SHALL
re-fetch and re-render the whole Supercharger Stats region without a full page reload.

#### Scenario: Window defaults to the month-aligned 6-month window when both params are absent

- **GIVEN** a signed-in user on the Supercharger Stats page
- **WHEN** the fragment is requested with no `start` and no `end` parameter
- **THEN** the page renders using a window ending today and starting on the 1st of the month
  5 months before today's month
- **AND** the tiles, the chart, and the table all reflect the same window
- **AND** the kWh-per-month chart renders exactly 6 bars

#### Scenario: An explicit valid start/end window is honored exactly as given

- **GIVEN** a signed-in user on the Supercharger Stats page
- **WHEN** the fragment is requested with a well-formed `?start=&end=` pair where `end` is not
  before `start` and the window is 400 days or narrower
- **THEN** the page renders using exactly that window, un-rounded to any month boundary
- **AND** no HTTP 400 is returned

#### Scenario: A malformed or partial date request is rejected with 400 and no selector

- **GIVEN** a signed-in user on the Supercharger Stats page
- **WHEN** the fragment is requested with either a non-ISO-format `start` or `end` value, or
  with only one of `start`/`end` present
- **THEN** the endpoint responds with HTTP 400
- **AND** the region renders its empty state with no window selector
- **AND** no Supercharger read is performed

#### Scenario: An end date earlier than the start date is rejected with 400

- **GIVEN** a signed-in user on the Supercharger Stats page
- **WHEN** the fragment is requested with `end` earlier than `start`
- **THEN** the endpoint responds with HTTP 400
- **AND** the region renders its empty state with no window selector

#### Scenario: An end date after today is rejected with 400, but an end date of today is accepted

- **GIVEN** a signed-in user on the Supercharger Stats page
- **WHEN** the fragment is requested with an `end` date later than today, within the 400-day cap
- **THEN** the endpoint responds with HTTP 400
- **AND** the region renders its empty state with no window selector
- **AND** no Supercharger read is performed
- **AND** the SAME request with `end` set to today is accepted (HTTP 200), because charge
  sessions are readable on the day they end — unlike the dashboard history endpoint, which
  rejects an `end` of today owing to its nightly capture lag

#### Scenario: A window wider than 400 days is rejected with 400

- **GIVEN** a signed-in user on the Supercharger Stats page
- **WHEN** the fragment is requested with a `start`/`end` pair spanning more than 400 days
- **THEN** the endpoint responds with HTTP 400
- **AND** the region renders its empty state with no window selector
- **AND** the SAME request narrowed to exactly 400 days or fewer is accepted (HTTP 200)

#### Scenario: Changing the window preset re-fetches the whole region

- **GIVEN** a signed-in user viewing the Supercharger Stats page with the default window
- **WHEN** the user selects a different preset (3 or 12 months)
- **THEN** the region re-fetches `GET /ui/supercharger-stats?start=...&end=...` for the
  newly-selected window
- **AND** the tiles, chart, and table all re-render for that window without a full page reload
- **AND** the selected preset is marked active in the window-selector control
