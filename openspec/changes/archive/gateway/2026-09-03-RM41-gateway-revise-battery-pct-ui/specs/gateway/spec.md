## MODIFIED Requirements

### Requirement: Supercharger Stats session table displays battery percentages

The Supercharger Stats session table SHALL add two columns for the existing
`charging.Session` battery values: start battery percentage and end battery
percentage. **This is a CHANGE from the prior revision of this requirement, under
which the table added four columns — start battery percentage, end battery
percentage, start battery percentage estimate, and end battery percentage estimate
— the two estimate columns are REMOVED by this revision
(`RM41-gateway-revise-battery-pct-ui`), because nothing in the repository has ever
written them and the estimator originally intended to fill them
(`derivedStartBatteryPct`) instead writes the real `start_battery_pct` column.**
The gateway SHALL map each remaining value to a preformatted view-model string
before rendering: a present value is rendered as its integer percentage followed by
`%`, and a nil value is rendered exactly as `"—"`.

The table SHALL obtain these values from the existing `charging.SessionReader`
result only; it SHALL NOT issue an additional read, write, Tesla API call, or
database query. The table SHALL retain Date, Site, Energy, and Cost and SHALL NOT
restore Country or Billing Type.

Every new header SHALL resolve through the existing gateway i18n catalogue with
non-empty ES and EN translations.

#### Scenario: Populated session battery values render in the table

- **GIVEN** a Supercharger session whose start and end battery percentage values
  are 40 and 80
- **WHEN** the Supercharger Stats page is rendered
- **THEN** its row displays `40%` and `80%` in the two battery columns
- **AND** the table displays translated headers for both columns in the active
  language
- **AND** Date, Site, Energy, and Cost remain displayed
- **AND** the row contains no start-estimate or end-estimate cell

#### Scenario: Missing battery values degrade visibly without an estimator

- **GIVEN** a Supercharger session whose start and end battery percentage values
  are NULL
- **WHEN** the Supercharger Stats page is rendered
- **THEN** both battery cells display `"—"`
- **AND** neither cell displays `0%`, an empty string, or a calculated estimate

#### Scenario: Country remains absent

- **GIVEN** any Supercharger Stats page render
- **WHEN** the session table is rendered
- **THEN** it contains no Country header or Country cell
- **AND** the gateway does not add a Country i18n key for this page

#### Scenario: The estimate columns are gone entirely

- **GIVEN** any Supercharger Stats page render, populated or empty
- **WHEN** the session table (or its edit row) is rendered
- **THEN** it contains no start-estimate or end-estimate header, cell, or field
- **AND** no `i18n` key resolves a "start estimate" or "end estimate" label

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

## ADDED Requirements

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
