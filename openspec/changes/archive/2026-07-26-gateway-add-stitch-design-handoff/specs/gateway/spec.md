# gateway Specification (delta — gateway-add-stitch-design-handoff)

Delta against `openspec/specs/gateway/spec.md`. Describes the
behavioral changes to the `gateway` capability introduced by this
change. Implementation (HOW) lives in `design.md`; this file states
WHAT the capability does, in GIVEN/WHEN/THEN.

The dev-workflow/convention part of this change (the Stitch MCP wiring
and the `ai/htmx-conventions.md` Stitch→`ui/`-kit translation section)
is enablement/convention, NOT runtime behavior — no behavioral spec is
required for it, per `openspec/config.yaml` `specs:` rules.

---

## ADDED Requirements

#### Requirement: Login Page (re-skinned)

The gateway SHALL serve a re-skinned sign-in page at `GET /login` that
presents the app's Google login flow as a single "Continue with Google"
affordance. The page SHALL NOT present email/password fields, a separate
"Sign In" action, or an "OR" divider. The "Continue with Google"
control SHALL start the existing Google OAuth flow at
`/auth/google/login` — no new auth model, route, or persistence is
introduced.

Rationale (grill D6 + D12): this is a VISUAL re-skin of the existing
Google login; Stitch's email/password/"Sign In"/"OR" elements are
dropped, and the external background image + all inline client-side JS
are dropped.

##### Scenario: Anonymous visitor sees the re-skinned login

- **GIVEN** an anonymous visitor
- **WHEN** they request `GET /login`
- **THEN** the gateway responds with an HTML page built from the
  unauthenticated base layout
- **AND** the page shows a "Continue with Google" control
- **AND** the page does NOT render an email input, a password input, a
  standalone "Sign In" button, or an "OR" divider
- **AND** the page contains no inline `<script>` and no external CDN /
  hotlinked image asset (the background is a CSS gradient using semantic
  theme tokens)

#### Scenario: "Continue with Google" starts the existing OAuth flow

- **GIVEN** an anonymous visitor on the re-skinned login page
- **WHEN** they activate the "Continue with Google" control
- **THEN** the browser navigates to `/auth/google/login`
- **AND** the existing Google OAuth flow begins (the gateway stores a
  CSRF state and redirects to Google's consent screen) — unchanged from
  pre-change behavior

#### Scenario: Login page uses semantic theme tokens only

- **GIVEN** the re-skinned login template
- **WHEN** it is rendered
- **THEN** no hardcoded hex color / raw Tailwind palette utility appears
  in the login template
- **AND** the page re-skins from the single `<html data-theme>`
- **AND** the only hardcoded color values permitted are the Google "G"
  SVG's official brand fills (a third-party logo's colors, not the app
  palette), clearly commented as that exception

#### Scenario: Login page needs no view model

- **GIVEN** the gateway handler rendering `GET /login`
- **WHEN** it renders the page
- **THEN** it renders the page with no per-request data
- **AND** no domain-module port is called to render the login page

---

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