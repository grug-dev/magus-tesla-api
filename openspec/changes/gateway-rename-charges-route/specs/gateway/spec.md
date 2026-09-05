## RENAMED Requirements

- FROM: `### Requirement: Charge Log Page`
- TO: `### Requirement: External Charges Page`

## MODIFIED Requirements

### Requirement: External Charges Page

The gateway SHALL serve a standalone "External charges" page at `/external-charges` for signed-in users,
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

#### Scenario: Signed-in user opens the External charges page

- **GIVEN** a signed-in user with at least one registered Tesla vehicle
- **WHEN** they navigate to `GET /external-charges`
- **THEN** the gateway renders the full External charges page
- **AND** the page shows a list of their manual charge entries (if any exist), ordered newest
  first by charge date, scoped to the session-selected vehicle and the active date filter
  window
- **AND** the page shows a "Log a charge" create form with required fields visible
- **AND** the create form has no vehicle picker — it operates on the session-selected vehicle
- **AND** no entry or vehicle belonging to another user is present on the page

#### Scenario: Anonymous visitor is redirected from the External charges page

- **GIVEN** a visitor with no authenticated session
- **WHEN** they request `GET /external-charges` or any `/ui/external-charges*` fragment route
- **THEN** the gateway redirects them to `/login`
- **AND** no charge entries, no create form, and no telemetry-sourced suggestion are shown

#### Scenario: External charges page renders when the user has no entries yet

- **GIVEN** a signed-in user with a registered vehicle who has never logged a manual charge
  entry
- **WHEN** they open the External charges page
- **THEN** the page renders successfully (no 500, no empty table with a header)
- **AND** the page shows an empty-state message indicating no entries have been logged yet
- **AND** the date-filter selector and the (all-zero) aggregation tiles are still shown
- **AND** the create form is still visible so the user can add their first entry

#### Scenario: No selected vehicle renders the bare empty state, with no account-wide fallback

- **GIVEN** a signed-in user whose account has zero registered vehicles, or whose session
  vehicle selection cannot be resolved
- **WHEN** they open `GET /external-charges`
- **THEN** the page renders the SAME "no entries yet" empty-state message used when a
  selected vehicle simply has no entries
- **AND** no date-filter selector is shown
- **AND** no aggregation tiles are shown
- **AND** no table (not even an empty one) is shown
- **AND** the gateway does NOT call `charging.Reader.ListEntriesByAccount` or any other
  account-wide read to populate this page

#### Scenario: External charges is linked from the dashboard navigation

- **GIVEN** a signed-in user viewing the dashboard
- **WHEN** the navigation is rendered
- **THEN** an "External" navigation link (translated; "Externas" in Spanish) pointing to `/external-charges` is visible
- **AND** anonymous users do not see this link

#### Scenario: The external charges create form follows a vehicle switch

- **GIVEN** a signed-in user with two or more registered vehicles on the External charges page,
  vehicle `V1` selected, and `V1` has a latest telemetry snapshot at `73%`
- **WHEN** the user switches the sidebar vehicle selector to vehicle `V2`, whose latest
  telemetry snapshot is at `58%` and which has no entries yet
- **THEN** the `#external-charges-content` region re-fetches `GET /ui/external-charges` on the
  `vehicle-changed from:body` event (no full page reload)
- **AND** the create form re-renders with `V2` as the implicit vehicle (no vehicle field), the
  `start_battery_pct` suggestion reflecting `58%`, and today's date pre-filled in `started_at`
  / `ended_at`
- **AND** the entries list re-renders filtered to `V2` and the default date-filter window
  (empty in this case)

---

### Requirement: Charge List Fragment

The gateway SHALL serve the charge entries list as an htmx-swappable fragment at
`GET /ui/external-charges/list`, returning only the fragment HTML and not the surrounding page shell.
The fragment SHALL include per-entry derived values (cost per kWh, battery delta, battery
range, session duration) pre-computed by the handler from the `charging.Entry` value-receiver
methods and the entry's own start/end battery percentages. Every value the handler cannot
compute for a given entry (no cost per kWh, no battery delta, no battery range, no duration)
SHALL render the em-dash `—` placeholder, never a blank cell.

**CHANGE from the prior revision:** `GET /ui/external-charges/list` now accepts optional
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

- **GIVEN** the External charges page is open in the browser
- **WHEN** htmx issues `GET /ui/external-charges/list` with no `start`/`end` parameters
- **THEN** the gateway returns only the external-charges-list fragment HTML, filtered to the last 7
  calendar days (inclusive, ending today in the browser's local timezone)
- **AND** the surrounding page shell is not included in the response
- **AND** the list is ordered newest-first by charge date

#### Scenario: A valid explicit window is honored exactly as given

- **GIVEN** the External charges page is open in the browser
- **WHEN** htmx issues `GET /ui/external-charges/list?start=2026-08-01&end=2026-08-31`
- **THEN** the gateway returns the fragment filtered to exactly that window
- **AND** entries whose `charged_on` falls outside `[2026-08-01, 2026-08-31]` are not shown
- **AND** entries whose `charged_on` falls on either boundary date are shown (inclusive)

#### Scenario: A malformed window is rejected with 400 and no filter chrome

- **GIVEN** the External charges page is open in the browser
- **WHEN** htmx issues `GET /ui/external-charges/list` with a non-ISO `start` or `end`, only one of the
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
- **WHEN** the External charges page or list fragment is rendered
- **THEN** the gateway renders the page (or fragment) without a 500 or raw error string
- **AND** the date-filter preset selector is still shown
- **AND** the aggregation tiles are still shown, each at their zero/empty value
- **AND** a user-facing error message ("Could not load your entries") is shown in the list
  region
- **AND** the create form remains accessible

---

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

#### Scenario: A validation failure preserves every submitted value, not only the date defaults

- **GIVEN** a signed-in user submitting the create form with a valid `location_kind` of `WORK`,
  a status of `DONE`, valid battery percentages, non-empty `energy_added_kwh`, `price`,
  `charging_type`, `location_label`, and `notes`, but an out-of-range `end_battery_pct`
- **WHEN** the server re-renders the form at HTTP 422
- **THEN** every one of those submitted values — including the ones with no day-based default —
  is still present in the re-rendered form's inputs, not reset to blank or to a different default
- **AND** the invalid `end_battery_pct` value itself is also echoed back so the user can see and
  correct exactly what they typed

### Requirement: Delete Charge Entry

The gateway SHALL let a signed-in user delete an existing charge entry from the table. The
delete action SHALL require a CSRF token, enforce tenant ownership, and on success remove the
row from the table without a full page reload and without a browser `alert()`. On failure (a
CSRF mismatch, a cross-tenant id, or a `charging.Writer.Delete` error) the gateway SHALL NOT
surface a plain browser `alert()`.

**CHANGE from the prior revision:** because the entries table now sits alongside aggregation
tiles computed over the same rows (see the aggregation-tiles requirement below), a delete SHALL
refresh the WHOLE entries region (`#external-charges-list` — presets, tiles, and table together), not
only the deleted row, on both success AND failure, within the SAME `?start=&end=` window the
table was showing before the delete. This REPLACES the prior revision's row-scoped swap (an
empty `<tr>` on success, a row-level error message on failure): a row-only swap would leave the
tiles showing stale, pre-delete totals.

#### Scenario: Deleting an entry removes the row and refreshes the tiles, without an alert

- **GIVEN** a signed-in user viewing the charge list with at least one entry within the active
  window, and the page carries a valid CSRF token in the form/button
- **WHEN** the user confirms deletion (browser native confirm dialog) and the browser issues
  `DELETE /ui/external-charges/row/{id}` with the CSRF token and the active window's `?start=&end=`
- **THEN** the gateway validates the CSRF token
- **AND** validates that the entry belongs to the user's account
- **AND** calls `charging.Writer.Delete(ctx, accountID, id)`
- **AND** the whole `#external-charges-list` region is refreshed (via htmx `hx-swap="outerHTML"`) to
  reflect the deletion, within the SAME window the table was showing before the delete
- **AND** the aggregation tiles in the refreshed region no longer include the deleted entry's
  values
- **AND** no browser `alert()` is shown to the user
- **AND** the entry is no longer present in a subsequent list render

#### Scenario: Delete with a stale, missing, or wrong CSRF token is rejected, not alerted

- **GIVEN** a DELETE request to `/ui/external-charges/row/{id}` with a missing, wrong, or stale
  (older than the session's current `csrf_manualcharge` value) CSRF token
- **WHEN** the gateway processes it
- **THEN** the handler returns HTTP 403 (invalid csrf token)
- **AND** `charging.Writer.Delete` is NOT called
- **AND** the row is not removed from the table
- **AND** the delete button sends its CSRF token on the `X-CSRF-Token` request header
  (not the DELETE request body, which Go's `net/http` does not parse for a DELETE
  method), so a normally-loaded page's delete uses the live session token

#### Scenario: Delete of another tenant's entry is rejected

- **GIVEN** a DELETE request to `/ui/external-charges/row/{id}` where `id` belongs to a different
  user's account
- **WHEN** the gateway processes it
  (the `charging.Writer.Delete(ctx, accountID, id)` scopes the DELETE to the
  caller's account_id via its WHERE clause)
- **THEN** the delete silently finds no row (the Writer's scoped DELETE affects 0 rows)
- **AND** the gateway returns a 404 or empty response — no data is deleted

#### Scenario: Delete shows a region-level error message, not a generic alert, on failure

- **GIVEN** a signed-in user clicking Delete on one of their entries within the active window
- **WHEN** the `charging.Writer.Delete` call returns an error (e.g. a transient store failure)
- **THEN** the server returns a non-2xx response carrying the SAME `#external-charges-list` region,
  unchanged data, with an inline error message shown above the table
- **AND** the user sees the region-level error message (e.g. "Could not delete entry — please
  try again."), not a raw HTTP status string or a browser `alert()`
- **AND** the entry is not removed from the list
- **AND** the aggregation tiles are unchanged (the delete did not commit)

### Requirement: Tenant Isolation

The gateway SHALL ensure that a signed-in user can only view, create, update, and delete
their own manual charge entries. No action by one user SHALL expose or mutate another user's
entries.

#### Scenario: User only sees their own entries in the list

- **GIVEN** two distinct accounts, each with manual charge entries
- **WHEN** account A's user views the External charges page
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

### Requirement: location_kind Visible Without Expanding "More Details"

In the create form, `location_kind` SHALL be visible in the always-visible required fields
section, not inside the `<details>` expander. The always-visible section also SHALL include
`started_at` and `ended_at` (both optional, pre-filled with today's date by default — see
`Requirement: Create Charge Entry`) and `start_battery_pct` / `end_battery_pct` (both
required — see `Requirement: Create Charge Entry`). The "More details" expander SHALL retain
only the remaining optional fields (`charging_type`, `location_label`, `notes`).

#### Scenario: location_kind is always visible in the create form

- **GIVEN** the External charges create form
- **WHEN** it is rendered (before any user interaction with the expander)
- **THEN** the `location_kind` picker is visible without expanding "More details"
- **AND** the "More details" expander still exists and reveals the remaining optional fields

---

### Requirement: Authenticated Navigation Shell

The gateway SHALL render an authenticated navigation shell (drawer
sidebar) on every authenticated page via `layouts.BaseAuth`. The shell
SHALL include a vehicle header, a navigation list, and the language
selector. The shell SHALL NOT render a "Wake Vehicle" control. No user-initiated Tesla
API call is triggered by rendering the shell. Every static string in the shell (nav item
labels, the "Soon" placeholder badge, the sidebar open/close controls) SHALL be rendered
through the translation catalogue in the request's resolved language. The rendered page's
`<html lang>` attribute SHALL reflect the request's resolved language, not a fixed value.

Rationale (grill D7, D8, D9, D10; RM24 D5/D9; this tier's Discoveries #1): the header reuses
existing read ports; the "Wake Vehicle" button is dropped; nav items resolve their labels via
`i18n.T`; the language selector is inherited from `layouts.Base`, not re-authored per page; the
`<html lang>` attribute — previously hardcoded to `"en"` regardless of the resolved language — now
reflects it, so assistive technology and search engines see the correct declared language for a
Spanish-resolved render.

#### Scenario: Authenticated page renders the navigation shell

- **GIVEN** a signed-in user requesting any authenticated page (`/dashboard`,
  `/external-charges`)
- **WHEN** the page is rendered
- **THEN** the response HTML contains the drawer sidebar
- **AND** the sidebar contains a vehicle-header region and the navigation
  entries, each with a translated label
- **AND** the response does NOT contain a "Wake Vehicle" button

#### Scenario: The rendered page's declared language matches the resolved language

- **GIVEN** a signed-in user whose resolved language is `es`
- **WHEN** any page built on `layouts.Base` (or `layouts.BaseAuth`) is rendered
- **THEN** the response's `<html>` tag carries `lang="es"`
- **AND** when the resolved language is `en`, the same tag carries `lang="en"`

#### Scenario: No "Wake Vehicle" control is ever rendered

- **GIVEN** any authenticated page
- **WHEN** the navigation shell is rendered
- **THEN** no "Wake Vehicle" button, link, or form is present
- **AND** no user-initiated Tesla wake API call is triggered by rendering
  or interacting with the shell

#### Scenario: Navigation shell renders without calling Tesla

- **GIVEN** a signed-in user on any authenticated page
- **WHEN** the navigation shell is rendered
- **THEN** the gateway does NOT call the Tesla Fleet API adapter
- **AND** the vehicle header (when shown) is populated exclusively from
  the account and telemetry read ports

#### Scenario: Sidebar nav labels render in the resolved language

- **GIVEN** a signed-in user whose resolved language is `en`
- **WHEN** the navigation shell is rendered
- **THEN** the sidebar nav item labels render in English
- **AND** when the resolved language is `es`, the same labels render in Spanish

### Requirement: Navigation Items

The navigation shell SHALL render its entries with translated labels resolved through the
translation catalogue. Live entries SHALL link to existing pages; a placeholder entry SHALL NOT
navigate to a real page and SHALL be visually marked as "soon" (translated). Each entry SHALL
render an icon. The Settings entry SHALL be a live entry linking to `/settings`, not a
placeholder.

#### Scenario: Live navigation entries link to existing pages, with translated labels

- **GIVEN** a signed-in user viewing the navigation shell
- **WHEN** the navigation list is rendered
- **THEN** a "Dashboard" entry (translated per the resolved language) links to `/dashboard`
- **AND** an "External" entry (translated) links to `/external-charges`
- **AND** a "Settings" entry (translated) links to `/settings`
- **AND** all three entries are active-highlighted when the current request path matches their
  target

#### Scenario: Placeholder navigation entries are marked "soon", translated

- **GIVEN** a signed-in user viewing the navigation shell
- **WHEN** the navigation list is rendered
- **THEN** any REMAINING placeholder entry (e.g. Vehicle Stats, Community Benchmark) is rendered
  as a placeholder link visually marked with a translated "Soon"/"Pronto" badge
- **AND** no placeholder navigates to a page not built by this change
- **AND** the Settings entry carries no such badge, since it is no longer a placeholder

#### Scenario: Navigation entries render icons without an external CDN

- **GIVEN** the navigation shell rendered with icons
- **WHEN** the entries are rendered
- **THEN** each entry's icon is an inline SVG owned by the `ui/` kit
  (a `ui.Icon` wrapper)
- **AND** no icon is loaded from an external CDN (no Google Fonts
  Material Symbols stylesheet)
- **AND** no inline client-side JavaScript is required to render icons

### Requirement: Vehicle-Scoped Cross-Region Refresh

Switching the active vehicle in the context switcher SHALL refresh the current page's
per-vehicle regions in place — without a full page reload — so the visible data follows the
newly-selected vehicle. The mechanism SHALL be event-driven: `POST /ui/vehicle/select` emits an
`HX-Trigger: vehicle-changed` response header (which bubbles to `<body>`), and each per-vehicle
region subscribes with `hx-trigger="vehicle-changed from:body"` and re-fetches its own `/ui`
fragment. The acting switcher SHALL NOT be coupled to the refreshed regions' markup (no
out-of-band swap of another region); it only fires the event, and regions opt in independently.

Every per-vehicle read triggered by a refresh SHALL scope to the selected vehicle's `TeslaID`
(resolved via `resolveSelectedVehicle`), never defaulting to the first registered vehicle or to
all vehicles when a selection exists. The `TeslaID` (Tesla's numeric vehicle `id`) is the
vehicle identity passed to module read ports; the VIN is only a tenant-ownership check.

#### Scenario: Dashboard bento refreshes for the newly-selected vehicle

- **GIVEN** a signed-in user on `/dashboard` whose account has two or more registered vehicles
- **AND** the dashboard's `#dashboard-content` region subscribes with `hx-trigger="vehicle-changed from:body"` and `hx-get="/ui/dashboard"`
- **WHEN** the user switches the active vehicle and the `vehicle-changed` event fires
- **THEN** the region re-fetches `GET /ui/dashboard`, which renders only the `dashboard` fragment (not the full page shell) for the selected vehicle
- **AND** the response is swapped into the region's `innerHTML`, keeping the listening wrapper element in place
- **AND** the bento's vehicle name and telemetry reflect the selected vehicle's `TeslaID`

#### Scenario: Manual-records content refreshes for the newly-selected vehicle

- **GIVEN** a signed-in user on `/external-charges` whose account has two or more registered vehicles
- **AND** the `#external-charges-content` region subscribes with `hx-trigger="vehicle-changed from:body"` and `hx-get="/ui/external-charges"`
- **WHEN** the user switches the active vehicle and the `vehicle-changed` event fires
- **THEN** the region re-fetches `GET /ui/external-charges`, which renders the `external-charges-create-form` and `external-charges-list` fragments (not the full page shell) scoped to the selected vehicle's `TeslaID`
- **AND** the entry list shows the selected vehicle's entries and the re-rendered create form sources its vehicle from the newly-selected session vehicle, rendering no vehicle picker
- **AND** a fresh manual-charge CSRF token is issued for the re-rendered create form

#### Scenario: A refresh fragment is not served to anonymous callers

- **GIVEN** an unauthenticated request to a per-vehicle refresh route (`GET /ui/dashboard` or `GET /ui/external-charges`)
- **WHEN** the handler resolves the session
- **THEN** the request is redirected to `/login` and no per-vehicle data is served

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
window selector) — mirroring the `/external-charges` + `/ui/external-charges` pairing. Anonymous requests to
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

### Requirement: Navbar Language Selector

The gateway SHALL render a language selector — a globe icon plus the current language — composed
from the typed `templates/ui/` kit, visible on every page including the anonymous login page. The
selector SHALL NOT require client-side JavaScript.

#### Scenario: Selector is visible on the login page

- **GIVEN** an anonymous visitor
- **WHEN** they load `/login`
- **THEN** the response contains the language selector showing a globe icon and the current
  (resolved) language

#### Scenario: Selector is visible on every authenticated page

- **GIVEN** a signed-in user
- **WHEN** they load any authenticated page (`/dashboard`, `/external-charges`, `/supercharger-stats`)
- **THEN** the response contains the language selector

#### Scenario: Selector requires no client-side JavaScript

- **GIVEN** the rendered language selector
- **WHEN** its markup is inspected
- **THEN** it uses only CSS-driven DaisyUI patterns (no inline `<script>`, no JS event listener
  added to support it)

### Requirement: Full-Application Translation Coverage

The gateway SHALL resolve every user-facing string it renders — on every page (`/`, `/login`,
`/dashboard`, `/dashboard/history`, `/external-charges`, `/supercharger-stats`), every htmx fragment those
pages compose, every reusable `templates/ui/` component, and every handler-produced flash, notice,
or validation error message — through the translation catalogue in the request's resolved
language. No page or fragment SHALL render a hardcoded English (or Spanish-only) string for
content a signed-in or anonymous user reads, with the sole exception of: brand/proper nouns
(rendered identically in both languages via a real catalogue entry, not a raw literal), format
verbs and unit symbols embedded inside an otherwise-translated interpolated string, and responses
consumed exclusively by non-UI/ops tooling (e.g. `/healthz`).

#### Scenario: The dashboard page renders fully translated for a Spanish-resolved request

- **GIVEN** a signed-in user whose resolved language is `es`
- **WHEN** they load `/dashboard` with at least one registered vehicle and a stored snapshot
- **THEN** every static label on the page (status words, stat-tile labels, card titles, the
  battery card's "Range"/"Last updated" prefixes) renders in Spanish
- **AND** when the same user's resolved language is `en`, the same labels render in English

#### Scenario: The manual-charge log page and its forms render fully translated

- **GIVEN** a signed-in user whose resolved language is `en`
- **WHEN** they load `/external-charges`
- **THEN** the create form's field labels, the "Home"/"Work"/"Other" and "AC"/"DC" option text,
  the "Log charge" submit button, the entries table's "Your entries" heading and empty-state
  sentence, and each row's "Edit"/"Delete" actions all render in English
- **AND** opening a row's inline edit form (`GET /ui/external-charges/row/:id/edit`) renders that form's
  field labels and its "Save"/"Cancel" actions in the same resolved language

#### Scenario: The Supercharger Stats page renders fully translated

- **GIVEN** a signed-in user whose resolved language is `es`
- **WHEN** they load `/supercharger-stats` with at least one Supercharger session recorded
- **THEN** the month-preset selector, the four KPI tile labels, both chart/table card titles, and
  the sessions table's column headers render in Spanish

#### Scenario: A handler-produced validation error renders translated

- **GIVEN** a signed-in user submitting `POST /ui/external-charges/create` with a missing required field
- **WHEN** the handler rejects the submission
- **THEN** the returned validation message for that field renders in the request's resolved
  language, not hardcoded English

#### Scenario: A handler-produced degraded-state notice renders translated

- **GIVEN** a signed-in user whose account read or Tesla connection read fails
- **WHEN** the dashboard or the vehicles region renders its degraded notice
- **THEN** the notice text renders in the request's resolved language

#### Scenario: An interpolated string renders with the correct language's phrasing, not just substituted numbers

- **GIVEN** a signed-in user whose active vehicle's latest snapshot is 3 days old
- **WHEN** the navigation header renders the "Last seen" relative-time label
- **THEN** a Spanish-resolved request shows "hace 3 días" and an English-resolved request shows
  "3 days ago" — not a partially-translated mix, and not the same phrasing pluralized identically
  in both languages when the underlying grammar differs

#### Scenario: A brand or proper noun renders identically in both languages

- **GIVEN** any page containing the "Magus" wordmark or the "TESLA CORE" login-page brand text
- **WHEN** the page renders under either resolved language
- **THEN** the brand text renders unchanged (not blank, not a missing-key marker) in both `es` and
  `en`

### Requirement: Monetary Value Display Formatting

Every monetary value the gateway renders as a display label SHALL be formatted with a comma
(`,`) thousands separator and a period (`.`) decimal separator, with exactly two decimal
places, followed by a space and the currency code — e.g. `58000` COP renders as
`"58,000.00 COP"`. This format SHALL be produced by a single shared function
(`formatMoney(amount float64, currency string) string`,
`internal/gateway/handlers/format.go`) and SHALL NOT vary by the active display language (ES or
EN) — the separator convention is fixed regardless of locale, a deliberate exception to
locale-varying number formats elsewhere in the region.

Every gateway render site that displays a monetary amount alongside a currency code SHALL call
this shared function rather than building its own decimal-formatted string. This applies to,
at minimum: the Supercharger Stats session table's cost column, the Supercharger Stats KPI
tiles' per-currency cost lines, the External charges entry list's price column, and the External charges
entry list's cost-per-kWh column.

#### Scenario: Supercharger session cost is comma-grouped

- **GIVEN** a Supercharger session with `TotalCost = 58000` and `Currency = "COP"`
- **WHEN** the Supercharger Stats sessions table renders that session's row
- **THEN** the cost cell reads `"58,000.00 COP"`

#### Scenario: External charges price is comma-grouped

- **GIVEN** a manual charge entry with `Price = 12500` and `Currency = "COP"`
- **WHEN** the External charges entry list renders that entry's row
- **THEN** the price label reads `"12,500.00 COP"`

#### Scenario: External charges cost-per-kWh is comma-grouped and keeps its unit suffix

- **GIVEN** a manual charge entry whose computed cost per kWh is `1200` in currency `COP`
- **WHEN** the External charges entry list renders that entry's cost-per-kWh label
- **THEN** the label reads `"1,200.00 COP/kWh"` — the comma-grouped, two-decimal money format
  with the `/kWh` unit suffix appended after it

#### Scenario: Zero, negative, and sub-thousand amounts format correctly

- **GIVEN** monetary amounts `0`, `-1500.5`, `999`, and `1000`, all in currency `COP`
- **WHEN** each is formatted by the shared money formatter
- **THEN** they render respectively as `"0.00 COP"`, `"-1,500.50 COP"`, `"999.00 COP"`, and
  `"1,000.00 COP"`

#### Scenario: The formatted currency label does not vary by active display language

- **GIVEN** the same monetary amount and currency rendered once with the active display
  language set to Spanish (ES) and once set to English (EN)
- **WHEN** the monetary label is rendered in each case
- **THEN** both renders produce the identical `1,234.56 CUR`-shaped string — the comma/period
  separator convention does not change between ES and EN

#### Scenario: Machine-parseable raw form values are NOT comma-grouped

- **GIVEN** the inline charge-entry edit form's `energy_added_kwh` and `price` number inputs
- **WHEN** their `value` attributes are populated from a stored entry's `EnergyAddedKWh` and
  `Price` fields
- **THEN** those values are formatted as a plain, two-decimal machine-parseable decimal string
  with NO thousands separator (e.g. `"1200.50"`, never `"1,200.50"`)
- **AND** the shared comma-grouped money formatter (`formatMoney`) is NEVER used to build these
  two values
- **AND** submitting the edit form with these unmodified values round-trips successfully (the
  browser's native number input parses the value without error)

### Requirement: Charge List Date Filter

The External charges page's entries list SHALL offer a date-filter preset selector with exactly two
presets: "Last 7 days" (the default) and "This month" (the full calendar month containing
today — the 1st through the last day of the month, not merely the days elapsed so far). Both
presets SHALL be computed against the requesting browser's local calendar day, never UTC.
Selecting a preset SHALL re-fetch and re-render only the `#external-charges-list` region (presets,
tiles, and table together) without a full page reload or a change to the create form's
displayed values.

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
- **AND** the "This month" preset button is shown in its active state and "Last 7 days" is not

#### Scenario: Selecting a preset refreshes only the entries region

- **GIVEN** a signed-in user on the External charges page with the create form partially filled in
- **WHEN** they select a different date-filter preset
- **THEN** only the `#external-charges-list` region (presets, tiles, table) is re-fetched and
  re-rendered
- **AND** the create form's own fields and any values the user had already typed into it are
  left untouched

---

### Requirement: Charge List Aggregation Tiles

The External charges page's entries list SHALL show four aggregation tiles — Sessions (count of
entries in the active window), Energy (sum of `energy_added_kwh` across entries where it is
non-nil), Cost (sum of `price` across entries in the window — a single total, since manual
entries are always recorded in COP), and Avg kWh per session (Energy divided by the count of
entries with a non-nil energy value, guarded against division by zero) — computed by the
handler from the SAME result set the table below them renders, so the tiles and the table can
never diverge.

#### Scenario: Tiles reflect the same entries the table shows

- **GIVEN** a signed-in user with several charge entries inside the active window, some with
  a nil energy value
- **WHEN** the External charges page is rendered
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
- **WHEN** the External charges page is rendered
- **THEN** the Sessions, Energy, and Cost tiles are still shown, each at `0` (or its
  zero-formatted equivalent)
- **AND** the Avg kWh per session tile shows the em-dash `—` placeholder (division-guarded),
  not a division error or a fabricated number
- **AND** the entries table shows its empty-state message in place of rows

---

