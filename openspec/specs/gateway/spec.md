# gateway Specification

## Purpose

The `gateway` capability is the web presentation layer — the single place HTML lives
(`ai/architecture.md` §2). It serves HTML pages and htmx fragments over HTTP, runs cookie-based
sessions, and serves its static assets from the binary. It is a translator: it calls domain
module interfaces in-process and renders their clean structs into Templ HTML; it holds no domain
state and hosts all `/ui` routes.
## Requirements
### Requirement: Web Landing Page
The gateway SHALL serve an HTML landing page at the web root, rendered from a shared base layout
that loads the gateway's pinned htmx asset.

#### Scenario: Visitor opens the site root
- **GIVEN** the web gateway is running
- **WHEN** a browser requests `GET /`
- **THEN** the gateway responds with an HTML page built from the base layout
- **AND** the page references the gateway's pinned htmx asset under `/static`

### Requirement: Health Check Endpoint
The gateway SHALL expose a health endpoint that reports whether the service and its database are
reachable.

#### Scenario: Database reachable
- **GIVEN** the database is reachable
- **WHEN** `GET /healthz` is requested
- **THEN** the gateway responds with a 200 status indicating healthy

#### Scenario: Database unreachable
- **GIVEN** the database cannot be reached
- **WHEN** `GET /healthz` is requested
- **THEN** the gateway responds with a non-200 status indicating unhealthy

### Requirement: htmx Fragment Rendering
The gateway SHALL serve htmx-targeted updates as HTML fragments — not full pages — from its `/ui`
routes.

#### Scenario: htmx requests a partial update
- **GIVEN** the landing page contains an htmx-driven region
- **WHEN** htmx issues a `GET` to that region's `/ui` route
- **THEN** the gateway returns only that fragment's HTML
- **AND** the surrounding page shell is not included in the response

### Requirement: Cookie-Based Session Foundation
The gateway SHALL run signed, encrypted cookie-based session middleware on all routes, persisting
per-visitor state in the browser cookie with no server-side session storage.

#### Scenario: Session value persists across requests and restarts
- **GIVEN** the session middleware is active
- **WHEN** a handler stores a value in the session and saves it
- **THEN** the value is returned to the browser in a signed, encrypted cookie
- **AND** it is available to handlers on the visitor's subsequent requests
- **AND** it remains valid after the server restarts, because no session state is held server-side

### Requirement: Embedded Pinned Static Assets
The gateway SHALL serve its static assets — including a pinned htmx — from files embedded in the
binary, under a `/static` path.

#### Scenario: Fetching the htmx asset
- **GIVEN** the running web gateway binary
- **WHEN** a browser requests the pinned htmx asset under `/static`
- **THEN** the gateway serves it from the embedded filesystem
- **AND** no external or CDN request is required

### Requirement: Google Sign-In
The gateway SHALL let a visitor sign in with Google. On a successful callback it SHALL provision
or resolve the account for the Google identity (through the account module) and establish an
authenticated session for that account.

#### Scenario: Starting the login flow
- **GIVEN** an anonymous visitor
- **WHEN** they begin Google login
- **THEN** the gateway redirects them to Google's consent screen with a state parameter

#### Scenario: Successful callback provisions the account and signs in
- **GIVEN** a visitor returning from Google with a valid authorization code and matching state
- **WHEN** the gateway handles the callback
- **THEN** it resolves the Google identity (id, email, name)
- **AND** provisions or resolves the account via the account module
- **AND** establishes an authenticated session for that account

### Requirement: OAuth State Validation
The gateway SHALL generate a state value for each login attempt and SHALL reject a callback whose
state does not match the value stored for that visitor, establishing no session.

#### Scenario: Mismatched state is rejected
- **GIVEN** a login attempt that stored a state value
- **WHEN** the callback arrives with a missing or different state
- **THEN** the gateway rejects the login
- **AND** no authenticated session is established

### Requirement: Authenticated Session Reflection
The gateway SHALL reflect authentication state in the UI: a signed-in visitor sees their identity
and a way to log out; an anonymous visitor sees a way to sign in.

#### Scenario: Signed-in visitor sees their identity
- **GIVEN** a visitor with an authenticated session
- **WHEN** they load the landing page
- **THEN** the page shows their account email and a log-out control

#### Scenario: Anonymous visitor is offered sign-in
- **GIVEN** a visitor with no authenticated session
- **WHEN** they load the landing page
- **THEN** the page offers a way to sign in with Google

### Requirement: Logout
The gateway SHALL let a signed-in visitor log out, clearing their session so subsequent requests
are anonymous.

#### Scenario: Logging out clears the session
- **GIVEN** a signed-in visitor
- **WHEN** they log out
- **THEN** the gateway clears their session
- **AND** subsequent requests are treated as anonymous

### Requirement: Tesla Account Connection
The gateway SHALL let a signed-in user connect a Tesla account through Tesla OAuth, and on a
successful callback SHALL store the connection's tokens (access token, refresh token, and access
expiry) via the account module.

#### Scenario: Starting the Tesla connect flow
- **GIVEN** a signed-in user
- **WHEN** they begin connecting their Tesla
- **THEN** the gateway redirects them to Tesla's authorization page with a state parameter

#### Scenario: Successful callback stores the tokens
- **GIVEN** a signed-in user returning from Tesla with a valid code and matching state
- **WHEN** the gateway handles the callback
- **THEN** it exchanges the code for Tesla tokens
- **AND** stores the tokens for the user's account via the account module

### Requirement: Tesla Connect Requires Authentication
The gateway SHALL require an authenticated session to begin or complete a Tesla connection; an
anonymous visitor SHALL be directed to sign in instead.

#### Scenario: Anonymous visitor cannot connect
- **GIVEN** a visitor with no authenticated session
- **WHEN** they request the Tesla connect route
- **THEN** the gateway redirects them to sign in
- **AND** does not start the Tesla OAuth flow

### Requirement: Tesla Connect State Validation
The gateway SHALL validate a CSRF state on the Tesla callback and SHALL reject a mismatch without
storing any tokens.

#### Scenario: Mismatched state is rejected
- **GIVEN** a Tesla connect attempt that stored a state value
- **WHEN** the callback arrives with a missing or different state
- **THEN** the gateway rejects the callback
- **AND** no tokens are stored

### Requirement: Vehicle Dashboard
The gateway SHALL show a signed-in user the Tesla vehicles registered to their account, reading
them through the account module's public interface (never directly from the registry table). It
SHALL render the vehicles as an htmx page and a refreshable fragment, mapping the account module's
clean vehicle data to a presentation model that shows each vehicle's `display_name` and `vin`.
The gateway SHALL request a Tesla `ListVehicles` call through the account module — which persists
the result into the per-account registry — only when the account has no vehicles registered.
Thereafter the dashboard SHALL read the registered vehicles from the account module and SHALL NOT
call Tesla. The presentation model SHALL NOT surface the volatile live vehicle `state`
(online/asleep); any additional per-vehicle telemetry shown on a card comes solely from stored
snapshots through the telemetry read port (see "Dashboard renders enriched vehicle cards from
stored telemetry"), never from a live Tesla call.

#### Scenario: Account with registered vehicles shows them without calling Tesla
- **GIVEN** a signed-in user whose account has working Tesla vehicles already registered
- **WHEN** they open the dashboard
- **THEN** the gateway reads the account's registered vehicles through the account module
- **AND** lists each vehicle with its display name and VIN
- **AND** it does not call the Tesla adapter

#### Scenario: Account with no registered vehicles triggers a one-time Tesla list and registration
- **GIVEN** a signed-in user whose account has a working Tesla connection but no vehicles registered
- **WHEN** they open the dashboard
- **THEN** the gateway obtains the account's current Tesla access token
- **AND** calls `tesla.ListVehicles`
- **AND** persists the returned vehicles through the account module as a one-time registration
- **AND** displays those vehicles' display names and VINs

#### Scenario: Vehicles are shown without the volatile state field
- **GIVEN** a signed-in user with vehicles registered
- **WHEN** the dashboard renders the vehicle list
- **THEN** each rendered vehicle shows its display name and VIN
- **AND** no online/asleep state value is shown for any vehicle

#### Scenario: Refreshing the vehicle list returns only the fragment
- **GIVEN** the dashboard page is open
- **WHEN** htmx requests the vehicles refresh route
- **THEN** the gateway returns only the vehicle-list fragment
- **AND** not the surrounding page shell
- **AND** when the account already has registered vehicles it does not call Tesla

#### Scenario: No Tesla connection prompts to connect instead of listing
- **GIVEN** a signed-in user whose account has no Tesla connection and no registered vehicles
- **WHEN** they open the dashboard
- **THEN** the gateway shows the Tesla connect prompt
- **AND** it does not call `tesla.ListVehicles`

### Requirement: Dashboard Requires Authentication
The gateway SHALL require an authenticated session to view the dashboard or its vehicle fragment;
an anonymous visitor SHALL be directed to sign in.

#### Scenario: Anonymous visitor cannot view the dashboard
- **GIVEN** a visitor with no authenticated session
- **WHEN** they request the dashboard
- **THEN** the gateway redirects them to sign in

### Requirement: Dashboard Connect and Reconnect States
The gateway SHALL show a connect prompt when the signed-in user has no Tesla connection, and a
reconnect prompt when the stored credentials are expired or invalid, rather than a blank list or a
raw error.

#### Scenario: No Tesla connection prompts to connect
- **GIVEN** a signed-in user whose account has no Tesla connection
- **WHEN** they open the dashboard
- **THEN** the gateway shows a prompt to connect their Tesla

#### Scenario: Expired credentials prompt to reconnect
- **GIVEN** a signed-in user whose stored Tesla credentials are expired or invalid
- **WHEN** the tesla adapter rejects the request as unauthorized
- **THEN** the gateway shows a prompt to reconnect their Tesla
- **AND** does not leak a raw error to the page

### Requirement: Dashboard renders enriched vehicle cards from stored telemetry

The gateway dashboard (`/dashboard` and `/ui/vehicles`) SHALL enrich each registered
vehicle card with the vehicle's latest stored nightly telemetry snapshot. The data
SHALL come exclusively from the `telemetry.Reader` port — no live Tesla Fleet API call
on a normal dashboard render. Every registered vehicle SHALL appear on the dashboard
regardless of whether a snapshot exists.

The Extended field set displayed per enriched vehicle card is: battery % (integer),
range in kilometres (float, via `BatteryRangeKm()` companion), charging state (string),
odometer in kilometres (float, via `OdometerKm()` companion), inside temperature in °C
(float), outside temperature in °C (float), locked status (boolean), sentry mode
(three-state: nil / off / on), and last-updated timestamp (`CapturedAt`).

---

#### Scenario: Enriched vehicle card is shown when a snapshot exists

- **GIVEN** a signed-in user whose account has at least one registered vehicle
- **AND** the telemetry module has a stored snapshot for that vehicle
- **WHEN** the user's dashboard is rendered (either full-page `GET /dashboard` or
  htmx fragment `GET /ui/vehicles`)
- **THEN** the vehicle card displays:
  - Battery level as an integer percentage
  - Battery range in kilometres (converted from the stored miles value via the
    `BatteryRangeKm()` companion method)
  - Charging state as a string (e.g. "Charging", "Disconnected")
  - Odometer in kilometres (converted from the stored miles value via the
    `OdometerKm()` companion method)
  - Inside temperature in degrees Celsius (displayed as stored — no conversion)
  - Outside temperature in degrees Celsius (displayed as stored — no conversion)
  - Locked status (shown as locked / unlocked)
  - Sentry mode with three distinct visual states: nil ("not reported"), false ("off"),
    true ("on")
  - Last-updated label showing when the snapshot was captured (`CapturedAt`)
- **AND** no live Tesla Fleet API call is triggered to render the card

---

#### Scenario: Placeholder card is shown for a vehicle with no snapshot yet

- **GIVEN** a signed-in user whose account has a registered vehicle
- **AND** no snapshot has been stored for that vehicle (e.g. the nightly poller has
  not yet run since the vehicle was registered)
- **WHEN** the dashboard is rendered
- **THEN** the vehicle card is still shown (the vehicle is not hidden or omitted)
- **AND** the card displays the vehicle's identity information (display name, VIN)
- **AND** the card shows a placeholder message indicating that no telemetry data is
  available yet (e.g. "No data yet — awaiting first nightly snapshot")
- **AND** no stale marker is shown on a no-snapshot card

---

#### Scenario: Last-updated label is always visible on an enriched card

- **GIVEN** a signed-in user whose account has a registered vehicle with a stored
  snapshot
- **WHEN** the dashboard is rendered
- **THEN** the vehicle card always shows a human-readable "last updated" label derived
  from the snapshot's `CapturedAt` timestamp
- **AND** the label is visible regardless of whether the snapshot is fresh or stale

---

#### Scenario: Stale marker appears when the snapshot is older than the staleness threshold

- **GIVEN** a signed-in user whose account has a registered vehicle
- **AND** the vehicle's latest stored snapshot has a `CapturedAt` value that is more
  than 36 hours before the time of the current request (the staleness threshold — one
  full missed nightly poll cycle, 24 h + 12 h buffer)
- **WHEN** the dashboard is rendered
- **THEN** the vehicle card shows a clearly visible stale marker (e.g. a "stale" badge
  or highlighted warning label) in addition to the normal last-updated label
- **AND** the staleness threshold is controlled by a named constant in the handler code
  (not a magic number)

---

#### Scenario: No stale marker when the snapshot is within the staleness threshold

- **GIVEN** a signed-in user whose account has a registered vehicle
- **AND** the vehicle's latest stored snapshot has a `CapturedAt` value within the
  last 36 hours
- **WHEN** the dashboard is rendered
- **THEN** no stale marker is shown — only the normal last-updated label

---

#### Scenario: Sentry mode nil is rendered distinctly from false and true

- **GIVEN** a vehicle card with a snapshot where sentry mode was not reported by the
  vehicle at capture time (the `SentryMode` field on the snapshot is nil)
- **WHEN** the dashboard renders that vehicle card
- **THEN** the sentry mode display state is "not reported" (or equivalent placeholder)
- **AND** this visual state is distinct from "off" (false) and "on" (true)
- **AND** no boolean assumption is made — nil is not treated as false

---

#### Scenario: Telemetry reader failure degrades gracefully

- **GIVEN** a signed-in user whose account has registered vehicles
- **AND** the telemetry `Reader.LatestSnapshotsByAccount` call returns an error
- **WHEN** the dashboard is rendered
- **THEN** the dashboard still renders successfully — it does NOT return an error page
  or a 500 response
- **AND** each registered vehicle is shown with its identity information (display name,
  VIN) as if it had no snapshot
- **AND** a non-fatal notice is shown in the vehicles region indicating that telemetry
  data is temporarily unavailable (e.g. "Telemetry unavailable — showing vehicle
  identity only")
- **AND** the user can still see all their registered vehicles

---

#### Scenario: First-connect seed is retained unchanged

- **GIVEN** a signed-in user who has just connected their Tesla account and has zero
  registered vehicles
- **WHEN** the dashboard is first rendered
- **THEN** the gateway performs the one-time seed call (`account.SeedVehicles`)
  to populate the account registry from the Tesla Fleet API
- **AND** this first-connect seed behavior is unchanged from the pre-change behavior
- **AND** after seeding, if no snapshot exists yet, each vehicle card shows the
  placeholder ("no data yet") state

---

#### Scenario: Templates contain no business logic

- **GIVEN** any Templ template in `internal/gateway/templates/`
- **WHEN** it renders a vehicle card (enriched or placeholder)
- **THEN** all km conversion, staleness computation, sentry-nil state, and timestamp
  formatting have already been computed by the Go handler before the template receives
  the view model
- **AND** the template uses only presentation logic (if/for/display) — no arithmetic,
  no method calls on domain types, no time calculations

---

#### Scenario: Gateway never imports telemetrydb

- **GIVEN** the gateway handler that reads telemetry snapshot data
- **WHEN** it obtains snapshot data
- **THEN** it does so exclusively through the `telemetry.Reader` public interface
- **AND** it imports no package from `internal/telemetry/db` (`telemetrydb`)
- **AND** no `pgtype` type appears in any gateway file

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

### Requirement: Authenticated Navigation Shell

The gateway SHALL render an authenticated navigation shell (drawer
sidebar) on every authenticated page via `layouts.BaseAuth`. The shell
SHALL include a vehicle header and a four-item navigation list. The
shell SHALL NOT render a "Wake Vehicle" control. No user-initiated Tesla
API call is triggered by rendering the shell.

Rationale (grill D7, D8, D9, D10): the header reuses existing read ports;
the "Wake Vehicle" button is dropped; two nav items are live pages and
two are placeholders for future work.

#### Scenario: Authenticated page renders the navigation shell

- **GIVEN** a signed-in user requesting any authenticated page (`/dashboard`,
  `/charges`)
- **WHEN** the page is rendered
- **THEN** the response HTML contains the drawer sidebar
- **AND** the sidebar contains a vehicle-header region and the four
  navigation entries (Dashboard, Manual Records, Supercharger Stats,
  Settings)
- **AND** the response does NOT contain a "Wake Vehicle" button

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

---

### Requirement: Navigation Vehicle Header

The gateway SHALL render a vehicle header in the navigation shell that
shows the primary vehicle's display name, battery level, and a
freshness-derived status. The data SHALL come exclusively from existing
read ports: `account.RegisteredVehicles` (vehicle name) and
`telemetry.Reader.LatestSnapshotsByAccount` (battery level +
`CapturedAt`). The status SHALL be derived from the snapshot's
freshness, not from any live Tesla call.

The primary vehicle is the first entry from `account.RegisteredVehicles`
for the signed-in account. A multi-vehicle selector is out of scope.

#### Scenario: Vehicle header shows "Connected" when a fresh snapshot exists

- **GIVEN** a signed-in user whose account has at least one registered
  vehicle
- **AND** the latest stored snapshot for the primary vehicle has a
  `CapturedAt` within the connected-freshness window (48 hours of the
  current request time)
- **WHEN** the navigation header is rendered
- **THEN** the header shows the vehicle's display name
- **AND** shows a "Connected" status with a success-colored status dot
- **AND** shows the battery level as an integer percentage
- **AND** the freshness window is controlled by a named constant in the
  handler code (not a magic number)

#### Scenario: Vehicle header shows "Asleep / Last seen" when the snapshot is stale

- **GIVEN** a signed-in user whose primary vehicle's latest stored
  snapshot has a `CapturedAt` older than the connected-freshness window
- **WHEN** the navigation header is rendered
- **THEN** the header shows the vehicle's display name
- **AND** shows an "Asleep" (or equivalent) status with a
  warning-colored status dot
- **AND** shows a relative "Last seen" label (e.g. "2 days ago")
  pre-computed by the handler
- **AND** the template performs no time arithmetic

#### Scenario: Vehicle header degrades when no snapshot exists

- **GIVEN** a signed-in user whose account has a registered primary
  vehicle but no stored snapshot for it
- **WHEN** the navigation header is rendered
- **THEN** the header renders without a 500 or raw error
- **AND** shows the vehicle's display name with an "awaiting first
  snapshot" status and a neutral status dot
- **AND** no battery percentage is shown

#### Scenario: Vehicle header degrades when no vehicle is registered

- **GIVEN** a signed-in user whose account has no registered vehicles
- **WHEN** the navigation header is rendered
- **THEN** the header renders an "awaiting connect" state (no status dot,
  no battery percentage)
- **AND** offers a link to `/connect/tesla`
- **AND** no vehicle name is shown

#### Scenario: Navigation header read failures degrade gracefully

- **GIVEN** a signed-in user whose account read or telemetry read returns
  an error
- **WHEN** the navigation header is rendered
- **THEN** the header still renders (no 500, no raw error string)
- **AND** shows a degraded state (e.g. "unavailable") with a neutral dot

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
  relative "last seen" label, and the primary-vehicle name have all been
  computed by the Go handler before the template receives the view model
- **AND** the template uses only presentation logic (if/for/display) — no
  arithmetic, no time calculations, no method calls on domain types

#### Scenario: Navigation header never imports telemetrydb or accountdb

- **GIVEN** the gateway handler that builds the navigation header
- **WHEN** it obtains vehicle and snapshot data
- **THEN** it does so exclusively through the `account.Service` and
  `telemetry.Reader` public interfaces
- **AND** it imports no package from `internal/telemetry/db` or
  `internal/account/db`
- **AND** no `pgtype` type appears in any gateway file involved

#### Scenario: Status dot uses semantic tokens

- **GIVEN** the navigation header rendered with any status
- **WHEN** the status dot is rendered
- **THEN** its color is expressed via DaisyUI semantic tokens (e.g.
  `badge-success`, `badge-warning`, `badge-ghost`)
- **AND** no hardcoded hex color appears for the status dot

---

### Requirement: Navigation Items

The navigation shell SHALL render four navigation entries: Dashboard and
Manual Records as live links; Supercharger Stats and Settings as
placeholder ("soon") links. Each entry SHALL render an icon. The
placeholder entries SHALL NOT navigate to a real page in this change and
SHALL be visually marked as "soon".

#### Scenario: Live navigation entries link to existing pages

- **GIVEN** a signed-in user viewing the navigation shell
- **WHEN** the navigation list is rendered
- **THEN** a "Dashboard" entry links to `/dashboard`
- **AND** a "Manual Records" entry links to `/charges`
- **AND** both entries are active-highlighted when the current request
  path matches their target

#### Scenario: Placeholder navigation entries are marked "soon"

- **GIVEN** a signed-in user viewing the navigation shell
- **WHEN** the navigation list is rendered
- **THEN** a "Supercharger Stats" entry is rendered as a placeholder link
  visually marked "soon"
- **AND** a "Settings" entry is rendered as a placeholder link visually
  marked "soon"
- **AND** neither placeholders navigate to a built page (this change
  introduces no Supercharger Stats or Settings page)

#### Scenario: Navigation entries render icons without an external CDN

- **GIVEN** the navigation shell rendered with icons
- **WHEN** the entries are rendered
- **THEN** each entry's icon is an inline SVG owned by the `ui/` kit
  (a `ui.Icon` wrapper)
- **AND** no icon is loaded from an external CDN (no Google Fonts
  Material Symbols stylesheet)
- **AND** no inline client-side JavaScript is required to render icons

---

### Requirement: Home Page Debug Cleanup

The gateway SHALL remove the debug artifacts from the landing page: the
"Visits this session" counter and the "Check database" htmx health
fragment demo. The authenticated/anonymous states (signed-in email,
"View your vehicles", "Connect your Tesla", "Log out", sign-in link) SHALL
remain. The dead `/ui/health` route, its handler, the `fragments.Health`
template, and tests covering them SHALL be removed (they exist only to
serve the now-removed demo).

#### Scenario: Landing page no longer shows the visit counter

- **GIVEN** any visitor (anonymous or signed-in) loading `GET /`
- **WHEN** the landing page is rendered
- **THEN** the page does NOT contain a "Visits this session" line
- **AND** the handler does not read or increment a `visits` session value

#### Scenario: Landing page no longer shows the health fragment demo

- **GIVEN** any visitor loading `GET /`
- **WHEN** the landing page is rendered
- **THEN** the page does NOT contain a "Check database" button
- **AND** the page does NOT contain an htmx-swappable `health` fragment
  region

#### Scenario: Dead health route is removed

- **GIVEN** the gateway after this change
- **WHEN** a request is made to `GET /ui/health`
- **THEN** the route is not registered (404 from the router)
- **AND** no `HealthFragment` handler or `health()` helper exists in the
  handlers package
- **AND** no `fragments.Health` / `fragments.HealthCard` template exists

#### Scenario: Landing page keeps authenticated/anonymous state

- **GIVEN** a signed-in visitor loading `GET /`
- **WHEN** the landing page is rendered
- **THEN** the page shows their email and a "Log out" control and a
  "View your vehicles" link
- **AND** a "Connect your Tesla" link is shown
- **GIVEN** an anonymous visitor loading `GET /`
- **WHEN** the landing page is rendered
- **THEN** the page offers a way to sign in with Google

