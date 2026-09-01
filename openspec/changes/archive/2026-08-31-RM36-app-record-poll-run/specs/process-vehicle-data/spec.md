## ADDED Requirements

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
