## ADDED Requirements

### Requirement: Nightly-Path Query Logging

The charging capability SHALL log, for every call made through its Supercharger-mirror
write path, its mirror-watermark read and write paths, its monthly pack-capacity
measurement, and the one manual-entry read method the nightly cycle uses, the call's
identifying arguments and, where applicable, its date window, its row count, or its
report counts.

This closes an observability gap: without it, nothing `internal/charging` does during
step 2 (mirroring Supercharger sessions) or step 4 (measuring monthly pack capacity) of
the nightly cycle can be seen in a successful run's log output.

#### Scenario: A Supercharger session mirror logs its vehicle and count
- **GIVEN** a call to mirror a batch of Supercharger sessions for one vehicle
- **WHEN** the call is made
- **THEN** a log line records the vehicle identity and how many sessions were supplied

#### Scenario: A mirror watermark read logs its vehicle and cursor
- **GIVEN** a call to read the stored Supercharger-mirror cursor for one vehicle
- **WHEN** the call completes
- **THEN** a log line records the vehicle identity and the cursor value returned

#### Scenario: A mirror watermark advance logs its vehicle and the observed instant
- **GIVEN** a call to advance the stored Supercharger-mirror cursor for one vehicle
- **WHEN** the call is made
- **THEN** a log line records the vehicle identity and the instant the cursor advances to

#### Scenario: An analytics-driven Supercharger session read logs its vehicle, window or cursor, and row count
- **GIVEN** a call, reached through the analytics-facing Supercharger session read port,
  to read one vehicle's sessions either within a date window or updated since a given
  instant
- **WHEN** the call completes
- **THEN** a log line records the vehicle identity, the window or instant supplied, and
  the number of sessions returned

#### Scenario: An analytics-driven manual entry read logs its vehicle, cursor, and row count
- **GIVEN** a call to read one vehicle's manual charge entries updated since a given
  instant
- **WHEN** the call completes
- **THEN** a log line records the vehicle identity, the instant supplied, and the number
  of entries returned

#### Scenario: A monthly pack-capacity measurement logs its period, scope, and report counts
- **GIVEN** a call to measure and store effective pack capacity for one calendar month,
  for one vehicle or for every vehicle
- **WHEN** the call completes
- **THEN** a log line records the period, the vehicle scope, and the report's found,
  measured, and thin counts

### Requirement: Gateway-Facing Reads And Writes Are Not Logged

The charging capability SHALL NOT log a call reachable only from a live
`internal/gateway` request — a manual-charge write, a manual-charge list read reached
through the gateway-facing constructor, a Supercharger session read reached through the
gateway-facing constructor, or a Supercharger session battery-percentage verification —
and SHALL NOT log a call to a port method with no caller anywhere in the codebase.

This keeps the added logging scoped to the nightly cycle's own path, so no page load or
form submission served by the gateway gains a new log line from this capability.

#### Scenario: A manual-charge create, update, or delete produces no new log line
- **GIVEN** a call to create, update, or delete a manual charge entry
- **WHEN** the call completes
- **THEN** no log line attributable to this capability is produced for that call

#### Scenario: A gateway-facing manual entry list read produces no new log line
- **GIVEN** a call to list manual charge entries by vehicle, by vehicle and date window,
  or by a caller-supplied set of vehicles
- **WHEN** the call completes
- **THEN** no log line attributable to this capability is produced for that call

#### Scenario: A gateway-facing Supercharger session read produces no new log line
- **GIVEN** a call, reached through the gateway-facing Supercharger session read port, to
  read one vehicle's sessions within a date window
- **WHEN** the call completes
- **THEN** no log line attributable to this capability is produced for that call

#### Scenario: A Supercharger session battery-percentage verification produces no new log line
- **GIVEN** a call to verify or correct a Supercharger session's battery-percentage
  readings
- **WHEN** the call is made
- **THEN** no log line attributable to this capability is produced for that call
