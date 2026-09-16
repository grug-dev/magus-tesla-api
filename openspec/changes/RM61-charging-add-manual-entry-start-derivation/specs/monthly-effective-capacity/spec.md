## MODIFIED Requirements

### Requirement: A Vehicle's Effective Pack Capacity Is Measured Once Per Month

The charging capability SHALL compute, for each calendar month and each vehicle, a measured pack
capacity in kilowatt-hours, from that vehicle's own valid charge records for that month, and SHALL
store the result together with how many valid records supported it.

A charge record SHALL count as valid evidence only when its own energy or battery-percentage
figures were not themselves derived from an assumed capacity. A user-logged charge record SHALL
count only when its energy was supplied by the person, not estimated, **and its starting battery
percentage was supplied by the person, not derived** — a record can fail either condition on its
own, since the two provenances are recorded and computed independently. A Supercharger session
record SHALL count only when it is complete and its battery percentages were both supplied
directly, not calculated from an assumed capacity.

**This requirement is MODIFIED from its prior revision only in the manual-entry validity rule.**
A manual charge entry's starting battery percentage can now itself be derived from an assumed
capacity, the same way a Supercharger session's starting percentage already could be. The
manual-entry rule now names both of a record's independently-computed provenances, matching the
Supercharger-session rule's own shape, instead of naming only the energy provenance.

The capability SHALL discard, from that month's evidence, any valid record whose battery
percentage change is too small to measure capacity reliably. The capability SHALL require a
minimum number of remaining records before it reports a measured capacity for a vehicle and month;
below that minimum, it SHALL still record how many records it found, but SHALL leave the measured
capacity absent, never a guessed number.

When enough records remain, the capability SHALL derive the measured capacity as their median,
computed after discarding unreliable records, so a single unusual record cannot dominate the
result.

The capability SHALL identify a vehicle by its own stable vehicle identifier, not by the account
that logged a given record — so records logged under different accounts for the same vehicle SHALL
be pooled into the same monthly measurement. A Supercharger session record that cannot be
attributed to any currently registered vehicle SHALL be excluded from every month's evidence.

The capability SHALL NOT recompute or alter any existing charge record's own stored energy or
capacity figures. A monthly measurement is a new, separate fact; it never rewrites history.

#### Scenario: A month with enough reliable evidence gets a measured capacity

- **GIVEN** a vehicle with at least the required minimum number of valid, reliable charge records
  in a calendar month
- **WHEN** the capability computes that month's effective capacity for the vehicle
- **THEN** a measured capacity is recorded for that vehicle and month
- **AND** the recorded sample count matches the number of reliable records that contributed

#### Scenario: A month with too little evidence records no capacity, visibly

- **GIVEN** a vehicle with fewer than the required minimum number of reliable charge records in a
  calendar month
- **WHEN** the capability computes that month's effective capacity for the vehicle
- **THEN** no measured capacity is recorded for that vehicle and month
- **AND** the recorded sample count still reflects how many reliable records were found

#### Scenario: A record whose energy was estimated from an assumed capacity is not evidence

- **GIVEN** a user-logged charge record whose energy was estimated rather than supplied by the
  person
- **WHEN** the capability computes that month's effective capacity
- **THEN** that record does not contribute to the measured capacity or the sample count

#### Scenario: A record whose starting percentage was derived from an assumed capacity is not
  evidence

- **GIVEN** a user-logged charge record whose energy was supplied by the person, but whose
  starting battery percentage was derived from an assumed capacity rather than supplied by the
  person
- **WHEN** the capability computes that month's effective capacity
- **THEN** that record does not contribute to the measured capacity or the sample count, even
  though its energy alone would otherwise qualify it

#### Scenario: A Supercharger session with a calculated starting percentage is not evidence

- **GIVEN** a Supercharger session record whose starting battery percentage was calculated from an
  assumed capacity rather than supplied directly
- **WHEN** the capability computes that month's effective capacity
- **THEN** that record does not contribute to the measured capacity or the sample count

#### Scenario: A small battery-percentage change is not evidence

- **GIVEN** a valid charge record whose battery-percentage change over the session is smaller than
  the capability's reliability threshold
- **WHEN** the capability computes that month's effective capacity
- **THEN** that record does not contribute to the measured capacity or the sample count

#### Scenario: Records for the same vehicle under different accounts are pooled

- **GIVEN** two valid, reliable charge records for the same vehicle, logged under two different
  accounts
- **WHEN** the capability computes that month's effective capacity for the vehicle
- **THEN** both records contribute to the same measured capacity and the same sample count

#### Scenario: A session not attributable to any registered vehicle is excluded

- **GIVEN** a Supercharger session record that is not attributable to any currently registered
  vehicle
- **WHEN** the capability computes that month's effective capacity
- **THEN** that record does not contribute to any vehicle's measured capacity or sample count

#### Scenario: Computing a month never changes any existing charge record

- **GIVEN** any set of existing charge records, valid or not
- **WHEN** the capability computes a month's effective capacity from them
- **THEN** none of those records' own stored energy or capacity figures change
