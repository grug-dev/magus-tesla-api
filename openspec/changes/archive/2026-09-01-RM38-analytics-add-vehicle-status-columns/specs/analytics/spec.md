## ADDED Requirements

### Requirement: Precomputed Vehicle Status Observations

The analytics capability SHALL persist, on each precomputed daily metrics row, eight
additional raw vehicle-status observations copied verbatim from that day's telemetry
capture: whether the vehicle was locked, whether sentry mode was engaged, the
installed software version, interior and exterior temperature, the current charging
state, the configured charge limit, and the exact instant of capture. These eight
observations SHALL be populated from the day's own capture on EVERY row the
capability persists for that day, regardless of whether that day has a computable
predecessor — unlike the capability's derived per-day figures (distance travelled,
battery percentage used, and the corrected consumed-percentage figure), which remain
absent on a day with no computable predecessor.

#### Scenario: A normal day's row carries all eight status observations
- **GIVEN** a telemetry capture reporting the vehicle locked, sentry mode off, a
  software version, interior and exterior temperature, a charging state, a charge
  limit, and a capture instant
- **WHEN** the capability recalculates that day
- **THEN** the persisted row for that day carries all eight values exactly as
  reported

#### Scenario: A vehicle's first-ever day still carries all eight status observations
- **GIVEN** a telemetry capture that is a vehicle's first-ever capture, with no
  predecessor
- **WHEN** the capability recalculates that day
- **THEN** the persisted row for that day carries all eight status observations from
  that capture
- **AND** that row's derived distance-travelled, battery-percentage-used, and
  corrected consumed-percentage figures remain absent, exactly as they already are
  for a predecessor-less day

#### Scenario: Existing rows are never retroactively populated
- **GIVEN** a precomputed metrics row persisted before the capability began tracking
  these eight observations
- **WHEN** that row is read, without any new capture having triggered a
  recalculation of that same day since
- **THEN** all eight status observations on that row remain absent
- **AND** this absence is indistinguishable, for every observation except sentry
  mode, from "not yet computed" — no fabricated value is ever substituted

### Requirement: Latest Vehicle Status Per Account

The analytics capability SHALL expose, for a given account, the most recently
computed status of every vehicle registered to that account — combining the raw
battery, range and odometer observations the capability already persisted with the
eight status observations above — as the capability's own domain result, never
exposing another module's capture-record type. For an account with multiple
vehicles, the capability SHALL return exactly one result per vehicle, describing that
vehicle's own most-recently-computed day, never an older day for that vehicle and
never a result attributable to a different account's vehicle.

#### Scenario: A single vehicle's latest status is returned
- **GIVEN** an account with one registered vehicle, whose most recently computed day
  carries a full set of status observations
- **WHEN** the capability is asked for that account's latest vehicle status
- **THEN** it returns exactly one result for that vehicle, carrying that day's
  battery, range, odometer and all eight status observations

#### Scenario: Each vehicle's own latest day is returned, not the account's latest day overall
- **GIVEN** an account with two registered vehicles, whose most recently computed
  days fall on two different calendar days
- **WHEN** the capability is asked for that account's latest vehicle status
- **THEN** the result contains one entry per vehicle, each carrying that specific
  vehicle's own most recently computed day — never one vehicle's entry describing
  the other vehicle's more recent day

#### Scenario: A pre-migration latest row reports absent status observations, never fabricated ones
- **GIVEN** a vehicle whose most recently computed day predates the capability's
  status-observation tracking
- **WHEN** the capability is asked for that account's latest vehicle status
- **THEN** the returned result for that vehicle carries its existing battery, range
  and odometer values
- **AND** all eight status observations on that result are absent, not a fabricated
  default such as "unlocked" or "sentry off"

#### Scenario: An account with no computed vehicles yet returns no results, not an error
- **GIVEN** an account with no precomputed metrics rows for any vehicle
- **WHEN** the capability is asked for that account's latest vehicle status
- **THEN** it returns an empty result and no error
