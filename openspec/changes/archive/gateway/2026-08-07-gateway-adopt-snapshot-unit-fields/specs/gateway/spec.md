## MODIFIED Requirements

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

### Requirement: Dashboard History Charts

The gateway SHALL render two per-vehicle history bar charts in the dashboard bento — an "Odometer
history" chart and a "Battery history" chart — for the currently selected vehicle, replacing the
"awaiting nightly snapshots" placeholders. The chart data SHALL come exclusively from the
`telemetry.Reader` port (the per-vehicle history read port); the gateway SHALL NOT read telemetry
tables directly and SHALL NOT make a live Tesla Fleet API call to render the charts.

The charts SHALL be served by an authenticated htmx fragment endpoint `GET /ui/dashboard/history`
that accepts a `days` query parameter. The endpoint SHALL resolve the target vehicle from the
session-selected vehicle (never from the URL) and scope the read to that vehicle's Tesla id and the
caller's account. Anonymous requests SHALL be redirected to `/login` with no history data served.

The `days` parameter SHALL be validated against a fixed, small preset set; a missing, invalid, or
out-of-set value SHALL fall back to the default of **6**. The same `days` window SHALL drive
**both** charts, which SHALL render the **same number of bars**.

The "Odometer history" bars SHALL represent **kilometres driven per day** — the difference between
consecutive daily odometer readings, which the telemetry port already provides in kilometres — not
the cumulative odometer value. The gateway SHALL NOT convert units when building either chart.
A negative computed delta SHALL be shown as zero. The "Battery
history" bars SHALL represent the **battery level percentage** at each snapshot (absolute 0–100).
Each bar SHALL carry a hover tooltip: the odometer bar's tooltip SHALL include the date, the
kilometres driven that day, and the cumulative odometer in kilometres; the battery bar's tooltip
SHALL include the date, the level percentage, and the rated range in kilometres.

The charts SHALL be rendered as **responsive inline SVG** (scaling to the container width) using no
client-side charting library. All numeric values — bar heights, deltas, percentages, and tooltip
strings — SHALL be computed by the Go handler before the template renders; the template SHALL
perform no arithmetic, unit conversion, or method calls on domain types. Bar colours SHALL use
DaisyUI semantic tokens (no hardcoded hex).

Changing the day count SHALL re-fetch and re-render both charts without a full page reload. When
too few snapshots exist to draw a chart (fewer than two snapshots for the odometer delta chart, or
none for the battery chart), that chart SHALL show the existing empty-state placeholder instead of
fabricated bars.

#### Scenario: History charts render for the selected vehicle

- **GIVEN** a signed-in user whose selected vehicle has several stored nightly snapshots
- **WHEN** the history fragment is requested (`GET /ui/dashboard/history`) with no `days` parameter
- **THEN** the response renders an "Odometer history" chart and a "Battery history" chart for the
  selected vehicle using a 6-day window
- **AND** the odometer bars show kilometres driven per day (consecutive-day deltas in kilometres)
- **AND** the battery bars show the battery level percentage at each snapshot
- **AND** both charts render the same number of bars
- **AND** no live Tesla Fleet API call is made and no telemetry table is read directly

#### Scenario: Day count is validated and defaults to 6

- **GIVEN** a signed-in user on the dashboard
- **WHEN** the history fragment is requested with a `days` value that is missing, non-numeric, or
  not one of the allowed presets
- **THEN** the charts render using the default window of 6 days
- **AND** when `days` is one of the allowed presets, the charts render using that window
- **AND** the same window is applied to both the odometer and the battery chart

#### Scenario: Odometer bars are km driven per day, not cumulative odometer

- **GIVEN** a selected vehicle whose consecutive snapshots have increasing odometer readings
- **WHEN** the odometer history chart is rendered
- **THEN** each bar represents the kilometres driven between two consecutive daily snapshots
  (the delta between the snapshots' stored kilometre odometer readings, computed without any
  unit conversion)
- **AND** a bar whose computed delta is negative is shown as zero
- **AND** each bar's tooltip shows the date, the kilometres driven that day, and the cumulative
  odometer in kilometres

#### Scenario: Battery bars are absolute level with a range tooltip

- **GIVEN** a selected vehicle with stored snapshots
- **WHEN** the battery history chart is rendered
- **THEN** each bar's height represents that snapshot's battery level percentage (0–100)
- **AND** each bar's tooltip shows the date, the battery level percentage, and the rated range in
  kilometres, read directly from the snapshot

#### Scenario: Changing the day count re-fetches both charts

- **GIVEN** a signed-in user viewing the dashboard history region with the default window
- **WHEN** the user selects a different day-count preset
- **THEN** the region re-fetches `GET /ui/dashboard/history?days=N` for the newly-selected count
- **AND** both the odometer and battery charts re-render for that window without a full page reload
- **AND** the selected preset is marked active in the day-count control

#### Scenario: History region follows a vehicle switch

- **GIVEN** a signed-in user with two or more registered vehicles on the dashboard
- **AND** the history region is nested inside the `#dashboard-content` region that subscribes to
  `vehicle-changed`
- **WHEN** the user switches the active vehicle
- **THEN** the dashboard content re-renders and the history region reloads its charts for the
  newly-selected vehicle at the default window

#### Scenario: Sparse data falls back to the empty state

- **GIVEN** a selected vehicle with fewer than two stored snapshots
- **WHEN** the odometer history chart is rendered
- **THEN** the odometer chart shows the empty-state placeholder (no fabricated bars)
- **AND** the battery chart shows bars only if at least one snapshot exists, otherwise its own
  empty-state placeholder

#### Scenario: Charts contain no business logic in templates

- **GIVEN** the history chart and selector templates
- **WHEN** they render
- **THEN** the bar heights, per-day kilometre deltas, battery percentages, and tooltip strings have
  all been computed by the Go handler before the template receives the view model
- **AND** the templates use only presentation logic (if/for/display) — no arithmetic, no unit
  conversion, no method calls on domain types
- **AND** the SVG scales to the container width (responsive) and bar colours use DaisyUI semantic
  tokens with no hardcoded hex

#### Scenario: Gateway never imports telemetrydb for history

- **GIVEN** the gateway handler that builds the history charts
- **WHEN** it obtains the vehicle's snapshot history
- **THEN** it does so exclusively through the `telemetry.Reader` public interface
- **AND** it imports no package from `internal/telemetry/db` (`telemetrydb`)
- **AND** no `pgtype` type appears in any gateway file involved

#### Scenario: History fragment is not served to anonymous callers

- **GIVEN** an unauthenticated request to `GET /ui/dashboard/history`
- **WHEN** the handler resolves the session
- **THEN** the request is redirected to `/login` and no history data is served
