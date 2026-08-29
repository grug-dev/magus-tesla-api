## ADDED Requirements

### Requirement: Charge Sessions Are Retrievable For A Vehicle By Recency Of Update

The charging capability SHALL provide a way to retrieve every charge session record
belonging to a specific vehicle within an account that has been created or modified at or
after a caller-specified instant. The retrieved records SHALL be ordered by their stop
instant, earliest first. A retrieval that matches no record SHALL return an empty result,
never an absence or an error.

A correction made to a record's verified battery percentages SHALL count as a modification
for the purpose of this retrieval, even when the record's charging time window is
unchanged.

A record whose vehicle is not currently registered to the account SHALL NOT be returned by
this retrieval, for any vehicle requested, even though the record itself continues to exist
and to be retained.

#### Scenario: A record modified after being first recorded becomes visible to a later cursor
- **GIVEN** a charge session record first recorded at one instant
- **AND** its verified battery percentages later corrected at a subsequent instant
- **WHEN** that vehicle's sessions are retrieved for modifications at or after an instant
  between the two
- **THEN** the record is included in the result
- **AND** the retrieved record carries the corrected percentages

#### Scenario: A record modified at exactly the requested instant is included
- **GIVEN** a charge session record last modified at a given instant
- **WHEN** that vehicle's sessions are retrieved for modifications at or after that exact
  instant
- **THEN** the record is included in the result

#### Scenario: A record modified before the requested instant is excluded
- **GIVEN** a charge session record last modified strictly before a given instant
- **WHEN** that vehicle's sessions are retrieved for modifications at or after that instant
- **THEN** the record is NOT included in the result

#### Scenario: No matching record returns an empty result
- **GIVEN** a vehicle with no charge session record modified at or after a given instant
- **WHEN** that vehicle's sessions are retrieved for modifications at or after that instant
- **THEN** the result is empty
- **AND** the retrieval succeeds, rather than failing or returning an absence

#### Scenario: Results are ordered earliest-stop-first
- **GIVEN** a vehicle with multiple charge session records modified at or after a given
  instant, with different stop instants
- **WHEN** that vehicle's sessions are retrieved for modifications at or after that instant
- **THEN** the records are ordered by their stop instant, earliest first

#### Scenario: A different account's record does not leak
- **GIVEN** two accounts, each holding a charge session record for the same vehicle
  identifier, both modified at or after a given instant
- **WHEN** one account's sessions for that vehicle are retrieved for modifications at or
  after that instant
- **THEN** only that account's record is included

#### Scenario: A session whose vehicle is no longer currently registered is never retrieved by vehicle
- **GIVEN** a charge session record whose vehicle is no longer currently registered to the
  account, modified at or after a given instant
- **WHEN** any vehicle's sessions are retrieved for modifications at or after that instant
- **THEN** the record is not included in the result for any vehicle
- **AND** the record itself continues to exist and to be retained

### Requirement: Charge Sessions Are Retrievable For A Vehicle By Recency Of Occurrence, Bounded By Count

The charging capability SHALL provide a way to retrieve the most recent charge session
records belonging to a specific vehicle within an account, up to a caller-specified count,
ordered by their stop instant, most recent first. When the caller specifies no positive
count, the capability SHALL apply its own default count. A retrieval that matches no record
SHALL return an empty result, never an absence or an error.

A record whose vehicle is not currently registered to the account SHALL NOT be returned by
this retrieval, for any vehicle requested, even though the record itself continues to exist
and to be retained.

#### Scenario: Retrieval returns the most recent records up to the requested count, newest first
- **GIVEN** a vehicle with more charge session records than a requested count
- **WHEN** that vehicle's sessions are retrieved for that count
- **THEN** exactly that many records are returned
- **AND** they are the records with the most recent stop instants
- **AND** they are ordered by stop instant, most recent first

#### Scenario: A requested count larger than the available records returns every record, still newest first
- **GIVEN** a vehicle with fewer charge session records than a requested count
- **WHEN** that vehicle's sessions are retrieved for that count
- **THEN** every one of the vehicle's records is returned
- **AND** they are ordered by stop instant, most recent first

#### Scenario: An unspecified or non-positive count applies the capability's own default
- **GIVEN** a vehicle with charge session records
- **WHEN** that vehicle's sessions are retrieved with no positive count specified
- **THEN** the capability applies its own default count
- **AND** the returned records are ordered by stop instant, most recent first

#### Scenario: No matching record returns an empty result
- **GIVEN** a vehicle with no charge session record
- **WHEN** that vehicle's sessions are retrieved for any count
- **THEN** the result is empty
- **AND** the retrieval succeeds, rather than failing or returning an absence

#### Scenario: A different vehicle's record within the same account does not leak
- **GIVEN** two charge session records within one account, belonging to two different
  vehicles
- **WHEN** one vehicle's sessions are retrieved for a count covering both records
- **THEN** only that vehicle's record is included

#### Scenario: A different account's record does not leak
- **GIVEN** two accounts, each holding a charge session record for the same vehicle
  identifier
- **WHEN** one account's sessions for that vehicle are retrieved for a count covering both
  records
- **THEN** only that account's record is included

#### Scenario: A session whose vehicle is no longer currently registered is never retrieved by vehicle
- **GIVEN** a charge session record whose vehicle is no longer currently registered to the
  account
- **WHEN** any vehicle's sessions are retrieved for any count
- **THEN** the record is not included in the result for any vehicle
- **AND** the record itself continues to exist and to be retained
