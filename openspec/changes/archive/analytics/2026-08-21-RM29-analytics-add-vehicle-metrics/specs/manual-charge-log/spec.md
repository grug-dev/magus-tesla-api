## ADDED Requirements

### Requirement: List entries by vehicle updated since a given instant

The manual-charge-log capability SHALL return a vehicle's entries for a given account whose
`updated_at` is at or after a caller-supplied instant, without a `limit` parameter — the instant
itself bounds the result. It SHALL return a non-nil empty slice when no entry for that vehicle
has been updated at or after the given instant, and SHALL exclude entries belonging to other
vehicles or other accounts.

#### Scenario: An entry edited after the given instant is included
- **GIVEN** an authenticated user with account `A` and vehicle `tesla_id = V`, and an entry whose
  `updated_at` is after a given instant `since` (created earlier, then edited)
- **WHEN** they request `ListEntriesByVehicleUpdatedSince(accountID=A, teslaID=V, since=since)`
- **THEN** that entry is included in the result

#### Scenario: An entry untouched since before the given instant is excluded
- **GIVEN** an authenticated user with account `A` and vehicle `tesla_id = V`, and an entry whose
  `updated_at` is before a given instant `since`, never edited since
- **WHEN** they request `ListEntriesByVehicleUpdatedSince(accountID=A, teslaID=V, since=since)`
- **THEN** that entry is not included in the result

#### Scenario: Empty result when nothing has been updated in the window
- **GIVEN** a vehicle with no entry updated at or after the requested instant
- **WHEN** they request `ListEntriesByVehicleUpdatedSince` for that vehicle and instant
- **THEN** a non-nil empty slice is returned, and no error is returned

#### Scenario: Per-account and per-vehicle scoping is preserved
- **GIVEN** two accounts that each own vehicles with entries updated at or after the requested
  instant
- **WHEN** the caller requests entries updated since that instant for one account and one vehicle
- **THEN** only entries belonging to that account AND that vehicle are returned
