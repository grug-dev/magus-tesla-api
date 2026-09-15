## MODIFIED Requirements

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
