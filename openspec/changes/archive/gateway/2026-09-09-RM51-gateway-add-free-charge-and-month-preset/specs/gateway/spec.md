## MODIFIED Requirements

### Requirement: Create Charge Entry

The gateway SHALL let a signed-in user create a manual charge entry by submitting the create
form via `POST /ui/external-charges/create`. The form SHALL NOT include a vehicle field — the handler
SHALL derive the target vehicle from the session-selected vehicle (the sidebar switcher) and
enforce tenant ownership of that resolved vehicle before writing. The handler SHALL require a
valid CSRF token and call `charging.Writer.Create` on success. `location_kind` is a
**required** field; the handler SHALL reject a missing or unrecognized value with a 422 and a
field-level error message. `start_battery_pct` is a **required** field, validated as an integer
in the 0–100 range, regardless of the entry's status. The `start_battery_pct` input SHALL carry
a suggestion label built from the session-selected vehicle's latest telemetry snapshot battery
percentage when one exists (graceful empty otherwise, no fabricated value). `currency` is fixed
to `COP` and is not user-editable; it SHALL be presented as a suffix on the price input rather
than as a separate field. `energy_added_kwh` and `price` are **optional**: an empty
`energy_added_kwh` SHALL be stored as absent (no fabricated value), and an empty `price` SHALL be
stored as `0`. `energy_added_kwh` SHALL accept up to three decimal places when supplied.

**The price field SHALL be paired with a "this charge was free" checkbox**, always visible and
never disabled, rendered next to the price input. The handler SHALL read this checkbox's
submitted state and pass it to `charging.Writer.Create` as the entry's confirmed-price intent.
The gateway SHALL NOT itself decide whether the intent is honored when the submitted price is
greater than zero — that precedence is the `charging` module's own rule. Submitting the checkbox
SHALL NOT require a second round-trip and SHALL NOT block the save.

The form SHALL include a status control (`IN_PROGRESS` / `DONE`), defaulting to `IN_PROGRESS`.
`ended_at` and `end_battery_pct` are **required only when the submitted status is `DONE`**; when
the status is `IN_PROGRESS`, both may be omitted. The gateway SHALL derive this required-field
set from the `charging` module's own declarative rule rather than encode it separately. The
optional `started_at` / `ended_at` fields default to today's date but remain optional whenever
they are not required by the entry's status. On success, the fragment SHALL reflect the new
entry; on failure, it SHALL show validation errors in place **and SHALL preserve every value the
user submitted — valid or not — for every field on the form, including the "this charge was
free" checkbox's checked state**, not only the fields that already had a today's-date default.

#### Scenario: Create form rejects a missing location kind

- **GIVEN** a signed-in user on the External charges page with a valid CSRF token
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

- **GIVEN** a signed-in user on the External charges page with a valid CSRF token
- **WHEN** they submit the create form with all required fields valid, including
  `location_kind` set to one of `HOME`, `WORK`, or `OTHER`
- **THEN** the gateway validates the CSRF token and vehicle ownership
- **AND** calls `charging.Writer.Create` with the entry including `LocationKind`
- **AND** the new entry appears in the updated charge list

#### Scenario: Create form sources the vehicle from the session selection, not a form field

- **GIVEN** a signed-in user with two or more registered vehicles, currently having
  vehicle `V1` selected in the sidebar switcher
- **WHEN** the External charges page renders
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

- **GIVEN** the rendered External charges create form
- **WHEN** it is inspected
- **THEN** there is no `<select name="vehicle">` and no single-vehicle disabled
  vehicle `<select>`+hidden pair in the form
- **AND** the only way to target a different vehicle with the form is to switch the
  sidebar vehicle selector (which reloads the form via the `vehicle-changed`
  subscription)

#### Scenario: Currency is always COP, shown as a suffix, and not user-editable

- **GIVEN** the rendered External charges create form
- **WHEN** it is inspected
- **THEN** the price field renders as an input with a `COP` suffix, not a separate
  Currency field
- **AND** there is no editable Currency `<input>` or `<select>` the user can change,
  and no disabled Currency field either
- **WHEN** the create form is submitted
- **THEN** the persisted entry's `currency` field is `COP` regardless of any value
  that could be supplied for it

#### Scenario: A checked "this charge was free" box is passed through, unconditionally

- **GIVEN** a signed-in user on the External charges page with a valid CSRF token
- **WHEN** they submit the create form with `price` empty (or `0`) and the "this charge was
  free" checkbox checked, all other required fields valid
- **THEN** the gateway calls `charging.Writer.Create` with the entry's confirmed-price intent
  set to true
- **WHEN** the same user instead submits a `price` greater than zero with the checkbox checked
- **THEN** the gateway still calls `charging.Writer.Create` with the intent set to true — the
  gateway does not inspect the submitted price to decide whether to honor the checkbox itself

#### Scenario: An unchecked "this charge was free" box submits as not confirmed

- **GIVEN** a signed-in user on the External charges page
- **WHEN** they submit the create form with the "this charge was free" checkbox left unchecked
  (or absent from the request)
- **THEN** the gateway calls `charging.Writer.Create` with the entry's confirmed-price intent
  set to false

#### Scenario: Start battery percentage is required regardless of status

- **GIVEN** a signed-in user on the External charges page
- **WHEN** they submit the create form with `start_battery_pct` empty, non-integer, or
  outside the 0–100 range, for either an `IN_PROGRESS` or a `DONE` status
- **THEN** the server rejects the submission with a field-level validation error
  indicating the required/invalid battery field
- **AND** the `charging.Writer.Create` port is not called
- **WHEN** `start_battery_pct` is supplied as an integer in 0–100
- **THEN** the entry is persisted with a non-nil `start_battery_pct` matching the
  submitted value

#### Scenario: Ending battery percentage and session end time are required only when the status is done

- **GIVEN** a signed-in user on the External charges page with the status control set to `IN_PROGRESS`
- **WHEN** they submit the create form with no `ended_at` and no `end_battery_pct`, and every
  other required field valid
- **THEN** the entry is created successfully with a nil `ended_at` and a nil `end_battery_pct`
- **GIVEN** the same user with the status control set to `DONE`
- **WHEN** they submit the create form with no `ended_at` or no `end_battery_pct`
- **THEN** the server rejects the submission with a field-level validation error naming the
  missing field, and `charging.Writer.Create` is not called

#### Scenario: Energy added and price are optional

- **GIVEN** a signed-in user on the External charges page
- **WHEN** they submit the create form with `energy_added_kwh` and `price` both left empty, and
  every required field valid
- **THEN** the entry is created successfully
- **AND** the persisted entry's energy added is absent (not a fabricated zero)
- **AND** the persisted entry's price is `0`
- **WHEN** they submit a non-empty `price` of `"-1"`
- **THEN** the server rejects the submission with a field-level validation error on price
- **WHEN** they submit a non-empty `energy_added_kwh` of `"0"`
- **THEN** the server rejects the submission with a field-level validation error on energy added

#### Scenario: Start battery field suggests the latest telemetry battery percentage

- **GIVEN** a signed-in user whose session-selected vehicle has at least one stored
  telemetry snapshot, the latest of which reports `BatteryLevelPct = 73`
- **WHEN** the External charges create form renders
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
- **WHEN** the External charges create form renders
- **THEN** the `start_battery_pct` input carries no suggestion label (no fabricated
  value is shown)
- **AND** the field still renders as a normal required integer input (0–100)
- **AND** the page renders without error

#### Scenario: Optional started_at and ended_at fields appear in the main card and default to today

- **GIVEN** a signed-in user on the External charges page
- **WHEN** the create form renders
- **THEN** the `started_at` and `ended_at` inputs are rendered inside the main "Log
  a charge" card (the same section as Date / Energy / Price), not behind a "More
  details" disclosure
- **AND** both inputs are pre-filled with today's date in `YYYY-MM-DD` form as a
  default value on a fresh page load
- **AND** `started_at` remains OPTIONAL at every status — clearing it still submits
- **AND** `ended_at` remains OPTIONAL when the status is `IN_PROGRESS` and becomes
  REQUIRED when the status is `DONE`

#### Scenario: Energy added accepts up to three decimal places when supplied

- **GIVEN** the rendered External charges create form
- **WHEN** it is inspected
- **THEN** the `energy_added_kwh` input declares a 3-decimal step (permitting
  values like `7.345`) and carries no `required` attribute
- **WHEN** the user submits `energy_added_kwh = 7.345`
- **THEN** the server accepts the value (parses as a positive float) and the entry
  is persisted with `energy_added_kwh = 7.345` (no server-side rounding to 2
  decimals)

#### Scenario: A validation failure preserves every submitted value, including the free-charge checkbox

- **GIVEN** a signed-in user submitting the create form with a valid `location_kind` of `WORK`,
  a status of `DONE`, valid battery percentages, non-empty `energy_added_kwh`, `price`,
  `charging_type`, `location_label`, `notes`, and the "this charge was free" checkbox checked,
  but an out-of-range `end_battery_pct`
- **WHEN** the server re-renders the form at HTTP 422
- **THEN** every one of those submitted values — including the ones with no day-based default —
  is still present in the re-rendered form's inputs, not reset to blank or to a different default
- **AND** the "this charge was free" checkbox is still rendered checked
- **AND** the invalid `end_battery_pct` value itself is also echoed back so the user can see and
  correct exactly what they typed

### Requirement: Inline Row Editing

The gateway SHALL let a signed-in user edit an existing charge entry directly in the table
row via htmx. `location_kind` is a **required** field on the edit form; the handler SHALL
reject a missing or unrecognized value with a 422 and a field-level error. The edit form SHALL
render a status control (`IN_PROGRESS` / `DONE`) pre-selected to the entry's persisted status,
and moving that control between the two values SHALL be the mechanism by which a user completes
an in-progress entry or reopens a done one — no other UI on this row does so. `ended_at` and
`end_battery_pct` are required only when the submitted status is `DONE`, mirroring the create
form's rule. `energy_added_kwh` and `price` are optional on the edit form, and `currency` renders
as a `COP` suffix on the price input rather than a separate field, mirroring the create form. A
validation failure on this form SHALL preserve every value the user submitted, exactly as the
create form does.

**The price field on the edit form SHALL be paired with the same "this charge was free"
checkbox as the create form.** On a normal (non-error) open, the checkbox's checked state SHALL
reflect the entry's currently stored confirmation — checked only when the entry's price is zero
and its price provenance is the user-confirmed kind. On a validation-failure re-render, the
checkbox SHALL instead echo whatever the user just submitted, exactly like every other field on
this form.

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

#### Scenario: Edit form's status control shows the entry's persisted status, not a default

- **GIVEN** an existing entry stored with status `DONE`
- **WHEN** the user opens its inline edit form
- **THEN** the status control's `DONE` option is pre-selected
- **GIVEN** an existing entry stored with status `IN_PROGRESS`
- **WHEN** the user opens its inline edit form
- **THEN** the status control's `IN_PROGRESS` option is pre-selected

#### Scenario: Completing an in-progress entry from the inline edit row

- **GIVEN** an existing entry stored with status `IN_PROGRESS` and no `ended_at` or
  `end_battery_pct`
- **WHEN** the user opens its inline edit form, changes the status control to `DONE`, and
  supplies both `ended_at` and `end_battery_pct` before saving
- **THEN** the entry is updated with status `DONE` and both previously-absent fields now
  populated
- **GIVEN** the same scenario but the user changes the status to `DONE` without supplying
  `ended_at` or `end_battery_pct`
- **THEN** the save is rejected with a field-level validation error naming the missing field(s)
- **AND** the stored entry is unchanged

#### Scenario: Edit form's free-charge checkbox reflects the entry's stored confirmation

- **GIVEN** an existing entry stored with `price` equal to zero and a price provenance the
  `charging` module recorded as user-confirmed
- **WHEN** the user opens its inline edit form
- **THEN** the "this charge was free" checkbox is rendered checked
- **GIVEN** an existing entry stored with `price` equal to zero and a price provenance the
  `charging` module recorded as unconfirmed
- **WHEN** the user opens its inline edit form
- **THEN** the "this charge was free" checkbox is rendered unchecked
- **GIVEN** an existing entry stored with a `price` greater than zero
- **WHEN** the user opens its inline edit form
- **THEN** the "this charge was free" checkbox is rendered unchecked, regardless of that
  entry's stored price provenance

### Requirement: Charge List Date Filter

The External charges page's entries list SHALL offer a date-filter preset selector with exactly
three presets: "Last 7 days" (the default), "This month" (the full calendar month containing
today — the 1st through the last day of the month, not merely the days elapsed so far), and
"Last month" (the full previous calendar month — the 1st through the last day of the calendar
month immediately before the one containing today). All three presets SHALL be computed against
the requesting browser's local calendar day, never UTC. Selecting a preset SHALL re-fetch and
re-render only the `#external-charges-list` region (presets, tiles, and table together) without a
full page reload or a change to the create form's displayed values. This preset selector is
specific to the External charges page — the Supercharger Stats page's own preset selector SHALL
be unaffected by this requirement.

#### Scenario: The default window is the last 7 calendar days

- **GIVEN** a signed-in user opening the External charges page with no explicit date filter
- **WHEN** the page renders
- **THEN** the entries list is filtered to the 7 calendar days ending today (inclusive), in
  the browser's local timezone
- **AND** the "Last 7 days" preset button is shown in its active state

#### Scenario: Selecting "This month" shows the full calendar month

- **GIVEN** a signed-in user on the External charges page on any day of a given month
- **WHEN** they select the "This month" preset
- **THEN** the entries list is filtered to the 1st through the LAST day of that calendar month
  (inclusive of days later than today, when today is not the last day of the month)
- **AND** the "This month" preset button is shown in its active state and the other two are not

#### Scenario: Selecting "Last month" shows the full previous calendar month

- **GIVEN** a signed-in user on the External charges page, where today falls in a given
  calendar month
- **WHEN** they select the "Last month" preset
- **THEN** the entries list is filtered to the 1st through the LAST day of the calendar month
  immediately before today's month
- **AND** the "Last month" preset button is shown in its active state and the other two are not

#### Scenario: "Last month" correctly crosses a year boundary in January

- **GIVEN** a signed-in user on the External charges page in January of a given year
- **WHEN** they select the "Last month" preset
- **THEN** the entries list is filtered to December 1st through December 31st of the PREVIOUS
  year

#### Scenario: Selecting a preset refreshes only the entries region

- **GIVEN** a signed-in user on the External charges page with the create form partially filled in
- **WHEN** they select a different date-filter preset
- **THEN** only the `#external-charges-list` region (presets, tiles, table) is re-fetched and
  re-rendered
- **AND** the create form's own fields and any values the user had already typed into it are
  left untouched

#### Scenario: The Supercharger Stats preset selector is unaffected

- **GIVEN** the Supercharger Stats page's own date-filter preset selector
- **WHEN** it is rendered, before or after this capability change
- **THEN** it still offers exactly the presets it offered before this requirement's "Last month"
  addition — the External charges page's third preset is not added to it
