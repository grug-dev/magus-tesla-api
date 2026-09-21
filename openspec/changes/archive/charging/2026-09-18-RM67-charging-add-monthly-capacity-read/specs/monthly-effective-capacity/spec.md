## ADDED Requirements

### Requirement: A Vehicle's Monthly Measurement Is Retrievable For An Exact Month

The charging capability SHALL provide a way to retrieve the measured pack capacity for a
specific vehicle and a specific calendar month, distinct from the retrieval that returns
the vehicle's most recently measured capacity across every month.

The retrieval SHALL report three distinct outcomes: no measurement was ever recorded for
that vehicle and month; a measurement was recorded but no capacity was reliably measured
that month; or a measurement was recorded with a reliably measured capacity. The first two
outcomes SHALL be distinguishable from each other, not merged into one absence.

The retrieval SHALL accept any date that falls within the target calendar month, not only
its first day, and SHALL return the same result for every date within that month.

The retrieval SHALL NOT return a measurement belonging to a different vehicle, and SHALL
NOT return a measurement belonging to a different calendar month for the same vehicle,
even when that other month has its own recorded measurement.

The retrieval SHALL NOT alter any existing measurement, and SHALL NOT trigger a new
measurement to be computed.

#### Scenario: No measurement exists yet for the vehicle and month

- **GIVEN** a vehicle with no monthly capacity measurement recorded for a given calendar
  month
- **WHEN** that vehicle's measurement is retrieved for that month
- **THEN** the retrieval reports that no measurement exists
- **AND** no capacity value is returned

#### Scenario: A measurement exists but the month had too little evidence

- **GIVEN** a vehicle with a monthly capacity measurement recorded for a given calendar
  month, where that month's evidence was too thin to measure a capacity
- **WHEN** that vehicle's measurement is retrieved for that month
- **THEN** the retrieval reports that a measurement exists
- **AND** no capacity value is returned
- **AND** this outcome is distinguishable from the case where no measurement exists at all

#### Scenario: A measurement exists with a reliably measured capacity

- **GIVEN** a vehicle with a monthly capacity measurement recorded for a given calendar
  month, with a reliably measured capacity
- **WHEN** that vehicle's measurement is retrieved for that month
- **THEN** the retrieval reports that a measurement exists
- **AND** the measured capacity value is returned

#### Scenario: Any date within the target month retrieves the same measurement

- **GIVEN** a vehicle with a monthly capacity measurement recorded for a given calendar
  month
- **WHEN** that vehicle's measurement is retrieved once using the first day of that month,
  and again using a later day within the same month
- **THEN** both retrievals return the identical outcome and, where present, the identical
  capacity value

#### Scenario: A different vehicle's measurement for the same month does not leak

- **GIVEN** two vehicles, each with a monthly capacity measurement recorded for the same
  calendar month
- **WHEN** one vehicle's measurement is retrieved for that month
- **THEN** only that vehicle's own measurement is returned
- **AND** the other vehicle's measurement is not returned

#### Scenario: A different month's measurement for the same vehicle does not leak

- **GIVEN** a vehicle with monthly capacity measurements recorded for two different
  calendar months
- **WHEN** that vehicle's measurement is retrieved for one of those months
- **THEN** only that month's own measurement is returned
- **AND** the other month's measurement is not returned, even though it belongs to the
  same vehicle

#### Scenario: A neighboring month with no measurement is reported as not found

- **GIVEN** a vehicle with a monthly capacity measurement recorded for one calendar month,
  and no measurement recorded for the immediately adjacent month
- **WHEN** that vehicle's measurement is retrieved for the adjacent month
- **THEN** the retrieval reports that no measurement exists for that month
