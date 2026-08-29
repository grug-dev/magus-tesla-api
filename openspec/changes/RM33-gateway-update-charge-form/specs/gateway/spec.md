## MODIFIED Requirements

### Requirement: Create Charge Entry

The gateway SHALL let a signed-in user create a manual charge entry by submitting the create
form via `POST /ui/charges/create`. The form SHALL NOT include a vehicle field — the handler
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

The form SHALL include a status control (`IN_PROGRESS` / `DONE`), defaulting to `IN_PROGRESS`.
`ended_at` and `end_battery_pct` are **required only when the submitted status is `DONE`**; when
the status is `IN_PROGRESS`, both may be omitted. The gateway SHALL derive this required-field
set from the `charging` module's own declarative rule rather than encode it separately. The
optional `started_at` / `ended_at` fields default to today's date but remain optional whenever
they are not required by the entry's status. On success, the fragment SHALL reflect the new
entry; on failure, it SHALL show validation errors in place **and SHALL preserve every value the
user submitted — valid or not — for every field on the form**, not only the fields that already
had a today's-date default.

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

#### Scenario: Currency is always COP, shown as a suffix, and not user-editable

- **GIVEN** the rendered Charge log create form
- **WHEN** it is inspected
- **THEN** the price field renders as an input with a `COP` suffix, not a separate
  Currency field
- **AND** there is no editable Currency `<input>` or `<select>` the user can change,
  and no disabled Currency field either
- **WHEN** the create form is submitted
- **THEN** the persisted entry's `currency` field is `COP` regardless of any value
  that could be supplied for it

#### Scenario: Start battery percentage is required regardless of status

- **GIVEN** a signed-in user on the Charge log page
- **WHEN** they submit the create form with `start_battery_pct` empty, non-integer, or
  outside the 0–100 range, for either an `IN_PROGRESS` or a `DONE` status
- **THEN** the server rejects the submission with a field-level validation error
  indicating the required/invalid battery field
- **AND** the `charging.Writer.Create` port is not called
- **WHEN** `start_battery_pct` is supplied as an integer in 0–100
- **THEN** the entry is persisted with a non-nil `start_battery_pct` matching the
  submitted value

#### Scenario: Ending battery percentage and session end time are required only when the status is done

- **GIVEN** a signed-in user on the Charge log page with the status control set to `IN_PROGRESS`
- **WHEN** they submit the create form with no `ended_at` and no `end_battery_pct`, and every
  other required field valid
- **THEN** the entry is created successfully with a nil `ended_at` and a nil `end_battery_pct`
- **GIVEN** the same user with the status control set to `DONE`
- **WHEN** they submit the create form with no `ended_at` or no `end_battery_pct`
- **THEN** the server rejects the submission with a field-level validation error naming the
  missing field, and `charging.Writer.Create` is not called

#### Scenario: Energy added and price are optional

- **GIVEN** a signed-in user on the Charge log page
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
  default value on a fresh page load
- **AND** `started_at` remains OPTIONAL at every status — clearing it still submits
- **AND** `ended_at` remains OPTIONAL when the status is `IN_PROGRESS` and becomes
  REQUIRED when the status is `DONE`

#### Scenario: Energy added accepts up to three decimal places when supplied

- **GIVEN** the rendered Charge log create form
- **WHEN** it is inspected
- **THEN** the `energy_added_kwh` input declares a 3-decimal step (permitting
  values like `7.345`) and carries no `required` attribute
- **WHEN** the user submits `energy_added_kwh = 7.345`
- **THEN** the server accepts the value (parses as a positive float) and the entry
  is persisted with `energy_added_kwh = 7.345` (no server-side rounding to 2
  decimals)

#### Scenario: A validation failure preserves every submitted value, not only the date defaults

- **GIVEN** a signed-in user submitting the create form with a valid `location_kind` of `WORK`,
  a status of `DONE`, valid battery percentages, non-empty `energy_added_kwh`, `price`,
  `charging_type`, `location_label`, and `notes`, but an out-of-range `end_battery_pct`
- **WHEN** the server re-renders the form at HTTP 422
- **THEN** every one of those submitted values — including the ones with no day-based default —
  is still present in the re-rendered form's inputs, not reset to blank or to a different default
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

## ADDED Requirements

### Requirement: Charge form odometer field

Both the create form and the inline edit form SHALL include an optional odometer reading field,
presented as a whole-kilometre integer, grouped with the other optional fields inside the "More
details" disclosure alongside charging type, location label, and notes.

#### Scenario: Odometer is optional and validated as a non-negative integer

- **GIVEN** a signed-in user submitting either the create form or an inline edit form
- **WHEN** the odometer field is left empty
- **THEN** the entry is stored with no odometer reading
- **WHEN** the odometer field is supplied as a non-negative integer
- **THEN** the entry is stored with that value
- **WHEN** the odometer field is supplied as a negative number or a non-integer value
- **THEN** the submission is rejected with a field-level validation error on the odometer field

### Requirement: Charging type option text explains AC and DC

Both the create form and the inline edit form SHALL present the charging type options with
descriptive text distinguishing AC (slow, home/destination) charging from DC (fast, Supercharger)
charging, in both supported languages. The underlying submitted and stored values remain `AC` and
`DC`, unchanged by the descriptive text.

#### Scenario: Charging type options are descriptive in both languages

- **GIVEN** either charge form rendered in Spanish
- **WHEN** the charging type options are inspected
- **THEN** the AC option reads "AC — Carga lenta (casa/destino)" and the DC option reads
  "DC — Carga rápida (Supercargador)"
- **GIVEN** the same form rendered in English
- **THEN** the AC option reads "AC — Slow charging (home/destination)" and the DC option reads
  "DC — Fast charging (Supercharger)"
- **AND** in both languages, submitting either option persists the unchanged value `AC` or `DC`

### Requirement: Charge date change keeps the time of day on start and end timestamps

When a user changes the charge date on either form, the browser SHALL update the calendar-day
portion of any non-empty session start/end timestamp to match, while leaving the time-of-day
portion of each unchanged. A session start/end timestamp that is empty SHALL remain empty — the
date change SHALL NOT populate it.

#### Scenario: Changing the charge date preserves an already-set time of day

- **GIVEN** a charge form with the session start time set to `14:30` on some date
- **WHEN** the user changes the charge date field to a different date
- **THEN** the session start field's date portion updates to the new date
- **AND** the session start field's time portion remains `14:30`

#### Scenario: Changing the charge date does not populate an empty session time

- **GIVEN** a charge form with the session end time field empty
- **WHEN** the user changes the charge date field
- **THEN** the session end time field remains empty
