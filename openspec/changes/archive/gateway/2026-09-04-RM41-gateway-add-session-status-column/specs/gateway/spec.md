## MODIFIED Requirements

### Requirement: Supercharger Stats session table displays battery percentages

The Supercharger Stats session table SHALL display the following columns, in
order: Date, Status, Site, Energy, Cost, start battery percentage, end battery
percentage, Actions. **This is a CHANGE from the prior revision of this
requirement, under which the table had seven columns with no Status column — a
Status column is ADDED by this revision (`RM41-gateway-add-session-status-column`)
as the 2nd column, immediately after Date**, taking the table from 7 columns to
8. Every other column, its formatting rule, and its data source are unchanged
from the prior revision.

The table SHALL obtain the Status column's value from the existing
`charging.SessionReader` result only (`charging.Session.Status`); it SHALL NOT
issue an additional read, write, Tesla API call, or database query to display it.

Every new header SHALL resolve through the existing gateway i18n catalogue with
non-empty ES and EN translations.

#### Scenario: The Status column is the 2nd column

- **GIVEN** any Supercharger Stats page render
- **WHEN** the session table's headers are rendered
- **THEN** the 2nd header is the translated Status label ("Estado"/"Status")
- **AND** the 1st header remains Date and the 3rd header is Site

#### Scenario: Populated session battery values still render alongside Status

- **GIVEN** a Supercharger session whose start and end battery percentage values
  are 40 and 80
- **WHEN** the Supercharger Stats page is rendered
- **THEN** its row displays `40%` and `80%` in the two battery columns exactly as
  before this change
- **AND** the same row also displays a Status badge in its 2nd cell

## ADDED Requirements

### Requirement: Supercharger Stats session status badge

The Supercharger Stats session table's Status column SHALL render one badge per
session, using the existing `ui.Badge` component, reflecting
`charging.Session.Status` (`charging.SessionStatus`: `IN_PROGRESS` |
`DONE_CALCULATED` | `DONE`). The mapping from status to Badge `Kind` and label
SHALL be:

| `charging.SessionStatus` | Badge `Kind` | Label (ES / EN) |
|---|---|---|
| `DONE` | `primary` | Finalizada / Done |
| `DONE_CALCULATED` | `neutral` | Finalizada (calculada) / Done (calculated) |
| `IN_PROGRESS` | `ghost` | En progreso / In progress |

The badge SHALL NOT use the `success` or `warning` Kind for any status. Every
label SHALL resolve through the gateway i18n catalogue with non-empty ES and EN
translations. The gateway SHALL NOT add a `ui.Dot` completeness indicator
alongside this badge.

The gateway SHALL NOT expose any way to set or change `Status` through this or
any route — the value is read-only from the gateway's perspective, always
computed by `charging.SessionVerifier.VerifySession`.

#### Scenario: A DONE session renders a primary badge

- **GIVEN** a Supercharger session whose `Status` is `DONE`
- **WHEN** the Supercharger Stats page is rendered
- **THEN** that session's row displays a badge with the `primary` Kind
- **AND** the badge's text is the translated "Done" label in the active language

#### Scenario: A DONE_CALCULATED session renders a neutral badge

- **GIVEN** a Supercharger session whose `Status` is `DONE_CALCULATED`
- **WHEN** the Supercharger Stats page is rendered
- **THEN** that session's row displays a badge with the `neutral` Kind
- **AND** the badge's text is the translated "Done (calculated)" label in the
  active language
- **AND** the badge is visually distinct from a `DONE` session's badge

#### Scenario: An IN_PROGRESS session renders a ghost badge

- **GIVEN** a Supercharger session whose `Status` is `IN_PROGRESS`
- **WHEN** the Supercharger Stats page is rendered
- **THEN** that session's row displays a badge with the `ghost` Kind
- **AND** the badge's text is the translated "In progress" label in the active
  language

#### Scenario: The badge column adds no additional read

- **GIVEN** any Supercharger Stats page or fragment render
- **WHEN** the session table is built
- **THEN** the gateway performs exactly the one existing
  `charging.SessionReader.ListSessionsByVehicleBetween` read it already performed
  before this change
- **AND** no new `charging` port, method, or database query is called

#### Scenario: The edit row still spans the full table width

- **GIVEN** a signed-in user opens the inline edit form for a Supercharger
  session row
- **WHEN** the edit row is rendered
- **THEN** its single full-width cell spans exactly 8 columns, matching the
  8-column header and static rows
- **AND** an error row rendered for that session likewise spans exactly 8 columns

### Requirement: Supercharger Stats in-progress session guidance

The Supercharger Stats page SHALL render a second bilingual informational alert,
below the existing battery-percentage guidance alert, telling the user that a
session shown as "In progress" is missing its percentages and that editing it to
fill in the end percentage lets the system calculate the start percentage. The
alert SHALL use the existing `ui.Alert` component with `Kind: "info"` — no new UI
kit component SHALL be introduced. The alert's text SHALL resolve through the
gateway i18n catalogue (key `supercharger.status_help`) with non-empty ES and EN
translations.

The alert SHALL render unconditionally, immediately below the existing
`supercharger.battery_pct_help` alert and above the KPI tiles, the chart, and the
sessions table (or their empty-state placeholder) — its presence SHALL NOT depend
on whether any session in the current window is `IN_PROGRESS`, and SHALL NOT
depend on whether the requested date window is valid.

#### Scenario: The in-progress guidance alert renders below the existing alert

- **GIVEN** a signed-in user with a selected vehicle
- **WHEN** `GET /supercharger-stats` is requested
- **THEN** the response contains two info-styled alerts, in this order: the
  existing battery-percentage guidance alert, then the in-progress guidance alert
- **AND** both alerts appear before the KPI tiles in the rendered markup

#### Scenario: The in-progress guidance alert renders even with no in-progress sessions

- **GIVEN** a signed-in user whose selected vehicle's Supercharger sessions in the
  requested window are all `DONE` or `DONE_CALCULATED`
- **WHEN** the Supercharger Stats page or fragment is rendered
- **THEN** the in-progress guidance alert is still rendered

#### Scenario: The in-progress guidance text is bilingual

- **GIVEN** a signed-in user whose resolved language is English
- **WHEN** the Supercharger Stats page is rendered
- **THEN** the alert displays the English guidance text
- **AND** switching the active language to Spanish renders the Spanish guidance
  text instead
