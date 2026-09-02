## ADDED Requirements

### Requirement: Per-Day Battery Level and Range Read

The analytics capability SHALL expose, for a given vehicle and closed calendar-day
date range, the vehicle's precomputed daily battery-level percentage and estimated
battery range for every day in that range for which a precomputed observation
exists. Unlike the capability's derived per-day figures (distance travelled, battery
percentage used, and the corrected consumed-percentage figure), a day's battery-level
and range observations SHALL be reported regardless of whether that day has a
computable predecessor, because they are raw per-day observations, not a delta
against a prior day.

#### Scenario: A day's battery level and range are returned exactly as observed

- **GIVEN** a precomputed daily observation for a vehicle reporting a battery level
  percentage and an estimated range for a given calendar day
- **WHEN** the capability is asked for that vehicle's battery level over a range
  containing that day
- **THEN** the result includes exactly one entry for that day, carrying the battery
  level percentage and range exactly as observed

#### Scenario: A day with no computable predecessor is still reported

- **GIVEN** a vehicle's first-ever tracked day, or any day immediately following a
  gap in tracking, for which the capability's derived distance-travelled and
  battery-percentage-used figures are absent because no predecessor day exists
- **WHEN** the capability is asked for that vehicle's battery level over a range
  containing that day
- **THEN** the result includes an entry for that day, carrying its battery level
  percentage and range
- **AND** this is true even though the same day is excluded from the capability's
  derived per-day consumption and distance results

#### Scenario: A day with no precomputed observation at all is absent, not zero

- **GIVEN** a calendar day within the requested range for which the capability has
  not yet computed any daily observation for the vehicle
- **WHEN** the capability is asked for that vehicle's battery level over a range
  containing that day
- **THEN** the result contains no entry for that day
- **AND** no fabricated or zero-valued entry is substituted for the missing day

#### Scenario: An empty range or a vehicle with no observations yields an empty result, not an error

- **GIVEN** a date range for which the vehicle has no precomputed observations at all
- **WHEN** the capability is asked for that vehicle's battery level over that range
- **THEN** it returns an empty result and no error

#### Scenario: Results are scoped to the requesting account's own vehicle

- **GIVEN** two different accounts, each with a vehicle sharing the same vehicle
  identifier, each with its own precomputed observation on the same calendar day
- **WHEN** one account's battery level is requested for that day
- **THEN** the result reflects only that account's own vehicle's observation, never
  the other account's
