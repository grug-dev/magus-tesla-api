## RENAMED Requirements

- FROM: `### Requirement: Gateway Imports No manualchargedb Package`
- TO: `### Requirement: Gateway Imports No chargingdb Package`

## MODIFIED Requirements

### Requirement: Charge List Fragment

The gateway SHALL serve the charge entries list as an htmx-swappable fragment at
`GET /ui/charges/list`, returning only the fragment HTML and not the surrounding page shell.
The fragment SHALL include per-entry derived values (cost per kWh, battery delta, session
duration) pre-computed by the handler from the `charging.Entry` value-receiver methods.

#### Scenario: htmx refreshes the charge list

- **GIVEN** the Charge log page is open in the browser
- **WHEN** htmx issues `GET /ui/charges/list`
- **THEN** the gateway returns only the charges-list fragment HTML
- **AND** the surrounding page shell is not included in the response
- **AND** the list is ordered newest-first by charge date

#### Scenario: Derived values are rendered on each entry row

- **GIVEN** a signed-in user with at least one charge entry
- **WHEN** the charge list fragment is rendered
- **THEN** each entry row displays at minimum: charge date, energy added (kWh), price with
  currency, cost per kWh (when computable), vehicle label
- **AND** battery delta (percentage change) is shown when both start and end battery levels
  were recorded
- **AND** session duration is shown when both start and end times were recorded
- **AND** all computed values are formatted in the handler before reaching the template (the
  template uses only display strings, no arithmetic)

#### Scenario: Reader failure degrades the list gracefully

- **GIVEN** the charging Reader returns an error
- **WHEN** the Charge log page or list fragment is rendered
- **THEN** the gateway renders the page (or fragment) without a 500 or raw error string
- **AND** a user-facing error message ("Could not load your entries") is shown in the list
  region
- **AND** the create form remains accessible

### Requirement: Create Charge Entry

The gateway SHALL let a signed-in user create a manual charge entry by submitting the create
form via `POST /ui/charges/create`. The form SHALL NOT include a vehicle field — the handler
SHALL derive the target vehicle from the session-selected vehicle (the sidebar switcher) and
enforce tenant ownership of that resolved vehicle before writing. The handler SHALL require a
valid CSRF token and call `charging.Writer.Create` on success. `location_kind` is a
**required** field; the handler SHALL reject a missing or unrecognized value with a 422 and a
field-level error message. `start_battery_pct` and `end_battery_pct` are also **required**
fields, each validated as an integer in the 0–100 range; the `start_battery_pct` input SHALL
carry a suggestion label built from the session-selected vehicle's latest telemetry snapshot
battery percentage when one exists (graceful empty otherwise, no fabricated value).
`currency` is fixed to `COP` and is not user-editable. `energy_added_kwh` SHALL accept up to
three decimal places. The optional `started_at` / `ended_at` fields default to today's date
but remain optional. On success, the fragment SHALL reflect the new entry; on failure, it
SHALL show validation errors in place.

#### Scenario: Create form rejects a missing location kind

- **GIVEN** a signed-in user on the Charge log page with a valid CSRF token
- **WHEN** they submit the create form with all other required fields valid but no
  `location_kind` selected (or an unrecognized value)
- **THEN** the handler does NOT call `charging.Writer.Create`
- **AND** the create form fragment is re-rendered at HTTP 422
- **AND** an error message is shown alongside the `location_kind` field
- **AND** the other submitted values are pre-filled in the re-rendered form

#### Scenario: Create form location_kind select has no blank option and is required

- **GIVEN** the "Log a charge" create form
- **WHEN** it is rendered
- **THEN** the `location_kind` `<select>` carries the HTML `required` attribute
- **AND** there is no blank (`value=""`) option in the `location_kind` picker
- **AND** the `location_kind` picker is visible in the always-visible section of the form
  (not hidden inside the "More details" expander)

#### Scenario: Successful create with valid location kind

- **GIVEN** a signed-in user on the Charge log page with a valid CSRF token
- **WHEN** they submit the create form with all required fields valid, including
  `location_kind` set to one of `HOME`, `WORK`, or `OTHER`
- **THEN** the gateway validates the CSRF token and vehicle ownership
- **AND** calls `charging.Writer.Create` with the entry including `LocationKind`
- **AND** the new entry appears in the updated charge list

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
  account before calling `charging.Writer.Create`
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
- **AND** the `charging.Writer.Create` port is not called
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
  are cleared, and the `charging.Entry.StartedAt` / `EndedAt` sent to the Writer
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

### Requirement: Inline Row Editing

The gateway SHALL let a signed-in user edit an existing charge entry directly in the table
row via htmx. `location_kind` is a **required** field on the edit form; the handler SHALL
reject a missing or unrecognized value with a 422 and a field-level error.

#### Scenario: Edit form rejects a missing location kind

- **GIVEN** a signed-in user with an inline edit form open for an existing entry
- **WHEN** they submit the edit form with `location_kind` missing or not in
  `{HOME, WORK, OTHER}` (e.g. bypassing the browser constraint with a crafted request)
- **THEN** the handler does NOT call `charging.Writer.Update`
- **AND** the edit form is re-rendered at HTTP 422
- **AND** an error message is shown alongside the `location_kind` field

#### Scenario: Edit form location_kind select has no blank option and is required

- **GIVEN** an inline edit form for an existing charge entry
- **WHEN** it is rendered
- **THEN** the `location_kind` `<select>` carries the HTML `required` attribute
- **AND** there is no blank (`value=""`) option in the `location_kind` picker
- **AND** the stored location value is pre-selected (exactly one option carries `selected`)

### Requirement: Delete Charge Entry

The gateway SHALL let a signed-in user delete an existing charge entry from the table. The
delete action SHALL require a CSRF token, enforce tenant ownership, and on success remove the
row from the table without a full page reload and without a browser `alert()`. On failure (a
CSRF mismatch, a cross-tenant id, or a `charging.Writer.Delete` error) the gateway SHALL
NOT surface a plain browser `alert()`; a Writer error SHALL render a row-level error message
in place of the row.

#### Scenario: Deleting an entry removes the row without an alert

- **GIVEN** a signed-in user viewing the charge list with at least one entry
- **AND** the page carries a valid CSRF token in the form/button
- **WHEN** the user confirms deletion (browser native confirm dialog)
  and the browser issues `DELETE /ui/charges/row/{id}` with the CSRF token
- **THEN** the gateway validates the CSRF token
- **AND** validates that the entry belongs to the user's account
- **AND** calls `charging.Writer.Delete(ctx, accountID, id)`
- **AND** the row is removed from the table (swapped for an empty element via htmx
  `hx-swap="outerHTML"`)
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

#### Scenario: Delete shows a server-side error message, not a generic alert, on failure

- **GIVEN** a signed-in user clicking Delete on one of their entries
- **WHEN** the `charging.Writer.Delete` call returns an error (e.g. a transient
  store failure)
- **THEN** the server returns a non-2xx response carrying an inline row-error
  fragment rendered inside the row
- **AND** the user sees the row-level error message (e.g. "Could not delete entry —
  please try again."), not a raw HTTP status string or a browser `alert()`
- **AND** the entry is not removed from the list

### Requirement: Gateway Imports No chargingdb Package

The gateway SHALL access manual charge data exclusively through the `charging.Writer`
and `charging.Reader` public interfaces. It SHALL NOT import `internal/charging/db`
(`chargingdb`) or any generated sqlc types. On the charges page, the gateway also reads
telemetry data (the `start_battery_pct` suggestion label) exclusively through
`telemetry.Reader.LatestSnapshotsByAccount` and SHALL NOT import `internal/telemetry/db`
(`telemetrydb`) for that purpose.

#### Scenario: Gateway only uses charging public interfaces

- **GIVEN** any handler or helper in `internal/gateway/`
- **WHEN** it reads or writes manual charge entries
- **THEN** it does so exclusively via `charging.Reader` or `charging.Writer`
- **AND** no `chargingdb` package is imported in any gateway file
- **AND** no `pgtype` type appears in any gateway handler, view model, or template

#### Scenario: Charges page never imports chargingdb or telemetrydb, and makes no live Tesla call

- **GIVEN** the gateway handlers and helpers that build and validate the charges
  create form
- **WHEN** they obtain vehicle identity, telemetry for the battery-suggestion label,
  and persist or delete an entry
- **THEN** they do so exclusively through the `account`, `telemetry.Reader`, and
  `charging.Reader`/`charging.Writer` public interfaces
- **AND** they import no package from `internal/charging/db` or
  `internal/telemetry/db`
- **AND** no `pgtype` type appears in any gateway file involved
- **AND** no live Tesla Fleet API call is made on any charges-page request
