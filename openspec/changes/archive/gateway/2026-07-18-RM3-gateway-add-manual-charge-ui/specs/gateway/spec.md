# gateway Specification — ADDED Requirements (delta for RM3-gateway-add-manual-charge-ui)

This file is a **delta** on the existing `openspec/specs/gateway/spec.md`. It adds
requirements for the "Charge log" page introduced by the RM3 Tier 2 change. It does NOT
duplicate existing gateway requirements (sessions, auth, logout, dashboard, telemetry
enrichment) — those remain unchanged in the main spec. It does NOT duplicate the data-layer
requirements for manual charge entries, which live in the `manual-charge-log` capability
(`openspec/specs/manual-charge-log/spec.md` — tier 1).

---

## ADDED Requirements

### Requirement: Charge Log Page

The gateway SHALL serve a standalone "Charge log" page at `/charges` for signed-in users,
displaying the user's manual charge entries (newest first) and a form to create new entries.
The page SHALL be accessible from the dashboard navigation and SHALL require an authenticated
session.

#### Scenario: Signed-in user opens the Charge log page

- **GIVEN** a signed-in user with at least one registered Tesla vehicle
- **WHEN** they navigate to `GET /charges`
- **THEN** the gateway renders the full Charge log page
- **AND** the page shows a list of their manual charge entries (if any exist), ordered newest
  first by charge date
- **AND** the page shows a "Log a charge" create form with required fields visible
- **AND** the vehicle picker in the create form is populated with the user's own registered
  vehicles (obtained through the account module's `RegisteredVehicles` port)
- **AND** no vehicle belonging to another user is present in the picker

#### Scenario: Anonymous visitor is redirected from the Charge log page

- **GIVEN** a visitor with no authenticated session
- **WHEN** they request `GET /charges`
- **THEN** the gateway redirects them to `/login`
- **AND** no charge entries or forms are shown

#### Scenario: Charge log page renders when the user has no entries yet

- **GIVEN** a signed-in user who has never logged a manual charge entry
- **WHEN** they open the Charge log page
- **THEN** the page renders successfully (no 500, no empty table with a header)
- **AND** the page shows an empty-state message indicating no entries have been logged yet
- **AND** the create form is still visible so the user can add their first entry

#### Scenario: Charge log is linked from the dashboard navigation

- **GIVEN** a signed-in user viewing the dashboard
- **WHEN** the navigation is rendered
- **THEN** a "Charge log" navigation link pointing to `/charges` is visible
- **AND** anonymous users do not see this link

---

### Requirement: Charge List Fragment

The gateway SHALL serve the charge entries list as an htmx-swappable fragment at
`GET /ui/charges/list`, returning only the fragment HTML and not the surrounding page shell.
The fragment SHALL include per-entry derived values (cost per kWh, battery delta, session
duration) pre-computed by the handler from the `manualcharge.Entry` value-receiver methods.

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

- **GIVEN** the manualcharge Reader returns an error
- **WHEN** the Charge log page or list fragment is rendered
- **THEN** the gateway renders the page (or fragment) without a 500 or raw error string
- **AND** a user-facing error message ("Could not load your entries") is shown in the list
  region
- **AND** the create form remains accessible

---

### Requirement: Create Charge Entry

The gateway SHALL let a signed-in user create a manual charge entry by submitting the create
form via `POST /ui/charges/create`. The handler SHALL validate the form, enforce tenant
ownership of the chosen vehicle, require a valid CSRF token, and call `manualcharge.Writer.Create`
on success. On success, the fragment SHALL reflect the new entry; on failure, it SHALL show
validation errors in place.

#### Scenario: Successful create adds the entry

- **GIVEN** a signed-in user on the Charge log page with a valid CSRF token in their session
- **WHEN** they submit the create form with valid required fields (charged_on, energy_added_kwh,
  price, currency) and a vehicle selected from their own registered vehicles
- **THEN** the gateway validates the CSRF token against the session value
- **AND** confirms the chosen vehicle belongs to the user's account
- **AND** calls `manualcharge.Writer.Create`
- **AND** the new entry appears in the updated charge list (newest first)
- **AND** the create form is reset for the next entry

#### Scenario: Validation error shows errors in place

- **GIVEN** a signed-in user submitting the create form with an invalid field
  (e.g. energy_added_kwh = 0, or charged_on missing)
- **WHEN** the gateway processes the POST
- **THEN** the handler does NOT call `manualcharge.Writer.Create`
- **AND** the create form fragment is re-rendered with the submitted values pre-filled
- **AND** error messages are shown alongside the invalid fields
- **AND** the HTTP response status is 422

#### Scenario: CSRF mismatch on create is rejected

- **GIVEN** a form POST to `/ui/charges/create` with a CSRF token that does not match the
  session value (or is absent)
- **WHEN** the gateway processes the POST
- **THEN** the handler returns HTTP 403
- **AND** no entry is created
- **AND** a user-facing error is shown (not a raw error string)

#### Scenario: Vehicle not owned by the user is rejected

- **GIVEN** a form POST to `/ui/charges/create` with a `tesla_id` that is not in the user's
  registered vehicle list (e.g. a tampered form value)
- **WHEN** the gateway processes the POST
- **THEN** the handler returns HTTP 403 (forbidden)
- **AND** `manualcharge.Writer.Create` is NOT called

#### Scenario: Optional fields in the create form are stored when provided

- **GIVEN** a signed-in user submitting the create form with valid required fields and also
  supplying started_at, ended_at, start_battery_pct, end_battery_pct
- **WHEN** the gateway processes the POST and calls Writer.Create
- **THEN** the stored entry includes the supplied optional values
- **AND** the resulting row in the list shows the derived battery delta and session duration

#### Scenario: Optional fields under "More details" expander are available

- **GIVEN** the Charge log create form
- **WHEN** a user clicks the "More details" expander
- **THEN** the optional fields (started_at, ended_at, start_battery_pct, end_battery_pct,
  charging_type, location_kind, location_label, notes) become visible
- **AND** these fields are optional — submitting without expanding still creates a valid entry
  with only the required fields

---

### Requirement: Inline Row Editing

The gateway SHALL let a signed-in user edit an existing charge entry directly in the table row
via htmx without navigating to a separate edit page. Clicking "Edit" on a row SHALL swap that
`<tr>` for an inline edit form. Saving or cancelling SHALL swap the row back.

#### Scenario: Clicking Edit swaps a row to an inline edit form

- **GIVEN** a signed-in user viewing the charge list with at least one entry
- **WHEN** they click the "Edit" button on an entry row
  (which issues `GET /ui/charges/row/{id}/edit`)
- **THEN** the gateway returns only that row's edit form HTML (a `<tr>` with inputs)
- **AND** the form inputs are pre-populated with the entry's current values
- **AND** all other rows remain unchanged

#### Scenario: Saving an edit updates the row

- **GIVEN** a signed-in user with an inline edit form open for an entry
- **WHEN** they modify a field and click "Save" (which issues `PUT /ui/charges/row/{id}`)
  with a valid CSRF token
- **THEN** the gateway validates the CSRF token
- **AND** validates tenant ownership of the entry (the entry's account_id matches the
  session uid)
- **AND** calls `manualcharge.Writer.Update`
- **AND** the row swaps back to a static display row showing the updated values
- **AND** derived values (cost per kWh, battery delta, duration) are recomputed and shown

#### Scenario: Save with validation error shows errors in the edit row

- **GIVEN** a signed-in user with an inline edit form open
- **WHEN** they submit with an invalid field (e.g. energy_added_kwh = -1)
- **THEN** the edit form row is re-rendered with the submitted values pre-filled
- **AND** error messages are shown alongside the invalid field
- **AND** `manualcharge.Writer.Update` is NOT called
- **AND** the HTTP response status is 422

#### Scenario: Cancelling edit restores the static row

- **GIVEN** a signed-in user with an inline edit form open for an entry
- **WHEN** they click "Cancel" (which issues `GET /ui/charges/row/{id}`)
- **THEN** the gateway returns the static display `<tr>` for that entry
- **AND** no data is changed
- **AND** no round-trip through the list query is required (the handler fetches only that
  single entry to render the static row)

---

### Requirement: Delete Charge Entry

The gateway SHALL let a signed-in user delete an existing charge entry from the table. The
delete action SHALL require a CSRF token, enforce tenant ownership, and on success remove the
row from the table without a full page reload.

#### Scenario: Deleting an entry removes the row

- **GIVEN** a signed-in user viewing the charge list with at least one entry
- **AND** the page carries a valid CSRF token in the form/button
- **WHEN** the user confirms deletion (browser native confirm dialog)
  and the browser issues `DELETE /ui/charges/row/{id}` with the CSRF token
- **THEN** the gateway validates the CSRF token
- **AND** validates that the entry belongs to the user's account
- **AND** calls `manualcharge.Writer.Delete(ctx, accountID, id)`
- **AND** the row is removed from the table (swapped for an empty element via htmx
  `hx-swap="outerHTML"`)

#### Scenario: Delete with CSRF mismatch is rejected

- **GIVEN** a DELETE request to `/ui/charges/row/{id}` with a missing or wrong CSRF token
- **WHEN** the gateway processes it
- **THEN** the handler returns HTTP 403
- **AND** `manualcharge.Writer.Delete` is NOT called
- **AND** the row is not removed from the table

#### Scenario: Delete of another tenant's entry is rejected

- **GIVEN** a DELETE request to `/ui/charges/row/{id}` where `id` belongs to a different
  user's account
- **WHEN** the gateway processes it
  (the `manualcharge.Writer.Delete(ctx, accountID, id)` scopes the DELETE to the
  caller's account_id via its WHERE clause)
- **THEN** the delete silently finds no row (the Writer's scoped DELETE affects 0 rows)
- **AND** the gateway returns a 404 or empty response — no data is deleted

---

### Requirement: Tenant Isolation

The gateway SHALL ensure that a signed-in user can only view, create, update, and delete
their own manual charge entries. No action by one user SHALL expose or mutate another user's
entries.

#### Scenario: User only sees their own entries in the list

- **GIVEN** two distinct accounts, each with manual charge entries
- **WHEN** account A's user views the Charge log page
- **THEN** only account A's entries are returned (the Reader's `accountID` filter prevents
  cross-tenant reads)
- **AND** none of account B's entries appear

#### Scenario: Write handlers always scope to the session account

- **GIVEN** a signed-in user submitting a create, edit, or delete form
- **WHEN** the gateway processes the write
- **THEN** the handler derives `accountID` from the session (`currentUID`) — not from any
  user-supplied form field
- **AND** all Writer calls carry this session-derived `accountID` as the tenant scope

---

### Requirement: Gateway Imports No manualchargedb Package

The gateway SHALL access manual charge data exclusively through the `manualcharge.Writer`
and `manualcharge.Reader` public interfaces. It SHALL NOT import `internal/manualcharge/db`
(`manualchargedb`) or any generated sqlc types.

#### Scenario: Gateway only uses manualcharge public interfaces

- **GIVEN** any handler or helper in `internal/gateway/`
- **WHEN** it reads or writes manual charge entries
- **THEN** it does so exclusively via `manualcharge.Reader` or `manualcharge.Writer`
- **AND** no `manualchargedb` package is imported in any gateway file
- **AND** no `pgtype` type appears in any gateway handler, view model, or template
