# charging Specification Delta

## ADDED Requirements

### Requirement: Supercharger Mirror Watermark Storage

The charging module SHALL own a per-account cursor table recording the
highest last-modified instant its Supercharger mirror has already
synchronized from the source it mirrors. Each account SHALL have at most
one such cursor. When no cursor exists yet for an account, the module SHALL
report this as the epoch — no error — so a caller can treat "no cursor" and
"never mirrored" identically. The cursor SHALL NOT identify a specific
vehicle or a specific source table: the mirror it bounds reads one account's
data as a whole, from exactly one upstream source.

No other module SHALL be granted access to this cursor table directly
(`ai/architecture.md` §2, "no cross-module database leaks") — every read and
write SHALL go through the module's own public port.

#### Scenario: An account with no cursor reports the epoch, not an error
- **GIVEN** an account for which the mirror has never advanced a cursor
- **WHEN** that account's cursor is read
- **THEN** the epoch (the earliest possible instant) is returned
- **AND** no error is returned

#### Scenario: A cursor advance is retained exactly
- **GIVEN** an account's cursor is advanced to a specific instant
- **WHEN** that account's cursor is read afterward
- **THEN** the exact instant it was advanced to is returned

#### Scenario: A later advance replaces an earlier one
- **GIVEN** an account's cursor already holds one instant
- **WHEN** the cursor is advanced to a later instant
- **THEN** reading the cursor afterward returns the later instant, not the
  earlier one

#### Scenario: Cursors are isolated per account
- **GIVEN** two accounts, each with the mirror having advanced their cursor
  to different instants
- **WHEN** each account's cursor is read
- **THEN** each returns only its own instant, never the other account's
