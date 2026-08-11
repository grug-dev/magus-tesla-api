## MODIFIED Requirements

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
A negative computed delta SHALL be shown as zero.

The "Battery history" bars SHALL represent **battery percentage consumed per day** — the drop
between consecutive daily snapshots' `BatteryLevelPct`, computed as the **previous** day's level
minus the **current** day's level (the reverse operand order from the odometer delta, since
consumption is a decrease, not an increase). A negative computed delta (the vehicle net-charged
that day) SHALL be shown as zero. Each bar's height SHALL be expressed as a percentage of the
window's largest single-day consumed delta, so the largest-consumption day in the window is always
the full-height reference bar — mirroring how the odometer chart already scales its own bars
against its window's maximum. To produce `days` consumed-delta bars, the handler SHALL use the
`days+1` most recent snapshots in the window (the same "one extra, older baseline snapshot"
mechanism the odometer chart already uses), so that **the first rendered bar is a real delta
against the snapshot immediately before it** — never a stub, and never dropped. The battery
chart's tooltip SHALL NOT change: it SHALL continue to show the **current day's own absolute
values** — the battery level percentage and rated range of that day's snapshot — even though the
bar it is attached to now represents a different quantity (consumed %, not absolute level). This
is a deliberate exception to this module's usual "bar and tooltip describe the same fact"
convention; see `design.md` D5 for the accepted trade-off.

Each bar SHALL carry a hover tooltip: the odometer bar's tooltip SHALL include the date, the
kilometres driven that day, and the cumulative odometer in kilometres; the battery bar's tooltip
SHALL include the date, the level percentage, and the rated range in kilometres — both computed
from the current day's own snapshot, unrelated to what each bar's height represents.

The date shown in each tooltip SHALL be the snapshot's **`EffectiveDate`** (the calendar day the
nightly snapshot represents — `CapturedAt` − 1 day, exposed by `telemetry.Reader`), formatted
**`MM-DD`** by the Go handler. The gateway SHALL NOT show the capture-morning date (`CapturedAt`)
in the tooltip, and SHALL NOT format dates inside the template. Each bar SHALL also carry a
per-bar **date label** rendered under the bar: a pre-formatted `MM-DD` string derived from the
snapshot's `EffectiveDate`, computed by the handler. The label orientation SHALL be **adaptive** —
**horizontal** for the 6-day preset (where bars are wide) and **rotated vertical** for the 14- and
30-day presets (where bars are narrow). The orientation SHALL be decided by the handler and exposed
to the template as a single chart-level boolean flag (`LabelVertical`); the template SHALL NOT
compare `days`, compute rotation, or call `time.Format`.

The charts SHALL be rendered as **responsive inline SVG** (scaling to the container width) using no
client-side charting library. All numeric values — bar heights, deltas, percentages, tooltip
strings, and per-bar label strings — SHALL be computed by the Go handler before the template
renders; the template SHALL perform no arithmetic, unit conversion, date formatting, or method calls
on domain types. Bar colours SHALL use DaisyUI semantic tokens (no hardcoded hex). The per-bar date
label SHALL be an SVG `<text>` element (horizontal, or rotated via an SVG `transform` when vertical)
using a muted semantic content token; no HTML `<div>` labels and no client-side library SHALL be
used for the labels.

Changing the day count SHALL re-fetch and re-render both charts without a full page reload. When
too few snapshots exist to draw a chart — **fewer than two snapshots for either the odometer delta
chart or the battery consumed-delta chart** (both charts now require a pair of snapshots to form a
delta) — that chart SHALL show the existing empty-state placeholder instead of fabricated bars.

#### Scenario: History charts render for the selected vehicle

- **GIVEN** a signed-in user whose selected vehicle has several stored nightly snapshots
- **WHEN** the history fragment is requested (`GET /ui/dashboard/history`) with no `days` parameter
- **THEN** the response renders an "Odometer history" chart and a "Battery history" chart for the
  selected vehicle using a 6-day window
- **AND** the odometer bars show kilometres driven per day (consecutive-day deltas in kilometres)
- **AND** the battery bars show battery percentage consumed per day (consecutive-day deltas)
- **AND** both charts render the same number of bars
- **AND** every bar carries a `MM-DD` date label under it
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

#### Scenario: Battery bars are % consumed per day, not absolute level

- **GIVEN** a selected vehicle whose two most recent daily snapshots recorded battery levels of
  93% and then 69%, with no charging in between
- **WHEN** the battery history chart is rendered
- **THEN** the bar for the second (69%) snapshot's day has a value of **24** — the previous day's
  level (93) minus the current day's level (69) — not the absolute level 69
- **AND** that bar's height is expressed as a percentage of the window's largest single-day
  consumed delta
- **AND** that bar's tooltip shows the date, the battery level percentage (69%), and the rated
  range in kilometres — the current day's own absolute values, unrelated to the bar's height

#### Scenario: The first battery bar uses the extra baseline snapshot, never a stub

- **GIVEN** a selected vehicle with a 6-day window requested (`days=6`)
- **AND** the telemetry reader returns 7 snapshots for that window (the existing `since` window
  already spans `days+1` calendar days)
- **WHEN** the battery history chart is rendered
- **THEN** exactly 6 bars are rendered, the same count as the odometer chart
- **AND** the first (oldest-day) rendered bar is a real delta computed against the 7th, extra,
  older snapshot — not a zero-value stub and not omitted from the chart

#### Scenario: A charging day is clamped to zero, not shown as negative

- **GIVEN** a selected vehicle whose battery level rose from one day's snapshot to the next (the
  vehicle charged more than it consumed that day)
- **WHEN** the battery history chart is rendered
- **THEN** the bar for that day has a height of zero (the computed negative delta is clamped, the
  same clamp behavior the odometer chart already applies to its own anomaly case)
- **AND** the chart renders no negative-height or below-baseline bar
- **AND** that bar's tooltip is unaffected — it still shows that day's own absolute battery level
  and range, exactly as on any other day

#### Scenario: Fewer than two snapshots renders the empty state, not a single absolute-level bar

- **GIVEN** a selected vehicle with exactly one stored snapshot in the requested window
- **WHEN** the battery history chart is rendered
- **THEN** the battery chart shows the empty-state placeholder (no fabricated or absolute-level
  bar is rendered)
- **AND** this matches the odometer chart's existing behavior for the same "only one snapshot"
  case

#### Scenario: Tooltip date is the EffectiveDate in MM-DD, not the capture morning

- **GIVEN** a selected vehicle with a snapshot whose `CapturedAt` is `2026-08-08 03:30 UTC`
- **AND** whose `EffectiveDate` is therefore `2026-08-07`
- **WHEN** either history chart's bar for that snapshot is rendered
- **THEN** the bar's tooltip shows the date as `08-07` (MM-DD of the `EffectiveDate`)
- **AND** the tooltip does NOT show `2026-08-08` (the `CapturedAt` morning) or a `YYYY-MM-DD`
  string
- **AND** the `MM-DD` string is pre-formatted by the Go handler (the template performs no
  `time.Format`)

#### Scenario: Each bar carries a MM-DD label derived from EffectiveDate

- **GIVEN** a selected vehicle with stored snapshots
- **WHEN** either history chart is rendered
- **THEN** every bar has a `MM-DD` date label rendered under it
- **AND** the label string equals the snapshot's `EffectiveDate` formatted `MM-DD`
- **AND** the label uses the same date as the bar's tooltip (no 1-day mismatch between label and
  tooltip)
- **AND** the label string is pre-formatted by the Go handler (the template emits it verbatim)

#### Scenario: Label orientation is adaptive to the day-count preset

- **GIVEN** the history fragment rendered with the 6-day preset
- **WHEN** the per-bar labels are rendered
- **THEN** the labels are horizontal under each bar
- **AND** the handler set the chart's `LabelVertical` flag to false

- **GIVEN** the history fragment rendered with the 14- or 30-day preset
- **WHEN** the per-bar labels are rendered
- **THEN** the labels are rotated vertical (via an SVG `transform`) so each label fits within its
  narrow bar's width
- **AND** the handler set the chart's `LabelVertical` flag to true
- **AND** the template chose the orientation solely from the `LabelVertical` flag (it did not
  compare `days` or compute rotation)

#### Scenario: Changing the day count re-fetches both charts

- **GIVEN** a signed-in user viewing the dashboard history region with the default window
- **WHEN** the user selects a different day-count preset
- **THEN** the region re-fetches `GET /ui/dashboard/history?days=N` for the newly-selected count
- **AND** both the odometer and battery charts re-render for that window without a full page reload
- **AND** the selected preset is marked active in the day-count control
- **AND** the per-bar label orientation updates to match the new preset (horizontal for 6,
  vertical for 14/30)

#### Scenario: History region follows a vehicle switch

- **GIVEN** a signed-in user with two or more registered vehicles on the dashboard
- **AND** the history region is nested inside the `#dashboard-content` region that subscribes to
  `vehicle-changed`
- **WHEN** the user switches the active vehicle
- **THEN** the dashboard content re-renders and the history region reloads its charts for the
  newly-selected vehicle at the default window

#### Scenario: Sparse data falls back to the empty state

- **GIVEN** a selected vehicle with fewer than two stored snapshots
- **WHEN** either history chart is rendered
- **THEN** the odometer chart shows the empty-state placeholder (no fabricated bars)
- **AND** the battery chart also shows its own empty-state placeholder (no fabricated bars) —
  unlike before this change, one lone snapshot is no longer enough to render a battery bar

#### Scenario: Charts contain no business logic in templates

- **GIVEN** the history chart and selector templates
- **WHEN** they render
- **THEN** the bar heights, per-day kilometre deltas, per-day battery-consumed-percentage deltas,
  tooltip strings, per-bar `MM-DD` label strings, and the `LabelVertical` orientation flag have all
  been computed by the Go handler before the template receives the view model
- **AND** the templates use only presentation logic (if/for/display) — no arithmetic, no unit
  conversion, no date formatting, no method calls on domain types
- **AND** the SVG scales to the container width (responsive) and bar colours use DaisyUI semantic
  tokens with no hardcoded hex
- **AND** the per-bar label is an SVG `<text>` (rotated via SVG `transform` when vertical), using a
  muted semantic token — no HTML `<div>` label and no client-side library

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
