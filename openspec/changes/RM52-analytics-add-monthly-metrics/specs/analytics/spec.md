## ADDED Requirements

### Requirement: Monthly Effective Pack Capacity Estimation

The analytics capability SHALL compute, for a given calendar month and a given vehicle
(identified by `tesla_id`, pooling every account that has that vehicle registered), an
effective pack-capacity measurement in kWh, using only charge records whose implied capacity
did not itself come from an assumed constant: manual charge entries whose energy was supplied
by a person, and Supercharger sessions whose battery percentages are the current, confirmed
values. Among those records, the capability SHALL further exclude any record whose battery
percentage change is too small to measure capacity reliably, then SHALL require at least a
minimum number of the remaining records before producing a number, taking their median when
that minimum is met. The capability SHALL persist exactly one result row per vehicle per
month, recording both the measurement (absent when the minimum was not met) and how many
records were actually used to reach that conclusion.

#### Scenario: A charge record whose energy came from the assumed constant is excluded
- **GIVEN** a manual charge entry whose energy was itself derived from the platform's
  existing assumed pack-capacity constant, rather than supplied by a person
- **WHEN** the capability estimates that vehicle's effective capacity for the month the
  entry falls in
- **THEN** that entry does not contribute to the estimate, regardless of how large its
  battery-percentage change was

#### Scenario: A Supercharger session with a derived start percentage is excluded
- **GIVEN** a Supercharger session whose current start battery percentage was itself
  computed from the platform's existing assumed pack-capacity constant, rather than being
  the session's confirmed reading
- **WHEN** the capability estimates that vehicle's effective capacity for the month the
  session falls in
- **THEN** that session does not contribute to the estimate

#### Scenario: A record with too small a battery-percentage change is dropped
- **GIVEN** a charge record that would otherwise be eligible, whose battery-percentage
  change over the charge is below the capability's minimum threshold
- **WHEN** the capability estimates that vehicle's effective capacity for the month
- **THEN** that record is excluded from the estimate, and this exclusion does not depend on
  the record's source

#### Scenario: Fewer than the minimum number of usable records yields no measurement, not a guess
- **GIVEN** a vehicle whose eligible, large-enough-delta charge records for a month number
  fewer than the capability's configured minimum
- **WHEN** the capability estimates that vehicle's effective capacity for the month
- **THEN** the persisted result for that vehicle-month has no capacity value
- **AND** the persisted result still records how many usable records were actually found,
  even though that count fell short of the minimum
- **AND** no fabricated or extrapolated number is ever stored in its place

#### Scenario: A month with enough usable records stores their median
- **GIVEN** a vehicle whose eligible, large-enough-delta charge records for a month meet or
  exceed the capability's configured minimum
- **WHEN** the capability estimates that vehicle's effective capacity for the month
- **THEN** the persisted result stores the median of those records' individually implied
  capacities
- **AND** the persisted result records exactly how many records fed that median

#### Scenario: Two accounts registered to the same vehicle have their records pooled
- **GIVEN** the same physical vehicle registered under two different accounts, each with
  its own eligible charge records for the same month
- **WHEN** the capability estimates that vehicle's effective capacity for the month
- **THEN** exactly one result row is produced for that vehicle and month
- **AND** the estimate is computed from every eligible record across both accounts,
  pooled into one set before the minimum-count and median rules are applied
- **AND** the stored result does not depend on which account's records were read first

#### Scenario: A specific vehicle and month can be requested directly
- **GIVEN** a request naming one specific vehicle and one specific calendar month
- **WHEN** the capability processes that request
- **THEN** only that vehicle's result for that month is computed and persisted
- **AND** no other registered vehicle's data is read or written

### Requirement: Effective Pack Capacity Read With Fallback To The Newest Earlier Measurement

The analytics capability SHALL expose a read that returns, for a given vehicle and a given
moment in time, the effective pack-capacity measurement that applies at that moment: the
value from the vehicle's most recently processed calendar month, at or before that moment,
whose result actually carried a measurement — never a month that recorded no measurement, and
never a fabricated or interpolated value. When no such measurement exists for that vehicle at
all, the capability SHALL report that plainly rather than returning an error or a default
number of its own invention.

#### Scenario: The requested month has its own measurement
- **GIVEN** a vehicle with a stored, non-absent capacity measurement for the calendar month
  containing the requested moment
- **WHEN** the capability is asked for that vehicle's effective capacity at that moment
- **THEN** it returns that month's own measurement

#### Scenario: The requested month has no measurement, but an earlier month does
- **GIVEN** a vehicle whose calendar month containing the requested moment has no stored
  measurement (or was never processed at all), but an earlier month does have one
- **WHEN** the capability is asked for that vehicle's effective capacity at that moment
- **THEN** it returns the measurement from the most recent earlier month that has one
- **AND** it never returns a measurement from a later month than the requested moment

#### Scenario: No measurement exists yet for this vehicle
- **GIVEN** a vehicle with no stored effective-capacity measurement at or before the
  requested moment
- **WHEN** the capability is asked for that vehicle's effective capacity at that moment
- **THEN** it reports that no measurement is available
- **AND** it does not return an error and does not fabricate a number
