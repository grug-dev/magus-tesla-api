## MODIFIED Requirements

### Requirement: Vehicle Inventory Listing

The tesla adapter SHALL return every vehicle associated with the account behind the supplied
credentials, each carrying its identity (identifier, VIN, display name), current state, and
access type. The access type reflects whether the authenticated account is the vehicle's
owner or has driver-level access, as reported by the Fleet API.

#### Scenario: Listing an account's vehicles
- **GIVEN** valid credentials for a Tesla account
- **WHEN** a caller requests the vehicle inventory
- **THEN** the adapter returns each vehicle associated with that account
- **AND** each vehicle carries its identifier, VIN, display name, and state

#### Scenario: Access type decoded for each vehicle
- **GIVEN** valid credentials for a Tesla account
- **AND** the Fleet API response includes an `access_type` field on one or more vehicles
- **WHEN** a caller requests the vehicle inventory
- **THEN** each vehicle in the result carries the `access_type` value as decoded from the
  Fleet API response
- **AND** a vehicle with `access_type: "OWNER"` in the response carries `AccessType` equal
  to `"OWNER"` in the returned DTO
- **AND** a vehicle with `access_type: "DRIVER"` in the response carries `AccessType` equal
  to `"DRIVER"` in the returned DTO

#### Scenario: Access type is empty when Tesla omits it
- **GIVEN** valid credentials for a Tesla account
- **AND** the Fleet API response omits the `access_type` field on a vehicle
- **WHEN** a caller requests the vehicle inventory
- **THEN** that vehicle's `AccessType` is the empty string
- **AND** the caller is not required to handle a nil or error for the missing field
