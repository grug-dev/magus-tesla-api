## ADDED Requirements

### Requirement: Monetary Value Display Formatting

Every monetary value the gateway renders as a display label SHALL be formatted with a comma
(`,`) thousands separator and a period (`.`) decimal separator, with exactly two decimal
places, followed by a space and the currency code — e.g. `58000` COP renders as
`"58,000.00 COP"`. This format SHALL be produced by a single shared function
(`formatMoney(amount float64, currency string) string`,
`internal/gateway/handlers/format.go`) and SHALL NOT vary by the active display language (ES or
EN) — the separator convention is fixed regardless of locale, a deliberate exception to
locale-varying number formats elsewhere in the region.

Every gateway render site that displays a monetary amount alongside a currency code SHALL call
this shared function rather than building its own decimal-formatted string. This applies to,
at minimum: the Supercharger Stats session table's cost column, the Supercharger Stats KPI
tiles' per-currency cost lines, the Charge log entry list's price column, and the Charge log
entry list's cost-per-kWh column.

#### Scenario: Supercharger session cost is comma-grouped

- **GIVEN** a Supercharger session with `TotalCost = 58000` and `Currency = "COP"`
- **WHEN** the Supercharger Stats sessions table renders that session's row
- **THEN** the cost cell reads `"58,000.00 COP"`

#### Scenario: Charge log price is comma-grouped

- **GIVEN** a manual charge entry with `Price = 12500` and `Currency = "COP"`
- **WHEN** the Charge log entry list renders that entry's row
- **THEN** the price label reads `"12,500.00 COP"`

#### Scenario: Charge log cost-per-kWh is comma-grouped and keeps its unit suffix

- **GIVEN** a manual charge entry whose computed cost per kWh is `1200` in currency `COP`
- **WHEN** the Charge log entry list renders that entry's cost-per-kWh label
- **THEN** the label reads `"1,200.00 COP/kWh"` — the comma-grouped, two-decimal money format
  with the `/kWh` unit suffix appended after it

#### Scenario: Zero, negative, and sub-thousand amounts format correctly

- **GIVEN** monetary amounts `0`, `-1500.5`, `999`, and `1000`, all in currency `COP`
- **WHEN** each is formatted by the shared money formatter
- **THEN** they render respectively as `"0.00 COP"`, `"-1,500.50 COP"`, `"999.00 COP"`, and
  `"1,000.00 COP"`

#### Scenario: The formatted currency label does not vary by active display language

- **GIVEN** the same monetary amount and currency rendered once with the active display
  language set to Spanish (ES) and once set to English (EN)
- **WHEN** the monetary label is rendered in each case
- **THEN** both renders produce the identical `1,234.56 CUR`-shaped string — the comma/period
  separator convention does not change between ES and EN

#### Scenario: Machine-parseable raw form values are NOT comma-grouped

- **GIVEN** the inline charge-entry edit form's `energy_added_kwh` and `price` number inputs
- **WHEN** their `value` attributes are populated from a stored entry's `EnergyAddedKWh` and
  `Price` fields
- **THEN** those values are formatted as a plain, two-decimal machine-parseable decimal string
  with NO thousands separator (e.g. `"1200.50"`, never `"1,200.50"`)
- **AND** the shared comma-grouped money formatter (`formatMoney`) is NEVER used to build these
  two values
- **AND** submitting the edit form with these unmodified values round-trips successfully (the
  browser's native number input parses the value without error)
