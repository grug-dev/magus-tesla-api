# gateway Specification — MODIFIED and ADDED Requirements (delta for RM4-gateway-require-location-and-autoselect-vehicle)

This file is a **delta** on the existing `openspec/specs/gateway/spec.md`. It adds and
modifies requirements for tier 4 of RM4: (a) `location_kind` is now required on both the
create and edit charge forms; (b) the create form vehicle picker pre-selects the right
vehicle automatically using `Vehicle.AccessType`; (c) the one-time Tesla vehicle seed
propagates `access_type` from the adapter into the account module's seed call.

This delta does NOT duplicate unchanged requirements from the main gateway spec.

---

## MODIFIED Requirements

### Requirement: Create Charge Entry

The gateway SHALL let a signed-in user create a manual charge entry by submitting the create
form via `POST /ui/charges/create`. The handler SHALL validate the form, enforce tenant
ownership of the chosen vehicle, require a valid CSRF token, and call
`manualcharge.Writer.Create` on success. `location_kind` is a **required** field; the handler
SHALL reject a missing or unrecognized value with a 422 and a field-level error message.
On success, the fragment SHALL reflect the new entry; on failure, it SHALL show validation
errors in place.

#### Scenario: Create form rejects a missing location kind

- **GIVEN** a signed-in user on the Charge log page with a valid CSRF token
- **WHEN** they submit the create form with all other required fields valid but no
  `location_kind` selected (or an unrecognized value)
- **THEN** the handler does NOT call `manualcharge.Writer.Create`
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
- **AND** calls `manualcharge.Writer.Create` with the entry including `LocationKind`
- **AND** the new entry appears in the updated charge list

---

### Requirement: Inline Row Editing

The gateway SHALL let a signed-in user edit an existing charge entry directly in the table
row via htmx. `location_kind` is a **required** field on the edit form; the handler SHALL
reject a missing or unrecognized value with a 422 and a field-level error.

#### Scenario: Edit form rejects a missing location kind

- **GIVEN** a signed-in user with an inline edit form open for an existing entry
- **WHEN** they submit the edit form with `location_kind` missing or not in
  `{HOME, WORK, OTHER}` (e.g. bypassing the browser constraint with a crafted request)
- **THEN** the handler does NOT call `manualcharge.Writer.Update`
- **AND** the edit form is re-rendered at HTTP 422
- **AND** an error message is shown alongside the `location_kind` field

#### Scenario: Edit form location_kind select has no blank option and is required

- **GIVEN** an inline edit form for an existing charge entry
- **WHEN** it is rendered
- **THEN** the `location_kind` `<select>` carries the HTML `required` attribute
- **AND** there is no blank (`value=""`) option in the `location_kind` picker
- **AND** the stored location value is pre-selected (exactly one option carries `selected`)

---

## ADDED Requirements

### Requirement: Vehicle Auto-Select on the Charge Create Form

The gateway SHALL pre-select the most appropriate vehicle in the create form's vehicle picker
on every render, using the following rule (RD4). If the account has exactly one registered
vehicle, the picker SHALL be disabled and a hidden input SHALL carry the vehicle value so the
form POST succeeds. If the account has multiple vehicles, the first vehicle with
`Vehicle.AccessType == "OWNER"` SHALL be pre-selected; if no OWNER vehicle exists, the first
vehicle in the list SHALL be pre-selected. The vehicle picker SHALL NEVER render without a
pre-selected option. The auto-select logic is pre-computed by the handler; templates receive
only a boolean `Selected` flag per option and do no AccessType comparisons.

#### Scenario: Single vehicle is pre-selected and the picker is disabled

- **GIVEN** a signed-in user whose account has exactly one registered vehicle
- **WHEN** the Charge log create form is rendered
- **THEN** the vehicle `<select>` is rendered with the `disabled` attribute
- **AND** the sole vehicle's option is the selected option
- **AND** a sibling `<input type="hidden" name="vehicle">` carries the vehicle's value
  so the form POST includes the vehicle even though the `<select>` is disabled

#### Scenario: Multiple vehicles with an OWNER vehicle — OWNER is pre-selected

- **GIVEN** a signed-in user whose account has two or more registered vehicles
- **AND** at least one vehicle has `Vehicle.AccessType == "OWNER"`
- **WHEN** the create form is rendered
- **THEN** the first vehicle with `AccessType == "OWNER"` is pre-selected in the picker
- **AND** the `<select>` is NOT disabled (the user can change the selection)

#### Scenario: Multiple vehicles with no OWNER — first in list is pre-selected

- **GIVEN** a signed-in user whose account has two or more registered vehicles
- **AND** no vehicle has `Vehicle.AccessType == "OWNER"` (all are DRIVER or nil)
- **WHEN** the create form is rendered
- **THEN** the first vehicle in the list is pre-selected
- **AND** the `<select>` is NOT disabled

#### Scenario: Auto-select is computed by the handler, not the template

- **GIVEN** any Templ template rendering the vehicle picker in the create form
- **WHEN** it renders each vehicle option
- **THEN** the `Selected` flag is a pre-computed boolean on the view model (`VehicleOptionVM`)
- **AND** the template only checks `opt.Selected` without any AccessType comparisons or
  `len()` calls inside the Templ file

---

### Requirement: Seed Mapping Preserves Vehicle Access Type

The gateway SHALL propagate each vehicle's `access_type` from the Tesla adapter into the
account module's seed call during the one-time Tesla vehicle seed (first Tesla connect).
Specifically, the gateway SHALL map a non-empty `VehicleTesla.AccessType` string to a
non-nil `SeedVehicle.AccessType *string`, and SHALL map an empty string to `nil`, following
the project's boundary-nil convention.

#### Scenario: Non-empty access_type is propagated to the seed call

- **GIVEN** the gateway building the seed list from a Tesla `ListVehicles` response
- **AND** at least one vehicle has a non-empty `access_type` string (e.g. `"OWNER"`)
- **WHEN** the gateway calls `account.SeedVehicles`
- **THEN** the `SeedVehicle.AccessType` for that vehicle is a non-nil `*string` pointing
  to the `access_type` value
- **AND** the persisted `vehicles.access_type` column is set to that value

#### Scenario: Empty access_type maps to nil at the seed boundary

- **GIVEN** the gateway building the seed list from a Tesla `ListVehicles` response
- **AND** a vehicle has an empty `access_type` string (the Fleet API omitted the field)
- **WHEN** the gateway calls `account.SeedVehicles`
- **THEN** `SeedVehicle.AccessType` for that vehicle is `nil`
- **AND** the persisted `vehicles.access_type` column is `NULL`

---

### Requirement: location_kind Visible Without Expanding "More Details"

In the create form, `location_kind` SHALL be visible in the always-visible required fields
section, not inside the `<details>` expander. The "More details" expander SHALL retain the
remaining optional fields (`started_at`, `ended_at`, `start_battery_pct`, `end_battery_pct`,
`charging_type`, `location_label`, `notes`).

#### Scenario: location_kind is always visible in the create form

- **GIVEN** the Charge log create form
- **WHEN** it is rendered (before any user interaction with the expander)
- **THEN** the `location_kind` picker is visible without expanding "More details"
- **AND** the "More details" expander still exists and reveals the remaining optional fields
