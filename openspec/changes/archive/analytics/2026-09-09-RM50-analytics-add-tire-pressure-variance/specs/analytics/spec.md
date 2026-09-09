## ADDED Requirements

### Requirement: Precomputed Tyre Pressure Day-Over-Day Deltas

The analytics capability SHALL persist, on each precomputed daily metrics row, four
derived tyre-pressure deltas — one per wheel (front-left, front-right, rear-left,
rear-right), in PSI — each computed as that day's raw tyre-pressure observation for that
wheel minus the immediately preceding day's raw observation for the same wheel and the
same vehicle. A delta SHALL be absent whenever either day's own raw observation for that
wheel is absent, or whenever the day itself has no computable predecessor — the same
absence rule the capability's other derived per-day figures (distance travelled, battery
percentage used) already follow. This delta is expected to partially reflect ambient air
temperature change alongside any genuine pressure change; that is an accepted property of
the reading, not a defect, and the capability SHALL NOT apply a threshold or a
target-pressure comparison to suppress it.

#### Scenario: A normal day with a predecessor gets all four wheel deltas
- **GIVEN** two consecutive days' telemetry captures for the same vehicle, both
  reporting all four wheels' tyre pressure
- **WHEN** the capability recalculates the second day
- **THEN** the persisted row for the second day carries, for each wheel, that day's
  pressure minus the first day's pressure for the same wheel

#### Scenario: A predecessor-less day has all four deltas absent
- **GIVEN** a telemetry capture that is a vehicle's first-ever capture, with no
  predecessor
- **WHEN** the capability recalculates that day
- **THEN** the persisted row's four tyre-pressure deltas are absent, exactly as its
  other derived per-day figures already are for a predecessor-less day

#### Scenario: A wheel missing on either day yields an absent delta for that wheel only
- **GIVEN** two consecutive days' telemetry captures for the same vehicle, where one
  wheel's pressure is absent on one of the two days
- **WHEN** the capability recalculates the second day
- **THEN** the persisted delta for that one wheel is absent
- **AND** the other three wheels' deltas, when both days report them, are computed
  normally

#### Scenario: A delta is never fabricated as zero
- **GIVEN** a day whose predecessor day, or whose own capture, lacks a wheel's raw
  pressure reading
- **WHEN** the capability recalculates that day
- **THEN** that wheel's persisted delta is absent
- **AND** it is never replaced with a fabricated `0`, even though `0` is itself a
  possible genuine reading for a different day

#### Scenario: Existing rows gain these deltas only through the one-time backfill
- **GIVEN** a precomputed metrics row persisted before the capability began tracking
  these four deltas
- **WHEN** that row is read, without either a new capture triggering a recalculation
  of that same day, or the platform's one-time historical backfill having run
- **THEN** all four tyre-pressure deltas on that row remain absent
- **AND** this absence is never replaced with a fabricated reading

## MODIFIED Requirements

### Requirement: Latest Vehicle Status Per Account

The analytics capability SHALL expose, for a given account, the most recently computed
status of every vehicle registered to that account — combining the raw battery, range and
odometer observations the capability already persisted with the eight status
observations, the four tyre-pressure observations, the two travel-progress figures
(distance travelled and corrected consumed-percentage), and the four tyre-pressure
day-over-day deltas on that vehicle's most recently computed day — as the capability's
own domain result, never exposing another module's capture-record type. For an account
with multiple vehicles, the capability SHALL return exactly one result per vehicle,
describing that vehicle's own most-recently-computed day, never an older day for that
vehicle and never a result attributable to a different account's vehicle.

#### Scenario: A single vehicle's latest status is returned
- **GIVEN** an account with one registered vehicle, whose most recently computed day
  carries a full set of status, tyre-pressure, travel-progress, and tyre-pressure-delta
  values
- **WHEN** the capability is asked for that account's latest vehicle status
- **THEN** it returns exactly one result for that vehicle, carrying that day's battery,
  range, odometer, all eight status observations, all four tyre-pressure observations,
  both travel-progress figures, and all four tyre-pressure deltas

#### Scenario: Each vehicle's own latest day is returned, not the account's latest day overall
- **GIVEN** an account with two registered vehicles, whose most recently computed
  days fall on two different calendar days
- **WHEN** the capability is asked for that account's latest vehicle status
- **THEN** the result contains one entry per vehicle, each carrying that specific
  vehicle's own most recently computed day — never one vehicle's entry describing the
  other vehicle's more recent day

#### Scenario: A pre-migration latest row reports absent tyre-pressure deltas, never fabricated ones
- **GIVEN** a vehicle whose most recently computed day predates the capability's
  tyre-pressure-delta tracking
- **WHEN** the capability is asked for that account's latest vehicle status
- **THEN** the returned result for that vehicle carries its existing battery, range,
  odometer, status, tyre-pressure, and travel-progress values
- **AND** all four tyre-pressure deltas on that result are absent, not a fabricated
  default such as `0`

#### Scenario: A predecessor-less latest day reports absent tyre-pressure deltas
- **GIVEN** a vehicle whose most recently computed day has no computable predecessor
- **WHEN** the capability is asked for that account's latest vehicle status
- **THEN** the returned result for that vehicle carries its raw battery, range,
  odometer, status, and tyre-pressure observations
- **AND** all four tyre-pressure deltas on that result are absent, exactly as the two
  existing travel-progress figures already are for a predecessor-less day

#### Scenario: An account with no computed vehicles yet returns no results, not an error
- **GIVEN** an account with no precomputed metrics rows for any vehicle
- **WHEN** the capability is asked for that account's latest vehicle status
- **THEN** it returns an empty result and no error
