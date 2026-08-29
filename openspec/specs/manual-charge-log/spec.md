# manual-charge-log Specification

## Purpose
TBD - created by archiving change RM3-manualcharge-add-entries. Update Purpose after archive.
## Requirements
### Requirement: Create a manual charge entry
The manual-charge-log capability SHALL let an authenticated user persist a user-asserted
charge entry for one of their own registered vehicles. The entry SHALL require `account_id`,
`tesla_id`, `vin`, `charged_on`, `energy_added_kwh`, `price`, `currency`, and `location_kind`,
and SHALL accept the optional fields `started_at`, `ended_at`, `start_battery_pct`,
`end_battery_pct`, `charging_type`, `location_label`, and `notes`. On success it SHALL return the
stored entry with a server-assigned `id`, `created_at`, and `updated_at`. It SHALL reject a
non-positive `energy_added_kwh`, a negative `price`, a missing required field, or a missing or
empty `location_kind`. When `currency` is not supplied the stored value SHALL default to `'COP'`.
`location_kind` SHALL be one of `HOME`, `WORK`, or `OTHER`; any other value SHALL be rejected.

#### Scenario: Persist an entry with all required fields
- **GIVEN** an authenticated user with at least one registered vehicle
- **WHEN** they submit a create request with `account_id` (their own account), `tesla_id` and
  `vin` (one of their registered vehicles), `charged_on` (a valid calendar date),
  `energy_added_kwh` (a positive decimal, e.g. `15.50`), `price` (a non-negative decimal, e.g.
  `8000.00`), `currency` (e.g. `'COP'`), and `location_kind` (e.g. `'HOME'`)
- **THEN** the system persists the entry and returns it with a server-assigned `id`,
  `created_at`, and `updated_at`

#### Scenario: Reject a non-positive energy value
- **GIVEN** an authenticated user
- **WHEN** they submit a create request with `energy_added_kwh = 0` or a negative value
- **THEN** the system rejects the request with a validation error (CHECK: `energy_added_kwh > 0`)

#### Scenario: Reject a negative price
- **GIVEN** an authenticated user
- **WHEN** they submit a create request with `price < 0`
- **THEN** the system rejects the request with a validation error (CHECK: `price >= 0`)

#### Scenario: Reject a missing required field
- **GIVEN** an authenticated user
- **WHEN** they submit a create request omitting any required field (`charged_on`,
  `energy_added_kwh`, `price`, `currency`, `location_kind`)
- **THEN** the system rejects the request with a validation error indicating the missing field

#### Scenario: Reject a missing location_kind on create
- **GIVEN** an authenticated user
- **WHEN** they submit a create request without supplying `location_kind` (nil or empty string)
- **THEN** the system rejects the request with a validation error before reaching the database

#### Scenario: Reject an invalid location_kind value on create
- **GIVEN** an authenticated user
- **WHEN** they submit a create request with `location_kind = 'STREET'` or any value not in
  `('HOME', 'WORK', 'OTHER')`
- **THEN** the system rejects the request (CHECK: `location_kind IN ('HOME','WORK','OTHER')`)

#### Scenario: Accept a valid HOME location_kind on create
- **GIVEN** an authenticated user
- **WHEN** they submit a create request with `location_kind = 'HOME'`
- **THEN** the system persists the entry with `location_kind = 'HOME'` and returns it

#### Scenario: Accept a valid WORK location_kind on create
- **GIVEN** an authenticated user
- **WHEN** they submit a create request with `location_kind = 'WORK'`
- **THEN** the system persists the entry with `location_kind = 'WORK'` and returns it

#### Scenario: Accept a valid OTHER location_kind on create
- **GIVEN** an authenticated user
- **WHEN** they submit a create request with `location_kind = 'OTHER'`
- **THEN** the system persists the entry with `location_kind = 'OTHER'` and returns it

#### Scenario: Currency defaults to COP when omitted
- **GIVEN** an authenticated user who submits a create request without supplying `currency`
  (but with a valid `location_kind`)
- **WHEN** the entry is persisted
- **THEN** the `currency` field defaults to `'COP'` (the Postgres DEFAULT on the column)

#### Scenario: Optional fields omitted are stored as NULL
- **GIVEN** an authenticated user
- **WHEN** they submit a create request that includes all required fields (including
  `location_kind`) but omits all optional fields
- **THEN** the system persists the entry with every optional field (`started_at`, `ended_at`,
  `start_battery_pct`, `end_battery_pct`, `charging_type`, `location_label`, `notes`) stored as
  NULL, and returns the full entry

#### Scenario: All optional fields supplied are persisted
- **GIVEN** an authenticated user
- **WHEN** they submit a create request that includes all optional fields with valid values
  (`started_at`/`ended_at` where `ended_at >= started_at`, `start_battery_pct = 20`,
  `end_battery_pct = 80`, `charging_type = 'AC'`, `location_kind = 'HOME'`,
  `location_label = 'Garage'`, `notes = 'Overnight charge'`)
- **THEN** the system persists all optional fields and returns them in the response

### Requirement: Validate optional field constraints
The manual-charge-log capability SHALL enforce the optional-field constraints at persistence
time: `start_battery_pct` and `end_battery_pct` SHALL be between 0 and 100, `charging_type`
SHALL be one of `AC` or `DC`, `location_kind` SHALL be one of `HOME`, `WORK`, or `OTHER` and
SHALL be required (not null, not empty), and when both `started_at` and `ended_at` are present
`ended_at` SHALL NOT precede `started_at`. Either timing field SHALL be permitted to be NULL
independently.

#### Scenario: Reject an out-of-range start battery percentage
- **GIVEN** an authenticated user
- **WHEN** they submit a create request with `start_battery_pct = -1` or `start_battery_pct = 101`
- **THEN** the system rejects the request (CHECK: `start_battery_pct BETWEEN 0 AND 100`)

#### Scenario: Reject an out-of-range end battery percentage
- **GIVEN** an authenticated user
- **WHEN** they submit a create request with `end_battery_pct = -1` or `end_battery_pct = 101`
- **THEN** the system rejects the request (CHECK: `end_battery_pct BETWEEN 0 AND 100`)

#### Scenario: Reject an invalid charging type
- **GIVEN** an authenticated user
- **WHEN** they submit a create request with `charging_type = 'HYBRID'`
- **THEN** the system rejects the request (CHECK: `charging_type IN ('AC','DC')`)

#### Scenario: Reject an invalid location kind
- **GIVEN** an authenticated user
- **WHEN** they submit a create request with `location_kind = 'STREET'`
- **THEN** the system rejects the request (CHECK: `location_kind IN ('HOME','WORK','OTHER')`)

#### Scenario: Reject a missing location kind
- **GIVEN** an authenticated user
- **WHEN** they submit a create request with `location_kind` absent (nil or empty string)
- **THEN** the system rejects the request with a service-layer validation error
  (before the database is reached)

#### Scenario: Reject a reversed session window
- **GIVEN** an authenticated user
- **WHEN** they supply both `started_at` and `ended_at` where `ended_at < started_at`
- **THEN** the system rejects the request (CHECK: `ended_at >= started_at` when both present)

#### Scenario: Accept a one-sided session window
- **GIVEN** an authenticated user
- **WHEN** they supply `started_at` but omit `ended_at` (or vice versa)
- **THEN** the system accepts the request — the timing CHECK allows one field to be NULL

### Requirement: Edit an existing entry
The manual-charge-log capability SHALL let a user correct an existing entry they own, applying
the supplied changes, advancing `updated_at`, and leaving `created_at` unchanged. Update SHALL
enforce the same field constraints as create — including that `location_kind` is required
(non-nil, non-empty, one of `HOME`/`WORK`/`OTHER`) — and SHALL NOT permit mutating an entry
belonging to a different account.

#### Scenario: Update supplied fields and advance updated_at
- **GIVEN** an authenticated user with an existing entry `id = X` belonging to `account_id = A`
- **WHEN** they submit an update for entry `X` with a corrected `price` and `notes` and a valid
  `location_kind`
- **THEN** the system updates the supplied fields, advances `updated_at` to now, leaves
  `created_at` unchanged, and returns the full updated entry

#### Scenario: Update is rejected when it violates a field constraint
- **GIVEN** an authenticated user
- **WHEN** they submit an update for an entry with `energy_added_kwh = 0`
- **THEN** the system rejects the update (CHECK: `energy_added_kwh > 0`)

#### Scenario: Reject a missing location_kind on update
- **GIVEN** an authenticated user with an existing entry `id = X`
- **WHEN** they submit an update that omits `location_kind` (nil or empty string)
- **THEN** the system rejects the update with a service-layer validation error before reaching
  the database

#### Scenario: Accept a location_kind change on update
- **GIVEN** an authenticated user with an existing entry where `location_kind = 'HOME'`
- **WHEN** they submit an update with `location_kind = 'WORK'`
- **THEN** the system persists `location_kind = 'WORK'` and returns the updated entry

#### Scenario: Update cannot cross tenant boundaries
- **GIVEN** an authenticated user in account `A`
- **WHEN** they submit an update for an entry that belongs to a different account
- **THEN** the system mutates nothing (zero rows / "not found") — no cross-tenant mutation is possible

### Requirement: Delete an entry
The manual-charge-log capability SHALL let a user delete an entry they own, scoping every
delete to the caller's `account_id` so that a valid entry `id` from another tenant deletes
nothing and does not leak the entry's existence.

#### Scenario: Delete an owned entry
- **GIVEN** an authenticated user with an existing entry `id = X` in `account_id = A`
- **WHEN** they submit a delete for entry `X` scoped to account `A`
- **THEN** the system removes the entry and subsequent reads for that `id` return not found

#### Scenario: Cross-tenant delete removes nothing
- **GIVEN** an authenticated user from account `A`
- **WHEN** they submit a delete for entry `id = Y` that belongs to account `B`
- **THEN** the system deletes nothing (WHERE scopes to `account_id = A`) and the caller receives
  zero rows affected, not an error that leaks entry existence

### Requirement: List entries by vehicle
The manual-charge-log capability SHALL return a vehicle's entries for a given account, ordered
by `charged_on` descending (newest day first), bounded by a caller-supplied `limit` (0 = server
default). It SHALL return a non-nil empty slice when the vehicle has no entries, and SHALL
exclude entries belonging to other vehicles or other accounts.

#### Scenario: Return a vehicle's entries newest-first
- **GIVEN** an authenticated user with account `A` and vehicle `tesla_id = V`
- **WHEN** they request `ListEntriesByVehicle(accountID=A, teslaID=V, limit=10)`
- **THEN** the system returns up to 10 entries that belong to account `A` and vehicle `V`,
  ordered by `charged_on DESC`
- **AND** when the vehicle has no entries a non-nil empty slice is returned

#### Scenario: Exclude other vehicles' entries
- **GIVEN** an account with entries for two vehicles (`V1` and `V2`)
- **WHEN** they request `ListEntriesByVehicle(accountID=A, teslaID=V1, limit=100)`
- **THEN** only entries for vehicle `V1` are returned; entries for `V2` are excluded

#### Scenario: Respect the limit
- **GIVEN** an account with more than `limit` entries for one vehicle
- **WHEN** they request with `limit = 5`
- **THEN** exactly 5 entries are returned, ordered newest `charged_on` first

### Requirement: List entries by account
The manual-charge-log capability SHALL return all of an account's entries across every vehicle,
ordered by `charged_on` descending, bounded by a caller-supplied `limit` (0 = server default),
returning a non-nil empty slice when the account has no entries.

#### Scenario: Return account-wide entries newest-first
- **GIVEN** an authenticated user with account `A` and multiple vehicles with entries
- **WHEN** they request `ListEntriesByAccount(accountID=A, limit=20)`
- **THEN** the system returns up to 20 entries for all of account `A`'s vehicles, ordered by
  `charged_on DESC` across all vehicles
- **AND** when the account has no entries a non-nil empty slice is returned

### Requirement: Multi-tenant isolation
The manual-charge-log capability SHALL scope every read and write by `account_id` so that no
account can ever see or affect another account's entries.

#### Scenario: Account-wide list never leaks another tenant
- **GIVEN** two accounts `A` and `B`, each with entries
- **WHEN** account `A` calls `ListEntriesByAccount(accountID=A, limit=100)`
- **THEN** only account `A`'s entries are returned; account `B`'s entries are never visible

#### Scenario: Per-vehicle list is account-scoped
- **GIVEN** account `A` creates an entry for vehicle `V`
- **WHEN** account `B` requests `ListEntriesByVehicle(accountID=B, teslaID=V, limit=100)`
- **THEN** account `B` sees zero entries (the query filters by `account_id = B`)

### Requirement: Derived read-time values
The manual-charge-log capability SHALL expose derived values as read-time methods on the domain
entry — cost per kWh, battery delta, and session duration — computed from stored columns and
never persisted. Each derived value SHALL be nil-safe, returning nil when its inputs are absent.

#### Scenario: Cost per kWh is computed, not stored
- **GIVEN** an entry with `price = 8000.00` and `energy_added_kwh = 15.50`
- **WHEN** the caller invokes `entry.CostPerKWh()`
- **THEN** the method returns a pointer to `price / energy_added_kwh` (≈ `516.13`)
- **AND** the stored `price` and `energy_added_kwh` columns are unchanged

#### Scenario: Battery delta is nil-safe
- **GIVEN** an entry with `start_battery_pct = 20` and `end_battery_pct = 80`
- **WHEN** the caller invokes `entry.BatteryDelta()`
- **THEN** the method returns a pointer to `60` (end − start)
- **AND** when either battery field is nil the method returns nil

#### Scenario: Session duration is nil-safe
- **GIVEN** an entry with `started_at = T1` and `ended_at = T2` where `T2 > T1`
- **WHEN** the caller invokes `entry.SessionDuration()`
- **THEN** the method returns a pointer to the duration `T2 − T1`
- **AND** when either timing field is nil the method returns nil

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

### Requirement: Inferred pack capacity is recorded on every entry

The manual-charge-log capability SHALL record, for each charge entry, the vehicle pack
capacity in kilowatt-hours implied by that entry alone — the energy added divided by the
fraction of the pack that the entry's battery percentages say was replenished.

The capability SHALL keep this recorded value correct at all times: it SHALL be present as
soon as an entry is created, SHALL be brought up to date whenever any of the three values
it derives from is changed, and SHALL be present for every entry that already existed
before this capability gained the value — with no separate action required of any caller.

The recorded value SHALL be derived, never supplied. A caller SHALL NOT be able to set,
override, or corrupt it, and an attempt to supply one SHALL NOT alter what is recorded.

The capability SHALL record the value **only** when the entry carries all three of the
values it derives from — the energy added, the starting battery percentage, and the ending
battery percentage — **and** the ending battery percentage is strictly greater than the
starting one. In every other case the capability SHALL record the absence of a value.
Recording the absence of a value SHALL NOT be an error and SHALL NOT prevent the entry
itself from being created or changed.

This is a separate value from the capability's existing derived read-time values (cost per
kWh, battery delta, session duration), which remain computed on read and unrecorded.

#### Scenario: A complete entry records its inferred capacity
- **GIVEN** a charge entry with an energy added of 7.04 kWh, a starting battery percentage
  of 64, and an ending battery percentage of 74
- **WHEN** the entry is created
- **THEN** the entry's recorded inferred pack capacity is 70.400 kWh

#### Scenario: An entry with a wide battery delta records its inferred capacity
- **GIVEN** a charge entry with an energy added of 41.31 kWh, a starting battery percentage
  of 18, and an ending battery percentage of 80
- **WHEN** the entry is created
- **THEN** the entry's recorded inferred pack capacity is 66.629 kWh

#### Scenario: An entry with no starting battery percentage records no capacity
- **GIVEN** a charge entry with an energy added and an ending battery percentage, but no
  starting battery percentage
- **WHEN** the entry is created
- **THEN** the entry is created successfully
- **AND** the entry records no inferred pack capacity

#### Scenario: An entry with no ending battery percentage records no capacity
- **GIVEN** a charge entry with an energy added and a starting battery percentage, but no
  ending battery percentage
- **WHEN** the entry is created
- **THEN** the entry is created successfully
- **AND** the entry records no inferred pack capacity

#### Scenario: An entry whose battery percentage did not change records no capacity
- **GIVEN** a charge entry whose starting and ending battery percentages are equal
- **WHEN** the entry is created
- **THEN** the entry is created successfully, rather than being rejected
- **AND** the entry records no inferred pack capacity

#### Scenario: An entry whose battery percentage decreased records no capacity
- **GIVEN** a charge entry whose ending battery percentage is lower than its starting
  battery percentage
- **WHEN** the entry is created
- **THEN** the entry is created successfully, rather than being rejected
- **AND** the entry records no inferred pack capacity
- **AND** no negative capacity is recorded

#### Scenario: Editing a battery percentage updates the recorded capacity
- **GIVEN** an existing entry with an energy added of 7.04 kWh, a starting battery
  percentage of 64, and an ending battery percentage of 74, recording an inferred pack
  capacity of 70.400 kWh
- **WHEN** the entry is edited to change its ending battery percentage to 84
- **THEN** the entry's recorded inferred pack capacity becomes 35.200 kWh
- **AND** the caller did not have to supply, request, or recompute that value

#### Scenario: Editing an entry so its delta becomes zero clears the recorded capacity
- **GIVEN** an existing entry that records an inferred pack capacity
- **WHEN** the entry is edited so that its ending battery percentage equals its starting
  battery percentage
- **THEN** the edit succeeds
- **AND** the entry records no inferred pack capacity

#### Scenario: A caller cannot supply the inferred capacity
- **GIVEN** a caller creating or editing a charge entry
- **WHEN** the caller supplies a value for the entry's inferred pack capacity
- **THEN** the supplied value is not recorded
- **AND** the value the capability records remains the one derived from the entry's energy
  added and battery percentages

#### Scenario: Entries that existed before the value was introduced carry it
- **GIVEN** charge entries that were recorded before the capability recorded inferred pack
  capacity, some carrying all three derivation values with an increasing battery percentage
  and some not
- **WHEN** the capability begins recording inferred pack capacity
- **THEN** every such entry carrying all three values with an increasing battery percentage
  records its inferred pack capacity
- **AND** every other such entry records no inferred pack capacity
- **AND** no caller had to request, trigger, or perform this

#### Scenario: The recorded capacity is returned with the entry
- **GIVEN** an entry recording an inferred pack capacity
- **WHEN** that entry is retrieved by any of the capability's retrieval operations
- **THEN** the retrieved entry carries its recorded inferred pack capacity
- **AND** an entry recording no inferred pack capacity is retrieved carrying its absence,
  not a zero

