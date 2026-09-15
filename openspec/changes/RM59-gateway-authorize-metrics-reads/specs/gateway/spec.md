## MODIFIED Requirements

### Requirement: Dashboard renders enriched vehicle cards from stored telemetry

The gateway dashboard (`/dashboard` and `/ui/vehicles`) SHALL enrich each registered
vehicle card with the vehicle's latest precomputed vehicle-status row. The data
SHALL come exclusively from the `analytics.Reader` port (`LatestMetricsForVehicles`)
— no live Tesla Fleet API call on a normal dashboard render, and no direct read of
`internal/telemetry` for this purpose. Every registered vehicle SHALL appear on the
dashboard regardless of whether a precomputed row exists.

The Extended field set displayed per enriched vehicle card is: battery % (integer),
range in kilometres (float), charging state (string, absent renders as not charging),
odometer in kilometres (float), inside temperature in °C (float, absent renders a
placeholder), outside temperature in °C (float, absent renders a placeholder), locked
status (three-state: absent / unlocked / locked), sentry mode (three-state: absent /
off / on), and last-updated timestamp (absent when the underlying row predates this
capability's status-observation tracking). The gateway SHALL read every one of these
values in its display unit as the analytics read port provides it, and SHALL NOT
perform any unit conversion of its own. An absent (nil) value SHALL render its
documented placeholder or omission — never a fabricated default such as "unlocked" or
"sentry off."

---

#### Scenario: Enriched vehicle card is shown when a precomputed row exists

- **GIVEN** a signed-in user whose account has at least one registered vehicle
- **AND** the analytics module has a precomputed vehicle-status row for that vehicle
  with every status observation populated
- **WHEN** the user's dashboard is rendered (either full-page `GET /dashboard` or
  htmx fragment `GET /ui/vehicles`)
- **THEN** the vehicle card displays:
  - Battery level as an integer percentage
  - Battery range in kilometres (read directly from the row, already stored in
    kilometres — no conversion in the gateway)
  - Charging state as a string (e.g. "Charging", "Disconnected")
  - Odometer in kilometres (read directly from the row, already stored in
    kilometres — no conversion in the gateway)
  - Inside temperature in degrees Celsius (displayed as stored — no conversion)
  - Outside temperature in degrees Celsius (displayed as stored — no conversion)
  - Locked status (shown as locked / unlocked)
  - Sentry mode with three distinct visual states: absent ("not reported"), false
    ("off"), true ("on")
  - Last-updated label showing when the row was captured
- **AND** no live Tesla Fleet API call is triggered to render the card

---

#### Scenario: Placeholder card is shown for a vehicle with no precomputed row yet

- **GIVEN** a signed-in user whose account has a registered vehicle
- **AND** no precomputed vehicle-status row has been stored for that vehicle (e.g.
  the nightly recalculation has not yet run since the vehicle was registered)
- **WHEN** the dashboard is rendered
- **THEN** the vehicle card is still shown (the vehicle is not hidden or omitted)
- **AND** the card displays the vehicle's identity information (display name, VIN)
- **AND** the card shows a placeholder message indicating that no status data is
  available yet
- **AND** no stale marker is shown on a no-row card

---

#### Scenario: A precomputed row that predates status-observation tracking degrades per field, not per card

- **GIVEN** a signed-in user whose account has a registered vehicle
- **AND** that vehicle's latest precomputed row exists (battery, range, and odometer
  are present) but predates the capability's status-observation tracking, so every
  status observation on that row is absent
- **WHEN** the dashboard is rendered
- **THEN** the vehicle card still shows battery, range, and odometer normally
- **AND** the temperature fields show their placeholder, the software-version and
  charge-limit lines are omitted, locked and sentry-mode show no badge/value, and no
  last-updated label or stale marker is shown
- **AND** the card is NOT treated as "no data yet" — it is not a placeholder card,
  it is a normal card with some fields absent

---

#### Scenario: Last-updated label is always visible when the row carries a capture instant

- **GIVEN** a signed-in user whose account has a registered vehicle with a
  precomputed row that carries a capture instant
- **WHEN** the dashboard is rendered
- **THEN** the vehicle card always shows a human-readable "last updated" label derived
  from that capture instant
- **AND** the label is visible regardless of whether the row is fresh or stale

---

#### Scenario: Stale marker appears when the row's capture instant is older than the staleness threshold

- **GIVEN** a signed-in user whose account has a registered vehicle
- **AND** the vehicle's latest precomputed row carries a capture instant that is more
  than 36 hours before the time of the current request (the staleness threshold — one
  full missed nightly cycle, 24 h + 12 h buffer)
- **WHEN** the dashboard is rendered
- **THEN** the vehicle card shows a clearly visible stale marker (e.g. a "stale" badge
  or highlighted warning label) in addition to the normal last-updated label
- **AND** the staleness threshold is controlled by a named constant in the handler code
  (not a magic number)

---

#### Scenario: No stale marker when the row's capture instant is within the staleness threshold

- **GIVEN** a signed-in user whose account has a registered vehicle
- **AND** the vehicle's latest precomputed row carries a capture instant within the
  last 36 hours
- **WHEN** the dashboard is rendered
- **THEN** no stale marker is shown — only the normal last-updated label

---

#### Scenario: Sentry mode absence is rendered distinctly from false and true

- **GIVEN** a vehicle card whose precomputed row has no sentry-mode observation (the
  vehicle did not report it at capture time, or the row predates status-observation
  tracking)
- **WHEN** the dashboard renders that vehicle card
- **THEN** the sentry mode display state is "not reported" (or equivalent placeholder)
- **AND** this visual state is distinct from "off" (false) and "on" (true)
- **AND** no boolean assumption is made — absence is not treated as false

---

#### Scenario: Analytics reader failure degrades gracefully

- **GIVEN** a signed-in user whose account has registered vehicles
- **AND** the analytics `Reader.LatestMetricsForVehicles` call returns an error
- **WHEN** the dashboard is rendered
- **THEN** the dashboard still renders successfully — it does NOT return an error page
  or a 500 response
- **AND** each registered vehicle is shown with its identity information (display name,
  VIN) as if it had no precomputed row
- **AND** a non-fatal notice is shown in the vehicles region indicating that status
  data is temporarily unavailable
- **AND** the user can still see all their registered vehicles

---

#### Scenario: First-connect seed is retained unchanged

- **GIVEN** a signed-in user who has just connected their Tesla account and has zero
  registered vehicles
- **WHEN** the dashboard is first rendered
- **THEN** the gateway performs the one-time seed call (`account.SeedVehicles`)
  to populate the account registry from the Tesla Fleet API
- **AND** this first-connect seed behavior is unchanged from the pre-change behavior
- **AND** after seeding, if no precomputed row exists yet, each vehicle card shows the
  placeholder ("no data yet") state

---

#### Scenario: Templates contain no business logic

- **GIVEN** any Templ template in `internal/gateway/templates/`
- **WHEN** it renders a vehicle card (enriched or placeholder)
- **THEN** number formatting (rounding and thousands separators), staleness computation,
  the absent-value placeholder, and timestamp formatting have already been computed by
  the Go handler before the template receives the view model
- **AND** no unit conversion happens anywhere in the render path — neither in the template
  nor in the handler, because the precomputed row already carries display units
- **AND** the template uses only presentation logic (if/for/display) — no arithmetic,
  no method calls on domain types, no time calculations

---

#### Scenario: Gateway never imports telemetrydb or analyticsdb for this read

- **GIVEN** the gateway handler that reads vehicle-status data for the dashboard and
  vehicle cards
- **WHEN** it obtains that data
- **THEN** it does so exclusively through the `analytics.Reader` public interface
- **AND** it imports no package from `internal/telemetry/db` or `internal/analytics/db`
- **AND** no `pgtype` type appears in any gateway file


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
- **AND** the suggestion is built from the `analytics.Reader.LatestMetricsForVehicles`
  port (the same port the dashboard uses), picking the status whose `TeslaID`
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


### Requirement: Navigation Vehicle Header

The gateway SHALL render a vehicle header in the navigation shell that
shows the active vehicle's display name, battery level, and a
freshness-derived status. The data SHALL come exclusively from existing
read ports: `account.RegisteredVehicles` (vehicle name) and
`analytics.Reader.LatestMetricsForVehicles` (battery level + capture instant).
The status SHALL be derived from the row's freshness, not from any live Tesla
call. A row whose capture instant is absent SHALL be treated as stale (never
"Connected") and SHALL render no relative "Last seen" label, since there is no
timestamp to compute one from. The status word, the connect-prompt text, and the
relative "Last seen" phrasing SHALL all render through the translation catalogue, derived from the
header's existing closed `Status` kind and a pre-computed relative-time magnitude — the handler
SHALL NOT compute a pre-formatted English status string or a pre-formatted English relative-time
phrase.

The active vehicle is the one selected in the vehicle context switcher
(below); when the session carries no selection the gateway SHALL
auto-select the first `OWNER` vehicle from `account.RegisteredVehicles`
(falling back to the first registered vehicle when none is `OWNER`), via
`resolveSelectedVehicle`. The header's name, battery, and status reflect
THIS selected vehicle's latest precomputed row.

The gateway SHALL render a vehicle context switcher whenever the account
has more than one registered vehicle: a `<select>` listing the registered
vehicles (each option value formatted `{TeslaID}:{VIN}`) with the active
vehicle pre-selected. Changing the selection SHALL `POST` to
`/ui/vehicle/select`, which SHALL (a) require an authenticated session;
(b) validate a CSRF token stored under the `csrf_vehicle_select` session
key; (c) validate that the submitted `(TeslaID, VIN)` pair belongs to the
calling account (tenant ownership — HTTP 403 otherwise); (d) persist the
chosen `TeslaID` + `VIN` to the session; (e) re-render the nav-header
fragment with the new selection; and (f) emit an `HX-Trigger:
vehicle-changed` response header so per-vehicle regions elsewhere on the
page can refresh (see "Vehicle-Scoped Cross-Region Refresh").

#### Scenario: Vehicle header shows "Connected" when a fresh row exists

- **GIVEN** a signed-in user whose account has at least one registered
  vehicle
- **AND** the latest precomputed row for the active vehicle carries a capture
  instant within the connected-freshness window (48 hours of the
  current request time)
- **WHEN** the navigation header is rendered
- **THEN** the header shows the vehicle's display name
- **AND** shows a "Connected" status (translated per the resolved language) with a
  success-colored status dot
- **AND** shows the battery level as an integer percentage
- **AND** the freshness window is controlled by a named constant in the
  handler code (not a magic number)

#### Scenario: Vehicle header shows "Asleep / Last seen" when the row is stale, with translated relative-time phrasing

- **GIVEN** a signed-in user whose active vehicle's latest precomputed
  row carries a capture instant older than the connected-freshness window
- **WHEN** the navigation header is rendered
- **THEN** the header shows the vehicle's display name
- **AND** shows an "Asleep" (or equivalent, translated) status with a
  warning-colored status dot
- **AND** shows a relative "Last seen" label (e.g. "2 days ago" for `en`, "hace 2 días" for `es`)
  whose magnitude is pre-computed by the handler but whose phrasing (including singular vs.
  plural) renders through the translation catalogue for the resolved language
- **AND** the template performs no time arithmetic and no pluralization logic

#### Scenario: Vehicle header shows "Asleep" with no last-seen label when the row's capture instant is absent

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row exists
  (battery and identity data are present) but carries no capture instant — the row
  predates this capability's status-observation tracking
- **WHEN** the navigation header is rendered
- **THEN** the header shows the vehicle's display name
- **AND** shows an "Asleep" status with a warning-colored status dot — never
  "Connected", since there is no timestamp to prove freshness with
- **AND** no relative "Last seen" label is shown — there is no capture instant to
  compute one from
- **AND** the battery percentage is shown as normal (battery is never absent on a
  row that exists)

#### Scenario: Vehicle header degrades when no precomputed row exists

- **GIVEN** a signed-in user whose account has a registered active
  vehicle but no precomputed row for it
- **WHEN** the navigation header is rendered
- **THEN** the header renders without a 500 or raw error
- **AND** shows the vehicle's display name with a translated "awaiting first
  snapshot" status and a neutral status dot
- **AND** no battery percentage is shown

#### Scenario: Vehicle header degrades when no vehicle is registered

- **GIVEN** a signed-in user whose account has no registered vehicles
- **WHEN** the navigation header is rendered
- **THEN** the header renders an "awaiting connect" state (no status dot,
  no battery percentage), with the connect-prompt text translated per the
  resolved language
- **AND** offers a link to `/connect/tesla`
- **AND** no vehicle name is shown

#### Scenario: Navigation header read failures degrade gracefully

- **GIVEN** a signed-in user whose account read or analytics read returns
  an error
- **WHEN** the navigation header is rendered
- **THEN** the header still renders (no 500, no raw error string)
- **AND** shows a degraded, translated state (e.g. "Unavailable") with a neutral dot

#### Scenario: Navigation header is served as an htmx fragment

- **GIVEN** an authenticated page rendered through `BaseAuth`
- **WHEN** the page loads in the browser
- **THEN** the navigation header is loaded via an htmx `GET /ui/nav-header`
  swap into a placeholder rendered by the shell
- **AND** a request to `GET /ui/nav-header` without an authenticated
  session is redirected to `/login` (no header is served to anonymous
  callers)

#### Scenario: Templates contain no business logic

- **GIVEN** the navigation header template
- **WHEN** it renders the header
- **THEN** the battery percentage, the freshness/connected state, the
  relative "last seen" label's magnitude, and the active-vehicle name have all been
  computed by the Go handler before the template receives the view model
- **AND** the status word and the "last seen" phrasing are derived in the template from the
  handler-computed `Status` enum and magnitude via the translation catalogue, not from a
  handler-computed English string
- **AND** the template uses only presentation logic (if/for/display, and the
  catalogue lookup) — no arithmetic, no time calculations, no method calls on
  domain types

#### Scenario: Navigation header never imports telemetrydb, analyticsdb, or accountdb

- **GIVEN** the gateway handler that builds the navigation header
- **WHEN** it obtains vehicle and status data
- **THEN** it does so exclusively through the `account.Service` and
  `analytics.Reader` public interfaces
- **AND** it imports no package from `internal/telemetry/db`,
  `internal/analytics/db`, or `internal/account/db`
- **AND** no `pgtype` type appears in any gateway file involved

#### Scenario: Status dot uses semantic tokens

- **GIVEN** the navigation header rendered with any status
- **WHEN** the status dot is rendered
- **THEN** its color is expressed via DaisyUI semantic tokens (e.g.
  `badge-success`, `badge-warning`, `badge-ghost`)
- **AND** no hardcoded hex color appears for the status dot

#### Scenario: Context switcher lists registered vehicles and marks the active one

- **GIVEN** a signed-in user whose account has two or more registered vehicles
- **WHEN** the navigation header is rendered
- **THEN** the header renders a `<select>` vehicle switcher listing every registered vehicle
- **AND** exactly one option — the active (selected) vehicle — is marked selected
- **AND** each option's submit value is formatted `{TeslaID}:{VIN}`

#### Scenario: Switching the vehicle persists the selection and fires vehicle-changed

- **GIVEN** a signed-in user with two or more registered vehicles
- **WHEN** they submit a valid `POST /ui/vehicle/select` for a vehicle in their account (with a valid `csrf_vehicle_select` token)
- **THEN** the gateway persists the chosen `TeslaID` + `VIN` to the session
- **AND** re-renders the nav-header fragment with the newly-selected vehicle
- **AND** the response carries an `HX-Trigger: vehicle-changed` header

#### Scenario: Vehicle switch rejects a vehicle outside the caller's account

- **GIVEN** a signed-in user submitting a `POST /ui/vehicle/select` for a `(TeslaID, VIN)` pair not registered to their account
- **WHEN** the handler validates ownership against `account.RegisteredVehicles`
- **THEN** the gateway returns HTTP 403 and does not change the session selection

#### Scenario: Vehicle switch requires a valid CSRF token

- **GIVEN** a signed-in user submitting `POST /ui/vehicle/select` without a valid `csrf_vehicle_select` token
- **WHEN** the handler checks the token
- **THEN** the gateway returns HTTP 403 and does not change the session selection


### Requirement: Gateway Imports No chargingdb Package

The gateway SHALL access manual charge data exclusively through the `charging.Writer`
and `charging.Reader` public interfaces. It SHALL NOT import `internal/charging/db`
(`chargingdb`) or any generated sqlc types. On the charges page, the gateway also reads
vehicle status data (the `start_battery_pct` suggestion label) exclusively through
`analytics.Reader.LatestMetricsForVehicles` and SHALL NOT import `internal/telemetry` or
`internal/telemetry/db` (`telemetrydb`) for that purpose.

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

