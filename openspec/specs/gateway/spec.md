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
authenticated session for that account. When the callback request carries an explicitly-present
`lang` cookie, the gateway SHALL persist that language to the resolved account via
`account.Service.SetLanguage` so a language chosen before authentication carries into the signed-in
session.

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

#### Scenario: A pre-login language cookie carries into the account

- **GIVEN** a visitor who selected `en` on the login page (setting a `lang=en` cookie) before
  authenticating
- **WHEN** the Google callback establishes their session
- **THEN** the gateway calls `account.Service.SetLanguage` with `"en"` for the resolved account

#### Scenario: A callback with no language cookie leaves the account's language untouched

- **GIVEN** a visitor whose callback request carries no `lang` cookie
- **WHEN** the Google callback establishes their session
- **THEN** the gateway does NOT call `account.Service.SetLanguage`

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
range in kilometres (float), charging state (string), odometer in kilometres (float),
inside temperature in °C (float), outside temperature in °C (float), locked status
(boolean), sentry mode (three-state: nil / off / on), and last-updated timestamp
(`CapturedAt`). The gateway SHALL read every one of these values in its display unit as
the telemetry read port provides it, and SHALL NOT perform any unit conversion of its
own.

---

#### Scenario: Enriched vehicle card is shown when a snapshot exists

- **GIVEN** a signed-in user whose account has at least one registered vehicle
- **AND** the telemetry module has a stored snapshot for that vehicle
- **WHEN** the user's dashboard is rendered (either full-page `GET /dashboard` or
  htmx fragment `GET /ui/vehicles`)
- **THEN** the vehicle card displays:
  - Battery level as an integer percentage
  - Battery range in kilometres (read directly from the snapshot, already stored in
    kilometres — no conversion in the gateway)
  - Charging state as a string (e.g. "Charging", "Disconnected")
  - Odometer in kilometres (read directly from the snapshot, already stored in
    kilometres — no conversion in the gateway)
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
- **THEN** number formatting (rounding and thousands separators), staleness computation,
  sentry-nil state, and timestamp formatting have already been computed by the Go handler
  before the template receives the view model
- **AND** no unit conversion happens anywhere in the render path — neither in the template
  nor in the handler, because the snapshot already carries display units
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
session. The page SHALL be vehicle-scoped: the entry list and the create form SHALL follow the
session-selected vehicle (the sidebar switcher), via the `vehicle-changed` event, exactly like
the dashboard's per-vehicle reads. The create form SHALL NOT render a vehicle picker of its
own.

#### Scenario: Signed-in user opens the Charge log page

- **GIVEN** a signed-in user with at least one registered Tesla vehicle
- **WHEN** they navigate to `GET /charges`
- **THEN** the gateway renders the full Charge log page
- **AND** the page shows a list of their manual charge entries (if any exist), ordered newest
  first by charge date, scoped to the session-selected vehicle
- **AND** the page shows a "Log a charge" create form with required fields visible
- **AND** the create form has no vehicle picker — it operates on the session-selected vehicle
- **AND** no entry or vehicle belonging to another user is present on the page

#### Scenario: Anonymous visitor is redirected from the Charge log page

- **GIVEN** a visitor with no authenticated session
- **WHEN** they request `GET /charges` or any `/ui/charges*` fragment route
- **THEN** the gateway redirects them to `/login`
- **AND** no charge entries, no create form, and no telemetry-sourced suggestion are shown

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
- **AND** the entries list re-renders filtered to `V2` (empty in this case)

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
form via `POST /ui/charges/create`. The form SHALL NOT include a vehicle field — the handler
SHALL derive the target vehicle from the session-selected vehicle (the sidebar switcher) and
enforce tenant ownership of that resolved vehicle before writing. The handler SHALL require a
valid CSRF token and call `manualcharge.Writer.Create` on success. `location_kind` is a
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
row from the table without a full page reload and without a browser `alert()`. On failure (a
CSRF mismatch, a cross-tenant id, or a `manualcharge.Writer.Delete` error) the gateway SHALL
NOT surface a plain browser `alert()`; a Writer error SHALL render a row-level error message
in place of the row.

#### Scenario: Deleting an entry removes the row without an alert

- **GIVEN** a signed-in user viewing the charge list with at least one entry
- **AND** the page carries a valid CSRF token in the form/button
- **WHEN** the user confirms deletion (browser native confirm dialog)
  and the browser issues `DELETE /ui/charges/row/{id}` with the CSRF token
- **THEN** the gateway validates the CSRF token
- **AND** validates that the entry belongs to the user's account
- **AND** calls `manualcharge.Writer.Delete(ctx, accountID, id)`
- **AND** the row is removed from the table (swapped for an empty element via htmx
  `hx-swap="outerHTML"`)
- **AND** no browser `alert()` is shown to the user
- **AND** the entry is no longer present in a subsequent list render

#### Scenario: Delete with a stale, missing, or wrong CSRF token is rejected, not alerted

- **GIVEN** a DELETE request to `/ui/charges/row/{id}` with a missing, wrong, or stale
  (older than the session's current `csrf_manualcharge` value) CSRF token
- **WHEN** the gateway processes it
- **THEN** the handler returns HTTP 403 (invalid csrf token)
- **AND** `manualcharge.Writer.Delete` is NOT called
- **AND** the row is not removed from the table
- **AND** the delete button sends its CSRF token on the `X-CSRF-Token` request header
  (not the DELETE request body, which Go's `net/http` does not parse for a DELETE
  method), so a normally-loaded page's delete uses the live session token

#### Scenario: Delete of another tenant's entry is rejected

- **GIVEN** a DELETE request to `/ui/charges/row/{id}` where `id` belongs to a different
  user's account
- **WHEN** the gateway processes it
  (the `manualcharge.Writer.Delete(ctx, accountID, id)` scopes the DELETE to the
  caller's account_id via its WHERE clause)
- **THEN** the delete silently finds no row (the Writer's scoped DELETE affects 0 rows)
- **AND** the gateway returns a 404 or empty response — no data is deleted

#### Scenario: Delete shows a server-side error message, not a generic alert, on failure

- **GIVEN** a signed-in user clicking Delete on one of their entries
- **WHEN** the `manualcharge.Writer.Delete` call returns an error (e.g. a transient
  store failure)
- **THEN** the server returns a non-2xx response carrying an inline row-error
  fragment rendered inside the row
- **AND** the user sees the row-level error message (e.g. "Could not delete entry —
  please try again."), not a raw HTTP status string or a browser `alert()`
- **AND** the entry is not removed from the list

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
(`manualchargedb`) or any generated sqlc types. On the charges page, the gateway also reads
telemetry data (the `start_battery_pct` suggestion label) exclusively through
`telemetry.Reader.LatestSnapshotsByAccount` and SHALL NOT import `internal/telemetry/db`
(`telemetrydb`) for that purpose.

#### Scenario: Gateway only uses manualcharge public interfaces

- **GIVEN** any handler or helper in `internal/gateway/`
- **WHEN** it reads or writes manual charge entries
- **THEN** it does so exclusively via `manualcharge.Reader` or `manualcharge.Writer`
- **AND** no `manualchargedb` package is imported in any gateway file
- **AND** no `pgtype` type appears in any gateway handler, view model, or template

#### Scenario: Charges page never imports manualchargedb or telemetrydb, and makes no live Tesla call

- **GIVEN** the gateway handlers and helpers that build and validate the charges
  create form
- **WHEN** they obtain vehicle identity, telemetry for the battery-suggestion label,
  and persist or delete an entry
- **THEN** they do so exclusively through the `account`, `telemetry.Reader`, and
  `manualcharge.Reader`/`manualcharge.Writer` public interfaces
- **AND** they import no package from `internal/manualcharge/db` or
  `internal/telemetry/db`
- **AND** no `pgtype` type appears in any gateway file involved
- **AND** no live Tesla Fleet API call is made on any charges-page request

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
section, not inside the `<details>` expander. The always-visible section also SHALL include
`started_at` and `ended_at` (both optional, pre-filled with today's date by default — see
`Requirement: Create Charge Entry`) and `start_battery_pct` / `end_battery_pct` (both
required — see `Requirement: Create Charge Entry`). The "More details" expander SHALL retain
only the remaining optional fields (`charging_type`, `location_label`, `notes`).

#### Scenario: location_kind is always visible in the create form

- **GIVEN** the Charge log create form
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
through the translation catalogue in the request's resolved language.

Rationale (grill D7, D8, D9, D10; RM24 D5/D9): the header reuses existing read ports;
the "Wake Vehicle" button is dropped; nav items resolve their labels via `i18n.T`; the
language selector is inherited from `layouts.Base`, not re-authored per page.

#### Scenario: Authenticated page renders the navigation shell

- **GIVEN** a signed-in user requesting any authenticated page (`/dashboard`,
  `/charges`)
- **WHEN** the page is rendered
- **THEN** the response HTML contains the drawer sidebar
- **AND** the sidebar contains a vehicle-header region and the navigation
  entries, each with a translated label
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

#### Scenario: Sidebar nav labels render in the resolved language

- **GIVEN** a signed-in user whose resolved language is `en`
- **WHEN** the navigation shell is rendered
- **THEN** the sidebar nav item labels render in English
- **AND** when the resolved language is `es`, the same labels render in Spanish

### Requirement: Navigation Vehicle Header

The gateway SHALL render a vehicle header in the navigation shell that
shows the active vehicle's display name, battery level, and a
freshness-derived status. The data SHALL come exclusively from existing
read ports: `account.RegisteredVehicles` (vehicle name) and
`telemetry.Reader.LatestSnapshotsByAccount` (battery level +
`CapturedAt`). The status SHALL be derived from the snapshot's
freshness, not from any live Tesla call. The status word and the connect-prompt text
SHALL render through the translation catalogue, derived from the header's existing closed
`Status` kind — the handler SHALL NOT compute a pre-formatted English status string.

The active vehicle is the one selected in the vehicle context switcher
(below); when the session carries no selection the gateway SHALL
auto-select the first `OWNER` vehicle from `account.RegisteredVehicles`
(falling back to the first registered vehicle when none is `OWNER`), via
`resolveSelectedVehicle`. The header's name, battery, and status reflect
THIS selected vehicle's latest snapshot.

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

#### Scenario: Vehicle header shows "Connected" when a fresh snapshot exists

- **GIVEN** a signed-in user whose account has at least one registered
  vehicle
- **AND** the latest stored snapshot for the active vehicle has a
  `CapturedAt` within the connected-freshness window (48 hours of the
  current request time)
- **WHEN** the navigation header is rendered
- **THEN** the header shows the vehicle's display name
- **AND** shows a "Connected" status (translated per the resolved language) with a
  success-colored status dot
- **AND** shows the battery level as an integer percentage
- **AND** the freshness window is controlled by a named constant in the
  handler code (not a magic number)

#### Scenario: Vehicle header shows "Asleep / Last seen" when the snapshot is stale

- **GIVEN** a signed-in user whose active vehicle's latest stored
  snapshot has a `CapturedAt` older than the connected-freshness window
- **WHEN** the navigation header is rendered
- **THEN** the header shows the vehicle's display name
- **AND** shows an "Asleep" (or equivalent, translated) status with a
  warning-colored status dot
- **AND** shows a relative "Last seen" label (e.g. "2 days ago")
  pre-computed by the handler
- **AND** the template performs no time arithmetic

#### Scenario: Vehicle header degrades when no snapshot exists

- **GIVEN** a signed-in user whose account has a registered active
  vehicle but no stored snapshot for it
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

- **GIVEN** a signed-in user whose account read or telemetry read returns
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
  relative "last seen" label, and the active-vehicle name have all been
  computed by the Go handler before the template receives the view model
- **AND** the status word is derived in the template from the handler-computed
  `Status` enum via the translation catalogue, not from a handler-computed string
- **AND** the template uses only presentation logic (if/for/display, and the
  catalogue lookup) — no arithmetic, no time calculations, no method calls on
  domain types

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

### Requirement: Navigation Items

The navigation shell SHALL render its entries with translated labels resolved through the
translation catalogue. Live entries SHALL link to existing pages; placeholder entries SHALL NOT
navigate to a real page in this change and SHALL be visually marked as "soon" (translated).
Each entry SHALL render an icon.

#### Scenario: Live navigation entries link to existing pages, with translated labels

- **GIVEN** a signed-in user viewing the navigation shell
- **WHEN** the navigation list is rendered
- **THEN** a "Dashboard" entry (translated per the resolved language) links to `/dashboard`
- **AND** a "Manual Records" entry (translated) links to `/charges`
- **AND** both entries are active-highlighted when the current request
  path matches their target

#### Scenario: Placeholder navigation entries are marked "soon", translated

- **GIVEN** a signed-in user viewing the navigation shell
- **WHEN** the navigation list is rendered
- **THEN** any placeholder entry is rendered as a placeholder link visually
  marked with a translated "Soon"/"Pronto" badge
- **AND** no placeholder navigates to a page not built by this change

#### Scenario: Navigation entries render icons without an external CDN

- **GIVEN** the navigation shell rendered with icons
- **WHEN** the entries are rendered
- **THEN** each entry's icon is an inline SVG owned by the `ui/` kit
  (a `ui.Icon` wrapper)
- **AND** no icon is loaded from an external CDN (no Google Fonts
  Material Symbols stylesheet)
- **AND** no inline client-side JavaScript is required to render icons

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

### Requirement: Dashboard History Charts

The gateway SHALL render two per-vehicle history bar charts in the dashboard bento — an "Odometer
history" chart and a "Battery history" chart — for the currently selected vehicle, replacing the
"awaiting nightly snapshots" placeholders. The chart data SHALL come exclusively from the
`telemetry.Reader` port; the gateway SHALL NOT read telemetry tables directly and SHALL NOT make a
live Tesla Fleet API call to render the charts.

The charts SHALL be served by an authenticated htmx fragment endpoint `GET /ui/dashboard/history`
that accepts **`?start=YYYY-MM-DD&end=YYYY-MM-DD`** — both whole calendar days, UTC-midnight-
bounded, `end` **inclusive** — never a `?days=N` count. The endpoint SHALL resolve the target
vehicle from the session-selected vehicle (never from the URL) and scope the read to that
vehicle's Tesla id and the caller's account. Anonymous requests SHALL be redirected to `/login`
with no history data served.

**"Today" for this endpoint's validation and defaulting is the browser's local calendar day, not
the server's UTC day.** The gateway SHALL derive it from a `browser_tz` cookie: a single inline
`<script>` in the authenticated base layout (`layouts.BaseAuth`, wrapping every authenticated
page — dashboard, charges, connect, etc., but never the anonymous `layouts.Base` shell) reads the
browser's IANA timezone via `Intl.DateTimeFormat().resolvedOptions().timeZone` and persists it in
a 1-year, `SameSite=Lax`, `path=/` cookie on every authenticated page load. The gateway SHALL
parse the cookie's value via `time.LoadLocation` and use the resulting `*time.Location` to compute
"browser-today" as local midnight in that zone. On ANY failure — the cookie absent (a direct API
call, a `<noscript>` browser, or a request that races the very first script execution), an empty
cookie value, or a value that does not parse as a valid IANA zone name — the gateway SHALL fall
back to `time.UTC`, preserving the pre-existing UTC behavior. The fallback SHALL be silent: no
error is surfaced to the caller and no caller has to special-case a cookie-read failure.

The `start`/`end` params SHALL be validated by a single helper (`parseHistoryRange`): when both
are absent the endpoint SHALL apply the default 6-day window (`end = browser-today` midnight,
`start = end-6`); a missing partner, a malformed non-ISO date, an `end` earlier than `start`, an
`end` later than browser-today, or a window wider than 90 days SHALL be rejected with HTTP 400.
The endpoint SHALL compute a 1-day lookback `readStart = start-1day` and fetch the window via
`telemetry.Reader.SnapshotsByVehicleBetween(ctx, uid, teslaID, readStart, end)`.

**The dashboard's own self-load window and its preset selector target the browser's local
YESTERDAY, not browser-today.** Because the nightly telemetry batch captures a calendar day's
snapshot the following night, a window ending at `browser-today` always renders an empty
"no snapshot" last bar. The dashboard self-load href (`defaultHistoryHref`, computed once by the
`Dashboard`/`DashboardFragment` handlers and emitted verbatim by the template — no time math in
markup) and the 6/14/30-day preset buttons (`buildHistoryPresets`) therefore compute their window
as `end = browser-today - 1day` ("browser-yesterday"), `start = end - N`. This is a UI-only
convenience layered on an unchanged API contract: a direct API caller who omits both `start` and
`end` still gets `parseHistoryRange`'s default-absent window ending at browser-today (UTC-today
with no cookie) — the dashboard page itself simply never issues that bare request; its self-load
and every preset always carry an explicit, pre-computed `?start=&end=` ending at yesterday.

Both charts SHALL render a **fixed `[start..end]` date axis** — one bar per calendar day in the
inclusive window, identical `MM-DD` labels on both charts — so a missing nightly snapshot no longer
shifts the axis. A calendar day with no stored snapshot SHALL render as an **empty labeled bar**:
zero height, its own `MM-DD` label retained, and a "no snapshot" tooltip. The pre-window `start-1`
snapshot (fetched via the lookback) SHALL be consumed solely as the first odometer delta's
kilometre basis and SHALL NOT be displayed as a bar.

The "Odometer history" bars SHALL represent **kilometres driven per day** — the difference between
the snapshot for day `d-1` and the snapshot for day `d`, in stored kilometres, with no unit
conversion; a negative computed delta SHALL be shown as zero. The "Battery history" bars SHALL
represent the **battery level percentage** at each day's snapshot (absolute 0–100). Each bar SHALL
carry a hover tooltip: the odometer bar's tooltip SHALL include the `MM-DD` date, the kilometres
driven that day, and the cumulative odometer in kilometres; the battery bar's tooltip SHALL include
the `MM-DD` date, the level percentage, and the rated range in kilometres. A missing-day bar's
tooltip SHALL state that no snapshot exists for that date.

The date shown in each tooltip and per-bar label SHALL be the snapshot's **`EffectiveDate`** (the
calendar day the nightly snapshot represents — `CapturedAt` − 1 day), formatted **`MM-DD`** by the
Go handler. The gateway SHALL NOT show the capture-morning date (`CapturedAt`) and SHALL NOT format
dates inside the template. Each bar SHALL carry a per-bar **date label** rendered under the bar in
an HTML grid row (one cell per bar), with orientation decided by a single chart-level boolean flag
(`LabelVertical`): **horizontal** for the 6-bar window (wide bars), **rotated vertical** (via the
`[writing-mode:vertical-rl]` CSS class) for windows of 14 bars or more (narrow bars). The handler
SHALL set `LabelVertical` from the number of bars in the fixed window (`labelVerticalFor(numBars)`,
true when `numBars >= 14`), not from a `days` count; the template SHALL NOT compare the window size,
compute rotation, or call `time.Format`.

The charts SHALL be rendered as **responsive inline SVG** (scaling to the container width) using no
client-side charting library. All numeric values — bar heights, deltas, percentages, tooltip strings,
and per-bar label strings — SHALL be computed by the Go handler before the template renders; the
template SHALL perform no arithmetic, unit conversion, date formatting, or method calls on domain
types. Bar colours SHALL use DaisyUI semantic tokens (no hardcoded hex).

The preset selector SHALL keep the 6/14/30 buttons but each button's `hx-get` SHALL emit a
server-rendered absolute `?start=<yesterday-N>&end=<yesterday>` href (computed by the handler at
render time, `yesterday = browser-today - 1day`); the selector SHALL mark the preset whose
`(start, end)` matches the requested window as active. Changing the preset SHALL re-fetch
`GET /ui/dashboard/history?start=...&end=...` and re-render both charts AND the selector by
swapping `#dashboard-history`'s `innerHTML` without a full page reload.

The dashboard page SHALL NOT carry a Refresh button; the `#dashboard-content` (subscribes to
`vehicle-changed from:body`) and `#dashboard-history` (self-loads on `hx-trigger="load"` with the
default 6-day window's absolute `start`/`end` ending yesterday, and re-renders on preset clicks)
htmx surfaces cover every refresh path. When too few snapshots exist to draw a chart (fewer than
two snapshots total in the lookback window for the odometer delta chart, or none for the battery
chart), that chart SHALL show the existing empty-state placeholder instead of fabricated bars; a
partially-missing axis (some days empty, some present) is NOT an empty chart.

#### Scenario: History charts render for the selected vehicle with the default window

- **GIVEN** a signed-in user whose selected vehicle has several stored nightly snapshots
- **WHEN** the history fragment is requested (`GET /ui/dashboard/history`) directly, with no `start`
  and no `end` parameter and no `browser_tz` cookie (a direct API call)
- **THEN** the response renders an "Odometer history" chart and a "Battery history" chart for the
  selected vehicle using a 6-day window (`end = UTC-today`, `start = UTC-today-6`, `end` inclusive)
- **AND** both charts render exactly 6 bars, one per calendar day in `[UTC-today-6 .. UTC-today]`
- **AND** the odometer and battery charts display identical `MM-DD` labels under corresponding bars
- **AND** no live Tesla Fleet API call is made and no telemetry table is read directly

#### Scenario: The default window and the end<=today cap honor the browser_tz cookie, for any UTC offset sign

- **GIVEN** a signed-in user whose browser sent a `browser_tz` cookie with a valid IANA zone — this
  holds for ANY such zone, whether its UTC offset is negative (e.g. `America/Bogota`, UTC-5) or
  positive (e.g. `Asia/Tokyo`, UTC+9; `Pacific/Auckland`, UTC+12/+13)
- **WHEN** the history fragment is requested with no `start` and no `end` parameter
- **THEN** the default window's `end` is midnight of "today" IN THAT ZONE, not UTC midnight
- **AND** a request carrying `end` equal to that same browser-local "today" is accepted (HTTP 200)
  — for EVERY zone regardless of the sign of its UTC offset, because the cap compares CALENDAR
  DATES (each side's Y/M/D evaluated in its own frame), never absolute instants; a positive-offset
  zone, where UTC-midnight-of-D is a later instant than local-midnight-of-D, is therefore never
  spuriously rejected
- **AND** a request carrying `end` equal to browser-local "tomorrow" is rejected with HTTP 400
  (future in the browser's frame), even where the equivalent instant is still "today" in UTC

#### Scenario: Missing, malformed, or empty browser_tz cookie falls back to UTC

- **GIVEN** any of: no `browser_tz` cookie present, a cookie whose value is an empty string, or a
  cookie whose value does not parse as a valid IANA timezone name via `time.LoadLocation` (e.g.
  `"Not/A/Zone"`)
- **WHEN** the gateway computes "browser-today" for `parseHistoryRange`, `defaultHistoryHref`, or
  `buildHistoryPresets`
- **THEN** the gateway uses `time.UTC` — the pre-browser-TZ behavior — without returning an error to
  the caller or the render
- **AND** the resulting default window's `end` equals `startOfDay(time.Now())` in UTC

#### Scenario: Dashboard self-load and presets default to the browser's local yesterday

- **GIVEN** a signed-in user whose browser sent a valid `browser_tz` cookie
- **WHEN** the dashboard page or fragment is rendered (`GET /dashboard`, `GET /ui/dashboard`)
- **THEN** the `#dashboard-history` region's self-load href (`defaultHistoryHref`) targets
  `end = browser-today - 1day` ("browser-yesterday"), `start = end - 6`
- **AND** each of the 6/14/30-day preset buttons targets `end = browser-yesterday`,
  `start = end - N`
- **AND** the requested window's last bar therefore always has a chance of carrying a real snapshot
  (the nightly batch has already captured browser-yesterday's data by the time the user opens the
  dashboard)
- **AND** a direct `GET /ui/dashboard/history` call with no params (bypassing the dashboard's
  self-load) is UNCHANGED by this default-to-yesterday UI behavior — it still returns the
  default-absent window ending at browser-today, per `parseHistoryRange`

#### Scenario: The browser_tz cookie is set on every authenticated page load

- **GIVEN** any authenticated page rendered through `layouts.BaseAuth` (dashboard, charges,
  connect, etc.)
- **WHEN** the page is rendered
- **THEN** the response HTML contains an inline `<script>` that reads
  `Intl.DateTimeFormat().resolvedOptions().timeZone` and sets `document.cookie` with a
  `browser_tz=` entry, `path=/`, `max-age=31536000` (1 year), `SameSite=Lax`
- **AND** the script is wrapped so a JavaScript failure (or a `<noscript>` browser) does not break
  page rendering — the cookie simply never gets set and the server falls back to UTC
- **AND** the anonymous `layouts.Base` shell (used for `/`, `/login`) does NOT contain this script

#### Scenario: Bounded calendar-day window is parsed and validated

- **GIVEN** a signed-in user on the dashboard
- **WHEN** the history fragment is requested with `?start=2026-08-03&end=2026-08-07`
- **THEN** the charts render a fixed axis for the 5-day inclusive window `[2026-08-03 .. 2026-08-07]`
- **AND** the handler called `SnapshotsByVehicleBetween(ctx, uid, teslaID, readStart, end)` with
  `readStart = 2026-08-02` (a 1-day lookback) and `end = 2026-08-07`
- **AND** the 1-day-lookback snapshot (whose `EffectiveDate` is `2026-08-02`) is NOT displayed as a
  bar; it is consumed only as the first odometer delta's kilometre basis

#### Scenario: Malformed or invalid date params are rejected with 400

- **GIVEN** a signed-in user on the dashboard
- **WHEN** the history fragment is requested with any of: a malformed non-ISO date (`?start=08-07`),
  a missing partner (`?start=2026-08-03` with no `end`), `end` earlier than `start`
  (`?start=2026-08-07&end=2026-08-03`), `end` later than browser-today
  (`?start=2026-08-03&end=2026-12-31`), or a window wider than 90 days
  (`?start=2026-05-01&end=2026-08-07` ≈ 99 days)
- **THEN** the endpoint responds with HTTP 400
- **AND** no `SnapshotsByVehicleBetween` call is made for a rejected request (throw at validation time)
- **AND** the 400 response renders the `#dashboard-history` empty-state placeholder (no fabricated
  bars, no 500)

#### Scenario: Both charts share a fixed calendar-day axis (MAG-7 offset fix)

- **GIVEN** a selected vehicle whose snapshots cover a 6-day window but with one missing nightly
  snapshot (e.g. no capture whose `EffectiveDate` is `2026-08-05`)
- **WHEN** the history fragment is rendered for `[2026-08-02 .. 2026-08-07]`
- **THEN** both charts render exactly 6 bars, one per calendar day in the inclusive window
- **AND** the missing day (`2026-08-05`) renders as an empty labeled bar (zero height) with the
  `MM-DD` label `08-05` and a "no snapshot" tooltip
- **AND** the odometer and battery labels for the same calendar day are identical (no 1-day offset
  between the two charts)
- **AND** the surrounding bars' labels do NOT shift to fill the missing day's slot

#### Scenario: Odometer bars are km driven per day, using the 1-day lookback for the first delta

- **GIVEN** a selected vehicle with stored snapshots for `2026-08-02` (lookback), `2026-08-03`,
  `2026-08-04`, … `2026-08-07` (the window `[2026-08-03 .. 2026-08-07]`)
- **WHEN** the odometer history chart is rendered
- **THEN** the bar for `2026-08-03` represents `odometerKm(snap@08-03) - odometerKm(snap@08-02)`,
  consuming the lookback snapshot (`EffectiveDate == 2026-08-02`) solely as the delta basis
- **AND** the lookback snapshot is NOT displayed as a bar (the axis starts at `08-03`)
- **AND** a bar whose computed delta is negative is shown as zero
- **AND** each bar's tooltip shows the `MM-DD` date, the kilometres driven that day, and the
  cumulative odometer in kilometres

#### Scenario: Battery bars are absolute level with a range tooltip, one per calendar day

- **GIVEN** a selected vehicle with stored snapshots in the window (and one missing day)
- **WHEN** the battery history chart is rendered
- **THEN** each bar's height represents that day's snapshot battery level percentage (0–100)
- **AND** a missing-day bar renders at zero height with its `MM-DD` label and a "no snapshot"
  tooltip (not a fabricated 0% reading)
- **AND** each present bar's tooltip shows the `MM-DD` date, the battery level percentage, and the
  rated range in kilometres

#### Scenario: Tooltip date is the EffectiveDate in MM-DD, not the capture morning

- **GIVEN** a selected vehicle with a snapshot whose `CapturedAt` is `2026-08-08 03:30 UTC`
- **AND** whose `EffectiveDate` is therefore `2026-08-07`
- **WHEN** either history chart's bar for that snapshot is rendered
- **THEN** the bar's tooltip shows the date as `08-07` (MM-DD of the `EffectiveDate`)
- **AND** the tooltip does NOT show `2026-08-08` (the `CapturedAt` morning) or a `YYYY-MM-DD` string
- **AND** the `MM-DD` string is pre-formatted by the Go handler (the template performs no
  `time.Format`)

#### Scenario: Label orientation is adaptive to the number of bars in the fixed window

- **GIVEN** the history fragment rendered with a 6-bar window (the default or the 6-day preset)
- **WHEN** the per-bar labels are rendered
- **THEN** the labels are horizontal under each bar
- **AND** the handler set the chart's `LabelVertical` flag to false

- **GIVEN** the history fragment rendered with a 14-bar or 30-bar window (the 14/30-day presets)
- **WHEN** the per-bar labels are rendered
- **THEN** the labels use the `[writing-mode:vertical-rl]` CSS class (vertical) so each label fits
  within its narrow bar's width
- **AND** the handler set the chart's `LabelVertical` flag to true
- **AND** the template chose the orientation solely from the `LabelVertical` flag (it did not
  compare the window size or compute rotation)

#### Scenario: Preset selector emits server-rendered absolute start/end hrefs ending yesterday

- **GIVEN** a signed-in user viewing the dashboard history region
- **WHEN** the preset selector is rendered (for any valid window)
- **THEN** each preset button's `hx-get` is an absolute `?start=<yesterday-N>&end=<yesterday>` href
  where `N` is 6, 14, or 30 respectively and `yesterday = browser-today - 1day`, computed at
  render time by the Go handler
- **AND** the button copy is unchanged ("6 days", "14 days", "30 days")
- **AND** the preset whose `(start, end)` matches the requested window is marked active
  (`btn-primary`); a custom non-preset window marks no preset active (all-ghost)

#### Scenario: Changing the preset re-fetches both charts by absolute dates

- **GIVEN** a signed-in user viewing the dashboard history region with the default 6-day window
  ending yesterday
- **WHEN** the user clicks the 14-day preset button
- **THEN** the region re-fetches `GET /ui/dashboard/history?start=<yesterday-14>&end=<yesterday>`
- **AND** both the odometer and battery charts re-render for the 14-day window with a fixed 14-bar
  axis without a full page reload
- **AND** the 14-day preset is marked active (`btn-primary`)
- **AND** the per-bar label orientation updates to vertical (14 bars ≥ 14 → `LabelVertical = true`)

#### Scenario: History region follows a vehicle switch at the default window

- **GIVEN** a signed-in user with two or more registered vehicles on the dashboard
- **AND** the history region is nested inside the `#dashboard-content` region that subscribes to
  `vehicle-changed`
- **WHEN** the user switches the active vehicle
- **THEN** the dashboard content re-renders and the `#dashboard-history` region self-loads with the
  default 6-day window's absolute `start`/`end` (ending browser-yesterday) for the newly-selected
  vehicle

#### Scenario: Dashboard page carries no Refresh button

- **GIVEN** the rendered dashboard page (`pages/dashboard.templ`)
- **WHEN** it is rendered for an authenticated user
- **THEN** the page header does NOT contain a Refresh button
- **AND** the `#dashboard-content` and `#dashboard-history` htmx self-refresh surfaces are present
- **AND** no other page's Refresh button is affected (the charges-list Refresh stays)

#### Scenario: Sparse data falls back to the empty state (not a partial axis)

- **GIVEN** a selected vehicle with fewer than two stored snapshots total in the lookback window
- **WHEN** the odometer history chart is rendered
- **THEN** the odometer chart shows the empty-state placeholder (no fabricated bars)
- **AND** the battery chart shows bars only if at least one snapshot exists, otherwise its own
  empty-state placeholder
- **AND** a partially-missing axis (some empty bars, some present) is NOT treated as an empty chart

#### Scenario: Charts contain no business logic in templates

- **GIVEN** the history chart and selector templates
- **WHEN** they render
- **THEN** the bar heights, per-day kilometre deltas, battery percentages, tooltip strings, per-bar
  `MM-DD` label strings, the `Present` flag, and the `LabelVertical` orientation flag have all been
  computed by the Go handler before the template receives the view model
- **AND** the templates use only presentation logic (if/for/display) — no arithmetic, no unit
  conversion, no date formatting, no method calls on domain types
- **AND** the SVG scales to the container width (responsive) and bar colours use DaisyUI semantic
  tokens with no hardcoded hex
- **AND** the per-bar label is a real DOM text cell in an HTML grid (not an SVG `<text>`), using a
  muted semantic token — no client-side library

#### Scenario: Gateway never imports telemetrydb for history

- **GIVEN** the gateway handler that builds the history charts
- **WHEN** it obtains the vehicle's snapshot history
- **THEN** it does so exclusively through the `telemetry.Reader` public interface
  (`SnapshotsByVehicleBetween` for the bounded window)
- **AND** it imports no package from `internal/telemetry/db` (`telemetrydb`)
- **AND** no `pgtype` type appears in any gateway file involved

#### Scenario: History fragment is not served to anonymous callers

- **GIVEN** an unauthenticated request to `GET /ui/dashboard/history`
- **WHEN** the handler resolves the session
- **THEN** the request is redirected to `/login` and no history data is served

#### Scenario: The start/end HTTP convention is recorded in the gateway module docs

- **GIVEN** a future date-filtered gateway HTTP endpoint is proposed
- **WHEN** an agent or human reads `internal/gateway/AGENTS.md` or follows the one-line pointer in
  `ai/go-conventions.md`
- **THEN** they find the convention "every date-filtered gateway HTTP endpoint takes
  `?start=YYYY-MM-DD&end=YYYY-MM-DD`, never a `?days=N` count", with the 400 cases and the 90-day
  cap
- **AND** the dashboard history endpoint is the reference implementation of that convention

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

- **GIVEN** a signed-in user on `/charges` whose account has two or more registered vehicles
- **AND** the `#charges-content` region subscribes with `hx-trigger="vehicle-changed from:body"` and `hx-get="/ui/charges"`
- **WHEN** the user switches the active vehicle and the `vehicle-changed` event fires
- **THEN** the region re-fetches `GET /ui/charges`, which renders the `charges-create-form` and `charges-list` fragments (not the full page shell) scoped to the selected vehicle's `TeslaID`
- **AND** the entry list shows the selected vehicle's entries and the re-rendered create form sources its vehicle from the newly-selected session vehicle, rendering no vehicle picker
- **AND** a fresh manual-charge CSRF token is issued for the re-rendered create form

#### Scenario: A refresh fragment is not served to anonymous callers

- **GIVEN** an unauthenticated request to a per-vehicle refresh route (`GET /ui/dashboard` or `GET /ui/charges`)
- **WHEN** the handler resolves the session
- **THEN** the request is redirected to `/login` and no per-vehicle data is served

### Requirement: Supercharger Stats page

The gateway SHALL render a "Supercharger Stats" page for the currently session-selected
vehicle, replacing the sidebar's "Soon" placeholder with a live route. The page SHALL show
that vehicle's Tesla-billed Supercharger / DC fast-charging session history sourced
exclusively from the `telemetry.SuperchargerReader` port; the gateway SHALL NOT read
telemetry tables directly and SHALL NOT make a live Tesla Fleet API call to render the page.

The page SHALL be served by two authenticated routes: `GET /supercharger-stats` (the full
page, initial load) and `GET /ui/supercharger-stats` (the htmx fragment, swapped by the
month-preset selector) — mirroring the `/charges` + `/ui/charges` pairing. Anonymous requests
to either route SHALL be redirected to `/login` with no Supercharger data served.

Both routes SHALL resolve the target vehicle from the session-selected vehicle (never from the
URL or a request parameter), scoping the read to that vehicle's Tesla id and the caller's
account — the same resolution mechanism used by the dashboard and history-chart fragments.

The read SHALL be a single call to `SuperchargerSessionsByVehicle`, capped at a named limit of
500 rows, ordered newest-first. If the reader returns an error, the page SHALL degrade to its
empty state (empty tiles, empty chart, empty table) rather than returning a 500.

#### Scenario: Stats page renders for the selected vehicle

- **GIVEN** a signed-in user whose selected vehicle has stored Supercharger sessions
- **WHEN** `GET /supercharger-stats` is requested
- **THEN** the response renders the KPI tile row, the kWh-per-month chart, and the sessions
  table for the selected vehicle's sessions
- **AND** no live Tesla Fleet API call is made and no telemetry table is read directly
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
- **WHEN** the `SuperchargerReader` call returns an error
- **THEN** the page renders its empty state (empty tiles, empty chart, empty table)
- **AND** the response is not a 500
- **AND** the error is logged server-side

#### Scenario: Zero sessions in the selected window renders the empty state

- **GIVEN** a signed-in user with a selected vehicle that has no Supercharger sessions within
  the selected month window
- **WHEN** the stats page or fragment is rendered
- **THEN** the tiles, chart, and table all show the empty state
- **AND** no fabricated data is shown

### Requirement: Unattributed Supercharger sessions are out of scope

The gateway SHALL NOT show or count, anywhere on the Supercharger Stats page, a
`SuperchargerSession` whose `TeslaID` is `NULL` (the session's VIN does not match any
currently-registered vehicle on the account) — not in the tiles, not in the chart, not in the
table. This is a deliberate, specified limitation: the page's single read is scoped by
`TeslaID` (`SuperchargerSessionsByVehicle`), so such sessions can never match the filter by
construction. The gateway SHALL NOT perform a second, account-wide read to discover or
disclose these sessions.

#### Scenario: A session with no matching registered vehicle is silently excluded

- **GIVEN** a Supercharger session stored for the account whose VIN does not match any of the
  account's currently-registered vehicles (its `TeslaID` is `NULL`)
- **AND** the account also has at least one Supercharger session that DOES match the selected
  vehicle
- **WHEN** the Supercharger Stats page is rendered for the selected vehicle
- **THEN** the unattributed session does not appear in the sessions table
- **AND** its energy and cost are not included in any KPI tile or chart bucket
- **AND** no error, warning, or "N sessions hidden" notice is shown for it
- **AND** only one read (`SuperchargerSessionsByVehicle`) is made — no second, account-wide
  read is performed to check for unattributed sessions

### Requirement: Supercharger Stats month-window selector

The page SHALL offer a month-window selector with a fixed, closed preset set of **{3, 6, 12}**
months, defaulting to **6**. A `months` query parameter value that is missing, non-numeric, or
not one of the allowed presets SHALL fall back to the default of 6. The selected window SHALL
drive the tiles, the chart, and the table identically — all three sections SHALL reflect the
same filtered set of sessions.

Selecting a different preset SHALL re-fetch and re-render the whole Supercharger Stats region
without a full page reload.

#### Scenario: Month window is validated and defaults to 6

- **GIVEN** a signed-in user on the Supercharger Stats page
- **WHEN** the fragment is requested with a `months` value that is missing, non-numeric, or not
  one of the allowed presets (3, 6, or 12)
- **THEN** the page renders using the default window of 6 months
- **AND** when `months` is one of the allowed presets, the page renders using that window
- **AND** the tiles, the chart, and the table all reflect the same window

#### Scenario: Changing the month preset re-fetches the whole region

- **GIVEN** a signed-in user viewing the Supercharger Stats page with the default window
- **WHEN** the user selects a different month preset
- **THEN** the region re-fetches `GET /ui/supercharger-stats?months=N` for the newly-selected
  window
- **AND** the tiles, chart, and table all re-render for that window without a full page reload
- **AND** the selected preset is marked active in the month-selector control

### Requirement: Supercharger Stats KPI tiles

The page SHALL show four KPI tiles computed from the single filtered session slice for the
selected window: **Sessions** (count of sessions in the window), **Energy** (sum of
`EnergyKWh` across sessions where it is non-nil), **Cost** (per-currency totals — see the cost
aggregation requirement below), and **Avg kWh per session** (energy divided by the count of
sessions with a non-nil `EnergyKWh`, guarded against division by zero). All four tiles, the
chart, and the table SHALL derive from the same filtered slice per render, so their counts
SHALL NOT diverge.

#### Scenario: Tiles reflect the filtered session slice

- **GIVEN** a selected vehicle with several Supercharger sessions inside the selected window,
  some with a nil `EnergyKWh`
- **WHEN** the Supercharger Stats page is rendered
- **THEN** the Sessions tile shows the count of sessions in the window
- **AND** the Energy tile shows the sum of `EnergyKWh` over sessions where it is non-nil
- **AND** the Avg kWh per session tile divides that energy sum by the count of sessions with a
  non-nil `EnergyKWh`
- **AND** the number of rows in the sessions table equals the Sessions tile's count

#### Scenario: Avg kWh per session guards against division by zero

- **GIVEN** a selected vehicle whose sessions in the window all have a nil `EnergyKWh`
- **WHEN** the Supercharger Stats page is rendered
- **THEN** the Avg kWh per session tile shows an empty/placeholder value, not a division error
  or a fabricated number

### Requirement: Supercharger Stats cost aggregation never sums across currencies

Session cost SHALL be aggregated into a map keyed by currency, built only from sessions where
both `TotalCost` and `Currency` are non-nil; a session with a nil cost or nil currency SHALL be
excluded from the cost aggregation but SHALL still count toward the Sessions and Energy tiles.
The Cost tile SHALL render one line per currency present in the aggregation and SHALL NOT sum
values across different currencies into a single combined figure.

#### Scenario: Sessions in two currencies render as two cost lines, never summed

- **GIVEN** a selected vehicle with Supercharger sessions in the window billed in two different
  currencies (e.g. some in USD, some in COP), each with a non-nil `TotalCost` and `Currency`
- **WHEN** the Supercharger Stats page is rendered
- **THEN** the Cost tile shows one line per currency with that currency's summed total
- **AND** no line combines amounts from different currencies into a single number

#### Scenario: Sessions with nil cost or currency are excluded from the cost tile only

- **GIVEN** a selected vehicle with some sessions in the window that have a nil `TotalCost` or
  a nil `Currency` (no fee data), alongside other sessions that have both
- **WHEN** the Supercharger Stats page is rendered
- **THEN** the Cost tile's per-currency totals include only the sessions with a non-nil
  `TotalCost` AND a non-nil `Currency`
- **AND** the Sessions tile still counts the nil-cost sessions
- **AND** the Energy tile still includes the nil-cost sessions' `EnergyKWh` when it is non-nil

### Requirement: Supercharger Stats kWh-per-month chart

The page SHALL render a responsive, zero-JavaScript inline SVG bar chart showing kWh charged
per calendar month within the selected window, one bar per month, following the project's
existing hand-rolled-SVG chart pattern (`internal/gateway/AGENTS.md` §"Charts are hand-rolled
SVG"). All bar heights and tooltip strings SHALL be pre-computed by the Go handler; the
template SHALL perform no arithmetic, no unit conversion, and no method calls on domain types.
Bar colors SHALL use DaisyUI semantic tokens, never hardcoded hex.

#### Scenario: Chart buckets sessions by month with pre-computed bar data

- **GIVEN** a selected vehicle with Supercharger sessions spanning multiple calendar months
  within the selected window
- **WHEN** the kWh-per-month chart is rendered
- **THEN** each bar represents one calendar month's summed `EnergyKWh` (nil-`EnergyKWh`
  sessions excluded from the sum)
- **AND** each bar's height and tooltip string arrive on the view model already computed — the
  template performs no arithmetic or domain-method calls
- **AND** bar colors are DaisyUI semantic tokens, not hardcoded hex

### Requirement: Supercharger Stats sessions table is unpaginated

The page SHALL render every session in the selected window in a single `ui.Table` — no
pagination and no "show more" control. The window (month preset) and the read cap already
bound the row count.

#### Scenario: All sessions in the window appear in the table

- **GIVEN** a selected vehicle with a number of Supercharger sessions within the selected
  window that is smaller than the read cap
- **WHEN** the Supercharger Stats page is rendered
- **THEN** every one of those sessions appears as a row in the sessions table
- **AND** no pagination control or "show more" affordance is present

### Requirement: Supercharger Stats charts and tables contain no business logic in templates

The Supercharger Stats Templ components SHALL perform presentation only — conditionals, loops,
and rendering of already-computed strings/numbers. All aggregation (tile sums, currency
grouping, month bucketing, averages) and all formatting (unit suffixes, currency labels, date
formatting) SHALL happen in the Go handler before the view model reaches the template.

#### Scenario: Templates receive fully-computed view model values

- **GIVEN** the Supercharger Stats page and fragment templates
- **WHEN** they render
- **THEN** every tile value, bar height, tooltip string, and table cell has already been
  computed by the Go handler
- **AND** the templates perform no arithmetic, no currency summation, no unit conversion, and
  no method calls on domain types

### Requirement: Translation Catalogue and Per-Request Language Resolution

The gateway SHALL resolve an active language for every request, exactly once per request, and
SHALL make every user-facing string it renders resolvable through a closed translation catalogue
covering exactly `es` (default) and `en`.

For a signed-in request, the active language SHALL come from `account.Service.LanguageFor`. For
an anonymous request, the active language SHALL come from a `lang` cookie. In both cases, an
absent, unrecognized, or error-producing source SHALL resolve to `es` — the gateway SHALL NEVER
fail or 500 a render because of a missing or invalid language source.

A catalogue key with no `es`/`en` entry SHALL render a visible marker distinguishing it from a
translated string (never a blank string, never a raw untranslated fallback with no marker), so an
incomplete catalogue is visible in manual QA rather than silently shipping.

#### Scenario: Signed-in request resolves language from the account

- **GIVEN** a signed-in user whose account's stored language is `en`
- **WHEN** any authenticated page or htmx fragment is rendered
- **THEN** the gateway calls `account.Service.LanguageFor` exactly once for that request
- **AND** every catalogue-driven string on the response renders in English

#### Scenario: Anonymous request resolves language from the cookie

- **GIVEN** an anonymous visitor whose browser carries a `lang=en` cookie
- **WHEN** an anonymous page (e.g. `/login`, `/`) is rendered
- **THEN** the gateway does not call `account.Service.LanguageFor`
- **AND** every catalogue-driven string on the response renders in English

#### Scenario: Missing or unrecognized language source falls back to Spanish

- **GIVEN** either (a) an anonymous visitor with no `lang` cookie or a cookie value outside
  `{es, en}`, or (b) a signed-in user whose `account.Service.LanguageFor` call errors
- **WHEN** a page is rendered
- **THEN** the gateway renders every catalogue-driven string in Spanish
- **AND** the render succeeds (no 500, no raw error)

#### Scenario: Language is resolved exactly once per request

- **GIVEN** a signed-in user requesting a page whose render composes multiple nested components
- **WHEN** the page is rendered
- **THEN** `account.Service.LanguageFor` is called exactly one time for that request
- **AND** every nested component renders in the same resolved language

#### Scenario: A catalogue key with no entry renders a visible marker

- **GIVEN** a translation lookup for a key that has no catalogue entry
- **WHEN** the string is rendered
- **THEN** the output is a visibly marked placeholder distinct from any real translated string
- **AND** no panic or error is raised

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
- **WHEN** they load any authenticated page (`/dashboard`, `/charges`, `/supercharger-stats`)
- **THEN** the response contains the language selector

#### Scenario: Selector requires no client-side JavaScript

- **GIVEN** the rendered language selector
- **WHEN** its markup is inspected
- **THEN** it uses only CSS-driven DaisyUI patterns (no inline `<script>`, no JS event listener
  added to support it)

### Requirement: Language Switch Endpoint

The gateway SHALL expose `POST /ui/lang/switch` accepting a `lang` form value of exactly `es` or
`en`. It SHALL work for both anonymous and signed-in callers. It SHALL always set/refresh the
`lang` cookie to the submitted value. When the caller is signed in, it SHALL additionally persist
the choice via `account.Service.SetLanguage`. It SHALL respond with an `HX-Location` header
targeting the caller's current path (and query string, when present) so the current page
re-renders in the new language without a full browser reload and without navigating away from the
page the caller was on.

#### Scenario: Anonymous switch sets the cookie only

- **GIVEN** an anonymous visitor
- **WHEN** they submit `POST /ui/lang/switch` with `lang=en`
- **THEN** the response sets the `lang` cookie to `en`
- **AND** no call is made to `account.Service.SetLanguage`
- **AND** the response carries an `HX-Location` header targeting the page they were on

#### Scenario: Signed-in switch persists to the account and syncs the cookie

- **GIVEN** a signed-in user
- **WHEN** they submit `POST /ui/lang/switch` with `lang=en`
- **THEN** the gateway calls `account.Service.SetLanguage` with the user's account id and `"en"`
- **AND** the response sets the `lang` cookie to `en`

#### Scenario: An unsupported language value is rejected

- **GIVEN** any caller
- **WHEN** they submit `POST /ui/lang/switch` with a `lang` value outside `{es, en}`
- **THEN** the gateway returns HTTP 400
- **AND** no cookie is set and no account write occurs

#### Scenario: The switch preserves the caller's current URL, including query string

- **GIVEN** a signed-in user viewing the dashboard history page with an explicit
  `?start=&end=` date range selected
- **WHEN** they submit `POST /ui/lang/switch`
- **THEN** the `HX-Location` response header's `path` includes the same `start`/`end` query
  string
- **AND** does not push a duplicate history entry for the same URL

