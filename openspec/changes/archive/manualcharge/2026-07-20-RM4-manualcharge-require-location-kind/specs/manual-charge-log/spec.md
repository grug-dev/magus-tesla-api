## MODIFIED Requirements

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
