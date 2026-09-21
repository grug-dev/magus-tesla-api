# process-vehicle-data Specification

## Purpose

One vehicle-data cycle, run by the poller on a schedule or by the manual-rerun
endpoint. The cycle syncs fleet data from Tesla, mirrors charging data, rebuilds the
analytics read model, measures each vehicle's monthly pack capacity, and syncs each
vehicle's monthly metrics. It also records exactly one summary row for every run,
including a run that failed outright.

This capability owns the ORDER of those steps and what happens when one fails. What
each step computes belongs to the module that owns it.
## Requirements
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

### Requirement: Every Attempt Record Is Correlated To Its Run And Its Trigger

Every per-vehicle collection attempt the platform records during fleet-data synchronization SHALL carry the run identifier of the cycle that produced it and what triggered that cycle. All attempt records produced by one cycle SHALL share the same run identifier.

#### Scenario: Attempts from one cycle share one run identifier
- **GIVEN** a vehicle-data cycle that processes more than one vehicle
- **WHEN** the cycle completes
- **THEN** every attempt record it produced carries the same run identifier
- **AND** none of them carries any other cycle's run identifier

#### Scenario: An attempt record carries its trigger
- **GIVEN** a vehicle-data cycle triggered by the scheduler
- **WHEN** the cycle produces an attempt record
- **THEN** that record's trigger is recorded as the scheduler

#### Scenario: A record from before this capability existed has no run identifier
- **GIVEN** an attempt record written before the platform correlated attempts to runs
- **WHEN** that record is read
- **THEN** its run identifier is absent
- **AND** its trigger is recorded as the scheduler, since no other trigger existed at the
  time it was written

### Requirement: Every Cycle Records A Poll Run Summary

The platform SHALL record exactly one poll-run summary for every vehicle-data cycle
it processes, spanning from before fleet-data synchronization begins to after the
cycle's last step completes — or to the point where a whole-cycle synchronization
failure short-circuits the remaining steps. The summary SHALL be recorded even when
synchronization fails as a whole, so a cycle that never attempts a single vehicle
still leaves a durable, queryable trace of having run at all. A failure to record the
summary SHALL NOT change the cycle's own reported outcome.

#### Scenario: A fully successful cycle's summary reflects its measured span and outcome

- **GIVEN** a vehicle-data cycle that completes every step it runs
- **WHEN** the cycle's poll-run summary is recorded
- **THEN** the summary's recorded start precedes fleet-data synchronization and its
  recorded finish follows analytics recalculation
- **AND** the summary's account and vehicle counts match the cycle's actual outcome

#### Scenario: A whole-cycle synchronization failure still records a summary

- **GIVEN** a vehicle-data cycle whose fleet-data synchronization step fails as a
  whole, before any vehicle is attempted
- **WHEN** the cycle is processed
- **THEN** a poll-run summary is recorded for that cycle
- **AND** its account and vehicle counts are all zero
- **AND** its recorded start and finish reflect how quickly the failure occurred,
  not the full cycle span

#### Scenario: A failure to record the summary does not change the cycle's reported outcome

- **GIVEN** a vehicle-data cycle that completes successfully
- **WHEN** recording that cycle's poll-run summary itself fails
- **THEN** the cycle is still reported as successful
- **AND** the cycle's own reported counts are unaffected by the recording failure

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

### Requirement: Analytics Recalculation Logs Are Attributable Per Vehicle And Per Half

The platform SHALL log the metrics-reconciliation half and the gap-reconciliation
half of the analytics-recalculation step separately, once per vehicle the step
processes, so that any query logged during that step can be attributed to the
vehicle and the half that caused it.

#### Scenario: A vehicle's metrics-reconciliation queries are attributable to that vehicle

- **GIVEN** the analytics-recalculation step processes a vehicle
- **WHEN** that vehicle's metrics reconciliation runs
- **THEN** a log line identifying that vehicle and the metrics-reconciliation half
  is recorded before the queries that half causes

#### Scenario: A vehicle's gap-reconciliation queries are attributable to that vehicle, not to metrics reconciliation

- **GIVEN** the analytics-recalculation step processes a vehicle whose metrics
  reconciliation succeeded
- **WHEN** that vehicle's gap reconciliation runs
- **THEN** a log line identifying that vehicle, the gap-reconciliation half, and
  the window being reconciled is recorded before the queries that half causes
- **AND** that log line is distinct from the metrics-reconciliation log line for
  the same vehicle

#### Scenario: Multiple vehicles each get their own pair of log lines

- **GIVEN** the analytics-recalculation step processes more than one vehicle
- **WHEN** the step completes
- **THEN** each processed vehicle has its own metrics-reconciliation log line and,
  if its metrics reconciliation succeeded, its own gap-reconciliation log line
- **AND** no single log line covers more than one vehicle

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

