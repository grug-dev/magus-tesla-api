## ADDED Requirements

### Requirement: Charge Sessions Are Retrievable For A Vehicle Within A Time Window

The charging capability SHALL provide a way to retrieve every charge session record
belonging to a specific vehicle within an account whose stop instant falls within a
caller-specified time window, inclusive of both the window's start and end instants. The
retrieved records SHALL be ordered by their stop instant, earliest first. A retrieval that
matches no record SHALL return an empty result, never an absence or an error.

Each retrieved record SHALL carry every fact the capability holds for that session,
including the session's charging site, energy delivered, cost, currency, payment status,
and its optional verified battery percentages, their provenance, and their frozen
estimated pair — so that a caller needs no further request to another capability to
describe the session completely.

A record whose vehicle is not currently registered to the account SHALL NOT be returned by
this retrieval, for any vehicle requested, even though the record itself continues to
exist and to be retained.

#### Scenario: A session stopping exactly at the window's start is included
- **GIVEN** a charge session record for a vehicle, whose stop instant equals a given
  instant
- **WHEN** that vehicle's sessions are retrieved for a window whose start is exactly that
  instant
- **THEN** the record is included in the result

#### Scenario: A session stopping exactly at the window's end is included
- **GIVEN** a charge session record for a vehicle, whose stop instant equals a given
  instant
- **WHEN** that vehicle's sessions are retrieved for a window whose end is exactly that
  instant
- **THEN** the record is included in the result

#### Scenario: A session that starts before the window but stops within it is included
- **GIVEN** a charge session record whose start instant falls before a given window but
  whose stop instant falls within it
- **WHEN** that vehicle's sessions are retrieved for that window
- **THEN** the record is included in the result
- **AND** its start instant is reported unchanged, even though it precedes the window

#### Scenario: A session that starts within the window but stops after it is excluded
- **GIVEN** a charge session record whose start instant falls within a given window but
  whose stop instant falls after it
- **WHEN** that vehicle's sessions are retrieved for that window
- **THEN** the record is NOT included in the result

#### Scenario: No matching session returns an empty result
- **GIVEN** a vehicle with no charge session record whose stop instant falls within a
  given window
- **WHEN** that vehicle's sessions are retrieved for that window
- **THEN** the result is empty
- **AND** the retrieval succeeds, rather than failing or returning an absence

#### Scenario: Results are ordered earliest-stop-first
- **GIVEN** a vehicle with multiple charge session records whose stop instants fall within
  a given window
- **WHEN** that vehicle's sessions are retrieved for that window
- **THEN** the records are ordered by their stop instant, earliest first

#### Scenario: A retrieved record carries its full session detail
- **GIVEN** a charge session record carrying a charging site, energy delivered, cost,
  currency, payment status, and verified battery percentages with their provenance and
  frozen estimates
- **WHEN** that record is retrieved
- **THEN** the retrieved record carries all of that same detail

#### Scenario: A different vehicle's session within the same account does not leak
- **GIVEN** two charge session records within one account, belonging to two different
  vehicles, both with stop instants inside a given window
- **WHEN** one vehicle's sessions are retrieved for that window
- **THEN** only that vehicle's record is included
- **AND** the other vehicle's record is absent from the result

#### Scenario: A different account's session does not leak
- **GIVEN** two accounts, each holding a charge session record for the same vehicle
  identifier, both with stop instants inside a given window
- **WHEN** one account's sessions for that vehicle are retrieved for that window
- **THEN** only that account's record is included
- **AND** the other account's record is absent from the result

#### Scenario: A session whose vehicle is no longer currently registered is never retrieved by vehicle
- **GIVEN** a charge session record whose vehicle is no longer currently registered to the
  account
- **WHEN** any vehicle's sessions are retrieved for a window containing that record's stop
  instant
- **THEN** the record is not included in the result for any vehicle
- **AND** the record itself continues to exist and to be retained
