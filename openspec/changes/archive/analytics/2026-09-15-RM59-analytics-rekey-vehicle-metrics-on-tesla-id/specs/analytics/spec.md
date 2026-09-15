## MODIFIED Requirements

### Requirement: Multi-Tenant Scoping On Every Underlying Read

The analytics capability SHALL scope every underlying read it performs when deriving
per-day consumption (telemetry snapshot history, Supercharger sessions, and
manually-logged charge entries) to the given vehicle identifier alone. The capability
SHALL NOT take or require an account identifier for this derivation — a vehicle
belongs to exactly one account at a time, so scoping by vehicle identity already
gives the same isolation an account identifier would have added. Proving that a
specific account may see a specific vehicle's derived consumption SHALL happen before
this capability is called, not inside it.

#### Scenario: Every underlying read for per-day consumption is scoped to the given vehicle
- **GIVEN** a request to derive per-day consumption for a specific vehicle
- **WHEN** the capability performs its underlying reads
- **THEN** every one of those reads is scoped to that vehicle's identifier

### Requirement: Latest Vehicle Status Per Account

The analytics capability SHALL expose, for a given set of vehicle identifiers, the
most recently computed status of each vehicle in that set — combining the raw
battery, range and odometer observations the capability already persisted with the
eight status observations, the four tyre-pressure observations, the two
travel-progress figures (distance travelled and corrected consumed-percentage), and
the four tyre-pressure day-over-day deltas on that vehicle's most recently computed
day — as the capability's own domain result, never exposing another module's
capture-record type. For a given set of vehicles, the capability SHALL return exactly
one result per vehicle in the set, describing that vehicle's own most-recently-
computed day, never an older day for that vehicle and never a result for a vehicle
outside the given set. The capability SHALL NOT take or require an account
identifier for this read — proving that every identifier in the given set belongs to
the requesting account SHALL happen before this capability is called, not inside it.

#### Scenario: A single vehicle's latest status is returned
- **GIVEN** a set containing one vehicle identifier, whose most recently computed day
  carries a full set of status, tyre-pressure, travel-progress, and tyre-pressure-delta
  values
- **WHEN** the capability is asked for the latest status of that vehicle set
- **THEN** it returns exactly one result for that vehicle, carrying that day's battery,
  range, odometer, all eight status observations, all four tyre-pressure observations,
  both travel-progress figures, and all four tyre-pressure deltas

#### Scenario: Each vehicle's own latest day is returned, not another vehicle's latest day
- **GIVEN** a set containing two vehicle identifiers, whose most recently computed
  days fall on two different calendar days
- **WHEN** the capability is asked for the latest status of that vehicle set
- **THEN** the result contains one entry per vehicle in the set, each carrying that
  specific vehicle's own most recently computed day — never one vehicle's entry
  describing the other vehicle's more recent day

#### Scenario: A pre-migration latest row reports absent tyre-pressure deltas, never fabricated ones
- **GIVEN** a vehicle whose most recently computed day predates the capability's
  tyre-pressure-delta tracking
- **WHEN** the capability is asked for the latest status of a set containing that
  vehicle
- **THEN** the returned result for that vehicle carries its existing battery, range,
  odometer, status, tyre-pressure, and travel-progress values
- **AND** all four tyre-pressure deltas on that result are absent, not a fabricated
  default such as `0`

#### Scenario: A predecessor-less latest day reports absent tyre-pressure deltas
- **GIVEN** a vehicle whose most recently computed day has no computable predecessor
- **WHEN** the capability is asked for the latest status of a set containing that
  vehicle
- **THEN** the returned result for that vehicle carries its raw battery, range,
  odometer, status, and tyre-pressure observations
- **AND** all four tyre-pressure deltas on that result are absent, exactly as the two
  existing travel-progress figures already are for a predecessor-less day

#### Scenario: An empty vehicle set returns no results, not an error
- **GIVEN** an empty set of vehicle identifiers, or a set of vehicle identifiers with
  no precomputed metrics rows
- **WHEN** the capability is asked for the latest status of that vehicle set
- **THEN** it returns an empty result and no error

#### Scenario: A result never includes a vehicle outside the given set
- **GIVEN** precomputed metrics rows exist for a vehicle that is NOT included in the
  requested set
- **WHEN** the capability is asked for the latest status of the requested set
- **THEN** the result contains no entry for the vehicle outside the set

### Requirement: Per-Day Battery Level and Range Read

The analytics capability SHALL expose, for a given vehicle and closed calendar-day
date range, the vehicle's precomputed daily battery-level percentage and estimated
battery range for every day in that range for which a precomputed observation
exists. Unlike the capability's derived per-day figures (distance travelled, battery
percentage used, and the corrected consumed-percentage figure), a day's battery-level
and range observations SHALL be reported regardless of whether that day has a
computable predecessor, because they are raw per-day observations, not a delta
against a prior day. The capability SHALL NOT take or require an account identifier
for this read — it is scoped by vehicle identity alone.

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
