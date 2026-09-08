## MODIFIED Requirements

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

## ADDED Requirements

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
