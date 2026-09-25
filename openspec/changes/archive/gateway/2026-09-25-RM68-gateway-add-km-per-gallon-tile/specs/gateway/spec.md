## ADDED Requirements

### Requirement: Vehicle Stats Page Shows A Gasoline Cost-Parity Tile

The Vehicle Stats page (`/vehicle-stats`) SHALL show one additional summary
figure, alongside its existing seven: how many kilometres the selected
period's charging cost would have bought at gasoline prices, expressed as
kilometres per gallon. This figure answers a cost-parity question ("what
would this month have cost in gasoline terms"), never an efficiency question
about the vehicle's motor.

The figure SHALL be computed as the period's total ELIGIBLE distance divided
by the period's total ELIGIBLE gasoline-equivalent gallons — a ratio of sums
across eligible months, never an average of per-month ratios. A calendar
month within the selected period is ELIGIBLE only when BOTH of the following
hold for it:

- A gasoline price resolves for that month (its own stored price, or the
  newest stored price at or before it).
- The month's total charging cost — external AC plus external DC plus
  Supercharger — is greater than zero.

A month that is not ELIGIBLE SHALL contribute neither its distance nor any
gallons to either sum. When no month in the selected period is ELIGIBLE, the
page SHALL render the seven existing tiles unchanged and SHALL show no
gasoline cost-parity tile at all — not a placeholder, not a zero.

The gasoline price itself SHALL NOT be shown anywhere on this page. Only the
resulting kilometres-per-gallon figure is user-visible.

This tile SHALL carry no period-over-period trend indicator and no
comparison to a previous period.

#### Scenario: A period with at least one eligible month shows the tile

- **GIVEN** a signed-in user's selected vehicle and period, where at least
  one calendar month in the period has both a resolvable gasoline price and
  a total charging cost greater than zero
- **WHEN** the Vehicle Stats page or its fragment is rendered
- **THEN** an eighth summary figure is shown alongside the existing seven
- **AND** its value is the period's total distance across eligible months
  divided by the period's total gasoline-equivalent gallons across the same
  eligible months
- **AND** it carries no trend indicator and no period-over-period comparison

#### Scenario: A month with no resolvable gasoline price is excluded

- **GIVEN** a selected period containing a calendar month for which no
  gasoline price has ever been recorded at or before that month
- **WHEN** the Vehicle Stats page is rendered
- **THEN** that month's distance and charging cost are excluded from the
  gasoline cost-parity figure
- **AND** every other one of the page's seven existing figures is unaffected
  by this exclusion

#### Scenario: A month with zero recorded charging cost is excluded even when a price resolves

- **GIVEN** a selected period containing a calendar month whose external AC
  cost, external DC cost, and Supercharger cost all sum to zero, even though
  a gasoline price resolves for that month
- **WHEN** the Vehicle Stats page is rendered
- **THEN** that month's distance is excluded from the gasoline cost-parity
  figure's numerator, not only its cost from the denominator
- **AND** every other one of the page's seven existing figures is unaffected
  by this exclusion

#### Scenario: No eligible month in the period hides the tile entirely

- **GIVEN** a selected period in which every calendar month fails the
  eligibility rule — no resolvable price, zero charging cost, or both
- **WHEN** the Vehicle Stats page is rendered
- **THEN** no gasoline cost-parity tile is shown
- **AND** no placeholder or zero value is shown in its place
- **AND** the page's seven existing figures render exactly as they do today

#### Scenario: The gasoline price is never rendered

- **GIVEN** the gasoline cost-parity figure is shown on the Vehicle Stats page
- **WHEN** the page or its fragment is rendered, in either supported language
- **THEN** the resolved gasoline price used in the calculation does not
  appear anywhere in the rendered markup
- **AND** only the resulting kilometres-per-gallon figure is visible

#### Scenario: The gateway reads the price only through the module's public interface

- **GIVEN** the gasoline cost-parity figure is being computed
- **WHEN** the page's data is assembled
- **THEN** the gateway obtains the resolved price only through the
  `reference` module's read port, bounded to the selected period's own
  `[start, end]` window
- **AND** the gateway performs no direct database access against that
  module's schema

#### Scenario: A failed price read degrades only this tile

- **GIVEN** the read that resolves gasoline prices for the selected period
  fails
- **WHEN** the Vehicle Stats page is rendered
- **THEN** the gasoline cost-parity tile is not shown, the same as when no
  month is eligible
- **AND** the page's seven existing figures still render normally from their
  own, unaffected reads

#### Scenario: The tile's label resolves through the translation catalogue

- **GIVEN** the gasoline cost-parity tile is shown
- **WHEN** it is rendered in either supported language
- **THEN** its label resolves through the translation catalogue
- **AND** both `ES` and `EN` are non-empty for that key
