# vehicle-monthly-metrics Specification

## Purpose

Summarise a vehicle's daily analytics into one row per calendar month, so a future
reader never has to sum a month of daily rows on the spot.

`analytics` already computes one row per vehicle per day. Nothing summarises a
month. This capability adds that summary: for one vehicle and one calendar month,
it reports the total distance driven, the total battery used, and the resulting
efficiency — each split into every day, weekdays only, and weekends only — plus
the pack capacity measured for that vehicle and month.

It also reports that month's charging: the energy, cost and record count of the
vehicle's external charges, split by charging type, and of its Supercharger
sessions. Each of the three carries a distribution of how many records ended at
each of five ending-battery ranges.

A day with no computable predecessor contributes to none of these figures, the
same way it already contributes to none of the platform's existing daily
figures. A month with no computable day at all still gets a row, so a caller can
always tell "we have not measured a month" (no row) apart from "we measured this
month and it was empty" (a row whose counts are zero) once a row exists at all —
this capability's own row always exists once asked for, so the second case is
the only one a reader of this capability's data will ever see.

Recomputing a month is a full replacement, never an addition: asking again for
the same vehicle and month produces the same row, updated in place.
## Requirements
### Requirement: A Vehicle's Month Is Summarised Into One Row

The analytics capability SHALL compute, for a given vehicle and a given calendar
month, one summary covering that vehicle's distance driven, battery used, and
resulting efficiency for that month, and SHALL store the result keyed by the
vehicle and the month alone.

The capability SHALL report these three figures three times over: once across
every day of the month, once restricted to weekdays, and once restricted to
weekend days. A day SHALL count as a weekend day when it is a Saturday or a
Sunday, and a weekday otherwise.

The capability SHALL derive each figure only from days that have a computable
distance and a computable battery-used figure for that vehicle. A day with no
computable predecessor SHALL NOT contribute to any of the three figure sets.

The capability SHALL compute a month's efficiency as the month's total distance
divided by the month's total battery used, both restricted to the days whose
battery-used figure for that day is positive. A day whose battery-used figure is
zero or negative SHALL still count toward that figure set's day total, but SHALL
NOT contribute to the efficiency division.

The capability SHALL record, for each of the three figure sets, how many days
contributed to it — so a reader can tell a real zero figure apart from a figure
set with nothing to measure.

#### Scenario: A month with both weekday and weekend driving gets all three figure sets

- **GIVEN** a calendar month with computable days on both weekdays and weekend days for a vehicle
- **WHEN** the capability summarises that vehicle's month
- **THEN** the all-days figures reflect every computable day in the month
- **AND** the weekday figures reflect only the computable weekdays
- **AND** the weekend figures reflect only the computable weekend days
- **AND** each figure set's day count matches the number of computable days that fed it

#### Scenario: A day with no computable predecessor is excluded from every figure

- **GIVEN** a vehicle's day within the month that has no computable predecessor
- **WHEN** the capability summarises that vehicle's month
- **THEN** that day contributes to no figure set's distance, battery used, or day count

#### Scenario: A day with zero or negative battery used still counts as a tracked day

- **GIVEN** a vehicle's computable day within the month whose battery-used figure is zero or negative
- **WHEN** the capability summarises that vehicle's month
- **THEN** that day's day count still includes this day
- **AND** the efficiency division for that figure set excludes this day's distance and battery used

#### Scenario: A month with no computable days at all still gets a summary

- **GIVEN** a vehicle with no computable day at all within a calendar month
- **WHEN** the capability summarises that vehicle's month
- **THEN** a summary is recorded for that vehicle and month
- **AND** every figure and every day count in it is zero

#### Scenario: Summarising a month again replaces the previous summary

- **GIVEN** a vehicle and calendar month that already has a stored summary
- **WHEN** the capability summarises that same vehicle and month again
- **THEN** exactly one summary exists for that vehicle and month afterward
- **AND** its figures reflect the data available at the time of the second summarisation

### Requirement: A Month's Summary Carries That Month's Measured Pack Capacity

The analytics capability SHALL copy, into a vehicle's monthly summary, the pack
capacity the charging capability measured for that vehicle and that exact
calendar month, when a measured capacity exists.

The capability SHALL record, alongside the copied capacity, whether a measured
capacity was actually found for that vehicle and month — so a reader can tell a
genuinely measured capacity apart from a month with nothing measured yet.

#### Scenario: A month with a measured capacity copies it into the summary

- **GIVEN** a vehicle and calendar month for which the charging capability has recorded a measured
  pack capacity
- **WHEN** the analytics capability summarises that vehicle's month
- **THEN** the summary's capacity equals the value the charging capability measured
- **AND** the summary records that a capacity was found

#### Scenario: A month with no measured capacity records that none was found

- **GIVEN** a vehicle and calendar month for which the charging capability has no measured pack
  capacity — whether because it recorded no evidence at all, or because it recorded evidence too
  thin to produce a measurement
- **WHEN** the analytics capability summarises that vehicle's month
- **THEN** the summary records that no capacity was found for that vehicle and month, regardless of
  which of the two reasons applied

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

