## RENAMED Requirements

- FROM: `### Requirement: Processing A Vehicle-Data Cycle Runs Three Steps In Order`
- TO: `### Requirement: Processing A Vehicle-Data Cycle Runs Four Steps In Order`

## MODIFIED Requirements

### Requirement: Processing A Vehicle-Data Cycle Runs Four Steps In Order

The platform SHALL provide a single operation that processes one vehicle-data cycle:
synchronizing fleet data, then processing charging data, then recalculating analytics, then
measuring monthly vehicle capacity — in that order, whoever triggers the cycle. The fourth step
SHALL run only when the cycle is processed on the first calendar day of the month, and SHALL
compute the previous month in that case. Each invocation SHALL be identified by a freshly
generated run identifier and SHALL record what triggered it, either the scheduler or a manual
request.

#### Scenario: A triggered cycle runs its four steps in order
- **GIVEN** the platform is asked to process a vehicle-data cycle on the first day of the month
- **WHEN** the cycle runs to completion
- **THEN** fleet data is synchronized first
- **AND** charging data is processed second
- **AND** analytics are recalculated third
- **AND** monthly vehicle capacity is measured fourth

#### Scenario: Each invocation gets its own run identifier
- **GIVEN** two separate requests to process a vehicle-data cycle
- **WHEN** both cycles run
- **THEN** each cycle is identified by a different run identifier

### Requirement: A Whole-Cycle Synchronization Failure Skips The Remaining Steps

The platform SHALL NOT process charging data, recalculate analytics, or measure monthly vehicle
capacity for a cycle whose fleet-data synchronization step fails as a whole (not a single
vehicle's failure, but the step itself being unable to proceed), and SHALL report the failure.

#### Scenario: A whole-cycle sync failure stops the cycle
- **GIVEN** a vehicle-data cycle whose fleet-data synchronization step fails as a whole
- **WHEN** the cycle is processed
- **THEN** charging data is not processed for that cycle
- **AND** analytics are not recalculated for that cycle
- **AND** monthly vehicle capacity is not measured for that cycle
- **AND** the cycle reports the failure

#### Scenario: A single vehicle's sync failure does not stop the cycle
- **GIVEN** a vehicle-data cycle in which fleet-data synchronization succeeds overall but
  fails for one vehicle among several
- **WHEN** the cycle is processed
- **THEN** charging data is still processed for the cycle
- **AND** analytics are still recalculated for the cycle
- **AND** monthly vehicle capacity is still measured for the cycle, if the cycle is running on
  the first day of the month

## ADDED Requirements

### Requirement: Monthly Vehicle Capacity Is Measured Only On The First Day Of The Month, For The Previous Month

The platform SHALL measure monthly vehicle capacity only when a vehicle-data cycle is processed on
the first calendar day of the month, using the platform's own default time zone to decide which
calendar day it is. On every other day, this step SHALL do nothing. When it does run, it SHALL
request the measurement for the month immediately before the current one, covering every vehicle
with at least one usable charge record — never a single vehicle picked by the nightly cycle
itself. A failure to measure monthly vehicle capacity SHALL be recorded but SHALL NOT change the
cycle's own reported outcome.

#### Scenario: The first day of the month triggers the measurement, for the previous month
- **GIVEN** a vehicle-data cycle is processed on the 1st day of a month, in the platform's default
  time zone
- **WHEN** fleet-data synchronization has succeeded
- **THEN** monthly vehicle capacity is measured for the month immediately before the current one
- **AND** the measurement covers every vehicle with at least one usable charge record that month

#### Scenario: Any other day of the month does not trigger the measurement
- **GIVEN** a vehicle-data cycle is processed on any day other than the 1st, in the platform's
  default time zone
- **WHEN** fleet-data synchronization has succeeded
- **THEN** monthly vehicle capacity is not measured for that cycle

#### Scenario: The platform's default time zone decides the calendar day, not a configurable one
- **GIVEN** a moment whose calendar day is the 1st of the month in the platform's default time
  zone, but a different day in UTC
- **WHEN** a vehicle-data cycle is processed at that moment
- **THEN** monthly vehicle capacity is measured, because the platform's default time zone says
  it is the 1st

#### Scenario: A measurement failure does not change the cycle's reported outcome
- **GIVEN** a vehicle-data cycle running on the first day of the month, whose fleet-data
  synchronization and other steps all succeed
- **WHEN** measuring monthly vehicle capacity fails
- **THEN** the cycle is still reported as successful
- **AND** the cycle's own reported counts are unaffected by the measurement failure
