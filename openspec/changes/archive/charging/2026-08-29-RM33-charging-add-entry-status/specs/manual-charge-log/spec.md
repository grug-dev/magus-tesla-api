## ADDED Requirements

### Requirement: A charge entry has a recorded status that governs its required fields

The manual-charge-log capability SHALL record, for every charge entry, a lifecycle status that is
either **in progress** or **done**, and SHALL make the set of fields an entry must carry a function
of that status.

An entry recorded as **in progress** SHALL be storable with only what a user can know at the moment
they plug in — it SHALL NOT require the end-of-session facts (the session end time and the ending
battery percentage). An entry recorded as **done** SHALL require those facts in addition to
everything an in-progress entry requires.

The capability SHALL expose that status-to-required-fields rule as a single declarative lookup that
is the sole source of truth for it: the capability itself SHALL enforce exactly that rule on every
create and every edit, for every caller, and any presentation layer deciding which inputs to mark
as required SHALL read the same lookup rather than restate it. Adding or removing a field from the
rule SHALL be a change in one place that both the capability and its presentation layer follow.

The capability SHALL NOT enforce the rule in the database, so that changing it later needs no
schema migration.

When a caller supplies no status at all, the capability SHALL record the entry as **in progress**.
When a caller supplies a value that is neither of the two recognized statuses, the capability SHALL
reject the request before any data is written.

The capability SHALL NOT restrict which status an entry may move to. An entry recorded as done may
be moved back to in progress.

Every entry that existed before the capability gained this status SHALL be recorded as **in
progress**, so that historical entries surface as unreviewed rather than silently asserted to be
complete.

#### Scenario: An in-progress entry is stored without the end-of-session facts
- **GIVEN** an authenticated user with a registered vehicle
- **WHEN** they log a charge as in progress, supplying the charge date, the location kind and the
  starting battery percentage, but no session end time and no ending battery percentage
- **THEN** the entry is stored
- **AND** its recorded status is in progress

#### Scenario: A done entry without an ending battery percentage is rejected
- **GIVEN** an authenticated user
- **WHEN** they submit an entry recorded as done that carries a session end time but no ending
  battery percentage
- **THEN** the request is rejected with a validation error naming the ending battery percentage
- **AND** no entry is stored

#### Scenario: A done entry without a session end time is rejected
- **GIVEN** an authenticated user
- **WHEN** they submit an entry recorded as done that carries an ending battery percentage but no
  session end time
- **THEN** the request is rejected with a validation error naming the session end time
- **AND** no entry is stored

#### Scenario: A complete done entry is accepted
- **GIVEN** an authenticated user
- **WHEN** they submit an entry recorded as done carrying the charge date, the location kind, the
  session end time and the ending battery percentage
- **THEN** the entry is stored with its recorded status done

#### Scenario: Completing an in-progress entry enforces the same rule
- **GIVEN** an existing entry recorded as in progress, with no session end time
- **WHEN** the user edits it to be recorded as done without supplying a session end time
- **THEN** the edit is rejected
- **AND** the stored entry is unchanged and still recorded as in progress

#### Scenario: A done entry may be reopened
- **GIVEN** an existing entry recorded as done
- **WHEN** the user edits it to be recorded as in progress, clearing the session end time and the
  ending battery percentage
- **THEN** the edit succeeds
- **AND** the stored entry is recorded as in progress

#### Scenario: An entry submitted with no status at all is recorded as in progress
- **GIVEN** a caller that does not supply a status
- **WHEN** the entry is stored
- **THEN** its recorded status is in progress

#### Scenario: An unrecognized status is rejected
- **GIVEN** an authenticated user
- **WHEN** they submit an entry whose status is neither in progress nor done
- **THEN** the request is rejected before any data is written

#### Scenario: Entries that pre-date the status are recorded as in progress
- **GIVEN** charge entries that were stored before the capability gained a status
- **WHEN** those entries are read
- **THEN** each one's recorded status is in progress

### Requirement: Energy added is optional, may be derived on write, and records its provenance

The manual-charge-log capability SHALL accept a charge entry that carries no energy added. It SHALL
continue to reject an energy value that is zero or negative; only its **absence** becomes
permissible.

When a caller supplies no energy added **and** the entry carries both a starting and an ending
battery percentage **and** the ending percentage is strictly greater than the starting one, the
capability SHALL derive the energy added from the vehicle's pack capacity and that battery
difference, and SHALL store the derived value at the moment the entry is written — never computing
it again on read. In every other case the capability SHALL store exactly what the caller supplied,
including nothing at all. It SHALL NOT substitute a fabricated zero for an unknown energy value.

The capability SHALL record, alongside every stored energy value, whether that value came from the
person or was derived. That provenance SHALL always be determined by the capability itself and
SHALL NOT be settable by any caller; a provenance supplied by a caller SHALL be ignored. The
provenance SHALL be re-determined on every write, so an entry whose derived value is later replaced
by a typed one records that it came from the person.

The capability SHALL obtain the pack capacity it divides by from a single named place, so that
replacing today's fixed figure with a real per-vehicle value is a change in one place.

The already-recorded inferred pack capacity of an entry SHALL be left to behave as it always has.
Where an entry has no energy added, it SHALL record no inferred pack capacity, and that SHALL NOT
be an error. Where an entry's energy added was derived, its recorded inferred pack capacity SHALL
be the pack capacity the derivation used — an arithmetic consequence, and the reason the provenance
above is recorded.

#### Scenario: An entry with no energy added is stored
- **GIVEN** an authenticated user logging a charge that has not finished
- **WHEN** they store the entry supplying no energy added, no ending battery percentage
- **THEN** the entry is stored recording no energy added
- **AND** the recorded energy provenance is "from the person"
- **AND** the entry records no inferred pack capacity

#### Scenario: Energy added is derived from the battery difference
- **GIVEN** an authenticated user who supplies no energy added, a starting battery percentage of
  50 and an ending battery percentage of 100, for a vehicle whose pack capacity is 62 kWh
- **WHEN** the entry is stored
- **THEN** the stored energy added is 31.00 kWh
- **AND** the recorded energy provenance is "derived"
- **AND** the entry's recorded inferred pack capacity is 62.000 kWh, being the capacity the
  derivation itself used

#### Scenario: A supplied energy value is never overwritten by a derivation
- **GIVEN** an authenticated user who supplies an energy added of 20.5 kWh together with a starting
  battery percentage of 50 and an ending battery percentage of 100
- **WHEN** the entry is stored
- **THEN** the stored energy added is 20.50 kWh, exactly as supplied
- **AND** the recorded energy provenance is "from the person"
- **AND** the entry's recorded inferred pack capacity is 41.000 kWh

#### Scenario: No derivation happens when the battery difference is not positive
- **GIVEN** an authenticated user who supplies no energy added and whose ending battery percentage
  is equal to, or lower than, the starting one
- **WHEN** the entry is stored
- **THEN** the entry is stored successfully rather than rejected
- **AND** it records no energy added
- **AND** the recorded energy provenance is "from the person"

#### Scenario: No derivation happens when a battery percentage is missing
- **GIVEN** an authenticated user who supplies no energy added and only one of the two battery
  percentages
- **WHEN** the entry is stored
- **THEN** the entry records no energy added

#### Scenario: A caller cannot set the energy provenance
- **GIVEN** a caller that supplies an energy added of 20.5 kWh and also asserts the provenance
  "derived"
- **WHEN** the entry is stored
- **THEN** the recorded energy provenance is "from the person"

#### Scenario: Correcting a derived value by hand changes its provenance
- **GIVEN** an existing entry whose energy added was derived
- **WHEN** the user edits it supplying an energy added of 25.0 kWh
- **THEN** the stored energy added is 25.00 kWh
- **AND** the recorded energy provenance is "from the person"

#### Scenario: A zero energy value is still rejected
- **GIVEN** an authenticated user
- **WHEN** they submit an entry with an energy added of zero or a negative number
- **THEN** the request is rejected with a validation error

### Requirement: An entry records the odometer reading taken at the charge event

The manual-charge-log capability SHALL accept an optional odometer reading, in whole kilometres,
observed at the charge event, and SHALL store it with the entry. It SHALL reject a negative
reading. When the reading is not supplied the entry SHALL record its absence, and that SHALL NOT
prevent the entry from being stored.

The reading SHALL belong to the charge event, not to the vehicle's current state — two entries for
the same vehicle carry two independent readings.

#### Scenario: An odometer reading is stored and returned
- **GIVEN** an authenticated user who supplies an odometer reading of 123456 km
- **WHEN** the entry is stored and read back
- **THEN** the entry's odometer reading is 123456 km

#### Scenario: An omitted odometer reading is recorded as absent
- **GIVEN** an authenticated user who supplies no odometer reading
- **WHEN** the entry is stored and read back
- **THEN** the entry records no odometer reading
- **AND** the entry was stored successfully

#### Scenario: A negative odometer reading is rejected
- **GIVEN** an authenticated user
- **WHEN** they submit an entry with an odometer reading below zero
- **THEN** the request is rejected with a validation error

## MODIFIED Requirements

### Requirement: Create a manual charge entry
The manual-charge-log capability SHALL let an authenticated user persist a user-asserted
charge entry for one of their own registered vehicles. The entry SHALL require `account_id`,
`tesla_id`, `vin`, `charged_on`, `price`, `currency`, and `location_kind`, together with the
additional fields that the entry's recorded `status` demands (see "A charge entry has a recorded
status that governs its required fields"), and SHALL accept the optional fields
`energy_added_kwh`, `started_at`, `ended_at`, `start_battery_pct`, `end_battery_pct`,
`charging_type`, `location_label`, `notes`, and `odometer_km`. On success it SHALL return the
stored entry with a server-assigned `id`, `created_at`, and `updated_at`. It SHALL reject a
non-positive `energy_added_kwh`, a negative `price`, a negative `odometer_km`, a missing required
field, or a missing or empty `location_kind`. An **absent** `energy_added_kwh` SHALL NOT be
rejected. When `currency` is not supplied the stored value SHALL default to `'COP'`.
`location_kind` SHALL be one of `HOME`, `WORK`, or `OTHER`; any other value SHALL be rejected.

#### Scenario: Persist an entry with all required fields
- **GIVEN** an authenticated user with at least one registered vehicle
- **WHEN** they submit a create request with `account_id` (their own account), `tesla_id` and
  `vin` (one of their registered vehicles), `charged_on` (a valid calendar date),
  `energy_added_kwh` (a positive decimal, e.g. `15.50`), `price` (a non-negative decimal, e.g.
  `8000.00`), `currency` (e.g. `'COP'`), and `location_kind` (e.g. `'HOME'`)
- **THEN** the system persists the entry and returns it with a server-assigned `id`,
  `created_at`, and `updated_at`

#### Scenario: Persist an entry with no energy value
- **GIVEN** an authenticated user with at least one registered vehicle
- **WHEN** they submit a create request omitting `energy_added_kwh` entirely, supplying every
  field their entry's `status` requires
- **THEN** the system persists the entry
- **AND** the stored `energy_added_kwh` is absent, not zero

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
- **WHEN** they submit a create request omitting any field required for their entry's `status`
  (`charged_on`, `price`, `currency`, `location_kind`, and — for an entry recorded as done —
  `ended_at` and `end_battery_pct`)
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
- **THEN** the system persists the entry with every optional field (`energy_added_kwh`,
  `started_at`, `ended_at`, `start_battery_pct`, `end_battery_pct`, `charging_type`,
  `location_label`, `notes`, `odometer_km`) stored as NULL, and returns the full entry

#### Scenario: All optional fields supplied are persisted
- **GIVEN** an authenticated user
- **WHEN** they submit a create request that includes all optional fields with valid values
  (`energy_added_kwh = 15.50`, `started_at`/`ended_at` where `ended_at >= started_at`,
  `start_battery_pct = 20`, `end_battery_pct = 80`, `charging_type = 'AC'`,
  `location_kind = 'HOME'`, `location_label = 'Garage'`, `notes = 'Overnight charge'`,
  `odometer_km = 123456`)
- **THEN** the system persists all optional fields and returns them in the response

### Requirement: Derived read-time values
The manual-charge-log capability SHALL expose derived values as read-time methods on the domain
entry — cost per kWh, battery delta, and session duration — computed from stored columns and
never persisted. Each derived value SHALL be nil-safe, returning nil when its inputs are absent.
Because energy added may itself be absent, cost per kWh SHALL return nil when no energy value is
recorded, as well as when the recorded value is zero.

These read-time values are distinct from the two values the capability *stores*: the inferred pack
capacity, which the database computes, and the energy added, which may be derived once on write.

#### Scenario: Cost per kWh is computed, not stored
- **GIVEN** an entry with `price = 8000.00` and `energy_added_kwh = 15.50`
- **WHEN** the caller invokes `entry.CostPerKWh()`
- **THEN** the method returns a pointer to `price / energy_added_kwh` (≈ `516.13`)
- **AND** the stored `price` and `energy_added_kwh` columns are unchanged

#### Scenario: Cost per kWh is nil when no energy is recorded
- **GIVEN** an entry recorded as in progress that has no `energy_added_kwh`
- **WHEN** the caller invokes `entry.CostPerKWh()`
- **THEN** the method returns nil

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
