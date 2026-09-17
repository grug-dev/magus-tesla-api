## ADDED Requirements

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
