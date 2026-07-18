## ADDED Requirements

### Requirement: Create a manual charge entry
The manual-charge-log capability SHALL let an authenticated user persist a user-asserted
charge entry for one of their own registered vehicles. The entry SHALL require `account_id`,
`tesla_id`, `vin`, `charged_on`, `energy_added_kwh`, `price`, and `currency`, and SHALL accept
the optional fields `started_at`, `ended_at`, `start_battery_pct`, `end_battery_pct`,
`charging_type`, `location_kind`, `location_label`, and `notes`. On success it SHALL return the
stored entry with a server-assigned `id`, `created_at`, and `updated_at`. It SHALL reject a
non-positive `energy_added_kwh`, a negative `price`, or a missing required field. When
`currency` is not supplied the stored value SHALL default to `'COP'`.

#### Scenario: Persist an entry with all required fields
- **GIVEN** an authenticated user with at least one registered vehicle
- **WHEN** they submit a create request with `account_id` (their own account), `tesla_id` and
  `vin` (one of their registered vehicles), `charged_on` (a valid calendar date),
  `energy_added_kwh` (a positive decimal, e.g. `15.50`), `price` (a non-negative decimal, e.g.
  `8000.00`), and `currency` (e.g. `'COP'`)
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
  `energy_added_kwh`, `price`, `currency`)
- **THEN** the system rejects the request with a validation error indicating the missing field

#### Scenario: Currency defaults to COP when omitted
- **GIVEN** an authenticated user who submits a create request without supplying `currency`
- **WHEN** the entry is persisted
- **THEN** the `currency` field defaults to `'COP'` (the Postgres DEFAULT on the column)

#### Scenario: Optional fields omitted are stored as NULL
- **GIVEN** an authenticated user
- **WHEN** they submit a create request that includes only the required fields
- **THEN** the system persists the entry with every optional field (`started_at`, `ended_at`,
  `start_battery_pct`, `end_battery_pct`, `charging_type`, `location_kind`, `location_label`,
  `notes`) stored as NULL, and returns the full entry

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
SHALL be one of `AC` or `DC`, `location_kind` SHALL be one of `HOME`, `WORK`, or `OTHER`, and
when both `started_at` and `ended_at` are present `ended_at` SHALL NOT precede `started_at`.
Either timing field SHALL be permitted to be NULL independently.

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
enforce the same field constraints as create, and SHALL NOT permit mutating an entry belonging
to a different account.

#### Scenario: Update supplied fields and advance updated_at
- **GIVEN** an authenticated user with an existing entry `id = X` belonging to `account_id = A`
- **WHEN** they submit an update for entry `X` with a corrected `price` and `notes`
- **THEN** the system updates the supplied fields, advances `updated_at` to now, leaves
  `created_at` unchanged, and returns the full updated entry

#### Scenario: Update is rejected when it violates a field constraint
- **GIVEN** an authenticated user
- **WHEN** they submit an update for an entry with `energy_added_kwh = 0`
- **THEN** the system rejects the update (CHECK: `energy_added_kwh > 0`)

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
