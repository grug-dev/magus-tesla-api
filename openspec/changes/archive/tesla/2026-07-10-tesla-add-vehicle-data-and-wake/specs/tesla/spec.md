## MODIFIED Requirements

### Requirement: Full Vehicle Snapshot
The tesla adapter SHALL fetch a single vehicle's full state snapshot with exactly one Fleet API
request and return BOTH the parsed typed snapshot AND the raw, unmodified payload of that same
response, so a caller can store the payload losslessly while reading typed fields. The typed
snapshot SHALL expose charge, climate, drive, and vehicle-state data, including the charge limit
and sentry-mode status.

#### Scenario: Fetching a snapshot for an online vehicle
- **GIVEN** valid credentials and the identifier of a vehicle that is online
- **WHEN** a caller requests that vehicle's data snapshot
- **THEN** the adapter returns the vehicle's full typed state
- **AND** the snapshot includes charge data (battery level, range, charging state, charge rate, charge limit)
- **AND** the snapshot includes climate data (inside and outside temperature, climate on/off)
- **AND** the snapshot includes drive data (speed, latitude, longitude, heading)
- **AND** the snapshot includes vehicle-state data (locked, odometer, software version, sentry mode)

#### Scenario: Raw payload returned alongside the typed snapshot
- **GIVEN** valid credentials and the identifier of a vehicle that is online
- **WHEN** a caller requests that vehicle's data snapshot
- **THEN** the adapter also returns the raw, unmodified vehicle-data payload from the same response
- **AND** parsing that raw payload yields the same values the typed snapshot exposes

#### Scenario: One request serves both results
- **GIVEN** valid credentials and the identifier of a vehicle that is online
- **WHEN** a caller requests that vehicle's data snapshot
- **THEN** the adapter makes exactly one Fleet API data request
- **AND** both the typed snapshot and the raw payload derive from that single response
