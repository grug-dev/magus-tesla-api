## ADDED Requirements

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

## MODIFIED Requirements

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
