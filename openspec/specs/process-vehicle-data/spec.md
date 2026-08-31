# process-vehicle-data Specification

## Purpose
TBD - created by archiving change RM29-app-add-process-vehicle-data. Update Purpose after archive.
## Requirements
### Requirement: Processing A Vehicle-Data Cycle Runs Three Steps In Order

The platform SHALL provide a single operation that processes one vehicle-data cycle:
synchronizing fleet data, then processing charging data, then recalculating analytics —
in that order, whoever triggers the cycle. Each invocation SHALL be identified by a
freshly generated run identifier and SHALL record what triggered it, either the
scheduler or a manual request.

#### Scenario: A triggered cycle runs its three steps in order
- **GIVEN** the platform is asked to process a vehicle-data cycle
- **WHEN** the cycle runs to completion
- **THEN** fleet data is synchronized first
- **AND** charging data is processed second
- **AND** analytics are recalculated third

#### Scenario: Each invocation gets its own run identifier
- **GIVEN** two separate requests to process a vehicle-data cycle
- **WHEN** both cycles run
- **THEN** each cycle is identified by a different run identifier

### Requirement: A Whole-Cycle Synchronization Failure Skips The Remaining Steps

The platform SHALL NOT process charging data or recalculate analytics for a cycle whose fleet-data synchronization step fails as a whole (not a single vehicle's failure, but the step itself being unable to proceed), and SHALL report the failure.

#### Scenario: A whole-cycle sync failure stops the cycle
- **GIVEN** a vehicle-data cycle whose fleet-data synchronization step fails as a whole
- **WHEN** the cycle is processed
- **THEN** charging data is not processed for that cycle
- **AND** analytics are not recalculated for that cycle
- **AND** the cycle reports the failure

#### Scenario: A single vehicle's sync failure does not stop the cycle
- **GIVEN** a vehicle-data cycle in which fleet-data synchronization succeeds overall but
  fails for one vehicle among several
- **WHEN** the cycle is processed
- **THEN** charging data is still processed for the cycle
- **AND** analytics are still recalculated for the cycle

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

- **GIVEN** a vehicle-data cycle that completes all three steps
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
  not the full three-step span

#### Scenario: A failure to record the summary does not change the cycle's reported outcome

- **GIVEN** a vehicle-data cycle that completes successfully
- **WHEN** recording that cycle's poll-run summary itself fails
- **THEN** the cycle is still reported as successful
- **AND** the cycle's own reported counts are unaffected by the recording failure

