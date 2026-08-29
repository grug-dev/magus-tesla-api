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
  `/charges`)
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

### Requirement: Navigation Vehicle Header

The gateway SHALL render a vehicle header in the navigation shell that
shows the active vehicle's display name, battery level, and a
freshness-derived status. The data SHALL come exclusively from existing
read ports: `account.RegisteredVehicles` (vehicle name) and
`telemetry.Reader.LatestSnapshotsByAccount` (battery level +
`CapturedAt`). The status SHALL be derived from the snapshot's
freshness, not from any live Tesla call. The status word, the connect-prompt text, and the
relative "Last seen" phrasing SHALL all render through the translation catalogue, derived from the
header's existing closed `Status` kind and a pre-computed relative-time magnitude — the handler
SHALL NOT compute a pre-formatted English status string or a pre-formatted English relative-time
phrase.

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

#### Scenario: Vehicle header shows "Asleep / Last seen" when the snapshot is stale, with translated relative-time phrasing

- **GIVEN** a signed-in user whose active vehicle's latest stored
  snapshot has a `CapturedAt` older than the connected-freshness window
- **WHEN** the navigation header is rendered
- **THEN** the header shows the vehicle's display name
- **AND** shows an "Asleep" (or equivalent, translated) status with a
  warning-colored status dot
- **AND** shows a relative "Last seen" label (e.g. "2 days ago" for `en`, "hace 2 días" for `es`)
  whose magnitude is pre-computed by the handler but whose phrasing (including singular vs.
  plural) renders through the translation catalogue for the resolved language
- **AND** the template performs no time arithmetic and no pluralization logic

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
  relative "last seen" label's magnitude, and the active-vehicle name have all been
  computed by the Go handler before the template receives the view model
- **AND** the status word and the "last seen" phrasing are derived in the template from the
  handler-computed `Status` enum and magnitude via the translation catalogue, not from a
  handler-computed English string
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

The gateway SHALL render THREE per-vehicle history bar charts in the dashboard bento — an
"Odometer history" chart, a "Battery history" chart, and a "Battery consumed" chart — for the
currently selected vehicle, replacing the "awaiting nightly snapshots" placeholders. **The
Battery chart's data SHALL come exclusively from the `telemetry.Reader` port; the Odometer
chart's data SHALL come exclusively from the `analytics.Reader.OdometerDeltaByDay` port; the
consumed chart's data SHALL come exclusively from the `analytics.Reader.ConsumedByDay` port —
this is a CHANGE from the prior revision of this requirement, under which the odometer chart
was sourced from `telemetry.Reader` alongside the battery chart (roadmap D5: the gateway
computes nothing about the vehicle, only about the chart).** The gateway SHALL NOT read
telemetry or analytics tables directly and SHALL NOT make a live Tesla Fleet API call to render
any of the three charts.

The charts SHALL be served by an authenticated htmx fragment endpoint `GET /ui/dashboard/history`
that accepts **`?start=YYYY-MM-DD&end=YYYY-MM-DD`** — both whole calendar days, UTC-midnight-
bounded, `end` **inclusive** — never a `?days=N` count. The endpoint SHALL resolve the target
vehicle from the session-selected vehicle (never from the URL) and scope the read to that
vehicle's Tesla id and the caller's account. Anonymous requests SHALL be redirected to `/login`
with no history data served.

**"Today" for this endpoint's validation and defaulting is the browser's local calendar day, not
the server's UTC day** (unchanged from the prior revision of this requirement) — derived from the
`browser_tz` cookie, falling back to `time.UTC` on any failure (absent cookie, empty value,
unparseable IANA zone). See the "Browser-Local Calendar Day" scenarios below (unchanged
behavior, restated here for completeness).

**The `start`/`end` params' default window AND the `end <= X` validation cap now both target
`browser-yesterday`, not `browser-today` — this is a BEHAVIOR CHANGE from the prior revision of
this requirement.** The `start`/`end` params SHALL be validated by a single helper
(`parseHistoryRange`): when both are absent the endpoint SHALL apply the default 6-day window
(`end = browser-yesterday` midnight, `start = end-6`); a missing partner, a malformed non-ISO
date, an `end` earlier than `start`, an `end` later than **browser-yesterday** (this bound moved
by one calendar day — an `end` equal to browser-TODAY is now ALSO rejected, where it was
previously accepted), or a window wider than 90 days SHALL be rejected with HTTP 400. **The
endpoint SHALL compute a 1-day lookback `readStart = start-1day` and fetch the Battery chart's
window via `telemetry.Reader.SnapshotsByVehicleBetween(ctx, uid, teslaID, readStart, end)`; it
SHALL fetch the Odometer chart's window via `analytics.Reader.OdometerDeltaByDay(ctx, uid,
teslaID, start, end)` (no lookback — the port performs its own internal lookback fetch, mirroring
the consumed chart's port contract); and it SHALL fetch the consumed window via
`analytics.Reader.ConsumedByDay(ctx, uid, teslaID, start, end)` (likewise no lookback). This is a
CHANGE from the prior revision, under which one `telemetry.Reader` call with a lookback fed both
the Odometer and Battery charts — the Battery chart's own fetch is otherwise unaffected by this
change (same call, same lookback, same result for that chart).**

**This closes a previously-documented divergence**: before this change, a direct API call
omitting both `start` and `end` returned a window ending at browser-today, while the dashboard's
own self-load and preset buttons always targeted a window ending at browser-yesterday (because
today's data is not captured until tomorrow's nightly poll). After this change, BOTH paths target
the same browser-yesterday-ending window — the no-params default and every preset now agree.

Both the Odometer and Battery charts SHALL continue to render a **fixed `[start..end]` date
axis** — one bar per calendar day in the inclusive window, identical `MM-DD` labels across all
three charts — so a missing nightly snapshot does not shift the axis. A calendar day with no
data (no stored snapshot for the Battery chart; no `analytics.DayDistance` entry for the
Odometer chart) SHALL render as an **empty labeled bar**: zero height, its own `MM-DD` label
retained, and a "no snapshot" tooltip. **The pre-window `start-1` day (consumed internally by
`analytics.Reader.OdometerDeltaByDay` as the first delta's kilometre basis, and by the Battery
chart's own `telemetry.Reader` lookback fetch, respectively) SHALL NOT be displayed as a bar on
either chart — this is unchanged from the prior revision; only WHICH port performs the lookback
for the Odometer chart has changed.**

**The "Odometer history" bars SHALL represent kilometres driven per day, as already computed and
already floored at zero by `internal/analytics`** (`OdometerDeltaByDay`'s `KmDriven` field) — **the
gateway SHALL NOT compute a delta between two snapshots and SHALL NOT clamp a negative value
itself; both the subtraction and the zero-floor are `internal/analytics`'s responsibility, not
the gateway's (roadmap D5). This is a CHANGE from the prior revision, under which the gateway
computed `cur.OdometerKm - prev.OdometerKm` and clamped it directly.** The "Battery history" bars
SHALL represent the **battery level percentage** at each day's snapshot (absolute 0–100),
unchanged — read directly from `telemetry.Reader`, with no delta and no clamp, exactly as before
this change (the Battery chart was never a roadmap D5 violation). Each bar SHALL carry a hover
tooltip: the odometer bar's tooltip SHALL include the `MM-DD` date, the kilometres driven that
day (`DayDistance.KmDriven`), and the cumulative odometer in kilometres (`DayDistance.OdometerKm`
— both values already supplied by `internal/analytics`, not computed by the gateway); the battery
bar's tooltip SHALL include the `MM-DD` date, the level percentage, and the rated range in
kilometres. A missing-day bar's tooltip SHALL state that no snapshot exists for that date.

**The "Battery consumed" chart's bars SHALL represent the corrected per-day battery-consumed
percentage** returned by `analytics.Reader.ConsumedByDay` — bucketed on each returned
`DayConsumption.Date` value DIRECTLY, never re-derived or re-bucketed through the odometer/
battery charts' `EffectiveDate`-based UTC bucketing. A day with no corresponding
`DayConsumption` entry (no computable value for that calendar day) SHALL render as an empty
labeled bar identical in shape to the odometer/battery "no snapshot" bar, but with a "no data"
tooltip rather than a "no snapshot" tooltip. The gateway SHALL NOT perform any timezone
computation of its own for this chart — the calendar day a value belongs to is decided entirely
by `internal/analytics` before the gateway receives it. **The Odometer chart's bucket day is
likewise the port's own `DayDistance.Date`, bucketed DIRECTLY — never re-derived through
`effectiveDayUTC` — this is a CHANGE from the prior revision, under which the gateway itself
computed each snapshot's `EffectiveDate` UTC bucket for the odometer chart; `internal/analytics`
now performs that bucketing internally, using the identical `effectiveDay`/poller-zone logic
`ConsumedByDay` already used (so the Odometer and Battery charts' bucket days remain in the SAME
reference frame as before this change — only the consumed chart's own, separately-documented,
poller-zone-vs-UTC mismatch is unaffected by this change).** A known, accepted consequence
(unchanged): because `internal/analytics` and the Battery chart bucket calendar days in
different reference frames (the poller's configured zone vs. UTC), the same underlying nightly
poll can label the consumed bar's calendar day one day apart from its battery sibling bar; the
gateway SHALL NOT attempt to reconcile this.

**The "Battery consumed" chart SHALL be scaled RELATIVE to the window's own maximum displayed
value** (the tallest bar occupies 100% of the chart canvas), NOT an absolute 0–100 scale like
the Battery chart. A day whose value is suppressed per the flagged-day rule below SHALL
contribute exactly zero to that maximum, so a flagged day can never compress or distort the
scale other bars are drawn against.

**A "flagged" day** (`DayConsumption.Flagged == true`, indicating the platform's own gap
detection could not reconcile that day's charging records against its battery delta) SHALL
render its bar at ZERO height and SHALL carry a visually distinct warning marker — never the
raw (possibly negative) percentage value, and never omitted from the axis.

**A "multi-day span" day** (`DayConsumption.DaysSpanned > 1`, indicating a missed nightly poll
whose delta covers more than one calendar day) SHALL carry its OWN visually distinct marker,
separate from the flagged-day warning marker, and its tooltip SHALL state how many days it
covers.

**A day that is BOTH flagged AND a multi-day span SHALL carry BOTH markers**, and its tooltip
SHALL state BOTH facts — the day count it spans AND that a charge source is suspected missing —
never only one. This is a deliberate, owner-ruled ("picking one hides a fact that is true")
decision: markers are a SET a bar can carry zero, one, or both of, never a single mutually-
exclusive choice between "flagged" and "spanned."

A **non-spanned** flagged day's tooltip SHALL identify which charge source is suspected missing
(manual entry or Supercharger session) and SHALL NOT include the suppressed numeric value
anywhere. A **spanned** day's tooltip (flagged or not) SHALL always state its real
`ConsumedPct` value — the zero-height rendering that a flagged, non-spanned day gets never
applies to a spanned day's NUMBER; a spanned day's bar height is floored at the chart's zero
axis only because a bar cannot be drawn with negative height (a rendering-mechanics fact,
distinct from the flagged-day value-suppression rule above), never because the value is hidden
from the tooltip.

The date shown in each battery tooltip and per-bar label SHALL be the snapshot's
**`EffectiveDate`** (the calendar day the nightly snapshot represents — `CapturedAt` − 1 day),
formatted **`MM-DD`** by the Go handler; the odometer chart's date SHALL be `DayDistance.Date`
formatted the same way (already the equivalent bucket day, per the bucketing change above); the
consumed chart's date SHALL be `DayConsumption.Date` formatted the same way. The gateway SHALL
NOT show the capture-morning date (`CapturedAt`) and SHALL NOT format dates inside the template.
Each bar on every chart SHALL carry a per-bar **date label** rendered under the bar in an HTML
grid row (one cell per bar), with orientation decided by a single chart-level boolean flag
(`LabelVertical`): **horizontal** for the 6-bar window (wide bars), **rotated vertical** (via the
`[writing-mode:vertical-rl]` CSS class) for windows of 14 bars or more (narrow bars). The handler
SHALL set `LabelVertical` from the number of bars in the fixed window (`labelVerticalFor(numBars)`,
true when `numBars >= 14`) independently for each chart, not from a `days` count; the template
SHALL NOT compare the window size, compute rotation, or call `time.Format`.

The charts SHALL be rendered as **responsive inline SVG** (scaling to the container width) using
no client-side charting library. All numeric values — bar heights, deltas, percentages, tooltip
strings, per-bar label strings, and the consumed chart's marker classification — SHALL be
computed by the Go handler (for the Odometer and consumed charts: received already-computed from
`internal/analytics` and only scaled/formatted by the handler; for the Battery chart: computed by
the handler directly from the raw snapshot, unchanged) before the template renders; the template
SHALL perform no arithmetic, unit conversion, date formatting, or method calls on domain types,
and SHALL select every marker's visual class from a literal written in the template source, never
from a string computed by the handler. Bar colours and marker colours SHALL use DaisyUI semantic
tokens (no hardcoded hex).

The preset selector SHALL keep the 6/14/30 buttons but each button's `hx-get` SHALL emit a
server-rendered absolute `?start=<yesterday-N>&end=<yesterday>` href (computed by the handler at
render time, unchanged by this revision — the preset windows already targeted browser-yesterday
before the default/cap change above); the selector SHALL mark the preset whose `(start, end)`
matches the requested window as active. Changing the preset SHALL re-fetch
`GET /ui/dashboard/history?start=...&end=...` and re-render all three charts AND the selector by
swapping `#dashboard-history`'s `innerHTML` without a full page reload.

The dashboard page SHALL NOT carry a Refresh button; the `#dashboard-content` (subscribes to
`vehicle-changed from:body`) and `#dashboard-history` (self-loads on `hx-trigger="load"` with the
default 6-day window's absolute `start`/`end` ending yesterday, and re-renders on preset clicks)
htmx surfaces cover every refresh path. When too few data points exist to draw a chart (**zero
`analytics.DayDistance` entries returned by `OdometerDeltaByDay` for the odometer chart — a
CHANGE from the prior revision's "fewer than two snapshots total in the lookback window"
condition, now equivalent in effect since a day only appears in that result when both it and its
predecessor were computable**, none for the battery chart, or zero `DayConsumption` entries for
the consumed chart), that chart SHALL show the existing empty-state placeholder instead of
fabricated bars; a partially-missing axis (some days empty, some present) is NOT an empty chart
for any of the three.

Every user-facing string introduced or changed by this chart (its title, and every tooltip
clause it composes from — the plain value, the multi-day-span value, and the flagged note)
SHALL resolve through `i18n.T(ctx, key)` against `internal/gateway/i18n/catalog.go`, with both
`ES` and `EN` non-empty. Composing multiple clauses into one tooltip (e.g. for a day that is
both flagged and spanned) SHALL join independently-translated, complete clauses with a
language-neutral separator and SHALL NOT hardcode a connective word from any one language.

#### Scenario: History charts render for the selected vehicle with the default window, now ending yesterday

- **GIVEN** a signed-in user whose selected vehicle has several stored nightly snapshots and
  several computable `analytics.DayConsumption` days
- **WHEN** the history fragment is requested (`GET /ui/dashboard/history`) directly, with no
  `start` and no `end` parameter and no `browser_tz` cookie (a direct API call)
- **THEN** the response renders an "Odometer history" chart, a "Battery history" chart, and a
  "Battery consumed" chart for the selected vehicle using a 6-day window (`end = UTC-yesterday`,
  `start = UTC-yesterday-6`, `end` inclusive) — NOT `end = UTC-today` (the prior behavior)
- **AND** all three charts render exactly 6 bars, one per calendar day in
  `[UTC-yesterday-6 .. UTC-yesterday]`
- **AND** the odometer and battery charts display identical `MM-DD` labels under corresponding
  bars; the consumed chart's labels cover the same calendar range (see the bucketing-mismatch
  scenario below for why an individual label can differ by one day)
- **AND** no live Tesla Fleet API call is made and no telemetry or analytics table is read
  directly

#### Scenario: The end<=today cap now rejects end=today; only end<=yesterday is accepted

- **GIVEN** a signed-in user whose browser-local "today" is `2026-08-16`
- **WHEN** the history fragment is requested with `?start=2026-08-10&end=2026-08-16` (`end`
  equal to browser-today)
- **THEN** the endpoint responds with HTTP 400 — this request was ACCEPTED before this change
- **AND** the SAME request with `?end=2026-08-15` (`end` equal to browser-yesterday) is
  accepted (HTTP 200)
- **AND** the empty-state placeholder (no preset selector) is rendered for the rejected request,
  per the existing malformed-request degradation rule

#### Scenario: Direct API default and the dashboard's own preset/self-load windows now agree

- **GIVEN** a signed-in user whose browser-local "today" is `2026-08-16` (so
  browser-yesterday is `2026-08-15`)
- **WHEN** a direct API call omits both `start` and `end`, AND separately the dashboard page's
  own self-load href is inspected
- **THEN** both resolve to the identical window: `end = 2026-08-15`, `start = 2026-08-09`
  (the default 6-day width)
- **AND** this is a change from the prior revision of this requirement, under which the direct
  API default ended at `2026-08-16` (browser-today) while the dashboard's self-load already
  ended at `2026-08-15` (browser-yesterday) — that divergence no longer exists

#### Scenario: The odometer chart's delta and clamp are computed by analytics, not the gateway

- **GIVEN** two consecutive stored snapshots whose odometer readings differ by `-2.0` km (a
  clock-skew/read anomaly)
- **WHEN** `analytics.Reader.OdometerDeltaByDay` is called for the window containing that day
- **THEN** the returned `DayDistance.KmDriven` is `0.0` — the negative value is already floored
  by `internal/analytics`
- **AND** the gateway handler building the odometer chart performs no subtraction between two
  snapshots and no comparison against zero — it renders `DayDistance.KmDriven` directly, scaled
  relative to the window's maximum

#### Scenario: The odometer chart bucket day is the port's own Date, never re-derived

- **GIVEN** `analytics.Reader.OdometerDeltaByDay` returns a `DayDistance` entry with
  `Date = 2026-08-10`
- **WHEN** the odometer chart buckets that entry onto the fixed `[start..end]` axis
- **THEN** the entry's bar is placed at the `2026-08-10` slot using `Date` verbatim
- **AND** the gateway does NOT pass `Date` through `effectiveDayUTC` or any other re-bucketing
  step

#### Scenario: The consumed chart bucket day is the port's own Date, never re-derived

- **GIVEN** `analytics.Reader.ConsumedByDay` returns a `DayConsumption` entry with
  `Date = 2026-08-10`
- **WHEN** the consumed chart buckets that entry onto the fixed `[start..end]` axis
- **THEN** the entry's bar is placed at the `2026-08-10` slot using `Date` verbatim
- **AND** the gateway does NOT pass `Date` through the battery chart's `effectiveDayUTC` helper
  or any other re-bucketing step
- **AND** a known, accepted consequence is that this bar's calendar day can differ by one day
  from the battery bar for the same underlying nightly poll, because `internal/analytics` buckets
  in the poller's configured zone while the Battery chart buckets in UTC — this mismatch is NOT
  corrected by the gateway

#### Scenario: The consumed chart scales relative to its own window maximum, not absolute 0-100

- **GIVEN** a selected vehicle whose consumed-chart window contains entries with `ConsumedPct`
  values `8.0`, `20.0`, and one flagged entry
- **WHEN** the consumed chart is rendered
- **THEN** the bar heights are computed relative to `20.0` (the window's maximum displayed
  value), so the `8.0` entry renders at 40% of the chart canvas and the `20.0` entry at 100%
- **AND** the flagged entry contributes zero toward that maximum regardless of its own
  (suppressed) underlying value
- **AND** this differs from the "Battery history" chart, whose bars remain an absolute 0–100
  scale

#### Scenario: A flagged, non-spanned day renders as a zero-height bar with a warning marker and no numeric value

- **GIVEN** a `DayConsumption` entry with `Flagged = true`, `DaysSpanned = 1`, and
  `MissingChargingType = "MANUAL"`
- **WHEN** the consumed chart renders that day's bar
- **THEN** the bar's height is zero
- **AND** the bar carries a visually distinct warning marker, separate from the normal bar fill
  color and from the multi-day-span marker
- **AND** the bar's tooltip identifies a possible missing manual charge record for that date
- **AND** the tooltip does NOT contain the entry's underlying (suppressed) numeric percentage
  anywhere
- **AND** the bar is NOT omitted from the axis — its calendar-day slot and label remain present

- **GIVEN** the same entry but with `MissingChargingType = "SUPERCHARGER"` instead
- **WHEN** the consumed chart renders that day's bar
- **THEN** the tooltip identifies a possible missing Supercharger session instead of a manual
  entry, otherwise identically to the manual case

#### Scenario: A multi-day span renders its real value with its own marker, distinct from a flagged day

- **GIVEN** a `DayConsumption` entry with `DaysSpanned = 3`, `Flagged = false`, and a positive
  `ConsumedPct`
- **WHEN** the consumed chart renders that day's bar
- **THEN** the bar's height reflects the entry's real `ConsumedPct`, scaled relative to the
  window's maximum — NOT a zero-height bar
- **AND** the bar carries a visually distinct span marker, different from the flagged-day
  warning marker
- **AND** the bar's tooltip states the real percentage value AND that it covers 3 days

#### Scenario: A multi-day span that is also flagged carries BOTH markers and states both facts

- **GIVEN** a `DayConsumption` entry with `DaysSpanned = 2`, `Flagged = true`,
  `ConsumedPct = -3.0`, and `MissingChargingType = "MANUAL"`
- **WHEN** the consumed chart renders that day's bar
- **THEN** the bar carries BOTH the multi-day-span marker AND the flagged-day warning marker,
  simultaneously and independently visible — neither marker is suppressed in favor of the
  other (roadmap D21: "picking one hides a fact that is true")
- **AND** the bar's height is zero — for this entry that outcome is unambiguous either way: the
  chart's zero-axis floor (a negative height cannot be drawn) and the flagged-day
  value-suppression rule agree, because a flagged day's `ConsumedPct` is always `<= 0` by
  construction (tier 3 D5)
- **AND** the tooltip states the real, signed percentage value (`-3.0%`) and that the entry
  covers 2 days, AND separately notes a possible missing manual charge record — both facts
  present, neither omitted in favor of the other
- **AND** this is the roadmap's own worked overlap case: `Flagged` and `DaysSpanned > 1` are
  independent conditions on `DayConsumption`, so this combination is reachable in production,
  not merely a hypothetical fixture

#### Scenario: A day absent from ConsumedByDay renders as a "no data" bar, distinct wording from "no snapshot"

- **GIVEN** a calendar day within the requested window for which `analytics.Reader.ConsumedByDay`
  returned no entry (no computable value for that day)
- **WHEN** the consumed chart renders that day's slot
- **THEN** the bar renders at zero height with its own `MM-DD` label retained
- **AND** the tooltip states that no data exists for that date, using wording distinct from the
  battery chart's "no snapshot" tooltip (the absence reason for the consumed chart is
  broader than "no snapshot exists")
- **AND** a window with zero computable days across its entire range renders the consumed
  chart's empty-state placeholder instead of an all-empty bar row

#### Scenario: Consumed chart data comes exclusively through analytics.Reader

- **GIVEN** the gateway handler that builds the consumed chart
- **WHEN** it obtains per-day consumption data
- **THEN** it does so exclusively through `analytics.Reader.ConsumedByDay`
- **AND** it imports no package other than `internal/analytics`'s public port for this data (there
  is no `analytics` database package the gateway may import — `internal/analytics` owns
  `internal/analytics/db`, but that package is imported only inside `internal/analytics` itself)
- **AND** an `analytics.Reader` error degrades only the consumed chart to its empty state; the
  battery chart, sourced from the separate `telemetry.Reader` call, is unaffected by an
  `analytics.Reader` failure

#### Scenario: Odometer chart data comes exclusively through analytics.Reader

- **GIVEN** the gateway handler that builds the odometer chart
- **WHEN** it obtains per-day odometer distance data
- **THEN** it does so exclusively through `analytics.Reader.OdometerDeltaByDay`
- **AND** it imports no package other than `internal/analytics`'s public port for this data
- **AND** an `analytics.Reader` error building the odometer chart degrades only the odometer
  chart to its empty state; the battery chart, sourced from the separate `telemetry.Reader`
  call, is unaffected

#### Scenario: Browser-Local Calendar Day (unchanged from the prior revision)

- **GIVEN** a signed-in user whose browser sent a `browser_tz` cookie with a valid IANA zone —
  for any zone, negative or positive UTC offset
- **WHEN** the history fragment is requested with no `start`/`end` parameter
- **THEN** the default window's `end` is midnight of browser-yesterday IN THAT ZONE, not UTC
  midnight
- **AND** on a missing, empty, or unparseable `browser_tz` cookie, the gateway falls back to
  `time.UTC` silently — no error surfaced, no caller special-casing required

#### Scenario: Charts contain no business logic in templates

- **GIVEN** the history chart and selector templates, including the new consumed-chart marker
  rendering
- **WHEN** they render
- **THEN** the bar heights, per-day deltas/percentages, tooltip strings, per-bar `MM-DD` label
  strings, the `Present` flag, the flagged-marker and multi-day-span-marker flags (independent
  of each other — a bar can carry both), and the `LabelVertical` orientation flag have all been
  computed by the Go handler before the template receives the view model
- **AND** the templates use only presentation logic (if/for/display) — no arithmetic, no unit
  conversion, no date formatting, no method calls on domain types
- **AND** every marker's CSS class (e.g. the warning color for a flagged bar, the info color for
  a span bar) is a literal string written in the `.templ` source, never a string value computed
  in a `.go` handler file and passed through as an attribute
- **AND** the SVG scales to the container width (responsive) and bar/marker colours use DaisyUI
  semantic tokens with no hardcoded hex
- **AND** the per-bar label is a real DOM text cell in an HTML grid (not an SVG `<text>`), using
  a muted semantic token — no client-side library

#### Scenario: Gateway never imports telemetrydb or an analytics database package for history

- **GIVEN** the gateway handler that builds all three history charts
- **WHEN** it obtains the vehicle's snapshot history, its per-day odometer distance, and its
  per-day consumption
- **THEN** it does so exclusively through the `telemetry.Reader` and `analytics.Reader` public
  interfaces
- **AND** it imports no package from `internal/telemetry/db` (`telemetrydb`) and no package from
  `internal/analytics/db` (`analyticsdb`)
- **AND** no `pgtype` type appears in any gateway file involved

#### Scenario: History fragment is not served to anonymous callers

- **GIVEN** an unauthenticated request to `GET /ui/dashboard/history`
- **WHEN** the handler resolves the session
- **THEN** the request is redirected to `/login` and no history data (including the consumed
  chart) is served

#### Scenario: All new consumed-chart strings are bilingual

- **GIVEN** the consumed chart's title, and every clause its tooltip composes from (the plain
  percentage value, the multi-day-span value, and the flagged note)
- **WHEN** the catalogue is inspected
- **THEN** every corresponding key has both an `ES` and an `EN` value, neither empty
- **AND** no consumed-chart string is a hardcoded literal bypassing `i18n.T`
- **AND** a tooltip composed from more than one clause (e.g. a day that is both flagged and
  spanned) joins the independently-translated clauses with a language-neutral separator, never
  a connective word hardcoded from one language

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
vehicle. The page SHALL show that vehicle's Tesla-billed Supercharger / DC fast-charging
session history sourced exclusively from the `charging.SessionReader` port; the gateway SHALL
NOT read `charge_sessions` or any other table directly and SHALL NOT make a live Tesla Fleet
API call to render the page. **This is a CHANGE from the prior revision of this requirement,
under which the page's source was `telemetry.SuperchargerReader` over `supercharger_sessions`
— the raw ingestion buffer, not the record the platform's `charging` capability owns.**

The page SHALL be served by two authenticated routes: `GET /supercharger-stats` (the full
page, initial load) and `GET /ui/supercharger-stats` (the htmx fragment, swapped by the
window selector) — mirroring the `/charges` + `/ui/charges` pairing. Anonymous requests to
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

### Requirement: Unattributed Supercharger sessions are out of scope

The gateway SHALL NOT show or count, anywhere on the Supercharger Stats page, a charge session
whose `TeslaID` is `NULL` (the session's VIN does not match any currently-registered vehicle on
the account) — not in the tiles, not in the chart, not in the table. This is a deliberate,
specified limitation: the page's single read is scoped by `TeslaID` (`ListSessionsByVehicleBetween`),
so such sessions can never match the filter by construction. The gateway SHALL NOT perform a
second, account-wide read to discover or disclose these sessions. **This requirement's
mechanism is unchanged from the prior revision — only the underlying port name changed, from
`telemetry.SuperchargerReader.SuperchargerSessionsByVehicle` to
`charging.SessionReader.ListSessionsByVehicleBetween` — both exclude a `NULL`-`TeslaID` row by
the identical SQL-equality mechanism.**

#### Scenario: A session with no matching registered vehicle is silently excluded

- **GIVEN** a charge session stored for the account whose VIN does not match any of the
  account's currently-registered vehicles (its `TeslaID` is `NULL`)
- **AND** the account also has at least one charge session that DOES match the selected
  vehicle
- **WHEN** the Supercharger Stats page is rendered for the selected vehicle
- **THEN** the unattributed session does not appear in the sessions table
- **AND** its energy and cost are not included in any KPI tile or chart bucket
- **AND** no error, warning, or "N sessions hidden" notice is shown for it
- **AND** only one read (`ListSessionsByVehicleBetween`) is made — no second, account-wide
  read is performed to check for unattributed sessions

### Requirement: Supercharger Stats month-window selector

The page SHALL offer a window selector with a fixed, closed preset set of **{3, 6, 12}**
months, each rendered as a button whose `hx-get` is a server-computed absolute
`?start=YYYY-MM-DD&end=YYYY-MM-DD` href — **never a `?months=N` query parameter, a CHANGE from
the prior revision of this requirement, which accepted `?months=N` directly.** The underlying
HTTP contract SHALL be `?start=YYYY-MM-DD&end=YYYY-MM-DD` (both whole UTC calendar days, `end`
inclusive), following the platform's single closed `?start=&end=` date-filter vocabulary
(`internal/gateway/AGENTS.md` §"HTTP date-filter convention") — this brings the endpoint into
compliance with a convention it previously violated.

When both `start` and `end` are absent, the endpoint SHALL apply the default **6-month**
window: `end` = today, `start` = the 1st of the month 5 months before today's month (a
month-aligned window, preserving the pre-existing chart-bucket behavior of exactly 6 monthly
bars). Each of the 3/6/12-month preset buttons SHALL be computed the same way, so selecting any
preset always renders exactly that many monthly chart bars, never one more or fewer.

A missing partner (`start` without `end` or vice versa), a malformed non-ISO date, an `end`
earlier than `start`, an `end` later than today, or a window wider than **400 days** SHALL be
rejected with HTTP 400; on a
400 the region SHALL render its empty state with NO window selector — mirroring the dashboard
history endpoint's malformed-request degradation. The 400-day cap (wider than the dashboard
history endpoint's 90-day cap) reflects the sparser row density of charge-session data compared
to per-day telemetry snapshots.

The selected window SHALL drive the tiles, the chart, and the table identically — all three
sections SHALL reflect the same filtered set of sessions. Selecting a different preset SHALL
re-fetch and re-render the whole Supercharger Stats region without a full page reload.

#### Scenario: Window defaults to the month-aligned 6-month window when both params are absent

- **GIVEN** a signed-in user on the Supercharger Stats page
- **WHEN** the fragment is requested with no `start` and no `end` parameter
- **THEN** the page renders using a window ending today and starting on the 1st of the month
  5 months before today's month
- **AND** the tiles, the chart, and the table all reflect the same window
- **AND** the kWh-per-month chart renders exactly 6 bars

#### Scenario: An explicit valid start/end window is honored exactly as given

- **GIVEN** a signed-in user on the Supercharger Stats page
- **WHEN** the fragment is requested with a well-formed `?start=&end=` pair where `end` is not
  before `start` and the window is 400 days or narrower
- **THEN** the page renders using exactly that window, un-rounded to any month boundary
- **AND** no HTTP 400 is returned

#### Scenario: A malformed or partial date request is rejected with 400 and no selector

- **GIVEN** a signed-in user on the Supercharger Stats page
- **WHEN** the fragment is requested with either a non-ISO-format `start` or `end` value, or
  with only one of `start`/`end` present
- **THEN** the endpoint responds with HTTP 400
- **AND** the region renders its empty state with no window selector
- **AND** no Supercharger read is performed

#### Scenario: An end date earlier than the start date is rejected with 400

- **GIVEN** a signed-in user on the Supercharger Stats page
- **WHEN** the fragment is requested with `end` earlier than `start`
- **THEN** the endpoint responds with HTTP 400
- **AND** the region renders its empty state with no window selector

#### Scenario: An end date after today is rejected with 400, but an end date of today is accepted

- **GIVEN** a signed-in user on the Supercharger Stats page
- **WHEN** the fragment is requested with an `end` date later than today, within the 400-day cap
- **THEN** the endpoint responds with HTTP 400
- **AND** the region renders its empty state with no window selector
- **AND** no Supercharger read is performed
- **AND** the SAME request with `end` set to today is accepted (HTTP 200), because charge
  sessions are readable on the day they end — unlike the dashboard history endpoint, which
  rejects an `end` of today owing to its nightly capture lag

#### Scenario: A window wider than 400 days is rejected with 400

- **GIVEN** a signed-in user on the Supercharger Stats page
- **WHEN** the fragment is requested with a `start`/`end` pair spanning more than 400 days
- **THEN** the endpoint responds with HTTP 400
- **AND** the region renders its empty state with no window selector
- **AND** the SAME request narrowed to exactly 400 days or fewer is accepted (HTTP 200)

#### Scenario: Changing the window preset re-fetches the whole region

- **GIVEN** a signed-in user viewing the Supercharger Stats page with the default window
- **WHEN** the user selects a different preset (3 or 12 months)
- **THEN** the region re-fetches `GET /ui/supercharger-stats?start=...&end=...` for the
  newly-selected window
- **AND** the tiles, chart, and table all re-render for that window without a full page reload
- **AND** the selected preset is marked active in the window-selector control

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

### Requirement: Full-Application Translation Coverage

The gateway SHALL resolve every user-facing string it renders — on every page (`/`, `/login`,
`/dashboard`, `/dashboard/history`, `/charges`, `/supercharger-stats`), every htmx fragment those
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
- **WHEN** they load `/charges`
- **THEN** the create form's field labels, the "Home"/"Work"/"Other" and "AC"/"DC" option text,
  the "Log charge" submit button, the entries table's "Your entries" heading and empty-state
  sentence, and each row's "Edit"/"Delete" actions all render in English
- **AND** opening a row's inline edit form (`GET /ui/charges/row/:id/edit`) renders that form's
  field labels and its "Save"/"Cancel" actions in the same resolved language

#### Scenario: The Supercharger Stats page renders fully translated

- **GIVEN** a signed-in user whose resolved language is `es`
- **WHEN** they load `/supercharger-stats` with at least one Supercharger session recorded
- **THEN** the month-preset selector, the four KPI tile labels, both chart/table card titles, and
  the sessions table's column headers render in Spanish

#### Scenario: A handler-produced validation error renders translated

- **GIVEN** a signed-in user submitting `POST /ui/charges/create` with a missing required field
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

### Requirement: Automated Hardcoded-String Verification

The gateway module SHALL provide an automated, locally runnable check that a developer or
reviewer can execute to verify no new hardcoded, untranslated user-facing string has been
introduced into `templates/pages/`, `templates/fragments/`, `templates/ui/`, or
`internal/gateway/handlers/`. The check SHALL be part of the module's standard verification gate
(reachable via the same command that already runs the module's other static-analysis guards) and
SHALL support an explicit, self-documenting, per-line exemption for a literal that is legitimately
not translatable.

#### Scenario: The check passes on a fully-translated codebase

- **GIVEN** the gateway module's `templates/` and `handlers/` source after this tier's sweep
- **WHEN** the verification check runs
- **THEN** it completes with a success/zero exit status and reports no violation

#### Scenario: The check fails when a new hardcoded string is introduced

- **GIVEN** a `.templ` file with a new element containing bare, untranslated English text (not
  wrapped in a translation-catalogue lookup)
- **WHEN** the verification check runs
- **THEN** it fails with a non-zero exit status and reports the offending file and line

#### Scenario: The check fails when a handler assigns a literal string to a user-facing message field

- **GIVEN** a handler function that assigns a hardcoded string literal to a notice, error, or
  validation-message field that is later rendered to the user
- **WHEN** the verification check runs
- **THEN** it fails with a non-zero exit status and reports the offending file and line

#### Scenario: A legitimately non-translatable literal is exempted via an explicit, visible marker

- **GIVEN** a string literal that is not user-facing prose (e.g. an operational health-check
  response body consumed by monitoring tooling, never rendered to a user)
- **WHEN** that literal carries the documented exemption marker
- **THEN** the verification check does not flag it
- **AND** the exemption is visible in the source at the exact line it applies to, requiring no
  separate file to cross-reference

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
tiles' per-currency cost lines, the Charge log entry list's price column, and the Charge log
entry list's cost-per-kWh column.

#### Scenario: Supercharger session cost is comma-grouped

- **GIVEN** a Supercharger session with `TotalCost = 58000` and `Currency = "COP"`
- **WHEN** the Supercharger Stats sessions table renders that session's row
- **THEN** the cost cell reads `"58,000.00 COP"`

#### Scenario: Charge log price is comma-grouped

- **GIVEN** a manual charge entry with `Price = 12500` and `Currency = "COP"`
- **WHEN** the Charge log entry list renders that entry's row
- **THEN** the price label reads `"12,500.00 COP"`

#### Scenario: Charge log cost-per-kWh is comma-grouped and keeps its unit suffix

- **GIVEN** a manual charge entry whose computed cost per kWh is `1200` in currency `COP`
- **WHEN** the Charge log entry list renders that entry's cost-per-kWh label
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

### Requirement: Manual Charge Write Path Triggers Analytics Recalculation

The gateway SHALL call the analytics module's recalculation port for the affected calendar day
after it commits a create, update, or delete of a manually-logged charge entry, before
responding to the caller. For an edit that changes the entry's date, the gateway SHALL
recalculate both the old and the new day. This ensures a subsequent read of that vehicle's
history charts reflects the change immediately.

#### Scenario: Creating a manual charge entry recalculates its day before the response is sent
- **GIVEN** an authenticated user submitting a new manually-logged charge entry for a given date
- **WHEN** the create request is handled and the entry is successfully persisted
- **THEN** the gateway calls the analytics recalculation port for that entry's date before
  returning its response
- **AND** a request for that vehicle's consumed chart, made immediately after, reflects the new
  entry with no separate refresh step

#### Scenario: Deleting a manual charge entry recalculates the entry's original day
- **GIVEN** an authenticated user deleting an existing manually-logged charge entry dated
  `2026-08-10`
- **WHEN** the delete request is handled and the entry is successfully removed
- **THEN** the gateway calls the analytics recalculation port for `2026-08-10` after the delete
  commits
- **AND** it resolves that date from the entry BEFORE the delete removes it — the delete port
  itself does not return the deleted entry's date

#### Scenario: A failed write does not trigger a recalculation call
- **GIVEN** an authenticated user submitting a charge-entry write that fails validation or the
  underlying write itself fails
- **WHEN** the request is handled
- **THEN** the gateway does not call the analytics recalculation port

### Requirement: Supercharger Stats monthly chart has readable month and kWh axes

The gateway SHALL continue to render the Supercharger kWh-per-month chart through the shared
`HistoryChart` / `HistoryBar` and `historyBarChart` rendering contract. For every calendar-month
bucket in the requested window, the gateway SHALL provide a bar label formatted exactly
`YYYY-MM`, including zero-energy months. It SHALL provide y-axis ticks by reusing the existing
`buildYAxisTicks` behavior with kWh-formatted labels; it SHALL NOT introduce a second tick
algorithm, chart renderer, chart library, client-side chart code, or template arithmetic.

The chart SHALL render its `YYYY-MM` labels vertically so they remain legible across the page's
3-, 6-, and 12-month selector windows. If the tallest bucket is zero, the chart SHALL retain its
bars and vertical-label selection but render no y-axis ticks, following the existing shared
chart behavior.

#### Scenario: Each month has a machine-readable label and kWh scale

- **GIVEN** Supercharger sessions in a March through May window with monthly totals of 10, 0,
  and 30 kWh
- **WHEN** the Supercharger Stats chart is built
- **THEN** its bars have labels `2026-03`, `2026-04`, and `2026-05` in that order
- **AND** the May bar has a 100 percent relative height
- **AND** the chart has the shared five kWh y-axis ticks from the maximum down to zero
- **AND** its labels are vertical

#### Scenario: A non-empty zero-energy chart has no fabricated axis scale

- **GIVEN** a selected-window session set whose non-nil energy values total zero in every month
- **WHEN** the Supercharger Stats chart is built
- **THEN** it renders the monthly bars at zero height with their `YYYY-MM` labels
- **AND** it has no y-axis ticks
- **AND** it does not fabricate a positive kWh maximum

### Requirement: Supercharger Stats session table displays battery percentages

The Supercharger Stats session table SHALL add four columns for the existing `charging.Session`
battery values: start battery percentage, end battery percentage, start battery percentage
estimate, and end battery percentage estimate. The gateway SHALL map each value to a
preformatted view-model string before rendering: a present value is rendered as its integer
percentage followed by `%`, and a nil value is rendered exactly as `"—"`.

The table SHALL obtain these values from the existing `charging.SessionReader` result only; it
SHALL NOT issue an additional read, write, Tesla API call, or database query. Both estimate
fields SHALL continue to render `"—"` while they are NULL; the gateway SHALL NOT calculate or
persist an estimate. The table SHALL retain Date, Site, Energy, and Cost and SHALL NOT restore
Country or Billing Type.

Every new header SHALL resolve through the existing gateway i18n catalogue with non-empty ES and
EN translations.

#### Scenario: Populated session battery values render in the table

- **GIVEN** a Supercharger session whose start, end, start-estimate, and end-estimate values are
  40, 80, 42, and 78
- **WHEN** the Supercharger Stats page is rendered
- **THEN** its row displays `40%`, `80%`, `42%`, and `78%` in the four battery columns
- **AND** the table displays translated headers for all four columns in the active language
- **AND** Date, Site, Energy, and Cost remain displayed

#### Scenario: Missing battery values degrade visibly without an estimator

- **GIVEN** a Supercharger session whose four battery values are NULL
- **WHEN** the Supercharger Stats page is rendered
- **THEN** all four battery cells display `"—"`
- **AND** no cell displays `0%`, an empty string, or a calculated estimate

#### Scenario: Country remains absent

- **GIVEN** any Supercharger Stats page render
- **WHEN** the session table is rendered
- **THEN** it contains no Country header or Country cell
- **AND** the gateway does not add a Country i18n key for this page

### Requirement: Supercharger session battery percentages are correctable inline

The gateway SHALL let a signed-in user, on `/supercharger-stats`, correct the `start_battery_pct`
and `end_battery_pct` of one of their own account's Supercharger sessions inline, by swapping
that session's table row for an editable row and saving through the existing
`charging.SessionVerifier.VerifySession` port. The gateway SHALL NOT add a delete action for a
Supercharger session, and SHALL NOT add a new `charging` read or write port for this feature —
the write goes through `SessionVerifier` exactly as it already exists, and any read the gateway
needs to resolve one session by id SHALL be performed by listing the account/vehicle-scoped,
`?start=&end=`-windowed session set and matching the id in memory, never by adding a by-id method
to `charging.SessionReader`.

The edit row SHALL render the session's date, site, energy, cost, and both battery percentage
ESTIMATE fields as read-only display text; ONLY the two verified battery percentage fields SHALL
be editable inputs. The gateway SHALL NOT write `battery_pct_source`, `start_battery_pct_est`, or
`end_battery_pct_est` from any value it receives from this row — `battery_pct_source` is always
computed by `VerifySession` itself, and the two estimate columns are not reachable through this
write path at all.

#### Scenario: A user edits and saves a session's battery percentages

- **GIVEN** a signed-in user viewing `/supercharger-stats` with a session row showing start
  battery 40% and end battery 80%
- **WHEN** they click Edit on that row, change the values to 42% and 82%, and click Save
- **THEN** the gateway calls `charging.SessionVerifier.VerifySession` with the new values for
  that session's id, scoped to the caller's account
- **AND** on success the row swaps back to its static display showing 42% and 82%
- **AND** no other row, tile, or chart in the page changes

#### Scenario: No delete action exists for a Supercharger session

- **GIVEN** a signed-in user viewing a Supercharger session row, in either its static or edit
  state
- **WHEN** they inspect the available row controls
- **THEN** no delete control, delete route, or delete confirmation exists for a Supercharger
  session anywhere on the page

#### Scenario: Editing a session's battery percentages does not change the KPI tiles or the chart

- **GIVEN** a signed-in user who successfully edits and saves a session's battery percentages
- **WHEN** the row swaps back to its static display
- **THEN** the page's KPI tiles (session count, energy, cost, average kWh/session) and the
  kWh-per-month chart are NOT refreshed or altered by the edit — neither value is derived from a
  battery percentage

### Requirement: Supercharger session battery edit uses a strict PATCH body — an absent field is rejected, an empty field clears it

The gateway SHALL accept the save as an `HTTP PATCH` to a per-session route. The request body
SHALL be REQUIRED to contain BOTH the `start_battery_pct` and `end_battery_pct` form fields as
present keys — a request missing either key SHALL be rejected with `HTTP 400` and SHALL NOT
result in any call to `VerifySession`. A present key whose value is empty SHALL be treated as an
explicit instruction to clear that percentage (passed as `nil`/NULL to `VerifySession`), NOT as
an error and NOT as "leave the existing value unchanged."

When BOTH percentages are cleared in the same request, the gateway SHALL rely on
`VerifySession`'s own behavior of also clearing `battery_pct_source` to NULL in the same write —
the gateway SHALL NOT attempt to set or preserve a source value itself.

A present, non-empty value for either field SHALL be validated as an integer in `[0, 100]`
BEFORE any call to `VerifySession`; a value outside that range or not a valid integer SHALL be
rejected with `HTTP 422`, a field-specific error message attached to the offending field, and
the submitted (unmodified) values redisplayed in the re-rendered edit row — without calling
`VerifySession`. The gateway SHALL NOT reject a request where the new end percentage is less
than the new start percentage — no relative ordering is enforced.

#### Scenario: A request missing one of the two required keys is rejected

- **GIVEN** a signed-in user's browser issuing `PATCH` to a session's row route with a body that
  contains `start_battery_pct` but no `end_battery_pct` key at all
- **WHEN** the gateway handles the request
- **THEN** the response is `HTTP 400`
- **AND** `VerifySession` is never called
- **AND** no session data is altered

#### Scenario: Both percentages present but empty clears both the percentages and the source

- **GIVEN** a signed-in user's edit row submitting `start_battery_pct=` and `end_battery_pct=`
  (both keys present, both values empty)
- **WHEN** the gateway handles the `PATCH`
- **THEN** `VerifySession` is called with both percentages `nil`
- **AND** on success the row's static display shows both battery cells as empty ("—")
- **AND** the underlying session's `battery_pct_source` is cleared to NULL as a consequence of
  `VerifySession`'s own behavior, not a separate gateway write

#### Scenario: An out-of-range value is rejected per-field without writing

- **GIVEN** a signed-in user's edit row submitting `start_battery_pct=101` and a valid
  `end_battery_pct`
- **WHEN** the gateway handles the `PATCH`
- **THEN** the response is `HTTP 422` and the edit row is re-rendered with a field-specific error
  on the start battery percentage input
- **AND** `VerifySession` is never called
- **AND** the submitted value `101` remains visible in the re-rendered input, not silently reset

#### Scenario: A lower end percentage than the start percentage is accepted

- **GIVEN** a signed-in user's edit row submitting a `start_battery_pct` of 80 and an
  `end_battery_pct` of 40
- **WHEN** the gateway handles the `PATCH`
- **THEN** `VerifySession` is called with both values exactly as submitted
- **AND** the request is not rejected for the values being in a decreasing order

### Requirement: A successful battery-percentage save immediately recalculates the affected vehicle's derived metrics

The gateway SHALL, immediately after a Supercharger session's battery percentages are
successfully verified, call `analytics.Recalculator.Recalculate` for the session's vehicle,
bounded to a window of exactly one calendar day before through one calendar day after the UTC
calendar day of the session's `ChargeStopDateTime` (inclusive on both ends) — a
THREE-calendar-day window total — rather than the session's own calendar day alone. The gateway
SHALL derive this window
using the session's `ChargeStopDateTime`, not its `ChargeStartDateTime`, and SHALL compute the
UTC calendar day without consulting any per-request or per-browser timezone. A `Recalculate`
error SHALL be logged and SHALL NOT be surfaced to the user or fail the request — the save has
already committed successfully regardless of the recalculation outcome.

When the verified session's vehicle identifier is absent (its VIN does not currently match a
registered vehicle on the account), the gateway SHALL skip the recalculation call entirely,
SHALL log that it did so, and SHALL still return the successful save response to the user.

This recalculation call SHALL be performed by logic separate from the existing manual-charge
write path's own single-day recalculation helper; the gateway SHALL NOT widen or otherwise alter
the manual-charge path's existing single-day recalculation window to accommodate this
requirement.

#### Scenario: A session stopping just after UTC midnight still recalculates its true metric day

- **GIVEN** a Supercharger session whose `ChargeStopDateTime` is 20 minutes after UTC midnight
  on a given calendar day
- **WHEN** its battery percentages are successfully verified
- **THEN** `analytics.Recalculator.Recalculate` is called for that vehicle with a window spanning
  from one calendar day before that UTC date through one calendar day after it
- **AND** the window is derived solely from `ChargeStopDateTime`, ignoring any different calendar
  day carried by the same session's `ChargeStartDateTime`

#### Scenario: A verified session with no resolvable vehicle skips recalculation but still saves

- **GIVEN** a Supercharger session whose VIN does not match any of the account's currently
  registered vehicles
- **WHEN** its battery percentages are successfully verified
- **THEN** the save succeeds and the row's static display reflects the new values
- **AND** `analytics.Recalculator.Recalculate` is never called
- **AND** the gateway logs that recalculation was skipped

#### Scenario: A recalculation failure does not undo or fail the save

- **GIVEN** a Supercharger session whose battery percentages are successfully verified and whose
  vehicle IS resolvable
- **WHEN** the subsequent `analytics.Recalculator.Recalculate` call returns an error
- **THEN** the save response the user sees is still successful, showing the newly verified values
- **AND** the recalculation error is logged, not surfaced to the user

#### Scenario: The manual-charge recalculation window is unaffected

- **GIVEN** the existing manual charge log write path (`ChargeCreate`, `ChargeRowUpdate`,
  `ChargeRowDelete`)
- **WHEN** any of those handlers triggers its existing post-write recalculation
- **THEN** it continues to recalculate only the single `ChargedOn` calendar day (and, for an
  update that changed the date, the single prior `ChargedOn` day) exactly as before this change
- **AND** it does not use a `±1 day` window

### Requirement: Supercharger session battery edit is CSRF-protected and account-scoped

The gateway SHALL require a valid CSRF token, issued for this feature's own session key and
checked via the gateway's existing generic CSRF-check mechanism, on every state-changing request
against a Supercharger session's battery percentages (the `PATCH` save); a missing, stale, or
mismatched token SHALL be rejected with `HTTP 403` without calling `VerifySession`. The token used
for this feature SHALL be distinct from the manual-charge-log feature's own CSRF token, and SHALL
be issued only by the Supercharger Stats page/fragment routes, never re-issued by the row-level
edit, cancel, or save routes themselves.

The gateway SHALL rely on `VerifySession`'s own account-scoped `WHERE` clause as the sole tenant
boundary for the save — it SHALL NOT perform an additional, separate vehicle-ownership check
before calling `VerifySession`, unlike the manual-charge-log write path's explicit
`RegisteredVehicles` check. A session belonging to a different vehicle on the SAME account remains
writable through this route; a session belonging to a DIFFERENT account's data SHALL NOT be
reachable or alterable through any account-scoped id.

#### Scenario: A save with no CSRF token ever issued for this session is rejected

- **GIVEN** a signed-in user's session that has never loaded `/supercharger-stats` or
  `/ui/supercharger-stats` (so no Supercharger-specific CSRF token has ever been issued)
- **WHEN** a `PATCH` is sent to a session's row route with an otherwise well-formed body
- **THEN** the response is `HTTP 403`
- **AND** `VerifySession` is never called

#### Scenario: A stale or mismatched CSRF token is rejected

- **GIVEN** a signed-in user whose session holds a Supercharger CSRF token that does not match
  the token submitted with the `PATCH` request
- **WHEN** the gateway handles the request
- **THEN** the response is `HTTP 403`
- **AND** `VerifySession` is never called

#### Scenario: A session id belonging to a different account is never altered

- **GIVEN** a signed-in user submitting a well-formed, CSRF-valid `PATCH` naming a session id
  that belongs to a different account
- **WHEN** the gateway calls `VerifySession`
- **THEN** the write matches zero rows (the account-scoped `WHERE` clause excludes it)
- **AND** no data belonging to the other account is altered

### Requirement: A Supercharger session row not in the currently requested window is treated as not found

The gateway SHALL resolve a Supercharger session's edit, cancel-to-static, and save actions by
re-listing that vehicle's sessions within the SAME `?start=&end=` window the row's action link
carries (matching the window the table was rendered under), then matching the requested session
id within that result set — never by issuing an unbounded or by-id lookup. A session id that does
not fall within the requested window SHALL be treated identically to a session id that does not
exist at all: `HTTP 404` on a `GET` (edit or cancel-to-static), and the same not-found outcome on
a `PATCH` whose underlying write also cannot be resolved.

#### Scenario: Requesting the edit form for a session id outside the given window returns not found

- **GIVEN** a hand-built `GET` request to a session's edit-row route carrying a `?start=&end=`
  window that does not include the named session id
- **WHEN** the gateway handles the request
- **THEN** the response is `HTTP 404`
- **AND** no edit form is rendered

#### Scenario: Every row's own action links carry the table's rendered window

- **GIVEN** a signed-in user viewing `/supercharger-stats` for a given `?start=&end=` window
- **WHEN** the page renders each session row's Edit, Cancel, and Save action URLs
- **THEN** each of those URLs carries the SAME `start` and `end` values the table itself was
  rendered with

