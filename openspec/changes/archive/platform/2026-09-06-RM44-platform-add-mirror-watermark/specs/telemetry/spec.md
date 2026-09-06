# telemetry Specification Delta

## ADDED Requirements

### Requirement: Supercharger History Account-Wide Updated-Since Read Port

The telemetry capability SHALL expose a read port through which other
modules can retrieve every stored Supercharger session for a single account
whose `updated_at` is at or after a caller-supplied instant, without
accessing the telemetry module's database tables directly and without
identifying any particular vehicle. The port SHALL return the existing
`SuperchargerHistory` domain type, ordered oldest-first by `updated_at`.
Callers SHALL receive an empty result (not an error) when nothing in the
account has been updated at or after the given instant.

Unlike the per-vehicle updated-since port, this port SHALL include a session
whose registered vehicle identifier is absent — a session for a vehicle that
is not currently registered to the account. This is a deliberate difference,
not an omission: a per-vehicle read can never surface such a session, so a
caller that needs to recover a session once its vehicle re-registers SHALL
use this account-wide port instead.

#### Scenario: Sessions for every vehicle in the account are included
- **GIVEN** an account with two vehicles, each holding one Supercharger
  session updated at or after a given instant
- **WHEN** the account's sessions are retrieved for modifications at or
  after that instant
- **THEN** both sessions are included in the result

#### Scenario: A session whose vehicle is not currently registered is included
- **GIVEN** a Supercharger session whose vehicle identification number does
  not belong to any vehicle currently registered to the account, last
  modified at or after a given instant
- **WHEN** the account's sessions are retrieved for modifications at or
  after that instant
- **THEN** the session is included in the result

#### Scenario: A different account's session does not leak
- **GIVEN** two accounts, each holding a Supercharger session last modified
  at or after a given instant
- **WHEN** one account's sessions are retrieved for modifications at or
  after that instant
- **THEN** only that account's session is included

#### Scenario: Results are ordered earliest-updated-first
- **GIVEN** an account with multiple Supercharger sessions modified at or
  after a given instant, with different last-modified instants
- **WHEN** the account's sessions are retrieved for modifications at or
  after that instant
- **THEN** the records are ordered by their last-modified instant, earliest
  first

#### Scenario: A session modified at exactly the requested instant is included
- **GIVEN** a Supercharger session last modified at a given instant
- **WHEN** the account's sessions are retrieved for modifications at or
  after that exact instant
- **THEN** the session is included in the result

#### Scenario: Empty result when nothing has been updated in the window
- **GIVEN** an account with no Supercharger session updated at or after the
  requested instant
- **WHEN** the account's sessions are retrieved for modifications at or
  after that instant
- **THEN** an empty collection is returned, and no error is returned

#### Scenario: Callers never access the telemetry database directly for this port either
- **GIVEN** any caller that needs to detect which of an account's
  Supercharger sessions changed recently, across every vehicle
- **WHEN** it obtains that data
- **THEN** it does so exclusively through this read port
- **AND** it imports no package from `internal/telemetry/db`
