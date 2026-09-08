## ADDED Requirements

### Requirement: Precomputed Tyre Pressure Observations

The analytics capability SHALL persist, on each precomputed daily metrics row, four
additional raw tyre-pressure observations copied verbatim from that day's telemetry
capture — one value per wheel (front-left, front-right, rear-left, rear-right), in
PSI. These four observations SHALL be populated from the day's own capture on EVERY
row the capability persists for that day, regardless of whether that day has a
computable predecessor — the same rule the capability's other raw status observations
already follow, and the opposite rule to the capability's derived per-day figures
(distance travelled, battery percentage used, the corrected consumed-percentage
figure), which remain absent on a day with no computable predecessor.

#### Scenario: A normal day's row carries all four tyre-pressure observations
- **GIVEN** a telemetry capture reporting a pressure reading for all four wheels
- **WHEN** the capability recalculates that day
- **THEN** the persisted row for that day carries all four values exactly as
  reported, in PSI, with no conversion applied

#### Scenario: A vehicle's first-ever day still carries all four tyre-pressure observations
- **GIVEN** a telemetry capture that is a vehicle's first-ever capture, with no
  predecessor
- **WHEN** the capability recalculates that day
- **THEN** the persisted row for that day carries all four tyre-pressure observations
  from that capture
- **AND** that row's derived distance-travelled, battery-percentage-used, and
  corrected consumed-percentage figures remain absent, exactly as they already are
  for a predecessor-less day

#### Scenario: A wheel with no reported pressure is absent, not a fabricated reading
- **GIVEN** a telemetry capture whose reading for one wheel is absent (no sensor, or
  no report from the vehicle for that wheel at capture time)
- **WHEN** the capability recalculates that day
- **THEN** the persisted row's observation for that wheel is absent
- **AND** the other three wheels' observations, if reported, are persisted exactly as
  reported

#### Scenario: Existing rows gain these observations only through the one-time backfill
- **GIVEN** a precomputed metrics row persisted before the capability began tracking
  these four observations
- **WHEN** that row is read, without either a new capture triggering a
  recalculation of that same day, or the platform's one-time historical backfill
  having run
- **THEN** all four tyre-pressure observations on that row remain absent
- **AND** this absence is never replaced with a fabricated reading

## MODIFIED Requirements

### Requirement: Latest Vehicle Status Per Account

The analytics capability SHALL expose, for a given account, the most recently
computed status of every vehicle registered to that account — combining the raw
battery, range and odometer observations the capability already persisted with the
eight status observations, the four tyre-pressure observations, and the two derived
travel-progress figures (distance travelled and corrected consumed-percentage on that
vehicle's most recently computed day) — as the capability's own domain result, never
exposing another module's capture-record type. For an account with multiple
vehicles, the capability SHALL return exactly one result per vehicle, describing that
vehicle's own most-recently-computed day, never an older day for that vehicle and
never a result attributable to a different account's vehicle.

#### Scenario: A single vehicle's latest status is returned
- **GIVEN** an account with one registered vehicle, whose most recently computed day
  carries a full set of status and tyre-pressure observations plus travel-progress
  figures
- **WHEN** the capability is asked for that account's latest vehicle status
- **THEN** it returns exactly one result for that vehicle, carrying that day's
  battery, range, odometer, all eight status observations, all four tyre-pressure
  observations, and both travel-progress figures

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
- **AND** all eight status observations and all four tyre-pressure observations on
  that result are absent, not a fabricated default such as "unlocked" or "sentry off"
  or a fabricated pressure reading

#### Scenario: A predecessor-less latest day reports absent travel-progress figures
- **GIVEN** a vehicle whose most recently computed day has no computable predecessor
- **WHEN** the capability is asked for that account's latest vehicle status
- **THEN** the returned result for that vehicle carries its raw battery, range,
  odometer, status, and tyre-pressure observations
- **AND** both travel-progress figures (distance travelled, corrected
  consumed-percentage) on that result are absent, exactly as they already are for a
  predecessor-less day on the capability's other per-day reads

#### Scenario: An account with no computed vehicles yet returns no results, not an error
- **GIVEN** an account with no precomputed metrics rows for any vehicle
- **WHEN** the capability is asked for that account's latest vehicle status
- **THEN** it returns an empty result and no error
