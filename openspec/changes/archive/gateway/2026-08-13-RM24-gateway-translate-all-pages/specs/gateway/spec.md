## ADDED Requirements

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

## MODIFIED Requirements

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
