## ADDED Requirements

### Requirement: Supercharger Stats monthly chart has readable month and kWh axes

The gateway SHALL continue to render the Supercharger kWh-per-month chart through the shared
`HistoryChart` / `HistoryBar` and `historyBarChart` rendering contract. For every calendar-month
bucket in the requested window, the gateway SHALL provide a bar label formatted exactly
`YYYY-MM`, including zero-energy months. It SHALL provide y-axis ticks by reusing the existing
`buildYAxisTicks` behavior with kWh-formatted labels; it SHALL NOT introduce a second tick
algorithm, chart renderer, chart library, client-side chart code, or template arithmetic.

The chart SHALL render its `YYYY-MM` labels vertically so they remain legible across the page's
3-, 6-, and 12-month selector windows. If the tallest bucket is zero, the chart SHALL retain its
bars and vertical-label selection but render no y-axis ticks, following the existing shared
chart behavior.

#### Scenario: Each month has a machine-readable label and kWh scale

- **GIVEN** Supercharger sessions in a March through May window with monthly totals of 10, 0,
  and 30 kWh
- **WHEN** the Supercharger Stats chart is built
- **THEN** its bars have labels `2026-03`, `2026-04`, and `2026-05` in that order
- **AND** the May bar has a 100 percent relative height
- **AND** the chart has the shared five kWh y-axis ticks from the maximum down to zero
- **AND** its labels are vertical

#### Scenario: A non-empty zero-energy chart has no fabricated axis scale

- **GIVEN** a selected-window session set whose non-nil energy values total zero in every month
- **WHEN** the Supercharger Stats chart is built
- **THEN** it renders the monthly bars at zero height with their `YYYY-MM` labels
- **AND** it has no y-axis ticks
- **AND** it does not fabricate a positive kWh maximum

### Requirement: Supercharger Stats session table displays battery percentages

The Supercharger Stats session table SHALL add four columns for the existing `charging.Session`
battery values: start battery percentage, end battery percentage, start battery percentage
estimate, and end battery percentage estimate. The gateway SHALL map each value to a
preformatted view-model string before rendering: a present value is rendered as its integer
percentage followed by `%`, and a nil value is rendered exactly as `"—"`.

The table SHALL obtain these values from the existing `charging.SessionReader` result only; it
SHALL NOT issue an additional read, write, Tesla API call, or database query. Both estimate
fields SHALL continue to render `"—"` while they are NULL; the gateway SHALL NOT calculate or
persist an estimate. The table SHALL retain Date, Site, Energy, and Cost and SHALL NOT restore
Country or Billing Type.

Every new header SHALL resolve through the existing gateway i18n catalogue with non-empty ES and
EN translations.

#### Scenario: Populated session battery values render in the table

- **GIVEN** a Supercharger session whose start, end, start-estimate, and end-estimate values are
  40, 80, 42, and 78
- **WHEN** the Supercharger Stats page is rendered
- **THEN** its row displays `40%`, `80%`, `42%`, and `78%` in the four battery columns
- **AND** the table displays translated headers for all four columns in the active language
- **AND** Date, Site, Energy, and Cost remain displayed

#### Scenario: Missing battery values degrade visibly without an estimator

- **GIVEN** a Supercharger session whose four battery values are NULL
- **WHEN** the Supercharger Stats page is rendered
- **THEN** all four battery cells display `"—"`
- **AND** no cell displays `0%`, an empty string, or a calculated estimate

#### Scenario: Country remains absent

- **GIVEN** any Supercharger Stats page render
- **WHEN** the session table is rendered
- **THEN** it contains no Country header or Country cell
- **AND** the gateway does not add a Country i18n key for this page
