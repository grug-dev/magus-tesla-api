## RENAMED Requirements

- FROM: `### Requirement: Processing A Vehicle-Data Cycle Runs Four Steps In Order`
- TO: `### Requirement: Processing A Vehicle-Data Cycle Runs Five Steps In Order`

## MODIFIED Requirements

### Requirement: Processing A Vehicle-Data Cycle Runs Five Steps In Order

The platform SHALL provide a single operation that processes one vehicle-data cycle:
synchronizing fleet data, then processing charging data, then recalculating analytics, then
measuring monthly vehicle capacity, then syncing monthly vehicle metrics — in that order, whoever
triggers the cycle. The fourth step SHALL run only when the cycle is processed on the first
calendar day of the month, and SHALL compute the previous month in that case. The fifth step
SHALL run on every invocation, regardless of the calendar day. Each invocation SHALL be identified
by a freshly generated run identifier and SHALL record what triggered it, either the scheduler or
a manual request.

#### Scenario: A triggered cycle runs its five steps in order
- **GIVEN** the platform is asked to process a vehicle-data cycle on the first day of the month
- **WHEN** the cycle runs to completion
- **THEN** fleet data is synchronized first
- **AND** charging data is processed second
- **AND** analytics are recalculated third
- **AND** monthly vehicle capacity is measured fourth
- **AND** monthly vehicle metrics are synced fifth

#### Scenario: The fifth step runs on every day, unlike the fourth
- **GIVEN** the platform is asked to process a vehicle-data cycle on a day that is not the first
  of the month
- **WHEN** the cycle runs to completion
- **THEN** monthly vehicle capacity is not measured for that cycle
- **AND** monthly vehicle metrics are still synced for that cycle

#### Scenario: Each invocation gets its own run identifier
- **GIVEN** two separate requests to process a vehicle-data cycle
- **WHEN** both cycles run
- **THEN** each cycle is identified by a different run identifier

### Requirement: A Whole-Cycle Synchronization Failure Skips The Remaining Steps

The platform SHALL NOT process charging data, recalculate analytics, measure monthly vehicle
capacity, or sync monthly vehicle metrics for a cycle whose fleet-data synchronization step fails
as a whole (not a single vehicle's failure, but the step itself being unable to proceed), and
SHALL report the failure.

#### Scenario: A whole-cycle sync failure stops the cycle
- **GIVEN** a vehicle-data cycle whose fleet-data synchronization step fails as a whole
- **WHEN** the cycle is processed
- **THEN** charging data is not processed for that cycle
- **AND** analytics are not recalculated for that cycle
- **AND** monthly vehicle capacity is not measured for that cycle
- **AND** monthly vehicle metrics are not synced for that cycle
- **AND** the cycle reports the failure

#### Scenario: A single vehicle's sync failure does not stop the cycle
- **GIVEN** a vehicle-data cycle in which fleet-data synchronization succeeds overall but
  fails for one vehicle among several
- **WHEN** the cycle is processed
- **THEN** charging data is still processed for the cycle
- **AND** analytics are still recalculated for the cycle
- **AND** monthly vehicle capacity is still measured for the cycle, if the cycle is running on
  the first day of the month
- **AND** monthly vehicle metrics are still synced for the cycle

## ADDED Requirements

### Requirement: Monthly Vehicle Metrics Are Synced Every Night For The Current And Previous Month

The platform SHALL sync monthly vehicle metrics for every vehicle on every cycle, regardless of
the calendar day, using the platform's own default time zone to decide which calendar month is
current. For each vehicle, the sync SHALL cover exactly two calendar months: the current month and
the month immediately before it. Syncing a month for a vehicle SHALL overwrite any previously
synced result for that same vehicle and month, rather than adding to it. A failure to sync one
vehicle's month SHALL be recorded but SHALL NOT change the cycle's own reported outcome, and SHALL
NOT prevent syncing that same vehicle's other month or any other vehicle's months.

#### Scenario: Every cycle syncs the current and previous month, for every vehicle
- **GIVEN** a vehicle-data cycle is processed on any calendar day, and fleet-data synchronization
  has succeeded
- **WHEN** the cycle reaches the monthly-metrics sync step
- **THEN** every registered vehicle has its current calendar month synced
- **AND** every registered vehicle has the calendar month immediately before it synced

#### Scenario: The platform's default time zone decides the current calendar month, not a configurable one
- **GIVEN** a moment whose calendar day, in the platform's default time zone, falls in a different
  calendar month than in UTC
- **WHEN** a vehicle-data cycle is processed at that moment
- **THEN** the current month synced is the one the platform's default time zone reports, not UTC's

#### Scenario: Re-syncing the same vehicle and month overwrites the previous result
- **GIVEN** a vehicle's month was already synced by an earlier cycle
- **WHEN** a later cycle syncs that same vehicle and month again
- **THEN** the result reflects only the later sync, not a combination of the two

#### Scenario: A sync failure for one vehicle's month does not stop the vehicle's other month
- **GIVEN** a vehicle-data cycle in which syncing a vehicle's current month fails
- **WHEN** the cycle continues
- **THEN** that same vehicle's previous month is still synced

#### Scenario: A sync failure for one vehicle does not stop another vehicle
- **GIVEN** a vehicle-data cycle in which syncing one vehicle's months fails entirely
- **WHEN** the cycle continues
- **THEN** every other vehicle's current and previous month are still synced

#### Scenario: A sync failure does not change the cycle's reported outcome
- **GIVEN** a vehicle-data cycle whose other steps all succeed
- **WHEN** syncing monthly vehicle metrics fails for at least one vehicle
- **THEN** the cycle is still reported as successful
- **AND** the cycle's own reported counts are unaffected by the sync failure
