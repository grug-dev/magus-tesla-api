# manual-rerun-api Specification

## Purpose
TBD - created by archiving change platform-add-manual-rerun-api. Update Purpose after archive.
## Requirements
### Requirement: The Endpoint Is Never Open By Accident

The platform SHALL expose an HTTP endpoint that starts one on-demand vehicle-data
cycle, protected only by a secret value the owner configures. The platform SHALL NOT
start this endpoint at all when no secret is configured — there SHALL be no state in
which the endpoint accepts a request without one.

#### Scenario: No secret configured means no endpoint

- **GIVEN** the platform starts with no rerun secret configured
- **WHEN** the platform finishes starting
- **THEN** no request to the rerun endpoint is accepted, at any path

#### Scenario: A configured secret is required in the request path

- **GIVEN** the platform starts with a rerun secret configured
- **WHEN** a request arrives at the rerun path without the correct secret
- **THEN** the request is rejected as not found

#### Scenario: The correct secret starts a cycle

- **GIVEN** the platform starts with a rerun secret configured
- **WHEN** a request arrives at the rerun path with the correct secret
- **THEN** a vehicle-data cycle is started, triggered by the API

### Requirement: A Manual Rerun Never Overlaps Any Other Cycle

The platform SHALL NOT run a manually-triggered cycle at the same time as any other
vehicle-data cycle, whether that other cycle was started by the scheduler or by
another manual request. A request that arrives while a cycle is already running
SHALL be rejected without starting anything.

#### Scenario: A manual request while the scheduler is running is rejected

- **GIVEN** a scheduled vehicle-data cycle is currently running
- **WHEN** a manual rerun request arrives
- **THEN** the request is rejected as a conflict
- **AND** no second cycle is started

#### Scenario: A manual request while another manual request is running is rejected

- **GIVEN** a manually-triggered vehicle-data cycle is currently running
- **WHEN** a second manual rerun request arrives
- **THEN** the request is rejected as a conflict
- **AND** no second cycle is started

#### Scenario: The scheduler is not blocked by a finished manual cycle

- **GIVEN** a manually-triggered vehicle-data cycle has already finished
- **WHEN** the scheduler's own trigger time arrives
- **THEN** the scheduler starts its cycle normally

#### Scenario: The scheduler skips its turn when a manual cycle is running

- **GIVEN** a manually-triggered vehicle-data cycle is currently running
- **WHEN** the scheduler's own trigger time arrives
- **THEN** the scheduler does not start a second cycle
- **AND** the platform records that the scheduled cycle was skipped
- **AND** the schedule continues, so the next day runs normally

### Requirement: A Manual Rerun Responds Immediately And Runs In The Background

The platform SHALL respond to an accepted manual rerun request without waiting for
the vehicle-data cycle to finish. The cycle SHALL continue running after the response
is sent, using the platform's own lifetime, not the lifetime of the request that
started it.

#### Scenario: The response arrives before the cycle finishes

- **GIVEN** a manual rerun request is accepted
- **WHEN** the platform responds
- **THEN** the response is sent before the vehicle-data cycle completes

#### Scenario: The cycle survives the response being sent

- **GIVEN** a manual rerun request is accepted and the response has been sent
- **WHEN** the requester's own connection is closed immediately after
- **THEN** the vehicle-data cycle still runs to completion

