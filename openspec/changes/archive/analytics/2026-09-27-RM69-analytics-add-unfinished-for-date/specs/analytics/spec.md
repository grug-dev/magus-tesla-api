## ADDED Requirements

### Requirement: Unfinished Vehicle Detection For A Date

The analytics capability SHALL expose a read that, given a set of vehicle
identifiers and a calendar day, returns exactly the identifiers from that
set for which no precomputed daily metrics row exists for that day. This
read SHALL require no proof that a requesting account owns any of the given
vehicles: it serves a periodic job that considers every registered vehicle
across every account, not one signed-in user's request.

A vehicle identifier with no precomputed daily metrics row for the given day
SHALL be reported as not finished, regardless of whether that identifier is
currently registered to any account. A vehicle identifier repeated in the
input set SHALL appear at most once in the result. An empty input set SHALL
yield an empty result and no error.

**Reason**: MAG-98 / `RM69-nightly-retry-unfinished-vehicles` tier 3. A
later tier's retry schedule (`internal/app`) asks this question every 30
minutes for every registered vehicle; it has no signed-in user and no single
account to scope the check to.

#### Scenario: Some vehicles have a row for the date, some do not

- **GIVEN** three vehicles, where the first has a precomputed daily metrics
  row for a given calendar day, the second has a row only for the day
  before, and the third has no row at all
- **WHEN** this read is asked about all three vehicles for that calendar day
- **THEN** the result contains exactly the second and third vehicles
- **AND** the first vehicle is absent from the result

#### Scenario: An unregistered identifier is reported as not finished

- **GIVEN** a vehicle identifier that has never had a precomputed daily
  metrics row of any kind
- **WHEN** this read is asked about that identifier for any calendar day
- **THEN** the identifier is included in the result

#### Scenario: A repeated identifier appears once in the result

- **GIVEN** one vehicle identifier with no precomputed daily metrics row for
  a given calendar day
- **WHEN** this read is asked about a set containing that identifier more
  than once
- **THEN** the result contains that identifier exactly once

#### Scenario: An empty input set yields an empty result

- **GIVEN** an empty set of vehicle identifiers
- **WHEN** this read is asked about that set for any calendar day
- **THEN** the result is empty
- **AND** no error is returned

#### Scenario: Every requested vehicle already has a row

- **GIVEN** a set of vehicle identifiers that each have a precomputed daily
  metrics row for a given calendar day
- **WHEN** this read is asked about that set for that calendar day
- **THEN** the result is empty
- **AND** no error is returned
