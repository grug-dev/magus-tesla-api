## ADDED Requirements

### Requirement: Charge entry authorship is recorded but never scopes a read

The manual-charge-log capability SHALL record, on every stored entry, which account typed
it. The value SHALL be required on create and SHALL be returned on every read. No read
SHALL filter, order, group or join by it.

Authorship cannot be re-derived from anything else the platform stores: a vehicle may be
registered to more than one account, and the account vehicle registry records registration
rather than authorship. That is why the value is kept at all, rather than removed the way
the Supercharger session store removed its own account identifier.

**Transitional, and deliberate:** update and delete DO still match on this value, as the
only guard they have until they can name the entry's vehicle instead (see "Edit an existing
entry" and "Delete an entry"). The end state — authorship that nothing predicates on at all
— is reached when those two writes are re-keyed onto the vehicle. Until then the rule above
binds every read, and only reads.

#### Scenario: No read decides its result from the authoring account

- **GIVEN** stored entries created by more than one account
- **WHEN** any read of this capability is made
- **THEN** the result is decided by the vehicle asked for, never by which account created
  an entry

#### Scenario: The authoring account is stored and returned

- **GIVEN** an authenticated user in account `A` with a registered vehicle `V`
- **WHEN** they create an entry for vehicle `V`
- **THEN** the stored entry records account `A` as the account that created it
- **AND** every later read of that entry returns account `A` as its creator

#### Scenario: Authorship does not decide what a read returns

- **GIVEN** two accounts `A` and `B`, both registered to the same vehicle `V`, each having
  created one entry for `V`
- **WHEN** a caller requests vehicle `V`'s entries
- **THEN** both entries are returned
- **AND** each carries the account that created it

### Requirement: List entries for a set of vehicles

The manual-charge-log capability SHALL return the entries of a caller-supplied set of
vehicles, ordered by `charged_on` descending across the whole set, bounded by a
caller-supplied `limit` (0 = server default). It SHALL return a non-nil empty slice when
none of those vehicles has an entry, and SHALL exclude every entry belonging to a vehicle
outside the supplied set.

An empty set SHALL return a non-nil empty result. An empty set SHALL NEVER be read as "no
filter" — a caller that supplies no vehicle is entitled to no entry.

This replaces the previous account-wide list. The caller, not this capability, decides which
vehicles it may see.

#### Scenario: Entries for the supplied vehicles are returned newest-first

- **GIVEN** entries exist for vehicles `V1` and `V2`
- **WHEN** a caller requests entries for the set `{V1, V2}` with a limit of 20
- **THEN** up to 20 entries for those two vehicles are returned, ordered by `charged_on`
  descending across both vehicles

#### Scenario: A vehicle outside the supplied set is excluded

- **GIVEN** entries exist for vehicles `V1` and `V2`
- **WHEN** a caller requests entries for the set `{V1}`
- **THEN** only vehicle `V1`'s entries are returned
- **AND** no entry for `V2` appears

#### Scenario: An empty set returns nothing, not everything

- **GIVEN** entries exist for several vehicles
- **WHEN** a caller requests entries for an empty set of vehicles
- **THEN** a non-nil empty result is returned
- **AND** no error is returned

## MODIFIED Requirements

### Requirement: Create a manual charge entry
The manual-charge-log capability SHALL let an authenticated user persist a user-asserted
charge entry for one of their own registered vehicles. The entry SHALL require
`created_by_account_id` (the account typing the entry — see "Charge entry authorship is
recorded but never scopes a read"), `tesla_id`, `vin`, `charged_on`, `price`, `currency`,
and `location_kind`, together with the additional fields that the entry's recorded `status`
demands (see "A charge entry has a recorded status that governs its required fields"), and
SHALL accept the optional fields `energy_added_kwh`, `started_at`, `ended_at`,
`start_battery_pct`, `end_battery_pct`, `charging_type`, `location_label`, `notes`, and
`odometer_km`. On success it SHALL return the stored entry with a server-assigned `id`,
`created_at`, and `updated_at`. It SHALL reject a non-positive `energy_added_kwh`, a
negative `price`, a negative `odometer_km`, a missing required field, or a missing or empty
`location_kind`. An **absent** `energy_added_kwh` SHALL NOT be rejected. When `currency` is
not supplied the stored value SHALL default to `'COP'`. `location_kind` SHALL be one of
`HOME`, `WORK`, or `OTHER`; any other value SHALL be rejected.

**This requirement is CHANGED from its prior revision only in the name and meaning of one
field.** The required `account_id` became `created_by_account_id`: the same value, still
required, no longer the entry's tenant key.

#### Scenario: Persist an entry with all required fields
- **GIVEN** an authenticated user with at least one registered vehicle
- **WHEN** they submit a create request with `created_by_account_id` (their own account),
  `tesla_id` and `vin` (one of their registered vehicles), `charged_on` (a valid calendar
  date), `energy_added_kwh` (a positive decimal, e.g. `15.50`), `price` (a non-negative
  decimal, e.g. `8000.00`), `currency` (e.g. `'COP'`), and `location_kind` (e.g. `'HOME'`)
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

### Requirement: Edit an existing entry
The manual-charge-log capability SHALL let a user correct an existing entry, applying the
supplied changes, advancing `updated_at`, and leaving `created_at` unchanged. Update SHALL
enforce the same field constraints as create — including that `location_kind` is required
(non-nil, non-empty, one of `HOME`/`WORK`/`OTHER`) — and SHALL NOT permit mutating an entry
created by a different account. `created_by_account_id`, `tesla_id`, `vin` and `created_at`
SHALL remain immutable.

**This requirement is CHANGED from its prior revision only in which value carries the
guard.** The guard itself is unchanged in strength: an update must still name the account
the entry belongs to, and an update naming a different one mutates nothing. What changed is
that the value is now `created_by_account_id` — authorship — rather than a tenant key.

**This scoping is transitional and deliberate.** Reads of this capability are already
car-wide, so an account registered to a shared vehicle can see an entry it cannot yet edit.
The end state replaces this guard with one on the entry's own vehicle, so that any account
registered to a car may correct that car's entries. The guard is swapped, never dropped:
there is no revision of this capability in which an update is unscoped.

#### Scenario: Update supplied fields and advance updated_at
- **GIVEN** an authenticated user with an existing entry `id = X` they created
- **WHEN** they submit an update for entry `X` with a corrected `price` and `notes` and a
  valid `location_kind`
- **THEN** the system updates the supplied fields, advances `updated_at` to now, leaves
  `created_at` unchanged, and returns the full updated entry

#### Scenario: Update is rejected when it violates a field constraint
- **GIVEN** an authenticated user
- **WHEN** they submit an update for an entry with `energy_added_kwh = 0`
- **THEN** the system rejects the update (CHECK: `energy_added_kwh > 0`)

#### Scenario: Reject a missing location_kind on update
- **GIVEN** an existing entry `id = X`
- **WHEN** a caller submits an update that omits `location_kind` (nil or empty string)
- **THEN** the system rejects the update with a service-layer validation error before reaching
  the database

#### Scenario: Accept a location_kind change on update
- **GIVEN** an existing entry where `location_kind = 'HOME'`
- **WHEN** a caller submits an update with `location_kind = 'WORK'`
- **THEN** the system persists `location_kind = 'WORK'` and returns the updated entry

#### Scenario: Update naming the wrong account mutates nothing
- **GIVEN** an entry created by account `A`
- **WHEN** a caller submits an update for that entry naming account `B`
- **THEN** the system mutates nothing (zero rows / "not found")

#### Scenario: The authoring account is never rewritten by an update
- **GIVEN** an entry created by account `A`
- **WHEN** any update is applied to that entry
- **THEN** the stored `created_by_account_id` is still account `A`

### Requirement: Delete an entry
The manual-charge-log capability SHALL let a user delete an entry they created, scoping
every delete to the caller's own account identifier so that a valid entry identifier from
another account deletes nothing and does not leak the entry's existence.

**This requirement is CHANGED from its prior revision only in which value carries the
guard**: `created_by_account_id` — authorship — in place of a tenant key. The guard's
strength is unchanged.

**This scoping is transitional and deliberate**, for the same reason given under "Edit an
existing entry": the end state scopes a delete by the entry's own vehicle, so a shared car's
co-owner may delete that car's entries. The guard is swapped, never dropped.

#### Scenario: Delete an entry the caller created
- **GIVEN** an authenticated user with an existing entry `id = X` they created
- **WHEN** they submit a delete for entry `X` scoped to their own account
- **THEN** the system removes the entry and subsequent reads for that `id` return not found

#### Scenario: Delete naming another account removes nothing
- **GIVEN** an entry created by account `A`
- **WHEN** a caller submits a delete for that entry naming account `B`
- **THEN** the system deletes nothing and the caller receives zero rows affected, not an
  error that leaks entry existence

### Requirement: List entries by vehicle
The manual-charge-log capability SHALL return a vehicle's entries, ordered by `charged_on`
descending (newest day first), bounded by a caller-supplied `limit` (0 = server default). It
SHALL return a non-nil empty slice when the vehicle has no entries, and SHALL exclude
entries belonging to other vehicles.

**This requirement is CHANGED from its prior revision, which also excluded entries belonging
to other accounts.** The read is now car-wide: it returns every entry for the vehicle,
whoever typed it. A charge happened to a car, and the Supercharger session store already
reads the same way, so the two charging sources now agree about what one car did.

#### Scenario: Return a vehicle's entries newest-first
- **GIVEN** a vehicle `tesla_id = V`
- **WHEN** a caller requests `ListEntriesByVehicle(teslaID=V, limit=10)`
- **THEN** the system returns up to 10 entries for vehicle `V`, ordered by `charged_on DESC`
- **AND** when the vehicle has no entries a non-nil empty slice is returned

#### Scenario: Exclude other vehicles' entries
- **GIVEN** entries exist for two vehicles (`V1` and `V2`)
- **WHEN** a caller requests `ListEntriesByVehicle(teslaID=V1, limit=100)`
- **THEN** only entries for vehicle `V1` are returned; entries for `V2` are excluded

#### Scenario: Every account's entries for the vehicle are returned
- **GIVEN** two accounts registered to the same vehicle `V`, each having created one entry
  for `V`
- **WHEN** a caller requests `ListEntriesByVehicle(teslaID=V, limit=100)`
- **THEN** both entries are returned

#### Scenario: Respect the limit
- **GIVEN** a vehicle with more than `limit` entries
- **WHEN** a caller requests with `limit = 5`
- **THEN** exactly 5 entries are returned, ordered newest `charged_on` first

### Requirement: Multi-tenant isolation
The manual-charge-log capability SHALL scope every read by vehicle, so that a read for one
vehicle can never return another vehicle's entries. For reads it SHALL NOT perform a tenant
check of its own: which vehicles a caller may see is decided before this capability is
reached, and this capability trusts the vehicle identifiers it is given.

Every write SHALL remain scoped, so that no revision of this capability permits an update or
delete that names only an entry identifier.

**This requirement is CHANGED from its prior revision, under which every read was also
scoped by `account_id` inside this capability.** Reads are now car-wide: two accounts
registered to the same vehicle see the same entries for it, deliberately, because the
entries describe the car. Isolation between users on the read path is upheld by the vehicle
registry the caller resolves against, not by a predicate here. The write path keeps a
predicate of its own, on the authoring account, until it can be re-keyed onto the entry's
vehicle.

#### Scenario: A read never crosses to another vehicle
- **GIVEN** entries exist for vehicles `V1` and `V2`
- **WHEN** any read of this capability is made for `V1`
- **THEN** no entry belonging to `V2` is returned

#### Scenario: A shared vehicle is shared data on the read path
- **GIVEN** accounts `A` and `B` both registered to vehicle `V`
- **WHEN** either of them reads vehicle `V`'s entries
- **THEN** the same entries are returned to both, whichever account typed them

#### Scenario: A write is never reachable by entry identifier alone
- **GIVEN** a stored entry
- **WHEN** a caller attempts to update or delete it
- **THEN** the capability requires a second value identifying who may write it
- **AND** a caller supplying the wrong value changes nothing

#### Scenario: Callers never reach the table directly
- **GIVEN** any caller that needs to read or write a manual charge entry
- **WHEN** it obtains or changes that data
- **THEN** it does so exclusively through this capability's public ports
- **AND** it imports no package from `internal/charging/db`

### Requirement: List entries by vehicle within a date range

The manual-charge-log capability SHALL return a vehicle's entries whose `charged_on` falls
within a caller-supplied `[from, to]` date range, inclusive of both bounds, ordered by
`charged_on` descending (newest day first). It SHALL return a non-nil empty slice when no
entries fall within the range, and SHALL exclude entries belonging to other vehicles even
when their `charged_on` falls inside the same range. This method SHALL NOT accept a `limit`
parameter — the date range itself bounds the result.

**This requirement is CHANGED from its prior revision, which also excluded entries belonging
to other accounts.** The read is car-wide, for the reason given under "List entries by
vehicle".

#### Scenario: An entry exactly on the start bound is included
- **GIVEN** a vehicle `tesla_id = V` with an entry whose `charged_on` equals a date `from`
- **WHEN** a caller requests `ListEntriesByVehicleBetween(teslaID=V, from=from, to=to)` where
  `to` is on or after `from`
- **THEN** the entry dated exactly `from` is included in the result

#### Scenario: An entry exactly on the end bound is included
- **GIVEN** a vehicle `tesla_id = V` with an entry whose `charged_on` equals a date `to`
- **WHEN** a caller requests `ListEntriesByVehicleBetween(teslaID=V, from=from, to=to)` where
  `from` is on or before `to`
- **THEN** the entry dated exactly `to` is included in the result

#### Scenario: An entry one day before the start bound is excluded
- **GIVEN** a vehicle `tesla_id = V` with an entry whose `charged_on` is the day immediately
  before `from`
- **WHEN** a caller requests `ListEntriesByVehicleBetween(teslaID=V, from=from, to=to)`
- **THEN** that entry is not included in the result

#### Scenario: An entry one day after the end bound is excluded
- **GIVEN** a vehicle `tesla_id = V` with an entry whose `charged_on` is the day immediately
  after `to`
- **WHEN** a caller requests `ListEntriesByVehicleBetween(teslaID=V, from=from, to=to)`
- **THEN** that entry is not included in the result

#### Scenario: Results within the range are returned newest-first
- **GIVEN** a vehicle `tesla_id = V` with multiple entries whose `charged_on` values fall
  within `[from, to]`
- **WHEN** a caller requests `ListEntriesByVehicleBetween(teslaID=V, from=from, to=to)`
- **THEN** the returned entries are ordered by `charged_on` descending (newest day first)

#### Scenario: Empty range returns a non-nil empty slice
- **GIVEN** a vehicle `tesla_id = V` with no entries whose `charged_on` is inside `[from, to]`
- **WHEN** a caller requests `ListEntriesByVehicleBetween(teslaID=V, from=from, to=to)`
- **THEN** the system returns a non-nil empty slice and no error

#### Scenario: Vehicle isolation within the range
- **GIVEN** two vehicles (`V1` and `V2`), each holding an entry whose `charged_on` falls
  inside the same `[from, to]` range
- **WHEN** a caller requests `ListEntriesByVehicleBetween(teslaID=V1, from=from, to=to)`
- **THEN** only the entry belonging to vehicle `V1` is returned; the entry belonging to `V2`
  is excluded even though its `charged_on` is within range

#### Scenario: Entries typed by different accounts for the same vehicle are all returned
- **GIVEN** two accounts, each holding an entry for the same `tesla_id = V` with a
  `charged_on` inside the same `[from, to]` range
- **WHEN** a caller requests `ListEntriesByVehicleBetween(teslaID=V, from=from, to=to)`
- **THEN** both entries are returned, ordered by `charged_on` descending

### Requirement: List entries by vehicle updated since a given instant

The manual-charge-log capability SHALL return a vehicle's entries whose `updated_at` is at
or after a caller-supplied instant, without a `limit` parameter — the instant itself bounds
the result. It SHALL return a non-nil empty slice when no entry for that vehicle has been
updated at or after the given instant, and SHALL exclude entries belonging to other
vehicles.

**This requirement is CHANGED from its prior revision, which also excluded entries belonging
to other accounts.** The read is car-wide, for the reason given under "List entries by
vehicle". This matters for the analytics recompute this port exists to serve: an edit made
by either account registered to a car now marks that car's metrics as needing recalculation.

#### Scenario: An entry edited after the given instant is included
- **GIVEN** a vehicle `tesla_id = V` with an entry whose `updated_at` is after a given
  instant `since` (created earlier, then edited)
- **WHEN** a caller requests `ListEntriesByVehicleUpdatedSince(teslaID=V, since=since)`
- **THEN** that entry is included in the result

#### Scenario: An entry untouched since before the given instant is excluded
- **GIVEN** a vehicle `tesla_id = V` with an entry whose `updated_at` is before a given
  instant `since`, never edited since
- **WHEN** a caller requests `ListEntriesByVehicleUpdatedSince(teslaID=V, since=since)`
- **THEN** that entry is not included in the result

#### Scenario: Empty result when nothing has been updated in the window
- **GIVEN** a vehicle with no entry updated at or after the requested instant
- **WHEN** a caller requests `ListEntriesByVehicleUpdatedSince` for that vehicle and instant
- **THEN** a non-nil empty slice is returned, and no error is returned

#### Scenario: Per-vehicle scoping is preserved
- **GIVEN** two vehicles that each have entries updated at or after the requested instant
- **WHEN** the caller requests entries updated since that instant for one of them
- **THEN** only entries belonging to that vehicle are returned

## REMOVED Requirements

### Requirement: List entries by account

**Reason**: entries are no longer owned by an account, so an account-wide list has nothing
to scope on. It is replaced by "List entries for a set of vehicles", which asks the caller
to supply the vehicles it is entitled to see.

**Migration**: a caller that listed an account's entries now resolves that account's
registered vehicles first and passes their identifiers to the new read. The gateway already
resolves that list for other reasons, so no new lookup is introduced.
