# vehicleref Specification

## Purpose
TBD - created by archiving change platform-add-vehicle-authorization-seam. Update Purpose after archive.
## Requirements
### Requirement: Vehicle Ownership Proof
The platform SHALL provide a way to prove, from a caller's own list of owned vehicle
identities and one requested vehicle identity, whether the caller may act on that vehicle. The
proof SHALL take the form of a value that cannot be constructed for a vehicle identity absent
from the caller's owned list.

#### Scenario: A requested vehicle in the owned list produces a proof
- **GIVEN** a caller's list of owned vehicle identities
- **AND** a requested vehicle identity that appears in that list
- **WHEN** the platform is asked to authorize that vehicle
- **THEN** it returns a proof value
- **AND** the proof identifies the same vehicle identity that was requested

#### Scenario: A requested vehicle absent from the owned list produces no proof
- **GIVEN** a caller's list of owned vehicle identities
- **AND** a requested vehicle identity that does NOT appear in that list
- **WHEN** the platform is asked to authorize that vehicle
- **THEN** no proof is returned
- **AND** the caller is told the authorization did not succeed

#### Scenario: An empty owned list produces no proof for any requested vehicle
- **GIVEN** a caller with no owned vehicle identities
- **WHEN** the platform is asked to authorize any vehicle identity
- **THEN** no proof is returned

### Requirement: Proof For Every Owned Vehicle
The platform SHALL provide a way to obtain a proof for every vehicle identity in a caller's own
owned list at once, without repeating the single-vehicle authorization check per vehicle, so
that a read spanning the caller's whole vehicle fleet can be expressed as one call.

#### Scenario: Every owned vehicle yields one proof each
- **GIVEN** a caller's list of owned vehicle identities
- **WHEN** the platform is asked for proofs of every owned vehicle at once
- **THEN** it returns one proof per vehicle identity in the list
- **AND** each proof identifies the vehicle identity it was built from

#### Scenario: An empty owned list yields no proofs
- **GIVEN** a caller with no owned vehicle identities
- **WHEN** the platform is asked for proofs of every owned vehicle at once
- **THEN** it returns no proofs

### Requirement: Vehicle Ownership Check Happens At The Gateway
The gateway SHALL be the only layer that checks whether a signed-in user may act on a
requested vehicle. A domain module below the gateway SHALL NOT independently re-check tenant
ownership of a vehicle identity it receives — it trusts that the gateway already proved
ownership before the call was made.

#### Scenario: A vehicle the caller does not own is rejected before any domain module is called
- **GIVEN** a signed-in user
- **AND** a vehicle identity that does not belong to that user's account
- **WHEN** the user's request names that vehicle identity
- **THEN** the gateway rejects the request
- **AND** the response does not reveal whether the vehicle identity exists at all

#### Scenario: A vehicle the caller owns is authorized once, at the gateway
- **GIVEN** a signed-in user
- **AND** a vehicle identity that belongs to that user's account
- **WHEN** the user's request names that vehicle identity
- **THEN** the gateway authorizes it
- **AND** the resulting proof is what any domain module call for that vehicle receives

### Requirement: A Fleet-Wide Read Uses The Bulk Ownership Proof

A gateway read scoped to the caller's whole vehicle fleet SHALL obtain its vehicle
identities from a bulk ownership proof, never from a list assembled beside the call. This
extends the same guarantee "Vehicle Ownership Check Happens At The Gateway" already gives
a single named vehicle to a read spanning the caller's entire fleet: the set of vehicle
identities a fleet-wide read may return SHALL be exactly the caller's own owned set,
proved once, not re-derived per read.

A fleet-wide read SHALL fail closed when the caller's owned set is empty or cannot be
determined. It SHALL NOT fall through to an unfiltered read in that case — an empty or
unknown owned set is never treated as "no filter."

#### Scenario: A caller with vehicles gets a read scoped to exactly those ids

- **GIVEN** a caller whose account owns one or more vehicles
- **WHEN** the caller triggers a fleet-wide read
- **THEN** the read is scoped to exactly the vehicle identities in the caller's bulk
  ownership proof
- **AND** no other vehicle identity is included

#### Scenario: A caller with no vehicles gets no read at all

- **GIVEN** a caller whose account owns no vehicles
- **WHEN** the caller triggers a fleet-wide read
- **THEN** no read is performed
- **AND** the caller is told nothing was found, never that every vehicle was searched

#### Scenario: A failure to determine the fleet is treated the same as owning nothing

- **GIVEN** a caller whose account's owned vehicle set cannot be determined
- **WHEN** the caller triggers a fleet-wide read
- **THEN** no read is performed
- **AND** the result is indistinguishable from a caller that owns no vehicles

