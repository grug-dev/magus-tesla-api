## MODIFIED Requirements

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
