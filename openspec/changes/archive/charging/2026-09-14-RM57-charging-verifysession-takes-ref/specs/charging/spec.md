## MODIFIED Requirements

### Requirement: Supercharger Port Vehicle Scoping

Every public port the charging capability exposes over its Supercharger session store SHALL
identify its scope by the vehicle's Tesla numeric identifier alone, and SHALL NOT take an
account identifier. This covers the mirror write, the three session reads, and the
battery-percentage verification write.

The verification write SHALL keep a scope rather than lose one: it SHALL match a session
by BOTH its own identifier and the caller-supplied vehicle identifier, so a caller that
names a vehicle the session does not belong to changes nothing. A verification write
naming a mismatched vehicle SHALL be reported to the caller exactly as an unknown session
identifier is, so that "not yours" and "does not exist" are indistinguishable from outside.

The verification write's vehicle identifier SHALL be a value that can only exist once the
caller has already proven it owns that vehicle — never a bare identifier a caller could
supply without that proof. This is stricter than the scoping rule above asks of the mirror
write and the three session reads, which still accept a bare vehicle identifier: the
verification write is the only Supercharger port that mutates a human-entered record, so it
is the one port this capability requires proof, rather than an unverified claim, for.

The mirror write SHALL NOT validate an owning account across the sessions it is given,
because no session carries one; an empty set of sessions SHALL remain a successful no-op.

#### Scenario: A verification write for the wrong vehicle changes nothing

- **GIVEN** a stored session belonging to one vehicle, with no battery percentages recorded
- **WHEN** a caller asks to verify that session's percentages while naming a different
  vehicle
- **THEN** an error indistinguishable from "no such session" is returned
- **AND** the stored session's battery percentages and lifecycle status are unchanged

#### Scenario: A verification write for the right vehicle succeeds

- **GIVEN** the same stored session
- **WHEN** a caller asks to verify its percentages while naming the vehicle the session
  belongs to
- **THEN** the percentages are stored
- **AND** the returned session carries the vehicle's identifier as a plain value, never an
  absent one

#### Scenario: A verification write cannot be issued without proof of ownership

- **GIVEN** a caller that has not established which vehicles it may act on behalf of
- **WHEN** it attempts to issue a verification write
- **THEN** it has no way to construct the vehicle identifier the write requires
- **AND** the attempt does not compile, rather than reaching the store and failing there

#### Scenario: Callers never reach the Supercharger tables directly

- **GIVEN** any caller that needs to read or write a Supercharger session
- **WHEN** it obtains or changes that data
- **THEN** it does so exclusively through this capability's public ports
- **AND** it imports no package from `internal/charging/db`
