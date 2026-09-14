## ADDED Requirements

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
