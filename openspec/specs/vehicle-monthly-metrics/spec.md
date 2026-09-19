# vehicle-monthly-metrics Specification

## Purpose
TBD - created by archiving change RM67-analytics-add-vehicle-monthly-metrics. Update Purpose after archive.
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

