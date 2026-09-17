# analytics

## ADDED Requirements

### Requirement: The Nightly Manual-Entry Read Is Logged By Its Caller

The analytics capability SHALL log, when it reads a vehicle's manual charge entries for
a date window while recomputing that vehicle's precomputed metrics, the vehicle
identity, the window boundaries it supplied, and how many entries came back.

The charging capability cannot log this read selectively. Its manual-entry read port is
built by one shared constructor, so logging the read there would also log it for the two
gateway callers that run on every page render and every form submission. Logging it from
this capability, at the one place this capability makes the call, records the nightly
read and leaves those gateway reads silent.

#### Scenario: A metrics recalculation logs the manual entries it read

- **GIVEN** a call to recalculate one vehicle's precomputed metrics over a date window
- **WHEN** the manual charge entries for that window have been read
- **THEN** a log line records the vehicle identity, the window boundaries supplied, and
  the number of entries returned

#### Scenario: A gateway manual-entry read is not logged by this capability

- **GIVEN** a manual-entry read made by a gateway page render or form submission,
  without recalculating any vehicle's precomputed metrics
- **WHEN** the read completes
- **THEN** no log line is produced by this capability for that read
