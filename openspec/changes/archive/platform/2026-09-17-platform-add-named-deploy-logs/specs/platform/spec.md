## ADDED Requirements

### Requirement: Named And Located Deploy Logs
The platform's deployment stack SHALL make the web gateway's, the poller's,
and the reverse proxy's log output available as a named file at a fixed,
predictable location, rather than requiring an operator to discover a
per-container internal storage path before reading a service's log. The
database service and the one-shot migration step are exempt from this
requirement and MAY continue to expose their log output only through the
container runtime's own log inspection command.

#### Scenario: An operator finds the web gateway's log by name
- **GIVEN** the deployment stack is running
- **WHEN** an operator looks for the web gateway's log output
- **THEN** it is available at a fixed, named location on the host
- **AND** the operator does not need to look up any per-container internal
  identifier first

#### Scenario: An operator finds the poller's log by name
- **GIVEN** the deployment stack is running
- **WHEN** an operator looks for the poller's log output
- **THEN** it is available at a fixed, named location on the host
- **AND** the operator does not need to look up any per-container internal
  identifier first

#### Scenario: An operator finds the reverse proxy's log by name
- **GIVEN** the deployment stack is running
- **WHEN** an operator looks for the reverse proxy's log output
- **THEN** it is available at a fixed, named location on the host
- **AND** the operator does not need to look up any per-container internal
  identifier first

#### Scenario: The database and migration services keep their existing log access
- **GIVEN** the deployment stack is running
- **WHEN** an operator looks for the database service's or the migration
  step's log output
- **THEN** it remains available through the container runtime's own log
  inspection command
- **AND** this requirement does not obligate a named file for either service

### Requirement: Time-Bounded Named Log Retention
The platform SHALL purge named deploy log data automatically once it exceeds
a bounded retention period, so that named log files do not accumulate
without limit on the host.

#### Scenario: A named log file older than the retention period is purged
- **GIVEN** a named deploy log has accumulated data older than the
  configured retention period
- **WHEN** the retention mechanism next runs
- **THEN** the data older than the retention period is removed
- **AND** data within the retention period is preserved

#### Scenario: A named log file growing quickly is bounded before its next scheduled check
- **GIVEN** a named deploy log is growing continuously, for example during a
  noisy or repeating failure
- **WHEN** that log's data reaches the configured size threshold before its
  next scheduled retention check
- **THEN** the retention mechanism rotates it immediately rather than
  waiting for the next scheduled check

#### Scenario: A self-rotating log is not also purged by the host retention mechanism
- **GIVEN** a named deploy log that performs its own rotation and retention
  internally
- **WHEN** the host-level retention mechanism runs
- **THEN** it does not act on that log file
- **AND** exactly one rotation mechanism governs that file at any time
