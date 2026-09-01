## MODIFIED Requirements

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

## ADDED Requirements

### Requirement: Dashboard Vehicle Status Card Shows Locked and Sentry-Mode Badges, Not a Status Tile

The dashboard's Vehicle Status card (`/dashboard`, `/ui/dashboard`) SHALL show the
active vehicle's locked state and sentry-mode state as badges in the card's header
row, alongside the existing staleness badge, and SHALL NOT show a separate "Status"
stat tile in its mini-stat grid. The card subtitle SHALL continue to show the
charging-derived status word exactly as before this change — only the stat tile is
removed, not the subtitle's status word. The mini-stat grid SHALL render its
remaining three tiles (odometer, interior temperature, exterior temperature) as a
single row.

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
- **AND** the mini-stat grid shows exactly three tiles: odometer, interior
  temperature, exterior temperature — no "Status" tile

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
