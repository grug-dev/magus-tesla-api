# manual-charge-log Specification

## Purpose
TBD - created by archiving change RM3-manualcharge-add-entries. Update Purpose after archive.
## Requirements
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
The manual-charge-log capability SHALL let a user correct an existing entry, applying the
supplied changes, advancing `updated_at`, and leaving `created_at` unchanged. Update SHALL
enforce the same field constraints as create — including that `location_kind` is required
(non-nil, non-empty, one of `HOME`/`WORK`/`OTHER`) — and SHALL require proof that the caller's
account owns the entry's vehicle, and SHALL NOT permit mutating an entry whose id does not
belong to that vehicle. `created_by_account_id`, `tesla_id`, `vin` and `created_at` SHALL
remain immutable, and `created_by_account_id` is never re-derived from the proof of vehicle
ownership — it keeps recording whoever originally created the entry.

**This requirement is CHANGED from its prior revision in which value carries the guard.**
The guard is no longer the account that typed the entry; it is the vehicle the entry
belongs to, proven before the call is made. An update naming a vehicle other than the
entry's own mutates nothing — the same strength the account-keyed guard had, now checking
the fact that actually matters: whose car this is, not who typed it.

**This closes the roadmap's transitional gap.** A prior revision of this capability kept
the account-keyed guard as the only check available until a vehicle-keyed one could be
built. That state has ended: every write is now scoped by vehicle, matching every read.

#### Scenario: Update supplied fields and advance updated_at
- **GIVEN** an authenticated user with an existing entry `id = X` for a vehicle they own
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

#### Scenario: Update naming the wrong vehicle mutates nothing
- **GIVEN** an entry belonging to vehicle `V1`
- **WHEN** a caller submits an update for that entry proving ownership of a different
  vehicle `V2`
- **THEN** the system mutates nothing (zero rows / "not found")
- **AND** the owner's row, read back for `V1`, is unchanged

#### Scenario: The authoring account is never rewritten by an update
- **GIVEN** an entry created by account `A`
- **WHEN** any update is applied to that entry, whichever account performs it
- **THEN** the stored `created_by_account_id` is still account `A`

#### Scenario: A co-owner of a shared vehicle may now edit the other account's entry
- **GIVEN** accounts `A` and `B` both registered to the same vehicle `V`, and an entry for
  `V` created by account `A`
- **WHEN** account `B` submits an update for that entry, proving ownership of `V`
- **THEN** the system applies the update
- **AND** the stored `created_by_account_id` remains `A`

### Requirement: Delete an entry
The manual-charge-log capability SHALL let a user delete an entry belonging to a vehicle
they own, requiring proof of that ownership before the delete runs. A delete naming a
vehicle the entry does not belong to SHALL remove nothing and SHALL report that no row was
affected, so a caller can distinguish a real delete from a rejected one.

**This requirement is CHANGED from its prior revision in two ways.** First, which value
carries the guard: the vehicle the entry belongs to, proven before the call, in place of
the account that typed it. Second, how a rejected delete is reported: a prior revision
reported success (zero rows affected, no error) indistinguishably from an accepted delete
unless the caller separately inspected a row count; this revision SHALL report a rejected
delete as an error, so a caller cannot mistake it for success by omission.

**This closes the roadmap's transitional gap**, the same way "Edit an existing entry" does:
every write is now scoped by vehicle, matching every read.

#### Scenario: Delete an entry belonging to the caller's vehicle
- **GIVEN** an authenticated user with an existing entry `id = X` belonging to a vehicle
  they own
- **WHEN** they submit a delete for entry `X`, proving ownership of that vehicle
- **THEN** the system removes the entry and subsequent reads for that `id` return not found

#### Scenario: Delete naming the wrong vehicle removes nothing and reports it
- **GIVEN** an entry belonging to vehicle `V1`
- **WHEN** a caller submits a delete for that entry, proving ownership of a different
  vehicle `V2`
- **THEN** the system deletes nothing
- **AND** the caller receives an error indicating no row was affected, not a silent success

#### Scenario: A co-owner of a shared vehicle may now delete the other account's entry
- **GIVEN** accounts `A` and `B` both registered to the same vehicle `V`, and an entry for
  `V` created by account `A`
- **WHEN** account `B` submits a delete for that entry, proving ownership of `V`
- **THEN** the system removes the entry

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

### Requirement: Multi-tenant isolation
The manual-charge-log capability SHALL scope every read by vehicle, so that a read for one
vehicle can never return another vehicle's entries. For reads it SHALL NOT perform a tenant
check of its own: which vehicles a caller may see is decided before this capability is
reached, and this capability trusts the vehicle identifiers it is given.

Every write SHALL remain scoped by vehicle, so that no revision of this capability permits
an update or delete that names only an entry identifier. Proof of vehicle ownership for a
write SHALL be a value this capability cannot construct on its own — supplied by the
caller, already proven — not a raw identifier this capability would have to re-check
itself.

**This requirement is CHANGED from its prior revision, under which the write-path proof was
an account identifier rather than a proven vehicle.** Reads were already car-wide, scoped
by the vehicle registry the caller resolves against, not by a predicate here (unchanged
from the prior revision). The write path now matches: it is scoped by the entry's own
vehicle, proven by the caller, instead of by the account that typed the entry.

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
- **THEN** the capability requires proof that the caller's account owns the entry's vehicle
- **AND** a caller supplying proof for the wrong vehicle changes nothing

#### Scenario: Callers never reach the table directly
- **GIVEN** any caller that needs to read or write a manual charge entry
- **WHEN** it obtains or changes that data
- **THEN** it does so exclusively through this capability's public ports
- **AND** it imports no package from `internal/charging/db`

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

**When a caller submits an entry as in progress and it already carries every fact a done entry
requires, the capability SHALL instead record it as done.** This promotion SHALL apply on both
creating a new entry and editing an existing one, so no write path can produce an entry that
carries every done-required fact while remaining recorded as in progress. The capability SHALL
NOT promote an entry submitted as done that is missing a required fact — that request SHALL still
be rejected exactly as it would be without this rule.

The capability SHALL NOT restrict which status an entry may move to. An entry recorded as done may
be moved back to in progress. Moving an entry back to in progress while it still carries every
done-required fact SHALL immediately record it as done again, by the same promotion rule — an
entry is only genuinely reopened when a caller also clears at least one done-required fact.

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

#### Scenario: A complete in-progress entry is promoted to done on creation
- **GIVEN** an authenticated user
- **WHEN** they create an entry recorded as in progress that carries the charge date, the location
  kind, the session end time and the ending battery percentage
- **THEN** the entry is stored
- **AND** its recorded status is done, not in progress

#### Scenario: A complete in-progress entry is promoted to done on an edit
- **GIVEN** an existing entry recorded as in progress that is missing the session end time and the
  ending battery percentage
- **WHEN** the user edits it, still recording it as in progress, but now supplying both missing
  facts
- **THEN** the stored entry's recorded status is done, not in progress

#### Scenario: An incomplete done submission is rejected, promotion or not
- **GIVEN** an authenticated user
- **WHEN** they submit an entry explicitly recorded as done that is missing the ending battery
  percentage
- **THEN** the request is rejected with a validation error naming the ending battery percentage
- **AND** no entry is stored

#### Scenario: Reopening a done entry without clearing its facts promotes it right back
- **GIVEN** an existing entry recorded as done, carrying a session end time and an ending battery
  percentage
- **WHEN** the user edits it to be recorded as in progress, without clearing the session end time
  or the ending battery percentage
- **THEN** the stored entry's recorded status is done again
- **AND** the entry is genuinely reopened only when the user also clears at least one of those two
  facts

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

### Requirement: A charge entry's price records whether a zero amount is confirmed real

The manual-charge-log capability SHALL record, for every charge entry, whether its price is a
confirmed real amount or an unconfirmed placeholder. A price greater than zero SHALL always be
recorded as confirmed. A price of zero SHALL be recorded as confirmed only when the caller
explicitly confirms it is a real free charge; otherwise it SHALL be recorded as unconfirmed.

The capability SHALL determine this provenance itself on every create and every edit, and SHALL
NOT accept it directly from a caller: a caller MAY only supply its confirmation intent for a zero
price, never the recorded provenance itself. The intent SHALL be ignored when the price is greater
than zero, since such a price is always confirmed regardless of it.

Every charge entry that existed before the capability gained this provenance SHALL be recorded
according to its own already-stored price: a positive price SHALL be recorded as confirmed, and a
zero price SHALL be recorded as unconfirmed.

#### Scenario: A positive price is always recorded as confirmed
- **GIVEN** an authenticated user
- **WHEN** they submit an entry with a price greater than zero
- **THEN** the entry's recorded price provenance is confirmed

#### Scenario: A confirmed zero price is recorded as confirmed
- **GIVEN** an authenticated user
- **WHEN** they submit an entry with a price of zero and explicitly confirm it is a real free
  charge
- **THEN** the entry's recorded price provenance is confirmed

#### Scenario: An unconfirmed zero price is recorded as unconfirmed
- **GIVEN** an authenticated user
- **WHEN** they submit an entry with a price of zero without confirming it is a real free charge
- **THEN** the entry's recorded price provenance is unconfirmed

#### Scenario: A confirmation intent is ignored when the price is positive
- **GIVEN** an authenticated user
- **WHEN** they submit an entry with a price greater than zero, whether or not they also send a
  confirmation intent
- **THEN** the entry's recorded price provenance is confirmed either way

#### Scenario: A caller cannot set the price provenance directly
- **GIVEN** a caller that submits an entry with a price of zero, does not confirm it, but also
  asserts the price provenance as confirmed
- **WHEN** the entry is stored
- **THEN** the entry's recorded price provenance is unconfirmed

#### Scenario: Editing the price recomputes its provenance
- **GIVEN** an existing entry whose price provenance is unconfirmed
- **WHEN** the user edits it, confirming a zero price is a real free charge
- **THEN** the entry's recorded price provenance is confirmed

#### Scenario: Entries that pre-date this provenance are recorded from their own price
- **GIVEN** charge entries that were stored before the capability recorded price provenance
- **WHEN** those entries are read
- **THEN** every entry whose price was greater than zero has its provenance recorded as confirmed
- **AND** every entry whose price was zero has its provenance recorded as unconfirmed

### Requirement: Charge entry authorship is recorded but never scopes a read

The manual-charge-log capability SHALL record, on every stored entry, which account typed
it. The value SHALL be required on create and SHALL be returned on every read. No read or
write SHALL filter, order, group or join by it.

Authorship cannot be re-derived from anything else the platform stores: a vehicle may be
registered to more than one account, and the account vehicle registry records registration
rather than authorship. That is why the value is kept at all, rather than removed the way
the Supercharger session store removed its own account identifier.

**This requirement is CHANGED from its prior revision, which stated the rule above bound
only reads.** A prior revision of this capability kept update and delete matching on this
value, as the only guard those two writes had until they could name the entry's vehicle
instead. That transitional period has ended: update and delete are now scoped by vehicle
(see "Edit an existing entry" and "Delete an entry"), so `created_by_account_id` is
authorship only, in both directions, with nothing left that predicates on it anywhere.

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

#### Scenario: Authorship does not decide whether a write succeeds

- **GIVEN** accounts `A` and `B` both registered to the same vehicle `V`, and an entry for
  `V` created by account `A`
- **WHEN** account `B` submits an update or delete for that entry, proving ownership of `V`
- **THEN** the write succeeds
- **AND** which account created the entry played no part in that decision

