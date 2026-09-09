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
or resolve the account for the Google identity (through the account module) and, **only when that
account's activation status is Active**, establish an authenticated session for that account. When
the callback request carries an explicitly-present `lang` cookie and the account is Active, the
gateway SHALL persist that language to the resolved account via `account.Service.SetLanguage` so a
language chosen before authentication carries into the signed-in session. An account whose status
is not Active is handled per the "Inactive Account Is Blocked At Login" requirement instead of
being signed in.

#### Scenario: Starting the login flow

- **GIVEN** an anonymous visitor
- **WHEN** they begin Google login
- **THEN** the gateway redirects them to Google's consent screen with a state parameter

#### Scenario: Successful callback provisions the account and signs in an Active account

- **GIVEN** a visitor returning from Google with a valid authorization code and matching state,
  whose resolved account has status Active
- **WHEN** the gateway handles the callback
- **THEN** it resolves the Google identity (id, email, name)
- **AND** provisions or resolves the account via the account module
- **AND** establishes an authenticated session for that account

#### Scenario: A pre-login language cookie carries into the account, only when the account is Active

- **GIVEN** a visitor who selected `en` on the login page (setting a `lang=en` cookie) before
  authenticating, whose resolved account has status Active
- **WHEN** the Google callback establishes their session
- **THEN** the gateway calls `account.Service.SetLanguage` with `"en"` for the resolved account

#### Scenario: A callback with no language cookie leaves the account's language untouched

- **GIVEN** a visitor whose callback request carries no `lang` cookie
- **WHEN** the Google callback establishes their session
- **THEN** the gateway does NOT call `account.Service.SetLanguage`

#### Scenario: A callback resolving an Inactive account never reaches the language sync or session steps

- **GIVEN** a visitor returning from Google with a valid authorization code and matching state,
  whose resolved account has status Inactive
- **WHEN** the gateway handles the callback
- **THEN** it does not call `account.Service.SetLanguage`
- **AND** it does not establish a session

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
vehicle card with the vehicle's latest precomputed vehicle-status row. The data
SHALL come exclusively from the `analytics.Reader` port (`LatestMetricsByAccount`)
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
- **AND** the analytics `Reader.LatestMetricsByAccount` call returns an error
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

### Requirement: Navigation Vehicle Header

The gateway SHALL render a vehicle header in the navigation shell that
shows the active vehicle's display name, battery level, and a
freshness-derived status. The data SHALL come exclusively from existing
read ports: `account.RegisteredVehicles` (vehicle name) and
`analytics.Reader.LatestMetricsByAccount` (battery level + capture instant).
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
Battery chart's data SHALL come exclusively from the `analytics.Reader.BatteryLevelByDay` port;
the Odometer chart's data SHALL come exclusively from the `analytics.Reader.OdometerDeltaByDay`
port; the consumed chart's data SHALL come exclusively from the `analytics.Reader.ConsumedByDay`
port — this is a CHANGE from the prior revision of this requirement, under which the Battery
chart was sourced from `telemetry.Reader.SnapshotsByVehicleBetween` (RM40-gateway-drop-telemetry-
dependency, roadmap D1: `internal/analytics` already owns the precomputed table this data lives
in and already serves the other two charts from it). After this change the gateway's history
fragment reads exclusively through `analytics.Reader` — `internal/telemetry` is not named
anywhere in `internal/gateway/` for any purpose.** The gateway SHALL NOT read telemetry or
analytics tables directly and SHALL NOT make a live Tesla Fleet API call to render any of the
three charts.

The charts SHALL be served by an authenticated htmx fragment endpoint `GET /ui/dashboard/history`
that accepts **`?start=YYYY-MM-DD&end=YYYY-MM-DD`** — both whole calendar days, UTC-midnight-
bounded, `end` **inclusive** — never a `?days=N` count. The endpoint SHALL resolve the target
vehicle from the session-selected vehicle (never from the URL) and scope the read to that
vehicle's Tesla id and the caller's account. Anonymous requests SHALL be redirected to `/login`
with no history data served.

**"Today" for this endpoint's validation and defaulting is the browser's local calendar day, not
the server's UTC day** — derived from the `browser_tz` cookie, falling back to the platform's
default time zone, `clock.Zone()` (`America/Bogota`), on any failure (absent cookie, empty value,
unparseable IANA zone) — the cookie itself always wins whenever present. See the
"Browser-Local Calendar Day" scenario below.

The `start`/`end` params SHALL be validated by a single helper (`parseHistoryRange`): when both
are absent the endpoint SHALL apply the default 6-day window (`end = browser-yesterday` midnight,
`start = end-6`, because the nightly batch captures today's data tomorrow — an `end = today`
window's last bar is always empty); a missing partner, a malformed non-ISO date, an `end` earlier
than `start`, an `end` later than **browser-yesterday** (an `end` equal to browser-TODAY is
rejected), or a window wider than 90 days SHALL be rejected with HTTP 400.

**The endpoint SHALL fetch each chart's window through its OWN port call, with NO gateway-level
lookback for any of the three — this is a CHANGE from the prior revision of this requirement,
under which the Battery chart's fetch computed a 1-day lookback (`readStart = start-1day`) before
calling `telemetry.Reader.SnapshotsByVehicleBetween`.** It SHALL fetch the Battery chart's window
via `analytics.Reader.BatteryLevelByDay(ctx, uid, teslaID, start, end)` (no lookback — the port
returns exactly `[start, end]`, because `vehicle_metrics.metric_date` is already the effective
day it needs no extra day to resolve); the Odometer chart's window via
`analytics.Reader.OdometerDeltaByDay(ctx, uid, teslaID, start, end)` (no lookback — the port
performs its own internal lookback fetch); and the consumed chart's window via
`analytics.Reader.ConsumedByDay(ctx, uid, teslaID, start, end)` (likewise no lookback). All three
reads fail INDEPENDENTLY: a Battery-read error degrades ONLY the Battery chart to its empty
state, an Odometer-read error degrades ONLY the Odometer chart, and a consumed-read error
degrades ONLY the consumed chart — none of the three blanks a sibling chart that already
succeeded.

Both the Odometer and Battery charts SHALL continue to render a **fixed `[start..end]` date
axis** — one bar per calendar day in the inclusive window, identical `MM-DD` labels across all
three charts — so a missing precomputed day does not shift the axis. A calendar day with no data
(no `analytics.DayBattery` entry for the Battery chart; no `analytics.DayDistance` entry for the
Odometer chart) SHALL render as an **empty labeled bar**: zero height, its own `MM-DD` label
retained, and a "no snapshot" tooltip. **A known, accepted consequence of the Battery chart's
port change: because `analytics.Reader.BatteryLevelByDay` reads the precomputed
`vehicle_metrics` table rather than the raw `vehicle_snapshots` table, a calendar day that the
nightly recalculation watermark has not yet reached renders as this SAME empty bar even though a
raw snapshot for that day already exists — this is a bounded, self-healing, typically
single-day-wide gap (RM40-gateway-drop-telemetry-dependency roadmap D6), not backfilled, and is
NOT a new UI state (it is byte-identical to the pre-existing "no snapshot at all" empty bar).**
The pre-window `start-1` day (consumed internally by `analytics.Reader.OdometerDeltaByDay` as the
first delta's kilometre basis) SHALL NOT be displayed as a bar on any chart.

The "Odometer history" bars SHALL represent kilometres driven per day, as already computed and
already floored at zero by `internal/analytics` (`OdometerDeltaByDay`'s `KmDriven` field) — the
gateway SHALL NOT compute a delta between two snapshots and SHALL NOT clamp a negative value
itself; both the subtraction and the zero-floor are `internal/analytics`'s responsibility. **The
"Battery history" bars SHALL represent the battery level percentage at each day's precomputed
observation (absolute 0–100), read directly from `analytics.Reader.BatteryLevelByDay` — a CHANGE
from the prior revision, under which this same absolute-percentage value was read directly from
`telemetry.Reader`; the value, its 0–100 scale, and the absence of any delta or clamp are all
unchanged by this move — only the port it is read from changed.** Each bar SHALL carry a hover
tooltip: the odometer bar's tooltip SHALL include the `MM-DD` date, the kilometres driven that
day (`DayDistance.KmDriven`), and the cumulative odometer in kilometres (`DayDistance.OdometerKm`
— both values already supplied by `internal/analytics`); the battery bar's tooltip SHALL include
the `MM-DD` date, the level percentage, and the rated range in kilometres
(`DayBattery.BatteryRangeKm`, already supplied by `internal/analytics`). A missing-day bar's
tooltip SHALL state that no snapshot exists for that date.

**The "Battery consumed" chart's bars SHALL represent the corrected per-day battery-consumed
percentage** returned by `analytics.Reader.ConsumedByDay` — bucketed on each returned
`DayConsumption.Date` value DIRECTLY, never re-derived or re-bucketed through the odometer/
battery charts' own bucket day. A day with no corresponding `DayConsumption` entry (no computable
value for that calendar day) SHALL render as an empty labeled bar identical in shape to the
odometer/battery "no snapshot" bar, but with a "no data" tooltip rather than a "no snapshot"
tooltip. The gateway SHALL NOT perform any timezone computation of its own for this chart — the
calendar day a value belongs to is decided entirely by `internal/analytics` before the gateway
receives it. **The Odometer and Battery charts' bucket day is likewise each port's own `Date`
field, bucketed DIRECTLY — the gateway performs no `effectiveDayUTC` re-derivation for either
chart** (this now applies uniformly to all three charts: `DayDistance.Date`, `DayBattery.Date`,
and `DayConsumption.Date` are each a FINAL bucket key supplied by `internal/analytics`, never
re-projected by the gateway — RM40-gateway-drop-telemetry-dependency roadmap D4 extends the same
rule tier 2's `RM29-analytics-add-vehicle-metrics` already established for the Odometer chart to
the Battery chart). A known, accepted consequence (unchanged): because `internal/analytics` and
`vehicle_metrics` bucket calendar days using the poller's configured zone for the consumed chart
specifically, while the Odometer and Battery charts' bucket day derives from the same
`Recalculate`-time effective-day computation, the same underlying nightly poll can in rare cases
label the consumed bar's calendar day one day apart from its Odometer/Battery siblings; the
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

The date shown in each battery tooltip and per-bar label SHALL be `analytics.DayBattery`'s own
`Date` field (already the calendar day the nightly recalculation represents — a CHANGE from the
prior revision, under which this same date came from the raw snapshot's `EffectiveDate` field),
formatted **`MM-DD`** by the Go handler; the odometer chart's date SHALL be `DayDistance.Date`
formatted the same way; the consumed chart's date SHALL be `DayConsumption.Date` formatted the
same way. The gateway SHALL NOT format dates inside the template. Each bar on every chart SHALL
carry a per-bar **date label** rendered under the bar in an HTML grid row (one cell per bar),
with orientation decided by a single chart-level boolean flag (`LabelVertical`): **horizontal**
for the 6-bar window (wide bars), **rotated vertical** (via the `[writing-mode:vertical-rl]` CSS
class) for windows of 14 bars or more (narrow bars). The handler SHALL set `LabelVertical` from
the number of bars in the fixed window (`labelVerticalFor(numBars)`, true when `numBars >= 14`)
independently for each chart, not from a `days` count; the template SHALL NOT compare the window
size, compute rotation, or call `time.Format`.

The charts SHALL be rendered as **responsive inline SVG** (scaling to the container width) using
no client-side charting library. All numeric values — bar heights, deltas, percentages, tooltip
strings, per-bar label strings, and the consumed chart's marker classification — SHALL be
computed by the Go handler (received already-computed from `internal/analytics` for all three
charts and only scaled/formatted by the handler — a CHANGE from the prior revision, under which
the Battery chart's values were read from a raw snapshot with no `internal/analytics`
intermediary; the handler still performs no arithmetic of its own for any of the three) before
the template renders; the template SHALL perform no arithmetic, unit conversion, date formatting,
or method calls on domain types, and SHALL select every marker's visual class from a literal
written in the template source, never from a string computed by the handler. Bar colours and
marker colours SHALL use DaisyUI semantic tokens (no hardcoded hex).

The preset selector SHALL keep the 6/14/30 buttons, each button's `hx-get` emitting a
server-rendered absolute `?start=<yesterday-N>&end=<yesterday>` href computed by the handler at
render time; the selector SHALL mark the preset whose `(start, end)` matches the requested window
as active. Changing the preset SHALL re-fetch `GET /ui/dashboard/history?start=...&end=...` and
re-render all three charts AND the selector by swapping `#dashboard-history`'s `innerHTML`
without a full page reload.

The dashboard page SHALL NOT carry a Refresh button; the `#dashboard-content` (subscribes to
`vehicle-changed from:body`) and `#dashboard-history` (self-loads on `hx-trigger="load"` with the
default 6-day window's absolute `start`/`end` ending yesterday, and re-renders on preset clicks)
htmx surfaces cover every refresh path. When too few data points exist to draw a chart (zero
`analytics.DayBattery` entries for the battery chart, zero `analytics.DayDistance` entries for
the odometer chart, or zero `DayConsumption` entries for the consumed chart), that chart SHALL
show the existing empty-state placeholder instead of fabricated bars; a partially-missing axis
(some days empty, some present) is NOT an empty chart for any of the three.

Every user-facing string this chart uses (its title, and every tooltip clause it composes from —
the plain value, the multi-day-span value, and the flagged note) SHALL resolve through
`i18n.T(ctx, key)` against `internal/gateway/i18n/catalog.go`, with both `ES` and `EN`
non-empty. **The Battery chart's "no snapshot" tooltip key (`KeyHistoryNoSnapshotTooltip`) is
UNCHANGED and NOT renamed by this move**, even though its English wording ("no snapshot") now
describes the absence of a `vehicle_metrics` row rather than the absence of a raw snapshot — this
is a deliberately accepted, flagged-not-actioned wording nuance
(RM40-gateway-drop-telemetry-dependency roadmap "Future work"), not a defect. Composing multiple
clauses into one tooltip (e.g. for a day that is both flagged and spanned) SHALL join
independently-translated, complete clauses with a language-neutral separator and SHALL NOT
hardcode a connective word from any one language.

#### Scenario: History charts render for the selected vehicle with the default window

- **GIVEN** a signed-in user whose selected vehicle has several precomputed daily
  `vehicle_metrics` observations and several computable `analytics.DayConsumption` days
- **WHEN** the history fragment is requested (`GET /ui/dashboard/history`) directly, with no
  `start` and no `end` parameter and no `browser_tz` cookie (a direct API call)
- **THEN** the response renders an "Odometer history" chart, a "Battery history" chart, and a
  "Battery consumed" chart for the selected vehicle using a 6-day window (`end =
  platform-default-yesterday`, `start = platform-default-yesterday-6`, `end` inclusive).
  `platform-default` is `clock.Zone()`, the platform's default time zone `America/Bogota`
- **AND** all three charts render exactly 6 bars, one per calendar day in
  `[platform-default-yesterday-6 .. platform-default-yesterday]`
- **AND** the odometer and battery charts display identical `MM-DD` labels under corresponding
  bars; the consumed chart's labels cover the same calendar range (see the bucketing-mismatch
  scenario below for why an individual label can differ by one day)
- **AND** no live Tesla Fleet API call is made and no telemetry or analytics table is read
  directly

#### Scenario: The end<=today cap rejects end=today; only end<=yesterday is accepted

- **GIVEN** a signed-in user whose browser-local "today" is `2026-08-16`
- **WHEN** the history fragment is requested with `?start=2026-08-10&end=2026-08-16` (`end`
  equal to browser-today)
- **THEN** the endpoint responds with HTTP 400
- **AND** the SAME request with `?end=2026-08-15` (`end` equal to browser-yesterday) is
  accepted (HTTP 200)
- **AND** the empty-state placeholder (no preset selector) is rendered for the rejected request,
  per the existing malformed-request degradation rule

#### Scenario: The odometer chart's delta and clamp are computed by analytics, not the gateway

- **GIVEN** two consecutive stored observations whose odometer readings differ by `-2.0` km (a
  clock-skew/read anomaly)
- **WHEN** `analytics.Reader.OdometerDeltaByDay` is called for the window containing that day
- **THEN** the returned `DayDistance.KmDriven` is `0.0` — the negative value is already floored
  by `internal/analytics`
- **AND** the gateway handler building the odometer chart performs no subtraction between two
  observations and no comparison against zero — it renders `DayDistance.KmDriven` directly,
  scaled relative to the window's maximum

#### Scenario: The odometer chart bucket day is the port's own Date, never re-derived

- **GIVEN** `analytics.Reader.OdometerDeltaByDay` returns a `DayDistance` entry with
  `Date = 2026-08-10`
- **WHEN** the odometer chart buckets that entry onto the fixed `[start..end]` axis
- **THEN** the entry's bar is placed at the `2026-08-10` slot using `Date` verbatim
- **AND** the gateway does NOT pass `Date` through `effectiveDayUTC` or any other re-bucketing
  step

#### Scenario: The battery chart bucket day is the port's own Date, never re-derived

- **GIVEN** `analytics.Reader.BatteryLevelByDay` returns a `DayBattery` entry with
  `Date = 2026-08-10`
- **WHEN** the battery chart buckets that entry onto the fixed `[start..end]` axis
- **THEN** the entry's bar is placed at the `2026-08-10` slot using `Date` verbatim
- **AND** the gateway does NOT pass `Date` through `effectiveDayUTC` or any other re-bucketing
  step
- **AND** this is a CHANGE from the prior revision, under which the gateway itself computed the
  bucket day from a raw snapshot's `EffectiveDate` via `effectiveDayUTC`; the two produce the
  identical bucket day for the same underlying nightly capture, so no rendered bar moves as a
  result of this change

#### Scenario: The consumed chart bucket day is the port's own Date, never re-derived

- **GIVEN** `analytics.Reader.ConsumedByDay` returns a `DayConsumption` entry with
  `Date = 2026-08-10`
- **WHEN** the consumed chart buckets that entry onto the fixed `[start..end]` axis
- **THEN** the entry's bar is placed at the `2026-08-10` slot using `Date` verbatim
- **AND** the gateway does NOT pass `Date` through the battery chart's `effectiveDayUTC` helper
  or any other re-bucketing step
- **AND** a known, accepted consequence is that this bar's calendar day can differ by one day
  from the battery bar for the same underlying nightly poll, because `internal/analytics` buckets
  the consumed figure in the poller's configured zone while the battery/odometer figures use the
  `Recalculate`-time effective day — this mismatch is NOT corrected by the gateway

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
  other
- **AND** the bar's height is zero — the chart's zero-axis floor (a negative height cannot be
  drawn) and the flagged-day value-suppression rule agree, because a flagged day's `ConsumedPct`
  is always `<= 0` by construction
- **AND** the tooltip states the real, signed percentage value (`-3.0%`) and that the entry
  covers 2 days, AND separately notes a possible missing manual charge record — both facts
  present, neither omitted in favor of the other

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
- **AND** an `analytics.Reader` error on `ConsumedByDay` degrades only the consumed chart to its
  empty state; the battery chart, sourced from the separate
  `analytics.Reader.BatteryLevelByDay` call, and the odometer chart, sourced from
  `analytics.Reader.OdometerDeltaByDay`, are both unaffected

#### Scenario: Odometer chart data comes exclusively through analytics.Reader

- **GIVEN** the gateway handler that builds the odometer chart
- **WHEN** it obtains per-day odometer distance data
- **THEN** it does so exclusively through `analytics.Reader.OdometerDeltaByDay`
- **AND** it imports no package other than `internal/analytics`'s public port for this data
- **AND** an `analytics.Reader` error on `OdometerDeltaByDay` degrades only the odometer chart to
  its empty state; the battery chart, sourced from the separate
  `analytics.Reader.BatteryLevelByDay` call, is unaffected

#### Scenario: Battery chart data comes exclusively through analytics.Reader

- **GIVEN** the gateway handler that builds the battery chart
- **WHEN** it obtains per-day battery-level and range data
- **THEN** it does so exclusively through `analytics.Reader.BatteryLevelByDay` — this is a CHANGE
  from the prior revision of this requirement, under which this data came from
  `telemetry.Reader.SnapshotsByVehicleBetween`
- **AND** it imports no package from `internal/telemetry` for this data, or for any other purpose
  anywhere in `internal/gateway/`
- **AND** an `analytics.Reader` error on `BatteryLevelByDay` degrades only the battery chart to
  its empty state; the odometer chart (`OdometerDeltaByDay`) and the consumed chart
  (`ConsumedByDay`) are both unaffected

#### Scenario: A day lagging the nightly recalculation renders as an empty bar (accepted, self-healing)

- **GIVEN** a calendar day for which `internal/telemetry` already holds a raw snapshot, but for
  which `internal/analytics`'s nightly `Recalculate`/`Reconcile` pass has not yet written the
  corresponding `vehicle_metrics` row
- **WHEN** the battery chart is rendered for a window containing that day
- **THEN** that day's bar renders as the SAME empty "no snapshot" bar a day with no raw snapshot
  at all would produce — zero height, its `MM-DD` label retained, the "no snapshot" tooltip
- **AND** this is an accepted, typically single-day-wide, self-healing consequence of reading a
  precomputed table instead of the raw snapshot table directly (RM40-gateway-drop-telemetry-
  dependency roadmap D6) — no backfill is performed, and the bar corrects itself once the next
  nightly recalculation catches up

#### Scenario: Browser-Local Calendar Day, with a platform-default fallback

- **GIVEN** a signed-in user whose browser sent a `browser_tz` cookie with a valid IANA zone —
  for any zone, negative or positive UTC offset
- **WHEN** the history fragment is requested with no `start`/`end` parameter
- **THEN** the default window's `end` is midnight of browser-yesterday IN THAT ZONE, not the
  platform default zone's midnight — the cookie always wins whenever present
- **AND** on a missing, empty, or unparseable `browser_tz` cookie, the gateway falls back to the
  platform's default time zone, `clock.Zone()` (`America/Bogota`) — silently, no error surfaced,
  no caller special-casing required

#### Scenario: Charts contain no business logic in templates

- **GIVEN** the history chart and selector templates
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

#### Scenario: Gateway never imports telemetry or an analytics database package for history

- **GIVEN** the gateway handler that builds all three history charts
- **WHEN** it obtains the vehicle's per-day battery level, its per-day odometer distance, and
  its per-day consumption
- **THEN** it does so exclusively through the `analytics.Reader` public interface — a CHANGE
  from the prior revision, under which the battery chart's data came through `telemetry.Reader`
- **AND** it imports no package from `internal/telemetry`, at all, for any purpose in
  `internal/gateway/` — not `internal/telemetry` itself and not `internal/telemetry/db`
  (`telemetrydb`)
- **AND** it imports no package from `internal/analytics/db` (`analyticsdb`)
- **AND** no `pgtype` type appears in any gateway file involved

#### Scenario: History fragment is not served to anonymous callers

- **GIVEN** an unauthenticated request to `GET /ui/dashboard/history`
- **WHEN** the handler resolves the session
- **THEN** the request is redirected to `/login` and no history data (including the consumed
  chart) is served

#### Scenario: All history-chart strings are bilingual

- **GIVEN** each chart's title, and every clause its tooltip composes from (the plain
  percentage/level value, the multi-day-span value, and the flagged note)
- **WHEN** the catalogue is inspected
- **THEN** every corresponding key has both an `ES` and an `EN` value, neither empty
- **AND** no history-chart string is a hardcoded literal bypassing `i18n.T`
- **AND** the battery chart's "no snapshot" key (`KeyHistoryNoSnapshotTooltip`) is unchanged by
  this requirement's battery-chart data-source change — its wording is a deliberately accepted,
  flagged-not-actioned nuance, not a defect (see the requirement text above)
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

For a signed-in request, the active language SHALL come from `account.Service.PreferencesFor`,
the SAME single call that also resolves the request's active theme (see "Per-Request Theme
Resolution" below) — the gateway SHALL NOT make a separate `account.Service.LanguageFor` call on
any request that also needs the theme, and SHALL NOT make more than one `account.Service`
preference call in total per request. For an anonymous request, the active language SHALL come
from a `lang` cookie, with no database call. In both cases, an absent, unrecognized, or
error-producing source SHALL resolve to `es` — the gateway SHALL NEVER fail or 500 a render
because of a missing or invalid language source.

A catalogue key with no `es`/`en` entry SHALL render a visible marker distinguishing it from a
translated string (never a blank string, never a raw untranslated fallback with no marker), so an
incomplete catalogue is visible in manual QA rather than silently shipping.

#### Scenario: Signed-in request resolves language from the account

- **GIVEN** a signed-in user whose account's stored language is `en`
- **WHEN** any authenticated page or htmx fragment is rendered
- **THEN** the gateway calls `account.Service.PreferencesFor` exactly once for that request
- **AND** every catalogue-driven string on the response renders in English

#### Scenario: Anonymous request resolves language from the cookie

- **GIVEN** an anonymous visitor whose browser carries a `lang=en` cookie
- **WHEN** an anonymous page (e.g. `/login`, `/`) is rendered
- **THEN** the gateway does not call `account.Service.PreferencesFor`
- **AND** every catalogue-driven string on the response renders in English

#### Scenario: Missing or unrecognized language source falls back to Spanish

- **GIVEN** either (a) an anonymous visitor with no `lang` cookie or a cookie value outside
  `{es, en}`, or (b) a signed-in user whose `account.Service.PreferencesFor` call errors
- **WHEN** a page is rendered
- **THEN** the gateway renders every catalogue-driven string in Spanish
- **AND** the render succeeds (no 500, no raw error)

#### Scenario: Language is resolved exactly once per request, in the same call that resolves theme

- **GIVEN** a signed-in user requesting a page whose render composes multiple nested components
- **WHEN** the page is rendered
- **THEN** `account.Service.PreferencesFor` is called exactly one time for that request
- **AND** every nested component renders in the same resolved language
- **AND** the same call's resolved theme is what the response's `data-theme` attribute carries
  (see "Per-Request Theme Resolution")

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
- **WHEN** they load any authenticated page (`/dashboard`, `/external-charges`, `/supercharger-stats`)
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

The Supercharger Stats session table SHALL display the following columns, in
order: Date, Status, Site, Energy, Cost, start battery percentage, end battery
percentage, Actions. **This is a CHANGE from the prior revision of this
requirement, under which the table had seven columns with no Status column — a
Status column is ADDED by this revision (`RM41-gateway-add-session-status-column`)
as the 2nd column, immediately after Date**, taking the table from 7 columns to
8. Every other column, its formatting rule, and its data source are unchanged
from the prior revision.

The table SHALL obtain the Status column's value from the existing
`charging.SessionReader` result only (`charging.Session.Status`); it SHALL NOT
issue an additional read, write, Tesla API call, or database query to display it.

Every new header SHALL resolve through the existing gateway i18n catalogue with
non-empty ES and EN translations.

#### Scenario: The Status column is the 2nd column

- **GIVEN** any Supercharger Stats page render
- **WHEN** the session table's headers are rendered
- **THEN** the 2nd header is the translated Status label ("Estado"/"Status")
- **AND** the 1st header remains Date and the 3rd header is Site

#### Scenario: Populated session battery values still render alongside Status

- **GIVEN** a Supercharger session whose start and end battery percentage values
  are 40 and 80
- **WHEN** the Supercharger Stats page is rendered
- **THEN** its row displays `40%` and `80%` in the two battery columns exactly as
  before this change
- **AND** the same row also displays a Status badge in its 2nd cell

### Requirement: Supercharger session battery percentages are correctable inline

The gateway SHALL let a signed-in user, on `/supercharger-stats`, correct the
`start_battery_pct` and `end_battery_pct` of one of their own account's
Supercharger sessions inline, by swapping that session's table row for an editable
row and saving through the existing `charging.SessionVerifier.VerifySession` port.
The gateway SHALL NOT add a delete action for a Supercharger session, and SHALL NOT
add a new `charging` read or write port for this feature — the write goes through
`SessionVerifier` exactly as it already exists, and any read the gateway needs to
resolve one session by id SHALL be performed by listing the account/vehicle-scoped,
`?start=&end=`-windowed session set and matching the id in memory, never by adding
a by-id method to `charging.SessionReader`.

**The edit row SHALL render the session's date, site, energy, and cost as read-only
display text; ONLY the two verified battery percentage fields SHALL be editable
inputs. This is a CHANGE from the prior revision of this requirement, under which
the edit row also rendered both battery percentage ESTIMATE fields as read-only
display text — those two fields are REMOVED from the edit row by this revision
(`RM41-gateway-revise-battery-pct-ui`), leaving nothing left to display read-only
for them.** The gateway SHALL NOT write `battery_pct_source`,
`start_battery_pct_est`, or `end_battery_pct_est` from any value it receives from
this row — `battery_pct_source` is always computed by `VerifySession` itself, and
the two estimate columns are not reachable through this write path at all.

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

#### Scenario: The edit row has no estimate fields

- **GIVEN** a signed-in user opens the inline edit form for a Supercharger session row
- **WHEN** the edit row is rendered
- **THEN** it displays date, site, energy, and cost as read-only text, and start/end battery
  percentage as the only two editable inputs
- **AND** it contains no start-estimate or end-estimate read-only field

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

### Requirement: Charge List Status Column

Each row in the entries table SHALL show a Status cell carrying two independent signals: a
two-state (green or yellow — never red) completeness indicator, and a text badge naming the
entry's lifecycle status (`IN_PROGRESS` or `DONE`). The completeness indicator SHALL be green
when the entry has every field required for a `DONE` entry (`ended_at`, `end_battery_pct`)
present, AND a non-nil energy value, AND a price greater than zero; yellow in every other case,
including a normally-shaped `IN_PROGRESS` entry that has not yet acquired those values. Both
signals SHALL be computed by the handler; the template SHALL perform no arithmetic and SHALL
NOT call any `charging` module function itself.

#### Scenario: A fully-complete DONE entry shows the green indicator

- **GIVEN** an entry with status `DONE`, a non-nil `ended_at`, a non-nil `end_battery_pct`, a
  non-nil energy value, and a price greater than zero
- **WHEN** the entries table renders that entry's row
- **THEN** the Status cell's completeness indicator renders in its green (complete) state
- **AND** the Status cell's badge reads the DONE label

#### Scenario: A DONE entry missing any one required value shows the yellow indicator

- **GIVEN** an entry with status `DONE` that is missing exactly one of: `ended_at`,
  `end_battery_pct`, a non-nil energy value, or a price greater than zero
- **WHEN** the entries table renders that entry's row
- **THEN** the Status cell's completeness indicator renders in its yellow (incomplete) state,
  never a red or third state
- **AND** the Status cell's badge still reads the DONE label (the badge reflects the stored
  lifecycle status independently of the completeness indicator)

#### Scenario: A normally-shaped IN_PROGRESS entry shows the yellow indicator

- **GIVEN** an entry with status `IN_PROGRESS`, no `ended_at`, and no `end_battery_pct` (the
  ordinary shape for an in-progress charge)
- **WHEN** the entries table renders that entry's row
- **THEN** the Status cell's completeness indicator renders in its yellow (incomplete) state
- **AND** the Status cell's badge reads the IN PROGRESS label

---

### Requirement: Charge List Battery Range Column

Each row in the entries table SHALL show a battery-range cell (e.g. `"22% → 70%"`) ALONGSIDE
the existing battery-delta cell — both are shown, not one in place of the other. The
battery-range cell SHALL render `—` when either the start or the end battery percentage is
absent.

#### Scenario: Both battery percentages present renders the range and the delta together

- **GIVEN** an entry with `start_battery_pct = 22` and `end_battery_pct = 70`
- **WHEN** the entries table renders that entry's row
- **THEN** the battery-range cell reads `"22% → 70%"`
- **AND** the battery-delta cell reads `"+48%"`, in a cell adjacent to the battery-range cell

#### Scenario: A missing end battery percentage renders the em-dash range

- **GIVEN** an entry with `start_battery_pct = 22` and a nil `end_battery_pct` (an
  `IN_PROGRESS` entry not yet completed)
- **WHEN** the entries table renders that entry's row
- **THEN** the battery-range cell reads `—`
- **AND** the battery-delta cell also reads `—`

### Requirement: Inactive Account Is Blocked At Login
The gateway SHALL refuse to establish an authenticated session for an account whose activation
status is not Active. When the Google OAuth callback resolves an account whose status is not
Active, the gateway SHALL respond with a dedicated page at HTTP 403 stating that the account is
deactivated and naming a contact address for reactivation, and SHALL NOT set any session identity
value. The contact address SHALL be a fixed, hardcoded value in the translation catalogue, not a
configuration value. The blocked page SHALL be presented in both supported languages.

#### Scenario: An Inactive account is refused a session at login
- **GIVEN** a visitor whose Google identity resolves to an account with status Inactive
- **WHEN** the OAuth callback completes
- **THEN** the gateway responds with HTTP 403 and a page stating the account is deactivated and
  naming the contact address
- **AND** no session identity is set for that request or any request using its cookie

#### Scenario: An Active account is signed in normally
- **GIVEN** a visitor whose Google identity resolves to an account with status Active
- **WHEN** the OAuth callback completes
- **THEN** the gateway establishes an authenticated session for that account
- **AND** the visitor is sent to the dashboard

#### Scenario: The blocked page is available in both supported languages
- **GIVEN** the blocked page rendered for an Inactive account
- **WHEN** it is rendered in either supported language
- **THEN** its title, message, and contact address text are all present and non-empty in that
  language

### Requirement: Dashboard Vehicle Status Card Shows Locked and Sentry-Mode Badges, Not a Status Tile

The dashboard's Vehicle Status card (`/dashboard`, `/ui/dashboard`) SHALL show the
active vehicle's locked state and sentry-mode state as badges in the card's header
row, alongside the existing staleness badge, and SHALL NOT show a separate "Status"
stat tile anywhere in the card. The card subtitle SHALL continue to show the
charging-derived status word exactly as before this change — only a "Status" tile is
prohibited, not the subtitle's status word.

The card's mini-stat tiles (odometer, interior temperature, exterior temperature, the
lifetime count of charges to 100%, and any tile added by a later capability) are
grouped per the "Dashboard Vehicle Status Panel Groups Metrics Into Named Subsections"
requirement below — this requirement no longer prescribes their grid shape or count.

A locked-state badge SHALL render only when the active vehicle's latest precomputed
row carries a locked observation; an absent observation SHALL render no badge at
all — never a fabricated "unlocked" default. The same absence rule applies
independently to the sentry-mode badge.

#### Scenario: Locked and sentry badges both render when both observations are present

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row reports
  the vehicle locked and sentry mode on
- **WHEN** the dashboard is rendered
- **THEN** the Vehicle Status card header shows a "Locked" badge in a
  success-colored (green) style
- **AND** shows a "Sentry: On" badge in a warning-colored style
- **AND** no "Status" tile is shown anywhere on the card

#### Scenario: Unlocked and sentry-off render distinct badge colors from locked and sentry-on

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row reports
  the vehicle unlocked and sentry mode off
- **WHEN** the dashboard is rendered
- **THEN** the Vehicle Status card header shows an "Unlocked" badge in an
  error-colored (red) style, visually distinct from the "Locked" badge's style
- **AND** shows a "Sentry: Off" badge in a neutral (ghost) style, visually distinct
  from the "Sentry: On" badge's style

#### Scenario: Absent locked or sentry observations render no badge, not a fabricated default

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row exists but
  carries no locked observation and no sentry-mode observation (the row predates this
  capability's status-observation tracking)
- **WHEN** the dashboard is rendered
- **THEN** the Vehicle Status card header shows neither a locked badge nor a sentry
  badge
- **AND** no badge defaults to "Unlocked" or "Sentry: Off" in the absence of data
- **AND** the card's other fields (subtitle, battery, odometer, temperatures) render
  per their own absence rules, independently of the missing badges

#### Scenario: The 100%-charge count distinguishes "not reported" from a real zero

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row carries no
  100%-charge count (the vehicle did not report it, or the row predates the column)
- **WHEN** the dashboard is rendered
- **THEN** the 100%-charge tile shows the "—" placeholder
- **AND** a vehicle whose row reports a count of `0` instead shows "0", because "never
  charged to 100%" is a real reading and SHALL NOT be rendered as absent

#### Scenario: Badges and the staleness marker coexist in the same header row

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row is both
  stale (per the staleness threshold) and reports the vehicle locked with sentry mode
  on
- **WHEN** the dashboard is rendered
- **THEN** the header row shows the staleness badge alongside the locked and sentry
  badges — none of the three suppresses the others
- **AND** each badge's presence is independently determined by its own underlying
  value

#### Scenario: No new translation keys are introduced

- **GIVEN** the locked and sentry badges added by this requirement
- **WHEN** their text is resolved
- **THEN** it resolves through catalogue keys that already existed before this
  change (the same keys the vehicle-list card already used for locked/unlocked/
  sentry-on/sentry-off/not-reported)
- **AND** both `ES` and `EN` translations for those keys are already complete

#### Scenario: Templates contain no business logic for badge selection

- **GIVEN** the dashboard template rendering the Vehicle Status card header
- **WHEN** it decides whether to show a badge and which color/text to use
- **THEN** that decision has already been computed by a Go helper function before
  the template receives the result (text, color, and a show/hide flag)
- **AND** the template only conditionally renders a pre-built `ui.Badge` — no
  nil-check-driven text or color selection happens inside the template itself

### Requirement: Supercharger Stats battery percentage guidance

The Supercharger Stats page SHALL render one bilingual informational alert
explaining that Tesla does not supply the start and end battery percentages for a
Supercharger session, that the user should try to record them when Supercharging,
and that supplying only the end percentage lets the system calculate the
approximate start percentage. The alert SHALL use the existing `ui.Alert` component
with `Kind: "info"` — no new UI kit component SHALL be introduced. The alert's text
SHALL resolve through the gateway i18n catalogue (key `supercharger.battery_pct_help`)
with non-empty ES and EN translations.

The alert SHALL render unconditionally, above the KPI tiles, the chart, and the
sessions table (or their empty-state placeholder) — its presence SHALL NOT depend
on whether the current window has any sessions, and SHALL NOT depend on whether the
requested date window is valid.

#### Scenario: The guidance alert renders on a normal page load

- **GIVEN** a signed-in user with a selected vehicle that has Supercharger sessions
  in the default window
- **WHEN** `GET /supercharger-stats` is requested
- **THEN** the response contains one info-styled alert with the battery-percentage
  guidance text in the active language
- **AND** the alert appears before the KPI tiles in the rendered markup

#### Scenario: The guidance alert renders even when the window has no sessions

- **GIVEN** a signed-in user with a selected vehicle that has zero Supercharger
  sessions in the requested window
- **WHEN** the Supercharger Stats page or fragment is rendered
- **THEN** the guidance alert is still rendered
- **AND** the empty-state placeholder is rendered below it

#### Scenario: The guidance text is bilingual

- **GIVEN** a signed-in user whose resolved language is English
- **WHEN** the Supercharger Stats page is rendered
- **THEN** the alert displays the English guidance text, not the Spanish text
- **AND** switching the active language to Spanish renders the Spanish guidance
  text instead

### Requirement: Supercharger Stats session status badge

The Supercharger Stats session table's Status column SHALL render one badge per
session, using the existing `ui.Badge` component, reflecting
`charging.Session.Status` (`charging.SessionStatus`: `IN_PROGRESS` |
`DONE_CALCULATED` | `DONE`). The mapping from status to Badge `Kind` and label
SHALL be:

| `charging.SessionStatus` | Badge `Kind` | Label (ES / EN) |
|---|---|---|
| `DONE` | `primary` | Finalizada / Done |
| `DONE_CALCULATED` | `neutral` | Finalizada (calculada) / Done (calculated) |
| `IN_PROGRESS` | `ghost` | En progreso / In progress |

The badge SHALL NOT use the `success` or `warning` Kind for any status. Every
label SHALL resolve through the gateway i18n catalogue with non-empty ES and EN
translations. The gateway SHALL NOT add a `ui.Dot` completeness indicator
alongside this badge.

The gateway SHALL NOT expose any way to set or change `Status` through this or
any route — the value is read-only from the gateway's perspective, always
computed by `charging.SessionVerifier.VerifySession`.

#### Scenario: A DONE session renders a primary badge

- **GIVEN** a Supercharger session whose `Status` is `DONE`
- **WHEN** the Supercharger Stats page is rendered
- **THEN** that session's row displays a badge with the `primary` Kind
- **AND** the badge's text is the translated "Done" label in the active language

#### Scenario: A DONE_CALCULATED session renders a neutral badge

- **GIVEN** a Supercharger session whose `Status` is `DONE_CALCULATED`
- **WHEN** the Supercharger Stats page is rendered
- **THEN** that session's row displays a badge with the `neutral` Kind
- **AND** the badge's text is the translated "Done (calculated)" label in the
  active language
- **AND** the badge is visually distinct from a `DONE` session's badge

#### Scenario: An IN_PROGRESS session renders a ghost badge

- **GIVEN** a Supercharger session whose `Status` is `IN_PROGRESS`
- **WHEN** the Supercharger Stats page is rendered
- **THEN** that session's row displays a badge with the `ghost` Kind
- **AND** the badge's text is the translated "In progress" label in the active
  language

#### Scenario: The badge column adds no additional read

- **GIVEN** any Supercharger Stats page or fragment render
- **WHEN** the session table is built
- **THEN** the gateway performs exactly the one existing
  `charging.SessionReader.ListSessionsByVehicleBetween` read it already performed
  before this change
- **AND** no new `charging` port, method, or database query is called

#### Scenario: The edit row still spans the full table width

- **GIVEN** a signed-in user opens the inline edit form for a Supercharger
  session row
- **WHEN** the edit row is rendered
- **THEN** its single full-width cell spans exactly 8 columns, matching the
  8-column header and static rows
- **AND** an error row rendered for that session likewise spans exactly 8 columns

### Requirement: Supercharger Stats in-progress session guidance

The Supercharger Stats page SHALL render a second bilingual informational alert,
below the existing battery-percentage guidance alert, telling the user that a
session shown as "In progress" is missing its percentages and that editing it to
fill in the end percentage lets the system calculate the start percentage. The
alert SHALL use the existing `ui.Alert` component with `Kind: "info"` — no new UI
kit component SHALL be introduced. The alert's text SHALL resolve through the
gateway i18n catalogue (key `supercharger.status_help`) with non-empty ES and EN
translations.

The alert SHALL render unconditionally, immediately below the existing
`supercharger.battery_pct_help` alert and above the KPI tiles, the chart, and the
sessions table (or their empty-state placeholder) — its presence SHALL NOT depend
on whether any session in the current window is `IN_PROGRESS`, and SHALL NOT
depend on whether the requested date window is valid.

#### Scenario: The in-progress guidance alert renders below the existing alert

- **GIVEN** a signed-in user with a selected vehicle
- **WHEN** `GET /supercharger-stats` is requested
- **THEN** the response contains two info-styled alerts, in this order: the
  existing battery-percentage guidance alert, then the in-progress guidance alert
- **AND** both alerts appear before the KPI tiles in the rendered markup

#### Scenario: The in-progress guidance alert renders even with no in-progress sessions

- **GIVEN** a signed-in user whose selected vehicle's Supercharger sessions in the
  requested window are all `DONE` or `DONE_CALCULATED`
- **WHEN** the Supercharger Stats page or fragment is rendered
- **THEN** the in-progress guidance alert is still rendered

#### Scenario: The in-progress guidance text is bilingual

- **GIVEN** a signed-in user whose resolved language is English
- **WHEN** the Supercharger Stats page is rendered
- **THEN** the alert displays the English guidance text
- **AND** switching the active language to Spanish renders the Spanish guidance
  text instead

### Requirement: Per-Request Theme Resolution

The gateway SHALL resolve an active UI theme for every request, exactly once per request, using
the SAME `account.Service.PreferencesFor` call that resolves language for a signed-in request
(see the modified "Translation Catalogue and Per-Request Language Resolution" requirement above)
— the gateway SHALL NOT issue a separate database call to resolve theme. For an anonymous
request, the active theme SHALL come from a `theme` cookie, with no database call. In both cases,
an absent, unrecognized, or error-producing source SHALL resolve to the platform default theme
(`graphite`) — the gateway SHALL NEVER fail or 500 a render because of a missing or invalid theme
source. The resolved theme SHALL be available to every component in the render tree via the
request's context, mirroring how the resolved language is made available.

#### Scenario: Signed-in request resolves theme from the account, in the same call as language

- **GIVEN** a signed-in user whose account's stored theme is `apex`
- **WHEN** any authenticated page or htmx fragment is rendered
- **THEN** the response's `data-theme` attribute is `apex`
- **AND** no `account.Service` call beyond the single `PreferencesFor` call was needed to
  obtain it

#### Scenario: Anonymous request resolves theme from the cookie

- **GIVEN** an anonymous visitor whose browser carries a `theme=apex` cookie
- **WHEN** an anonymous page (e.g. `/login`, `/`) is rendered
- **THEN** the gateway does not call `account.Service.PreferencesFor`
- **AND** the response's `data-theme` attribute is `apex`

#### Scenario: Missing or unrecognized theme source falls back to the platform default

- **GIVEN** either (a) an anonymous visitor with no `theme` cookie or a cookie value outside the
  supported vocabulary, or (b) a signed-in user whose `account.Service.PreferencesFor` call
  errors
- **WHEN** a page is rendered
- **THEN** the response's `data-theme` attribute is `graphite`
- **AND** the render succeeds (no 500, no raw error)

#### Scenario: A stale theme cookie is refreshed to match the stored preference

- **GIVEN** a signed-in user whose stored theme is `halloween` and whose incoming `theme` cookie
  says `graphite` (or carries no cookie at all)
- **WHEN** any authenticated page is rendered
- **THEN** the response sets a fresh `theme=halloween` cookie
- **AND** a request whose incoming cookie already matches the stored value triggers no new
  `Set-Cookie` for `theme`

#### Scenario: A theme chosen before logout still renders after logout

- **GIVEN** a user who, while signed in, changed their theme to `apex` (the change succeeded and
  the `theme` cookie was refreshed to `apex`)
- **WHEN** that user signs out and then loads an anonymous page (e.g. `/`, `/login`)
- **THEN** the response's `data-theme` attribute is `apex`
- **AND** no `account.Service` call is made to obtain it — the value comes entirely from the
  `theme` cookie, which is the ONLY reason this cookie exists (there is no anonymous write path
  to this cookie; see "Theme Switch Endpoint")

### Requirement: Theme Presentation Vocabulary and Switcher

The gateway SHALL expose a single, closed, exported list of supported theme codes
(`apex`, `graphite`, `halloween`) that is the sole source both the theme dropdown control and its
own input-validation check consult. The gateway SHALL render a theme selector composed from the
typed `templates/ui/` kit, requiring no client-side JavaScript to open or display its options.
Theme names SHALL render as their proper-noun display form (`Apex`, `Graphite`, `Halloween`)
and SHALL NOT be resolved through the translation catalogue — only the selector's own label and
accessible name are translated.

#### Scenario: The switcher lists every supported theme, in vocabulary order

- **GIVEN** the theme selector is rendered
- **WHEN** its options are inspected
- **THEN** it lists exactly the three supported themes, in the same order as the gateway's
  closed vocabulary
- **AND** each option's visible text is that theme's proper-noun display form, unaffected by
  the resolved language

#### Scenario: The switcher's own chrome is translated; theme names are not

- **GIVEN** a signed-in user with English resolved
- **WHEN** the theme selector is rendered
- **THEN** the selector's label and accessible name render in English
- **AND** every theme option's own name still renders as its proper noun (e.g. `Graphite`, not
  a translated form)

#### Scenario: The selector requires no client-side JavaScript to display or open

- **GIVEN** the rendered theme selector
- **WHEN** its markup is inspected
- **THEN** opening the list of options uses only a CSS-driven DaisyUI pattern
- **AND** no JavaScript is required to display the list of options (selecting an option is
  covered separately by "Instant Client-Side Theme Apply")

### Requirement: Settings Page

The gateway SHALL serve an authenticated `/settings` page rendering the theme selector, seeded
with the request's already-resolved theme. The page SHALL require an authenticated session, and
SHALL NOT issue any preference read beyond the one already performed for the request. The page
SHALL issue a fresh per-session CSRF token for the theme switch endpoint on every load.

#### Scenario: An authenticated user views their current theme on the Settings page

- **GIVEN** a signed-in user whose resolved theme for this request is `apex`
- **WHEN** they load `/settings`
- **THEN** the response renders the theme selector showing `apex` as the current selection
- **AND** rendering the page issues no `account.Service` preference call beyond the one already
  made for the request by the per-request resolution requirement above
- **AND** a fresh CSRF token for the theme switch endpoint is issued for this session

#### Scenario: An anonymous visitor cannot view the Settings page

- **GIVEN** an anonymous visitor
- **WHEN** they request `/settings`
- **THEN** they are redirected to `/login`
- **AND** no page content is rendered
- **AND** no CSRF token is issued

### Requirement: Theme Switch Endpoint

The gateway SHALL expose an endpoint that persists a theme change for the calling user. This
endpoint SHALL require an authenticated session — there is no anonymous path to it, since the
only control capable of submitting to it is rendered on the authenticated Settings page. The
endpoint SHALL require a valid per-session CSRF token issued by the Settings page, matching the
same write-protection pattern the platform already applies to other authenticated writes
(e.g. the Supercharger session-verification endpoint). On a successful persist, the endpoint
SHALL refresh the `theme` cookie to the newly persisted value; on any rejected or failed attempt,
the existing `theme` cookie SHALL be left unchanged. Submitting a value outside the supported
vocabulary SHALL be rejected without persisting anything. The endpoint's response SHALL NOT
instruct the client to reload or re-navigate — a theme change never triggers a page re-render.

#### Scenario: An anonymous caller cannot reach the endpoint

- **GIVEN** an anonymous visitor
- **WHEN** they submit any value to the theme switch endpoint
- **THEN** they are redirected to `/login`
- **AND** no theme preference is persisted
- **AND** the `theme` cookie is not changed

#### Scenario: A signed-in user changes their theme

- **GIVEN** a signed-in user who has loaded the Settings page (and therefore holds a valid CSRF
  token for this endpoint)
- **WHEN** they submit a supported theme value together with that CSRF token
- **THEN** the account's stored theme preference is updated to the submitted value
- **AND** only after that update succeeds is the `theme` cookie refreshed to the submitted value
- **AND** the response carries no reload/redirect instruction

#### Scenario: A request without a valid CSRF token is refused

- **GIVEN** a signed-in user
- **WHEN** they submit a supported theme value with a missing or incorrect CSRF token (including
  the case where no token was ever issued for this session)
- **THEN** the request is rejected
- **AND** no theme preference is persisted
- **AND** the `theme` cookie is not changed

#### Scenario: An unsupported theme value is rejected

- **GIVEN** a signed-in user submitting with a valid CSRF token
- **WHEN** they submit a value outside the supported theme vocabulary
- **THEN** the request is rejected
- **AND** neither the `theme` cookie nor any stored account preference is changed

#### Scenario: A failed persistence attempt leaves the cookie unchanged

- **GIVEN** a signed-in user submitting with a valid CSRF token and a supported theme value,
  whose underlying preference write fails
- **WHEN** the write fails
- **THEN** the request fails with a server error
- **AND** the `theme` cookie is left unchanged — it is never advanced to a value the write never
  actually reached

### Requirement: Instant Client-Side Theme Apply

Selecting a theme option SHALL apply that theme to the page's root element immediately, without
waiting for the persistence request to complete and without reloading or re-rendering the page.
If the background persistence request fails, the applied theme SHALL revert to the value the page
held immediately before the selection.

#### Scenario: Selecting a theme applies it before the network request resolves

- **GIVEN** a page with the theme selector rendered
- **WHEN** a user selects a different theme
- **THEN** the page's root element reflects the newly selected theme immediately
- **AND** this visual change does not wait for the background persistence request to complete
- **AND** the page does not reload or navigate

#### Scenario: A failed persistence attempt reverts the applied theme

- **GIVEN** a page whose theme was just changed via the selector, with the background
  persistence request about to fail
- **WHEN** that persistence request fails
- **THEN** the page's root element reverts to the theme it displayed before the selection
- **AND** no page reload or navigation occurs

#### Scenario: Without JavaScript, the theme still applies on the next page load

- **GIVEN** a browser with JavaScript disabled or the selector's script otherwise not running
- **WHEN** a user selects a theme option
- **THEN** the persistence request still completes normally
- **AND** the newly selected theme is reflected the next time any page is fully loaded (not
  instantly, since the instant-apply behavior itself requires JavaScript)

### Requirement: External Charge Date Restricted To Analysis Start

The gateway SHALL reject a manual charge entry whose `charged_on` date is before the
account's analysis start date, on both the create form (`POST
/ui/external-charges/create`) and the inline row-edit form (`PUT
/ui/external-charges/row/:id`). The gateway SHALL read the account's analysis start date
through the `account` module's public interface
(`account.Service.AnalysisStartDateFor`), never by any other means. The rejection SHALL
be reported through the same `charged_on`-keyed validation-error mechanism the page
already uses for every other `charged_on` validation rule (a required date, a malformed
date). An entry dated exactly ON the analysis start date SHALL be accepted. The
comparison SHALL consider only `charged_on` — an entry's optional `started_at` value
SHALL NOT be compared against the analysis start date.

The date input on both the create form and the row-edit form SHALL carry an HTML `min`
attribute set to the account's analysis start date, as a convenience only. This
attribute SHALL NOT be the enforcement mechanism — the server-side rejection above SHALL
apply regardless of the submitted value, including a submission that bypasses or
overrides the `min` attribute in the browser.

#### Scenario: Create form rejects a charge dated before the analysis start date

- **GIVEN** a signed-in user whose account's analysis start date is `2026-01-01`
- **WHEN** they submit the create form with `charged_on` set to `2025-12-31` and every
  other field valid
- **THEN** the handler does NOT call `charging.Writer.Create`
- **AND** the create form fragment is re-rendered at HTTP 422
- **AND** an error message naming the analysis start date is shown alongside the
  `charged_on` field
- **AND** the other submitted values are pre-filled in the re-rendered form

#### Scenario: Create form accepts a charge dated exactly on the analysis start date

- **GIVEN** a signed-in user whose account's analysis start date is `2026-01-01`
- **WHEN** they submit the create form with `charged_on` set to `2026-01-01` and every
  other field valid
- **THEN** the handler calls `charging.Writer.Create`
- **AND** the new entry appears in the updated charge list

#### Scenario: Create form accepts a charge dated after the analysis start date

- **GIVEN** a signed-in user whose account's analysis start date is `2026-01-01`
- **WHEN** they submit the create form with `charged_on` set to `2026-03-15` and every
  other field valid
- **THEN** the handler calls `charging.Writer.Create`

#### Scenario: Inline row edit rejects a charge dated before the analysis start date

- **GIVEN** a signed-in user editing an existing entry, whose account's analysis start
  date is `2026-01-01`
- **WHEN** they submit the edit form with `charged_on` changed to `2025-06-01`
- **THEN** the handler does NOT call `charging.Writer.Update`
- **AND** the row-edit fragment is re-rendered at HTTP 422 with the same
  `charged_on`-keyed error the create form uses
- **AND** the other submitted values on that row are preserved

#### Scenario: The rejection reads the analysis start date through the account module's public interface

- **GIVEN** a create or edit submission whose `charged_on` needs to be checked against
  the analysis start date
- **WHEN** the gateway performs the check
- **THEN** it calls `account.Service.AnalysisStartDateFor(ctx, accountID)`
- **AND** it does not read `internal/account`'s tables or internals by any other means

#### Scenario: internal/charging is not involved in the rejection

- **GIVEN** the analysis-start-date rejection rule
- **WHEN** a manual charge entry is validated
- **THEN** the rejection is decided and enforced entirely inside `internal/gateway`
- **AND** `internal/charging`'s `Writer`/`Reader` interfaces and validation rules are
  unchanged by this rule

#### Scenario: started_at is not compared against the analysis start date

- **GIVEN** a signed-in user submitting a create or edit form where `charged_on` is on or
  after the account's analysis start date, but the optional `started_at` value is before
  it
- **WHEN** the form is submitted
- **THEN** the submission is NOT rejected for this reason
- **AND** the entry is persisted with the submitted `started_at` value unchanged

#### Scenario: The date input carries a min attribute reflecting the analysis start date

- **GIVEN** a signed-in user opens the External charges page, whose account's analysis
  start date is `2026-01-01`
- **WHEN** the create form's `charged_on` date input is rendered
- **THEN** the input carries `min="2026-01-01"`
- **AND** the same `min` attribute is present on the `charged_on` input of a row opened
  for inline editing

#### Scenario: The min attribute is a convenience, not the enforcement

- **GIVEN** a signed-in user whose browser is made to submit a `charged_on` value earlier
  than the date input's `min` attribute (e.g. via a modified request, bypassing the
  browser's own constraint validation)
- **WHEN** the server receives the submission
- **THEN** the server-side rejection above still applies
- **AND** the entry is still not persisted

### Requirement: Dashboard Vehicle Status Panel Groups Metrics Into Named Subsections

The dashboard's Vehicle Status card SHALL present its metrics as: a lifetime-facts area
(the vehicle image plus the odometer reading and the lifetime count of charges to 100%,
stacked, not grouped under a subsection heading) and a set of named subsections, each
introduced by a title and a one-sentence description resolved through the translation
catalogue. On a viewport at or above the module's tablet breakpoint the lifetime-facts
area and the subsections SHALL render as two side-by-side columns; below that breakpoint
they SHALL stack in one column, with the lifetime-facts area (image and its two tiles)
appearing before the subsections.

A subsection MAY exist in the layout with no content yet, reserved for a metric a later
capability adds — such a reservation SHALL render nothing (no heading, no empty grid)
until that capability ships.

#### Scenario: Desktop viewport shows two side-by-side columns

- **GIVEN** a signed-in user with an active vehicle and a stored status row
- **WHEN** the dashboard is rendered on a viewport at or above the tablet breakpoint
- **THEN** the vehicle image, odometer tile and 100%-charges tile render in a left
  column
- **AND** the named subsections render in a right column, each with its own title and
  description

#### Scenario: Narrow viewport stacks lifetime facts before subsections

- **GIVEN** the same signed-in user
- **WHEN** the dashboard is rendered on a viewport below the tablet breakpoint
- **THEN** the vehicle image and its two tiles render first
- **AND** every subsection renders after them, in the same top-to-bottom order as the
  desktop layout

#### Scenario: A reserved, not-yet-built subsection renders nothing

- **GIVEN** the layout reserves a position for a subsection whose data capability has
  not shipped yet
- **WHEN** the dashboard is rendered
- **THEN** no heading, description, or empty tile grid appears at that position
- **AND** the subsections before and after it render normally, unaffected by the gap

### Requirement: Dashboard Travel Progress Subsection

The dashboard's Vehicle Status card SHALL include a "Travel Progress" subsection
showing two values for the active vehicle's latest computed day: the distance
travelled and the battery percentage used. Each value SHALL be read from the
account's latest precomputed vehicle-status row through the existing analytics read
port — no new database read, no live Tesla Fleet API call.

The distance-travelled value SHALL always display a trend indicator meaning "this
metric accumulates" (a visually up/increasing indicator), and the battery-used value
SHALL always display a trend indicator meaning "this metric depletes" (a visually
down/decreasing indicator) — both indicators SHALL be fixed by the metric's identity,
not computed from whether the value increased or decreased since a previous day. Each
indicator SHALL be rendered in a distinct semantic color (an "increasing" color for
distance travelled, a "decreasing" color for battery used), never a hardcoded color
value.

Either value SHALL render a placeholder — never a fabricated zero — when the
underlying computed value is absent, which happens when the latest computed day has no
prior day to compare against, or when there is no stored status row at all for the
active vehicle.

#### Scenario: Both values render with their fixed trend indicators

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row reports a
  non-absent distance-travelled value and a non-absent battery-used value for the
  latest computed day
- **WHEN** the dashboard is rendered
- **THEN** the Travel Progress subsection shows the distance travelled with an
  "increasing" trend indicator in its semantic color
- **AND** shows the battery percentage used with a "decreasing" trend indicator in its
  own semantic color
- **AND** neither indicator's direction depends on whether that day's value was larger
  or smaller than a previous day's

#### Scenario: A day with no predecessor renders a placeholder, not a fabricated zero

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row exists but
  its distance-travelled and battery-used values are absent (the latest computed day
  has no prior day to derive them against)
- **WHEN** the dashboard is rendered
- **THEN** the Travel Progress subsection shows its placeholder for both values
- **AND** neither value is shown as `0`

#### Scenario: No stored status row renders the same placeholder as every other tile

- **GIVEN** a signed-in user whose active vehicle has no stored status row yet
- **WHEN** the dashboard is rendered
- **THEN** the Travel Progress subsection shows its placeholder for both values,
  identically to how every other tile on the card renders its own placeholder in this
  state

#### Scenario: No new database read or Tesla call is introduced

- **GIVEN** the Travel Progress subsection's two values
- **WHEN** the dashboard is rendered
- **THEN** both values come from the same account-status read the rest of the card
  already performs
- **AND** no additional database query and no Tesla Fleet API call is made to render
  this subsection

#### Scenario: Both labels resolve through the translation catalogue

- **GIVEN** the Travel Progress subsection's title, description, and the two value
  labels
- **WHEN** they are rendered in either supported language
- **THEN** each resolves through the translation catalogue
- **AND** both `ES` and `EN` are non-empty for every one of them

### Requirement: Dashboard Tire Pressure Subsection

The dashboard's Vehicle Status card SHALL include a "Tire pressure (PSI)" subsection
showing one value per wheel — front-left, front-right, rear-left, rear-right — for the
active vehicle. Each value SHALL be read from the account's latest precomputed
vehicle-status row through the existing analytics read port — no new database read, no
live Tesla Fleet API call. The four wheel values SHALL render as a 2-by-2 grid, not a
single row of four.

Each wheel's tile SHALL show, independently of the other wheels:

- The current pressure reading, or a placeholder — never a fabricated value — when the
  vehicle did not report that wheel's pressure or the underlying row predates this
  capability.
- A trend indicator meaning "increased" when that wheel's day-over-day change is a
  positive number, or "decreased" when it is a negative number. When the change is
  absent (no prior day to compare against, or either day's reading for that wheel is
  itself missing), the tile SHALL show no trend indicator at all — never a fabricated
  "no change" indicator. When the change is present and exactly zero, the tile SHALL
  also show no trend indicator, because neither "increased" nor "decreased" applies —
  this is the same "no indicator" appearance as the absent case, but for a different
  reason.
- A numeric line stating the day-over-day change, whenever that change is present
  (including when it is exactly zero) — never rendered at all when the change is
  absent.

A wheel's trend indicator SHALL be rendered through the same mechanism the "Travel
Progress" subsection uses for its own trend indicators — no second, independent way of
rendering a trend indicator SHALL be introduced by this capability.

#### Scenario: A wheel with a positive change shows an "increased" indicator and its numeric line

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row reports a
  wheel's current pressure and a positive day-over-day change for that wheel
- **WHEN** the dashboard is rendered
- **THEN** that wheel's tile shows the current pressure reading
- **AND** shows an "increased" trend indicator
- **AND** shows a numeric line stating the change, with an explicit positive sign

#### Scenario: A wheel with a negative change shows a "decreased" indicator and its numeric line

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row reports a
  wheel's current pressure and a negative day-over-day change for that wheel
- **WHEN** the dashboard is rendered
- **THEN** that wheel's tile shows the current pressure reading
- **AND** shows a "decreased" trend indicator, visually distinct from the "increased"
  indicator
- **AND** shows a numeric line stating the change, with its negative sign

#### Scenario: A wheel with an exactly-zero change shows no indicator but still shows its numeric line

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row reports a
  wheel's current pressure and a day-over-day change of exactly zero for that wheel
- **WHEN** the dashboard is rendered
- **THEN** that wheel's tile shows the current pressure reading
- **AND** shows no trend indicator — the change is neither an increase nor a decrease
- **AND** still shows a numeric line stating the change is zero, because a known
  zero change is not the same as an unknown one

#### Scenario: A wheel with an absent change shows no indicator and no numeric line

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row reports a
  wheel's current pressure but no day-over-day change for that wheel (no prior day to
  compare against, or either day's reading for that wheel is itself missing)
- **WHEN** the dashboard is rendered
- **THEN** that wheel's tile shows the current pressure reading
- **AND** shows no trend indicator
- **AND** shows no numeric change line at all — never a fabricated "0" line for an
  unknown change

#### Scenario: A wheel with no reported pressure shows the placeholder, independently of its own change

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row does not
  report a given wheel's current pressure (the vehicle did not report TPMS at capture,
  or the row predates this capability)
- **WHEN** the dashboard is rendered
- **THEN** that wheel's tile shows the same placeholder every other absent value on
  this card shows — never a fabricated reading
- **AND** the other three wheels render independently, unaffected by this one wheel's
  missing reading

#### Scenario: The four wheels render as a 2-by-2 grid

- **GIVEN** a signed-in user with an active vehicle and a stored status row
- **WHEN** the dashboard is rendered on a viewport at or above the module's tablet
  breakpoint
- **THEN** the Tire pressure (PSI) subsection shows its four wheel tiles arranged as
  two rows of two, not one row of four

#### Scenario: No stored status row renders the same placeholder as every other tile

- **GIVEN** a signed-in user whose active vehicle has no stored status row yet
- **WHEN** the dashboard is rendered
- **THEN** the Tire pressure (PSI) subsection shows its placeholder for all four
  wheels, identically to how every other tile on the card renders its own placeholder
  in this state
- **AND** no wheel shows a trend indicator or a numeric change line

#### Scenario: No new database read or Tesla call is introduced

- **GIVEN** the Tire pressure (PSI) subsection's four wheel values
- **WHEN** the dashboard is rendered
- **THEN** all four come from the same account-status read the rest of the card
  already performs
- **AND** no additional database query and no Tesla Fleet API call is made to render
  this subsection

#### Scenario: All labels resolve through the translation catalogue

- **GIVEN** the Tire pressure (PSI) subsection's title, description, the four wheel
  labels, and the numeric change line's wording
- **WHEN** they are rendered in either supported language
- **THEN** each resolves through the translation catalogue
- **AND** both `ES` and `EN` are non-empty for every one of them

#### Scenario: The subsection reuses the Travel Progress trend mechanism, not a second one

- **GIVEN** the Tire pressure (PSI) subsection's trend indicators and the Travel
  Progress subsection's own trend indicators, both rendered on the same dashboard
- **WHEN** either subsection's indicator is inspected
- **THEN** both are produced by the same underlying rendering mechanism
- **AND** this capability introduces no second, independent mechanism for showing a
  trend indicator

