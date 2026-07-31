## ADDED Requirements

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
consecutive daily odometer readings converted to kilometres via the `OdometerKm()` companion — not
the cumulative odometer value. A negative computed delta SHALL be shown as zero. The "Battery
history" bars SHALL represent the **battery level percentage** at each snapshot (absolute 0–100).
Each bar SHALL carry a hover tooltip: the odometer bar's tooltip SHALL include the date, the
kilometres driven that day, and the cumulative odometer in kilometres; the battery bar's tooltip
SHALL include the date, the level percentage, and the rated range in kilometres (`BatteryRangeKm()`).

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
  (the odometer delta converted via `OdometerKm()`)
- **AND** a bar whose computed delta is negative is shown as zero
- **AND** each bar's tooltip shows the date, the kilometres driven that day, and the cumulative
  odometer in kilometres

#### Scenario: Battery bars are absolute level with a range tooltip

- **GIVEN** a selected vehicle with stored snapshots
- **WHEN** the battery history chart is rendered
- **THEN** each bar's height represents that snapshot's battery level percentage (0–100)
- **AND** each bar's tooltip shows the date, the battery level percentage, and the rated range in
  kilometres (`BatteryRangeKm()`)

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
