## ADDED Requirements

### Requirement: Nightly-Path Query Logging

The analytics capability SHALL log, for every call made through its precomputed-metrics
write path, its charge-gap write path, and the one read method the nightly cycle uses,
the call's identifying and date-filtering arguments, and — for the read method — how
many records were returned and how many of them were flagged.

This closes an observability gap: without it, which date range the nightly cycle passed
into a given analytics call cannot be determined from a successful run's log output.

#### Scenario: A metrics recalculation logs its vehicle and window
- **GIVEN** a call to recalculate one vehicle's precomputed metrics over a specific date
  window
- **WHEN** the call is made
- **THEN** a log line records the vehicle identity and the window boundaries supplied

#### Scenario: A metrics reconciliation logs its vehicle
- **GIVEN** a call to reconcile one vehicle's precomputed metrics against its stored
  watermarks
- **WHEN** the call is made
- **THEN** a log line records the vehicle identity

#### Scenario: A per-day consumption read logs its vehicle, window, and flagged count
- **GIVEN** a call to read one vehicle's per-day corrected battery-consumed figures over
  a specific date window
- **WHEN** the call completes
- **THEN** a log line records the vehicle identity, the window boundaries supplied, the
  number of days returned, and how many of those days were flagged

#### Scenario: A charge-gap reconciliation logs its vehicle, window, and flagged count
- **GIVEN** a call to reconcile one vehicle's charge-gap ledger against a freshly
  computed set of flagged days for a specific date window
- **WHEN** the call is made
- **THEN** a log line records the vehicle identity, the window boundaries supplied, and
  the number of flagged days supplied

### Requirement: Dashboard-Only Reads Are Not Logged

The analytics capability SHALL NOT log calls to a `Reader` method that has no caller on
the nightly cycle — a method reached only from a live dashboard or history request.

This keeps the added logging scoped to the nightly cycle's own path, so no page load
served by the gateway gains a new log line from this capability.

#### Scenario: A dashboard-only read produces no new log line
- **GIVEN** a call to a `Reader` method other than the per-day consumption read
- **WHEN** the call completes
- **THEN** no log line attributable to this capability is produced for that call
