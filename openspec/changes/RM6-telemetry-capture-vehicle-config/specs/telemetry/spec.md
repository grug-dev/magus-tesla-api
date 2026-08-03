## ADDED Requirements

### Requirement: Static Vehicle Config Capture During Nightly Collection
The telemetry capability SHALL, during each collection cycle, capture the two static
`vehicle_config` attributes (`exterior_color`, `car_type`) from the `VehicleData` response already
fetched for a vehicle, and SHALL persist them through the account module's public port method
(`SetVehicleConfigIfEmpty`) — WITHOUT making any additional Tesla Fleet API call beyond the
`VehicleData` fetch every vehicle capture already performs. The capability SHALL skip calling that
port method when the vehicle's already-known registry record has both `exterior_color` and
`car_type` non-nil (already captured in a prior cycle). The capability SHALL also skip calling that
port method when either value observed from `VehicleData` this cycle is an empty string — whether
because Tesla reported an empty string, or because the vehicle's capture attempt did not reach
`VehicleData` this cycle (e.g. a wake timeout, an unauthorized connection, or a persistent API
error). At most one write-back attempt SHALL occur per vehicle per collection cycle, even when the
vehicle's snapshot capture was internally retried once for a transient error.

#### Scenario: A vehicle with both config values already captured is never re-written
- **GIVEN** a registered vehicle whose registry record already has non-nil `exterior_color` and
  `car_type`
- **WHEN** a collection cycle captures that vehicle's snapshot successfully
- **THEN** the capability does not call the account port's config write-back method for that
  vehicle
- **AND** the vehicle's snapshot is still captured normally

#### Scenario: An uncaptured vehicle is written back after a successful capture
- **GIVEN** a registered vehicle whose registry record has `exterior_color` and `car_type` both nil
- **AND** that vehicle's `VehicleData` response reports non-empty values for both
- **WHEN** a collection cycle captures that vehicle's snapshot successfully
- **THEN** the capability calls the account port's config write-back method exactly once, passing
  the two observed values

#### Scenario: An empty observed value is never written back
- **GIVEN** a registered vehicle whose registry record has `exterior_color` and `car_type` both nil
- **AND** that vehicle's `VehicleData` response reports an empty string for at least one of the two
  values
- **WHEN** a collection cycle captures that vehicle's snapshot
- **THEN** the capability does not call the account port's config write-back method for that
  vehicle

#### Scenario: A vehicle whose capture attempt fails observes no config this cycle
- **GIVEN** a registered vehicle whose snapshot capture attempt fails for the cycle (wake timeout,
  unauthorized connection, or a persistent API error that exhausts the bounded retry)
- **WHEN** the cycle completes
- **THEN** the capability does not call the account port's config write-back method for that
  vehicle
- **AND** the vehicle's recorded attempt outcome and reason reflect the actual capture failure,
  unaffected by config capture

#### Scenario: A retried capture attempts config write-back at most once
- **GIVEN** a registered vehicle whose snapshot capture fails transiently on the first attempt this
  cycle and succeeds on the single bounded retry, with the retry's `VehicleData` response reporting
  non-empty config values
- **WHEN** the collection cycle completes
- **THEN** the capability calls the account port's config write-back method at most once for that
  vehicle, using the retry's observed values, never the first (failed) attempt's

### Requirement: Cycle Report Config Capture Failures Counter
The telemetry capability's cycle report SHALL include a count of static vehicle-config write-back
attempts that failed (the account port's write-back method returned an error). A failed write-back
SHALL NOT alter the vehicle's recorded attempt outcome or reason, and SHALL NOT abort collection for
any other vehicle. The counter SHALL be zero when no write-back was attempted, or every attempted
write-back succeeded.

#### Scenario: A failed config write-back is counted without affecting the recorded attempt
- **GIVEN** a registered vehicle whose snapshot is captured successfully and whose config
  write-back is attempted (the vehicle's registry record has at least one nil config field, and
  both observed values are non-empty)
- **AND** the account port's write-back call returns an error
- **WHEN** the collection cycle completes
- **THEN** the cycle report's config-capture-failures count is one
- **AND** the vehicle's recorded attempt still reflects a successful snapshot capture, unaffected
  by the write-back failure

#### Scenario: Cycle report's config capture counter is zero when nothing needed writing back
- **GIVEN** a collection cycle where every vehicle's registry record already has both config
  values captured
- **WHEN** the collection cycle completes
- **THEN** the cycle report's config-capture-failures count is zero
