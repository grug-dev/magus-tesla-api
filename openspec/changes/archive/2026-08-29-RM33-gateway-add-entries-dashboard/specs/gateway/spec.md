## MODIFIED Requirements

### Requirement: Charge Log Page

The gateway SHALL serve a standalone "Charge log" page at `/charges` for signed-in users,
displaying the user's manual charge entries (newest first) and a form to create new entries.
The page SHALL be accessible from the dashboard navigation and SHALL require an authenticated
session. The page SHALL be vehicle-scoped: the entry list and the create form SHALL follow the
session-selected vehicle (the sidebar switcher), via the `vehicle-changed` event, exactly like
the dashboard's per-vehicle reads. The create form SHALL NOT render a vehicle picker of its
own.

**CHANGE from the prior revision:** when no vehicle can be resolved for the session (the
account has zero registered vehicles, or the session's selection is stale), the page SHALL
render ONLY the existing "no entries yet" empty-state message — no date-filter selector, no
aggregation tiles, and no table. The prior fallback of listing entries across the whole
account (`ListEntriesByAccount`) when no vehicle is selected is REMOVED: an account with no
registered vehicle cannot create an entry either, so there is nothing meaningful to fall back
to.

#### Scenario: Signed-in user opens the Charge log page

- **GIVEN** a signed-in user with at least one registered Tesla vehicle
- **WHEN** they navigate to `GET /charges`
- **THEN** the gateway renders the full Charge log page
- **AND** the page shows a list of their manual charge entries (if any exist), ordered newest
  first by charge date, scoped to the session-selected vehicle and the active date filter
  window
- **AND** the page shows a "Log a charge" create form with required fields visible
- **AND** the create form has no vehicle picker — it operates on the session-selected vehicle
- **AND** no entry or vehicle belonging to another user is present on the page

#### Scenario: Anonymous visitor is redirected from the Charge log page

- **GIVEN** a visitor with no authenticated session
- **WHEN** they request `GET /charges` or any `/ui/charges*` fragment route
- **THEN** the gateway redirects them to `/login`
- **AND** no charge entries, no create form, and no telemetry-sourced suggestion are shown

#### Scenario: Charge log page renders when the user has no entries yet

- **GIVEN** a signed-in user with a registered vehicle who has never logged a manual charge
  entry
- **WHEN** they open the Charge log page
- **THEN** the page renders successfully (no 500, no empty table with a header)
- **AND** the page shows an empty-state message indicating no entries have been logged yet
- **AND** the date-filter selector and the (all-zero) aggregation tiles are still shown
- **AND** the create form is still visible so the user can add their first entry

#### Scenario: No selected vehicle renders the bare empty state, with no account-wide fallback

- **GIVEN** a signed-in user whose account has zero registered vehicles, or whose session
  vehicle selection cannot be resolved
- **WHEN** they open `GET /charges`
- **THEN** the page renders the SAME "no entries yet" empty-state message used when a
  selected vehicle simply has no entries
- **AND** no date-filter selector is shown
- **AND** no aggregation tiles are shown
- **AND** no table (not even an empty one) is shown
- **AND** the gateway does NOT call `charging.Reader.ListEntriesByAccount` or any other
  account-wide read to populate this page

#### Scenario: Charge log is linked from the dashboard navigation

- **GIVEN** a signed-in user viewing the dashboard
- **WHEN** the navigation is rendered
- **THEN** a "Charge log" navigation link pointing to `/charges` is visible
- **AND** anonymous users do not see this link

#### Scenario: The charges create form follows a vehicle switch

- **GIVEN** a signed-in user with two or more registered vehicles on the Charge log page,
  vehicle `V1` selected, and `V1` has a latest telemetry snapshot at `73%`
- **WHEN** the user switches the sidebar vehicle selector to vehicle `V2`, whose latest
  telemetry snapshot is at `58%` and which has no entries yet
- **THEN** the `#charges-content` region re-fetches `GET /ui/charges` on the
  `vehicle-changed from:body` event (no full page reload)
- **AND** the create form re-renders with `V2` as the implicit vehicle (no vehicle field), the
  `start_battery_pct` suggestion reflecting `58%`, and today's date pre-filled in `started_at`
  / `ended_at`
- **AND** the entries list re-renders filtered to `V2` and the default date-filter window
  (empty in this case)

---

### Requirement: Charge List Fragment

The gateway SHALL serve the charge entries list as an htmx-swappable fragment at
`GET /ui/charges/list`, returning only the fragment HTML and not the surrounding page shell.
The fragment SHALL include per-entry derived values (cost per kWh, battery delta, battery
range, session duration) pre-computed by the handler from the `charging.Entry` value-receiver
methods and the entry's own start/end battery percentages. Every value the handler cannot
compute for a given entry (no cost per kWh, no battery delta, no battery range, no duration)
SHALL render the em-dash `—` placeholder, never a blank cell.

**CHANGE from the prior revision:** `GET /ui/charges/list` now accepts optional
`?start=YYYY-MM-DD&end=YYYY-MM-DD` query parameters (both whole calendar days in the browser's
local timezone, `end` inclusive), following the platform's `?start=&end=` date-filter
convention. When both are absent, the endpoint defaults to the last 7 calendar days
(inclusive, ending today). The vehicle-scoped read SHALL be
`charging.Reader.ListEntriesByVehicleBetween(ctx, accountID, teslaID, start, end)` — no row
limit; the requested window itself bounds the result. This REPLACES the prior revision's
`ListEntriesByVehicle(ctx, accountID, teslaID, defaultChargeLimit)` call on this path. The
fragment additionally carries a date-filter preset selector and four aggregation tiles (see
their own requirements below), summed over the SAME result set the table renders.

#### Scenario: htmx refreshes the charge list within the default window

- **GIVEN** the Charge log page is open in the browser
- **WHEN** htmx issues `GET /ui/charges/list` with no `start`/`end` parameters
- **THEN** the gateway returns only the charges-list fragment HTML, filtered to the last 7
  calendar days (inclusive, ending today in the browser's local timezone)
- **AND** the surrounding page shell is not included in the response
- **AND** the list is ordered newest-first by charge date

#### Scenario: A valid explicit window is honored exactly as given

- **GIVEN** the Charge log page is open in the browser
- **WHEN** htmx issues `GET /ui/charges/list?start=2026-08-01&end=2026-08-31`
- **THEN** the gateway returns the fragment filtered to exactly that window
- **AND** entries whose `charged_on` falls outside `[2026-08-01, 2026-08-31]` are not shown
- **AND** entries whose `charged_on` falls on either boundary date are shown (inclusive)

#### Scenario: A malformed window is rejected with 400 and no filter chrome

- **GIVEN** the Charge log page is open in the browser
- **WHEN** htmx issues `GET /ui/charges/list` with a non-ISO `start` or `end`, only one of the
  two present, an `end` before `start`, or a window wider than the endpoint's cap
- **THEN** the endpoint responds with HTTP 400
- **AND** the response body shows the empty-state placeholder with no preset selector and no
  aggregation tiles
- **AND** no read is performed against `charging.Reader`

#### Scenario: Derived values are rendered on each entry row

- **GIVEN** a signed-in user with at least one charge entry within the requested window
- **WHEN** the charge list fragment is rendered
- **THEN** each entry row displays at minimum: charge date, status, energy added (kWh), price
  with currency, cost per kWh (when computable)
- **AND** a battery-range cell (`"22% → 70%"` shape) is shown when both start and end battery
  levels were recorded, and `—` when either is absent
- **AND** a battery-delta cell is shown alongside the battery-range cell when both start and
  end battery levels were recorded, and `—` when either is absent
- **AND** session duration is shown when both start and end times were recorded, and `—`
  otherwise
- **AND** all computed values are formatted in the handler before reaching the template (the
  template uses only display strings, no arithmetic)

#### Scenario: Reader failure degrades the list gracefully, but keeps the filter chrome

- **GIVEN** the charging Reader returns an error for a valid vehicle and a valid window
- **WHEN** the Charge log page or list fragment is rendered
- **THEN** the gateway renders the page (or fragment) without a 500 or raw error string
- **AND** the date-filter preset selector is still shown
- **AND** the aggregation tiles are still shown, each at their zero/empty value
- **AND** a user-facing error message ("Could not load your entries") is shown in the list
  region
- **AND** the create form remains accessible

---

### Requirement: Delete Charge Entry

The gateway SHALL let a signed-in user delete an existing charge entry from the table. The
delete action SHALL require a CSRF token, enforce tenant ownership, and on success remove the
row from the table without a full page reload and without a browser `alert()`. On failure (a
CSRF mismatch, a cross-tenant id, or a `charging.Writer.Delete` error) the gateway SHALL NOT
surface a plain browser `alert()`.

**CHANGE from the prior revision:** because the entries table now sits alongside aggregation
tiles computed over the same rows (see the aggregation-tiles requirement below), a delete SHALL
refresh the WHOLE entries region (`#charges-list` — presets, tiles, and table together), not
only the deleted row, on both success AND failure, within the SAME `?start=&end=` window the
table was showing before the delete. This REPLACES the prior revision's row-scoped swap (an
empty `<tr>` on success, a row-level error message on failure): a row-only swap would leave the
tiles showing stale, pre-delete totals.

#### Scenario: Deleting an entry removes the row and refreshes the tiles, without an alert

- **GIVEN** a signed-in user viewing the charge list with at least one entry within the active
  window, and the page carries a valid CSRF token in the form/button
- **WHEN** the user confirms deletion (browser native confirm dialog) and the browser issues
  `DELETE /ui/charges/row/{id}` with the CSRF token and the active window's `?start=&end=`
- **THEN** the gateway validates the CSRF token
- **AND** validates that the entry belongs to the user's account
- **AND** calls `charging.Writer.Delete(ctx, accountID, id)`
- **AND** the whole `#charges-list` region is refreshed (via htmx `hx-swap="outerHTML"`) to
  reflect the deletion, within the SAME window the table was showing before the delete
- **AND** the aggregation tiles in the refreshed region no longer include the deleted entry's
  values
- **AND** no browser `alert()` is shown to the user
- **AND** the entry is no longer present in a subsequent list render

#### Scenario: Delete with a stale, missing, or wrong CSRF token is rejected, not alerted

- **GIVEN** a DELETE request to `/ui/charges/row/{id}` with a missing, wrong, or stale
  (older than the session's current `csrf_manualcharge` value) CSRF token
- **WHEN** the gateway processes it
- **THEN** the handler returns HTTP 403 (invalid csrf token)
- **AND** `charging.Writer.Delete` is NOT called
- **AND** the row is not removed from the table
- **AND** the delete button sends its CSRF token on the `X-CSRF-Token` request header
  (not the DELETE request body, which Go's `net/http` does not parse for a DELETE
  method), so a normally-loaded page's delete uses the live session token

#### Scenario: Delete of another tenant's entry is rejected

- **GIVEN** a DELETE request to `/ui/charges/row/{id}` where `id` belongs to a different
  user's account
- **WHEN** the gateway processes it
  (the `charging.Writer.Delete(ctx, accountID, id)` scopes the DELETE to the
  caller's account_id via its WHERE clause)
- **THEN** the delete silently finds no row (the Writer's scoped DELETE affects 0 rows)
- **AND** the gateway returns a 404 or empty response — no data is deleted

#### Scenario: Delete shows a region-level error message, not a generic alert, on failure

- **GIVEN** a signed-in user clicking Delete on one of their entries within the active window
- **WHEN** the `charging.Writer.Delete` call returns an error (e.g. a transient store failure)
- **THEN** the server returns a non-2xx response carrying the SAME `#charges-list` region,
  unchanged data, with an inline error message shown above the table
- **AND** the user sees the region-level error message (e.g. "Could not delete entry — please
  try again."), not a raw HTTP status string or a browser `alert()`
- **AND** the entry is not removed from the list
- **AND** the aggregation tiles are unchanged (the delete did not commit)

## ADDED Requirements

### Requirement: Charge List Date Filter

The Charge log page's entries list SHALL offer a date-filter preset selector with exactly two
presets: "Last 7 days" (the default) and "This month" (the full calendar month containing
today — the 1st through the last day of the month, not merely the days elapsed so far). Both
presets SHALL be computed against the requesting browser's local calendar day, never UTC.
Selecting a preset SHALL re-fetch and re-render only the `#charges-list` region (presets,
tiles, and table together) without a full page reload or a change to the create form's
displayed values.

#### Scenario: The default window is the last 7 calendar days

- **GIVEN** a signed-in user opening the Charge log page with no explicit date filter
- **WHEN** the page renders
- **THEN** the entries list is filtered to the 7 calendar days ending today (inclusive), in
  the browser's local timezone
- **AND** the "Last 7 days" preset button is shown in its active state

#### Scenario: Selecting "This month" shows the full calendar month

- **GIVEN** a signed-in user on the Charge log page on any day of a given month
- **WHEN** they select the "This month" preset
- **THEN** the entries list is filtered to the 1st through the LAST day of that calendar month
  (inclusive of days later than today, when today is not the last day of the month)
- **AND** the "This month" preset button is shown in its active state and "Last 7 days" is not

#### Scenario: Selecting a preset refreshes only the entries region

- **GIVEN** a signed-in user on the Charge log page with the create form partially filled in
- **WHEN** they select a different date-filter preset
- **THEN** only the `#charges-list` region (presets, tiles, table) is re-fetched and
  re-rendered
- **AND** the create form's own fields and any values the user had already typed into it are
  left untouched

---

### Requirement: Charge List Aggregation Tiles

The Charge log page's entries list SHALL show four aggregation tiles — Sessions (count of
entries in the active window), Energy (sum of `energy_added_kwh` across entries where it is
non-nil), Cost (sum of `price` across entries in the window — a single total, since manual
entries are always recorded in COP), and Avg kWh per session (Energy divided by the count of
entries with a non-nil energy value, guarded against division by zero) — computed by the
handler from the SAME result set the table below them renders, so the tiles and the table can
never diverge.

#### Scenario: Tiles reflect the same entries the table shows

- **GIVEN** a signed-in user with several charge entries inside the active window, some with
  a nil energy value
- **WHEN** the Charge log page is rendered
- **THEN** the Sessions tile shows the count of entries in the window
- **AND** the Energy tile shows the sum of energy added over entries where it is non-nil
- **AND** the Avg kWh per session tile divides that energy sum by the count of entries with a
  non-nil energy value
- **AND** the Cost tile shows the sum of every entry's price in the window as a single COP
  total
- **AND** the number of rows in the entries table equals the Sessions tile's count

#### Scenario: An empty window renders zero tiles, not hidden ones

- **GIVEN** a signed-in user whose selected vehicle has no charge entries within the active
  window
- **WHEN** the Charge log page is rendered
- **THEN** the Sessions, Energy, and Cost tiles are still shown, each at `0` (or its
  zero-formatted equivalent)
- **AND** the Avg kWh per session tile shows the em-dash `—` placeholder (division-guarded),
  not a division error or a fabricated number
- **AND** the entries table shows its empty-state message in place of rows

---

### Requirement: Charge List Status Column

Each row in the entries table SHALL show a Status cell carrying two independent signals: a
two-state (green or yellow — never red) completeness indicator, and a text badge naming the
entry's lifecycle status (`IN_PROGRESS` or `DONE`). The completeness indicator SHALL be green
when the entry has every field required for a `DONE` entry (`ended_at`, `end_battery_pct`)
present, AND a non-nil energy value, AND a price greater than zero; yellow in every other case,
including a normally-shaped `IN_PROGRESS` entry that has not yet acquired those values. Both
signals SHALL be computed by the handler; the template SHALL perform no arithmetic and SHALL
NOT call any `charging` module function itself.

#### Scenario: A fully-complete DONE entry shows the green indicator

- **GIVEN** an entry with status `DONE`, a non-nil `ended_at`, a non-nil `end_battery_pct`, a
  non-nil energy value, and a price greater than zero
- **WHEN** the entries table renders that entry's row
- **THEN** the Status cell's completeness indicator renders in its green (complete) state
- **AND** the Status cell's badge reads the DONE label

#### Scenario: A DONE entry missing any one required value shows the yellow indicator

- **GIVEN** an entry with status `DONE` that is missing exactly one of: `ended_at`,
  `end_battery_pct`, a non-nil energy value, or a price greater than zero
- **WHEN** the entries table renders that entry's row
- **THEN** the Status cell's completeness indicator renders in its yellow (incomplete) state,
  never a red or third state
- **AND** the Status cell's badge still reads the DONE label (the badge reflects the stored
  lifecycle status independently of the completeness indicator)

#### Scenario: A normally-shaped IN_PROGRESS entry shows the yellow indicator

- **GIVEN** an entry with status `IN_PROGRESS`, no `ended_at`, and no `end_battery_pct` (the
  ordinary shape for an in-progress charge)
- **WHEN** the entries table renders that entry's row
- **THEN** the Status cell's completeness indicator renders in its yellow (incomplete) state
- **AND** the Status cell's badge reads the IN PROGRESS label

---

### Requirement: Charge List Battery Range Column

Each row in the entries table SHALL show a battery-range cell (e.g. `"22% → 70%"`) ALONGSIDE
the existing battery-delta cell — both are shown, not one in place of the other. The
battery-range cell SHALL render `—` when either the start or the end battery percentage is
absent.

#### Scenario: Both battery percentages present renders the range and the delta together

- **GIVEN** an entry with `start_battery_pct = 22` and `end_battery_pct = 70`
- **WHEN** the entries table renders that entry's row
- **THEN** the battery-range cell reads `"22% → 70%"`
- **AND** the battery-delta cell reads `"+48%"`, in a cell adjacent to the battery-range cell

#### Scenario: A missing end battery percentage renders the em-dash range

- **GIVEN** an entry with `start_battery_pct = 22` and a nil `end_battery_pct` (an
  `IN_PROGRESS` entry not yet completed)
- **WHEN** the entries table renders that entry's row
- **THEN** the battery-range cell reads `—`
- **AND** the battery-delta cell also reads `—`
