# vehicle-monthly-metrics Specification

## ADDED Requirements

### Requirement: A Month's Summary Carries That Month's External Charging Totals, Split By Type

The analytics capability SHALL compute, for a given vehicle and a given calendar month,
the total energy added, the total cost, and the count of external charging records for
that month, split into two groups by charging type: alternating current and direct
current.

The capability SHALL count and total an external charging record using its own charging
type, regardless of that record's lifecycle status.

The capability SHALL exclude an external charging record with no recorded charging type
from both groups entirely — it SHALL contribute to neither group's energy, cost, nor
count.

The capability SHALL treat an external charging record with no recorded energy added, or
no recorded cost, as contributing zero to that figure's total, while still contributing
to its group's count.

The capability SHALL record, for each of the two groups, a distribution of how many
records ended at each of five ending-battery-percentage ranges: 0 up to but excluding 20,
20 up to but excluding 40, 40 up to but excluding 60, 60 up to but excluding 80, and 80
up to and including 100. A record with no recorded ending battery percentage SHALL
contribute to its group's count but to none of its group's ranges.

#### Scenario: External charges split into their two groups by charging type

- **GIVEN** a vehicle's calendar month with external charging records of both charging types
- **WHEN** the capability summarises that vehicle's month
- **THEN** each group's total energy, total cost, and count reflect only the records of that group's own charging type

#### Scenario: A record with no recorded charging type is excluded from both groups

- **GIVEN** a vehicle's external charging record within the month that has no recorded charging type
- **WHEN** the capability summarises that vehicle's month
- **THEN** that record contributes to neither group's energy, cost, count, nor ending-battery distribution

#### Scenario: Every record counts regardless of its lifecycle status

- **GIVEN** a vehicle's external charging record within the month whose lifecycle status is not yet complete
- **WHEN** the capability summarises that vehicle's month
- **THEN** that record's group counts it exactly as it would a complete record

#### Scenario: A record missing energy added or cost contributes zero, and still counts

- **GIVEN** a vehicle's external charging record within the month that has a recorded charging type but no recorded energy added
- **WHEN** the capability summarises that vehicle's month
- **THEN** that record's group total energy is unaffected by this record
- **AND** that record's group count still includes this record

#### Scenario: A record with no recorded ending battery percentage counts but enters no range

- **GIVEN** a vehicle's external charging record within the month that has a recorded charging type but no recorded ending battery percentage
- **WHEN** the capability summarises that vehicle's month
- **THEN** that record's group count still includes this record
- **AND** none of that group's five ending-battery-percentage ranges includes this record

#### Scenario: An ending battery percentage of exactly 100 has a range

- **GIVEN** a vehicle's external charging record within the month whose ending battery percentage is exactly 100
- **WHEN** the capability summarises that vehicle's month
- **THEN** that record's group's 80-to-100 range includes this record

### Requirement: A Month's Summary Carries That Month's Supercharger Totals

The analytics capability SHALL compute, for a given vehicle and a given calendar month,
the total energy added, the total cost, and the count of Supercharger sessions that
ended within that month, plus a five-range ending-battery-percentage distribution over
those sessions, on the same terms as the external-charging groups above.

The capability SHALL decide which calendar month a Supercharger session ended in using
the platform's own default time zone, not any other zone, applied to the session's
ending time.

The capability SHALL treat a Supercharger session with no recorded energy added, or no
recorded cost, as contributing zero to that figure's total, while still contributing to
the session count. A session with no recorded ending battery percentage SHALL contribute
to the session count but to none of the five ranges.

#### Scenario: A session's month is decided by the platform's time zone, not by another zone

- **GIVEN** a vehicle's Supercharger session whose ending time falls on one calendar day in one time zone and on a different calendar day, in a different month, in the platform's own default time zone
- **WHEN** the capability summarises the vehicle's month using the platform's own default time zone
- **THEN** the session is counted in the month its ending time falls in under the platform's own default time zone

#### Scenario: A session ending late in the platform's time zone still counts in that month

- **GIVEN** a vehicle's Supercharger session whose ending time is near the boundary between two calendar months, such that a different time zone would place it outside the month the platform's own default time zone places it in
- **WHEN** the capability summarises the vehicle's month using the platform's own default time zone
- **THEN** the session is included in that month's total energy, total cost, count, and ending-battery distribution

#### Scenario: A session outside the month, however it is found, is excluded from that month's totals

- **GIVEN** a vehicle's Supercharger session whose ending time, in the platform's own default time zone, falls outside a given calendar month
- **WHEN** the capability summarises that vehicle's month
- **THEN** that session contributes nothing to that month's total energy, total cost, count, or ending-battery distribution

#### Scenario: A session missing energy added or cost contributes zero, and still counts

- **GIVEN** a vehicle's Supercharger session that ended within the month but has no recorded energy added
- **WHEN** the capability summarises that vehicle's month
- **THEN** the month's total energy is unaffected by this session
- **AND** the month's session count still includes this session

### Requirement: A Month's Summary States The Currency Its Charging Totals Are Expressed In

The analytics capability SHALL record, alongside a month's charging totals, the currency
those totals are expressed in. The capability SHALL total every charging record's cost
into its group's figure regardless of that individual record's own recorded currency.

#### Scenario: Costs are totaled regardless of the individual record's own currency

- **GIVEN** a vehicle's calendar month whose charging records do not all share the same recorded currency
- **WHEN** the capability summarises that vehicle's month
- **THEN** every record's cost is included in its group's total cost
- **AND** the summary records one currency for the month's totals
