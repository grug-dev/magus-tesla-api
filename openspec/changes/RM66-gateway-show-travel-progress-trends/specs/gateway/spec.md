## MODIFIED Requirements

### Requirement: Dashboard Travel Progress Subsection

The dashboard's Vehicle Status card SHALL include a "Travel Progress" subsection
showing three values for the active vehicle's latest computed day: the distance
travelled, the battery percentage used, and the driving efficiency (kilometres per
battery percent). Each value SHALL be read from the account's latest precomputed
vehicle-status row through the existing analytics read port — no new database read,
no live Tesla Fleet API call.

Each of the three values SHALL show, independently of the other two:

- The current figure, or a placeholder — never a fabricated zero — when the
  underlying value is absent.
- A trend indicator meaning "increased" when that figure's own day-over-day change
  is a positive number, or "decreased" when it is a negative number, compared
  against the previous day's own value of the same figure. When the change is
  absent (no prior day to compare against, or either day's own underlying figure
  is itself missing), the tile SHALL show no trend indicator at all — never a
  fabricated "no change" indicator. When the change is present and exactly zero,
  the tile SHALL also show no trend indicator, because neither "increased" nor
  "decreased" applies — this is the same "no indicator" appearance as the absent
  case, but for a different reason.
- A numeric line stating the day-over-day change, whenever that change is present
  (including when it is exactly zero) — never rendered at all when the change is
  absent.

Only the driving-efficiency value's trend indicator SHALL be rendered in a good/bad
semantic colour: an "increasing" colour for a positive change, a "decreasing"
colour for a negative one — a higher kilometres-per-percent figure is
unambiguously a better outcome. The distance-travelled and battery-used trend
indicators SHALL be rendered in one shared neutral colour regardless of their own
direction — travelling more or using more battery on a given day is neither a
good nor a bad outcome by itself, so neither earns a good/bad colour, even though
both still show their real direction. Every colour used SHALL be a semantic theme
token, never a hardcoded colour value.

A trend indicator on this subsection SHALL be rendered through the same mechanism
the "Tire pressure (PSI)" subsection's wheel tiles use for their own trend
indicators — no second, independent way of rendering a trend indicator SHALL be
introduced by this capability.

#### Scenario: A neutral tile with a positive change shows an "increased" indicator in the neutral colour and its numeric line

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row reports
  a positive day-over-day change for the distance-travelled figure (or,
  equivalently, the battery-used figure)
- **WHEN** the dashboard is rendered
- **THEN** that tile shows the current figure
- **AND** shows an "increased" trend indicator in the shared neutral colour, not a
  good/bad colour
- **AND** shows a numeric line stating the change, with an explicit positive sign

#### Scenario: A neutral tile with a negative change shows a "decreased" indicator in the neutral colour and its numeric line

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row reports
  a negative day-over-day change for the distance-travelled figure (or,
  equivalently, the battery-used figure)
- **WHEN** the dashboard is rendered
- **THEN** that tile shows the current figure
- **AND** shows a "decreased" trend indicator in the shared neutral colour, visually
  distinct from the "increased" indicator but not by colour
- **AND** shows a numeric line stating the change, with its negative sign

#### Scenario: The efficiency tile's positive change shows an "increased" indicator in the good colour

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row reports
  a positive day-over-day change for the driving-efficiency figure
- **WHEN** the dashboard is rendered
- **THEN** the efficiency tile shows the current figure
- **AND** shows an "increased" trend indicator in its own good/bad "increasing"
  semantic colour
- **AND** shows a numeric line stating the change, with an explicit positive sign

#### Scenario: The efficiency tile's negative change shows a "decreased" indicator in the bad colour

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row reports
  a negative day-over-day change for the driving-efficiency figure
- **WHEN** the dashboard is rendered
- **THEN** the efficiency tile shows the current figure
- **AND** shows a "decreased" trend indicator in its own "decreasing" semantic
  colour, visually distinct from the "increasing" one
- **AND** shows a numeric line stating the change, with its negative sign

#### Scenario: An exactly-zero change shows no indicator but still shows its numeric line, on any of the three tiles

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row reports
  a day-over-day change of exactly zero for one of the three figures
- **WHEN** the dashboard is rendered
- **THEN** that tile shows the current figure
- **AND** shows no trend indicator — neither the shared neutral colour nor a
  good/bad colour applies to a change that is neither an increase nor a decrease
- **AND** still shows a numeric line stating the change is zero, because a known
  zero change is not the same as an unknown one

#### Scenario: An absent change shows no indicator and no numeric line, on any of the three tiles

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row reports
  a figure's current value but no day-over-day change for it (no prior day to
  compare against, or either day's own underlying figure is itself missing)
- **WHEN** the dashboard is rendered
- **THEN** that tile shows the current figure
- **AND** shows no trend indicator
- **AND** shows no numeric change line at all — never a fabricated "0" line for an
  unknown change

#### Scenario: A figure with no computed value shows the placeholder, independently of its own change

- **GIVEN** a signed-in user whose active vehicle's latest precomputed row does not
  report a given figure's current value (the latest computed day has no prior day
  to derive it against, or there is no stored status row at all)
- **WHEN** the dashboard is rendered
- **THEN** that tile shows the same placeholder every other absent value on this
  card shows — never a fabricated value
- **AND** the other two tiles render independently, unaffected by this one tile's
  missing value

#### Scenario: No stored status row renders the same placeholder as every other tile

- **GIVEN** a signed-in user whose active vehicle has no stored status row yet
- **WHEN** the dashboard is rendered
- **THEN** the Travel Progress subsection shows its placeholder for all three
  values, identically to how every other tile on the card renders its own
  placeholder in this state
- **AND** none of the three tiles shows a trend indicator or a numeric change line

#### Scenario: No new database read or Tesla call is introduced

- **GIVEN** the Travel Progress subsection's three values
- **WHEN** the dashboard is rendered
- **THEN** all three values come from the same account-status read the rest of the
  card already performs
- **AND** no additional database query and no Tesla Fleet API call is made to
  render this subsection

#### Scenario: Every label resolves through the translation catalogue

- **GIVEN** the Travel Progress subsection's title, description, and the three
  value labels
- **WHEN** they are rendered in either supported language
- **THEN** each resolves through the translation catalogue
- **AND** both `ES` and `EN` are non-empty for every one of them
