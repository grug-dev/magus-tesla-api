## MODIFIED Requirements

### Requirement: Manual charge log UI

The gateway SHALL render an authenticated Charge log page at `GET /charges` (and its
`/ui/charges*` htmx fragment routes for the create form, the entries list, the
single-row edit/delete swaps, and the vehicle-changed region refresh). It SHALL let an
authenticated user create, edit, and delete their own manual charge entries through
that page, calling the `manualcharge.Writer` port exclusively — it SHALL NOT read or
write any `manualcharge` or `telemetry` database table directly. The page SHALL be
vehicle-scoped: the entry list and the create form SHALL follow the session-selected
vehicle (the sidebar switcher), via the `vehicle-changed` event, exactly like the
dashboard's per-vehicle reads.

#### Scenario: Delete an owned entry removes the row without an alert
- **GIVEN** a signed-in user with at least one existing manual charge entry for the
  currently-selected vehicle
- **WHEN** the user clicks the entry's Delete button and confirms the `hx-confirm`
  prompt
- **THEN** the server calls `manualcharge.Writer.Delete(accountID, entryID)` scoped
  to the user's account
- **AND** on success the server returns the empty-row swap target so htmx's
  `outerHTML` swap removes the `<tr>` from the entries table in place
- **AND** no browser `alert()` is shown to the user
- **AND** the entry is no longer present in a subsequent list render

#### Scenario: Delete shows a server-side error message, not a generic alert, on failure
- **GIVEN** a signed-in user clicking Delete on one of their entries
- **WHEN** the `manualcharge.Writer.Delete` call returns an error (e.g. a transient
  store failure)
- **THEN** the server returns a non-2xx response carrying an inline row-error
  fragment rendered inside the row
- **AND** the user sees the row-level error message (e.g. "Could not delete entry —
  please try again."), not a raw HTTP status string or a browser `alert()`
- **AND** the entry is not removed from the list

#### Scenario: Delete with a stale or missing CSRF token is rejected, not alerted
- **GIVEN** a signed-in user whose Charge log page holds a stale `csrf_token`
  (older than the session's current `csrf_manualcharge` value, e.g. because the
  create form was refreshed and a fresh token was issued)
- **WHEN** the Delete request is submitted
- **THEN** the server's CSRF check responds with HTTP 403 (invalid csrf token) and
  the row stays in the table
- **AND** the row CSRF lifecycle is corrected so a normally-loaded page's delete
  uses the live session token (the delete button's embedded token matches the
  session's current token at delete time)

#### Scenario: Create form sources the vehicle from the session selection, not a form field
- **GIVEN** a signed-in user with two or more registered vehicles, currently having
  vehicle `V1` selected in the sidebar switcher
- **WHEN** the Charge log page renders
- **THEN** the create form does not render a Vehicle input field
- **AND** the entries list below the form is filtered to the selected vehicle `V1`
- **WHEN** the user submits the create form
- **THEN** the server resolves the target vehicle from the session-selected vehicle
  (the same resolution the entry list already uses), not from a submitted `vehicle`
  form field
- **AND** the server confirms the resolved `(tesla_id, vin)` belongs to the caller's
  account before calling `manualcharge.Writer.Create`
- **AND** the created entry is persisted with the selected vehicle's `tesla_id` and
  `vin`

#### Scenario: Create form has no editable vehicle picker
- **GIVEN** the rendered Charge log create form
- **WHEN** it is inspected
- **THEN** there is no `<select name="vehicle">` and no single-vehicle disabled
  vehicle `<select>`+hidden pair in the form
- **AND** the only way to target a different vehicle with the form is to switch the
  sidebar vehicle selector (which reloads the form via the `vehicle-changed`
  subscription)

#### Scenario: Currency is always COP and not user-editable
- **GIVEN** the rendered Charge log create form
- **WHEN** it is inspected
- **THEN** there is a Currency field rendered as a disabled, read-only input
  pre-filled with `COP`
- **AND** there is no editable Currency `<input>` or `<select>` the user can change
- **WHEN** the create form is submitted
- **THEN** the persisted entry's `currency` field is `COP` regardless of any value
  that could be supplied for it

#### Scenario: Start and end battery percentages are required on the create form
- **GIVEN** a signed-in user on the Charge log page
- **WHEN** they submit the create form with `start_battery_pct` or `end_battery_pct`
  empty, non-integer, or outside the 0–100 range
- **THEN** the server rejects the submission with a field-level validation error
  indicating the required/invalid battery field
- **AND** the `manualcharge.Writer.Create` port is not called
- **WHEN** both battery percentages are supplied as integers in 0–100
- **THEN** the entry is persisted with non-nil `start_battery_pct` and
  `end_battery_pct` matching the submitted values

#### Scenario: Start battery field suggests the latest telemetry battery percentage
- **GIVEN** a signed-in user whose session-selected vehicle has at least one stored
  telemetry snapshot, the latest of which reports `BatteryLevelPct = 73`
- **WHEN** the Charge log create form renders
- **THEN** the `start_battery_pct` input carries a suggestion label reflecting the
  selected vehicle's latest snapshot battery percentage (e.g. a helper label or
  placeholder derived from `73`)
- **AND** the suggestion is a hint, not a forced value — the user may type any
  integer in 0–100 and the field submits whatever the user typed
- **AND** the suggestion is built from the telemetry `Reader.LatestSnapshotsByAccount`
  port (the same port the dashboard uses), picking the snapshot whose `TeslaID`
  matches the session-selected vehicle
- **AND** the gateway does not add a new `telemetry.Reader` method, does not read
  any telemetry table directly, and does not make a live Tesla API call to populate
  the suggestion

#### Scenario: No telemetry snapshot means no battery suggestion (graceful empty)
- **GIVEN** a signed-in user whose session-selected vehicle has no stored telemetry
  snapshot yet
- **WHEN** the Charge log create form renders
- **THEN** the `start_battery_pct` input carries no suggestion label (no fabricated
  value is shown)
- **AND** the field still renders as a normal required integer input (0–100)
- **AND** the page renders without error

#### Scenario: Optional started_at and ended_at fields appear in the main card and default to today
- **GIVEN** a signed-in user on the Charge log page
- **WHEN** the create form renders
- **THEN** the `started_at` and `ended_at` inputs are rendered inside the main "Log
  a charge" card (the same section as Date / Energy / Price), not behind a "More
  details" disclosure
- **AND** both inputs are pre-filled with today's date in `YYYY-MM-DD` form as a
  default value
- **AND** both fields remain OPTIONAL — the form is accepted when either or both
  are cleared, and the `manualcharge.Entry.StartedAt` / `EndedAt` sent to the Writer
  are nil for any cleared field

#### Scenario: Energy added accepts up to three decimal places
- **GIVEN** the rendered Charge log create form
- **WHEN** it is inspected
- **THEN** the `energy_added_kwh` input declares a 3-decimal step (permitting
  values like `7.345`)
- **AND** the browser's nearest-valid-range helper label still fires for any value
  that exceeds the 3-decimal precision (e.g. a 4+-decimal input), nudging the user
  toward the nearest 3-decimal value
- **WHEN** the user submits `energy_added_kwh = 7.345`
- **THEN** the server accepts the value (parses as a positive float) and the entry
  is persisted with `energy_added_kwh = 7.345` (no server-side rounding to 2
  decimals)

#### Scenario: The charges create form follows a vehicle switch
- **GIVEN** a signed-in user with two or more registered vehicles on the Charge log
  page, vehicle `V1` selected, and `V1` has a latest telemetry snapshot at `73%`
- **WHEN** the user switches the sidebar vehicle selector to vehicle `V2`, whose
  latest telemetry snapshot is at `58%` and which has no entries yet
- **THEN** the `#charges-content` region re-fetches `GET /ui/charges` on the
  `vehicle-changed from:body` event (no full page reload)
- **AND** the create form re-renders with `V2` as the implicit vehicle (no vehicle
  field), the `start_battery_pct` suggestion reflecting `58%`, and today's date
  pre-filled in `started_at` / `ended_at`
- **AND** the entries list re-renders filtered to `V2` (empty in this case)

#### Scenario: Manual charge create form is not served to anonymous callers
- **GIVEN** an unauthenticated request to `GET /charges` or to any
  `/ui/charges*` fragment route
- **WHEN** the handler resolves the session
- **THEN** the request is redirected to `/login` and no charge form, no entries
  list, and no telemetry-sourced suggestion is served

#### Scenario: The gateway never imports manualchargedb or telemetrydb for the charges page
- **GIVEN** the gateway handlers and helpers that build and validate the charges
  create form
- **WHEN** they obtain vehicle identity, telemetry for the suggestion, and persist
  or delete an entry
- **THEN** they do so exclusively through the `account`, `telemetry.Reader`, and
  `manualcharge.Reader`/`manualcharge.Writer` public interfaces
- **AND** they import no package from `internal/manualcharge/db` or
  `internal/telemetry/db`
- **AND** no `pgtype` type appears in any gateway file involved
- **AND** no live Tesla Fleet API call is made on any charges-page request