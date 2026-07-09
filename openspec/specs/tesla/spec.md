# tesla Specification

## Purpose

The `tesla` capability is an anti-corruption adapter over the external Tesla Fleet API
(`ai/architecture.md` §6). It lets any caller list an account's vehicles, fetch a vehicle's
full state snapshot, and wake a sleeping vehicle on behalf of caller-supplied credentials. It
holds no user identity of its own, signals expired credentials distinctly so callers can
refresh, and exposes metric-converted companion values for the miles/mph data the Fleet API
returns.

## Requirements

### Requirement: Vehicle Inventory Listing
The tesla adapter SHALL return every vehicle associated with the account behind the supplied
credentials, each carrying its identity (identifier, VIN, display name) and current state.

#### Scenario: Listing an account's vehicles
- **GIVEN** valid credentials for a Tesla account
- **WHEN** a caller requests the vehicle inventory
- **THEN** the adapter returns each vehicle associated with that account
- **AND** each vehicle carries its identifier, VIN, display name, and state

### Requirement: Full Vehicle Snapshot
The tesla adapter SHALL fetch a single vehicle's full state snapshot, exposing its charge,
climate, drive, and vehicle-state data.

#### Scenario: Fetching a snapshot for an online vehicle
- **GIVEN** valid credentials and the identifier of a vehicle that is online
- **WHEN** a caller requests that vehicle's data snapshot
- **THEN** the adapter returns the vehicle's full state
- **AND** the snapshot includes charge data (battery level, range, charging state, charge rate)
- **AND** the snapshot includes climate data (inside and outside temperature, climate on/off)
- **AND** the snapshot includes drive data (speed, latitude, longitude, heading)
- **AND** the snapshot includes vehicle-state data (locked, odometer, software version)

### Requirement: Vehicle Wake
The tesla adapter SHALL request that a sleeping vehicle come online and return the vehicle's
reported state after the request.

#### Scenario: Waking a sleeping vehicle
- **GIVEN** valid credentials and the identifier of a sleeping vehicle
- **WHEN** a caller issues a wake request
- **THEN** the adapter asks the vehicle to come online
- **AND** returns the vehicle's reported state

### Requirement: Wake-Before-Data Ordering
Fetching a full vehicle snapshot SHALL require the vehicle to be online. The documented path is
to wake the vehicle and poll its state until it reports online before requesting the snapshot.

#### Scenario: Snapshot needed for a sleeping vehicle
- **GIVEN** a vehicle that is not online
- **WHEN** a caller needs that vehicle's full snapshot
- **THEN** the caller wakes the vehicle and polls the inventory until the vehicle reports online
- **AND** only then requests the snapshot

### Requirement: Distinct Unauthorized Signal
The tesla adapter SHALL surface a distinct, detectable unauthorized error (not a generic
failure) when the Fleet API rejects the supplied credentials as expired or invalid (HTTP 401),
so that callers can trigger a token refresh and retry.

#### Scenario: Expired access token is signalled distinctly
- **GIVEN** credentials whose access token has expired
- **WHEN** any adapter operation calls the Fleet API and receives an HTTP 401
- **THEN** the adapter returns a distinct unauthorized error that a caller can detect
- **AND** the caller can use that signal to refresh the token and retry

### Requirement: Stateless Per-Call Credentials
The tesla adapter SHALL hold no user identity of its own; callers SHALL supply the credentials
to use on every operation, so a single shared adapter instance can serve any user.

#### Scenario: One instance serves multiple users
- **GIVEN** a single shared adapter instance
- **WHEN** operations are invoked for different users, each with its own credentials
- **THEN** each call acts on behalf of the account behind the credentials supplied to that call
- **AND** the adapter retains no identity between calls

### Requirement: Metric Conversion of Distance Values
For every distance or speed value the adapter exposes in miles or miles-per-hour, it SHALL also
expose a companion value in kilometres or kilometres-per-hour. Companion values for optional
fields SHALL be nil-safe — absent when the source value is absent.

#### Scenario: Range and odometer available in kilometres
- **GIVEN** a vehicle snapshot reporting battery range and odometer in miles
- **WHEN** a caller reads the companion metric values
- **THEN** the adapter provides the range and odometer converted to kilometres

#### Scenario: Optional speed is nil-safe
- **GIVEN** a snapshot for a parked vehicle that reports no speed
- **WHEN** a caller reads the companion metric speed
- **THEN** the adapter reports no speed rather than a converted zero
