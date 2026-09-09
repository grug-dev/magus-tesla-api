## ADDED Requirements

### Requirement: Dashboard Tire Pressure Subsection

The dashboard's Vehicle Status card SHALL include a "Tire pressure (PSI)" subsection
showing one value per wheel — front-left, front-right, rear-left, rear-right — for the
active vehicle. Each value SHALL be read from the account's latest precomputed
vehicle-status row through the existing analytics read port — no new database read, no
live Tesla Fleet API call. The four wheel values SHALL render as a 2-by-2 grid, not a
single row of four.

Each wheel's tile SHALL show, independently of the other wheels:

- The current pressure reading, or a placeholder — never a fabricated value — when the
  vehicle did not report that wheel's pressure or the underlying row predates this
  capability.
- A trend indicator meaning "increased" when that wheel's day-over-day change is a
  positive number, or "decreased" when it is a negative number. When the change is
  absent (no prior day to compare against, or either day's reading for that wheel is
  itself missing), the tile SHALL show no trend indicator at all — never a fabricated
  "no change" indicator. When the change is present and exactly zero, the tile SHALL
  also show no trend indicator, because neither "increased" nor "decreased" applies —
  this is the same "no indicator" appearance as the absent case, but for a different
  reason.
- A numeric line stating the day-over-day change, whenever that change is present
  (including when it is exactly zero) — never rendered at all when the change is
  absent.

A wheel's trend indicator SHALL be rendered through the same mechanism the "Travel
Progress" subsection uses for its own trend indicators — no second, independent way of
rendering a trend indicator SHALL be introduced by this capability.

#### Scenario: A wheel with a positive change shows an "increased" indicator and its numeric line

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row reports a
  wheel's current pressure and a positive day-over-day change for that wheel
- **WHEN** the dashboard is rendered
- **THEN** that wheel's tile shows the current pressure reading
- **AND** shows an "increased" trend indicator
- **AND** shows a numeric line stating the change, with an explicit positive sign

#### Scenario: A wheel with a negative change shows a "decreased" indicator and its numeric line

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row reports a
  wheel's current pressure and a negative day-over-day change for that wheel
- **WHEN** the dashboard is rendered
- **THEN** that wheel's tile shows the current pressure reading
- **AND** shows a "decreased" trend indicator, visually distinct from the "increased"
  indicator
- **AND** shows a numeric line stating the change, with its negative sign

#### Scenario: A wheel with an exactly-zero change shows no indicator but still shows its numeric line

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row reports a
  wheel's current pressure and a day-over-day change of exactly zero for that wheel
- **WHEN** the dashboard is rendered
- **THEN** that wheel's tile shows the current pressure reading
- **AND** shows no trend indicator — the change is neither an increase nor a decrease
- **AND** still shows a numeric line stating the change is zero, because a known
  zero change is not the same as an unknown one

#### Scenario: A wheel with an absent change shows no indicator and no numeric line

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row reports a
  wheel's current pressure but no day-over-day change for that wheel (no prior day to
  compare against, or either day's reading for that wheel is itself missing)
- **WHEN** the dashboard is rendered
- **THEN** that wheel's tile shows the current pressure reading
- **AND** shows no trend indicator
- **AND** shows no numeric change line at all — never a fabricated "0" line for an
  unknown change

#### Scenario: A wheel with no reported pressure shows the placeholder, independently of its own change

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row does not
  report a given wheel's current pressure (the vehicle did not report TPMS at capture,
  or the row predates this capability)
- **WHEN** the dashboard is rendered
- **THEN** that wheel's tile shows the same placeholder every other absent value on
  this card shows — never a fabricated reading
- **AND** the other three wheels render independently, unaffected by this one wheel's
  missing reading

#### Scenario: The four wheels render as a 2-by-2 grid

- **GIVEN** a signed-in user with an active vehicle and a stored status row
- **WHEN** the dashboard is rendered on a viewport at or above the module's tablet
  breakpoint
- **THEN** the Tire pressure (PSI) subsection shows its four wheel tiles arranged as
  two rows of two, not one row of four

#### Scenario: No stored status row renders the same placeholder as every other tile

- **GIVEN** a signed-in user whose active vehicle has no stored status row yet
- **WHEN** the dashboard is rendered
- **THEN** the Tire pressure (PSI) subsection shows its placeholder for all four
  wheels, identically to how every other tile on the card renders its own placeholder
  in this state
- **AND** no wheel shows a trend indicator or a numeric change line

#### Scenario: No new database read or Tesla call is introduced

- **GIVEN** the Tire pressure (PSI) subsection's four wheel values
- **WHEN** the dashboard is rendered
- **THEN** all four come from the same account-status read the rest of the card
  already performs
- **AND** no additional database query and no Tesla Fleet API call is made to render
  this subsection

#### Scenario: All labels resolve through the translation catalogue

- **GIVEN** the Tire pressure (PSI) subsection's title, description, the four wheel
  labels, and the numeric change line's wording
- **WHEN** they are rendered in either supported language
- **THEN** each resolves through the translation catalogue
- **AND** both `ES` and `EN` are non-empty for every one of them

#### Scenario: The subsection reuses the Travel Progress trend mechanism, not a second one

- **GIVEN** the Tire pressure (PSI) subsection's trend indicators and the Travel
  Progress subsection's own trend indicators, both rendered on the same dashboard
- **WHEN** either subsection's indicator is inspected
- **THEN** both are produced by the same underlying rendering mechanism
- **AND** this capability introduces no second, independent mechanism for showing a
  trend indicator
