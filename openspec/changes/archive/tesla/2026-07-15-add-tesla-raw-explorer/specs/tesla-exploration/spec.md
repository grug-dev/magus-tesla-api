## ADDED Requirements

### Requirement: Raw Payload Access per Endpoint
The tesla adapter SHALL expose the raw, undecoded JSON body returned by each Fleet API endpoint it
calls — the vehicle inventory listing, a vehicle's full data snapshot, and a vehicle wake — so that
exploration tooling can inspect the complete response, including fields the typed DTOs omit.

#### Scenario: Raw vehicle inventory
- **GIVEN** valid credentials for a Tesla account
- **WHEN** a caller requests the raw vehicle inventory
- **THEN** the adapter returns the unmodified JSON body from the inventory endpoint

#### Scenario: Raw vehicle snapshot
- **GIVEN** valid credentials and the identifier of an online vehicle
- **WHEN** a caller requests that vehicle's raw data snapshot
- **THEN** the adapter returns the unmodified JSON body from the vehicle-data endpoint
- **AND** the body includes fields beyond those exposed by the typed snapshot DTO

#### Scenario: Raw wake response
- **GIVEN** valid credentials and the identifier of a vehicle
- **WHEN** a caller issues a raw wake request
- **THEN** the adapter returns the unmodified JSON body from the wake endpoint

### Requirement: Typed Contract Isolation
Raw payload access SHALL NOT alter the typed vehicle-service contract used by domain callers. The
raw operations SHALL be reachable only from the concrete adapter, not from the interface that
domain code depends on, so that adding them changes no existing caller.

#### Scenario: Domain callers are unaffected
- **GIVEN** a domain caller that depends on the typed vehicle-service interface
- **WHEN** raw payload access is added to the adapter
- **THEN** the typed interface and its methods are unchanged
- **AND** the raw operations are not part of that interface

### Requirement: Preserved Unauthorized Signal
Raw operations SHALL surface the same distinct unauthorized error as the typed operations when the
Fleet API rejects the supplied credentials as expired or invalid (HTTP 401), so exploration tooling
can report an expired token rather than a generic failure.

#### Scenario: Expired token during a raw call
- **GIVEN** credentials whose access token has expired
- **WHEN** a raw operation calls the Fleet API and receives an HTTP 401
- **THEN** the adapter returns the same distinct unauthorized error a caller can detect
- **AND** the tooling can use that signal to prompt re-authentication
