## ADDED Requirements

### Requirement: List entries by vehicle within a date range

The manual-charge-log capability SHALL return a vehicle's entries for a given account
whose `charged_on` falls within a caller-supplied `[from, to]` date range, inclusive of
both bounds, ordered by `charged_on` descending (newest day first). It SHALL return a
non-nil empty slice when no entries fall within the range, and SHALL exclude entries
belonging to other vehicles or other accounts even when their `charged_on` falls inside
the same range. This method SHALL NOT accept a `limit` parameter — the date range itself
bounds the result.

#### Scenario: An entry exactly on the start bound is included
- **GIVEN** an authenticated user with account `A` and vehicle `tesla_id = V`, and an
  entry with `charged_on` equal to a date `from`
- **WHEN** they request `ListEntriesByVehicleBetween(accountID=A, teslaID=V, from=from, to=to)`
  where `to` is on or after `from`
- **THEN** the entry dated exactly `from` is included in the result

#### Scenario: An entry exactly on the end bound is included
- **GIVEN** an authenticated user with account `A` and vehicle `tesla_id = V`, and an
  entry with `charged_on` equal to a date `to`
- **WHEN** they request `ListEntriesByVehicleBetween(accountID=A, teslaID=V, from=from, to=to)`
  where `from` is on or before `to`
- **THEN** the entry dated exactly `to` is included in the result

#### Scenario: An entry one day before the start bound is excluded
- **GIVEN** an authenticated user with account `A` and vehicle `tesla_id = V`, and an
  entry with `charged_on` equal to the day immediately before `from`
- **WHEN** they request `ListEntriesByVehicleBetween(accountID=A, teslaID=V, from=from, to=to)`
- **THEN** that entry is not included in the result

#### Scenario: An entry one day after the end bound is excluded
- **GIVEN** an authenticated user with account `A` and vehicle `tesla_id = V`, and an
  entry with `charged_on` equal to the day immediately after `to`
- **WHEN** they request `ListEntriesByVehicleBetween(accountID=A, teslaID=V, from=from, to=to)`
- **THEN** that entry is not included in the result

#### Scenario: Results within the range are returned newest-first
- **GIVEN** an authenticated user with account `A` and vehicle `tesla_id = V`, and
  multiple entries whose `charged_on` values fall within `[from, to]`
- **WHEN** they request `ListEntriesByVehicleBetween(accountID=A, teslaID=V, from=from, to=to)`
- **THEN** the returned entries are ordered by `charged_on` descending (newest day
  first)

#### Scenario: Empty range returns a non-nil empty slice
- **GIVEN** an authenticated user with account `A` and vehicle `tesla_id = V` that has no
  entries with `charged_on` inside `[from, to]`
- **WHEN** they request `ListEntriesByVehicleBetween(accountID=A, teslaID=V, from=from, to=to)`
- **THEN** the system returns a non-nil empty slice and no error

#### Scenario: Vehicle isolation within the range
- **GIVEN** account `A` with two vehicles (`V1` and `V2`), each holding an entry whose
  `charged_on` falls inside the same `[from, to]` range
- **WHEN** they request `ListEntriesByVehicleBetween(accountID=A, teslaID=V1, from=from, to=to)`
- **THEN** only the entry belonging to vehicle `V1` is returned; the entry belonging to
  `V2` is excluded even though its `charged_on` is within range

#### Scenario: Multi-tenant isolation within the range
- **GIVEN** two accounts `A` and `B`, each holding an entry for the same `tesla_id = V`
  value with a `charged_on` inside the same `[from, to]` range
- **WHEN** account `A` requests `ListEntriesByVehicleBetween(accountID=A, teslaID=V, from=from, to=to)`
- **THEN** only account `A`'s entry is returned; account `B`'s entry is excluded even
  though it shares the same `tesla_id` and falls within the same range
