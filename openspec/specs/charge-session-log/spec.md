# charge-session-log Specification

## Purpose
TBD - created by archiving change RM29-charging-add-charge-sessions. Update Purpose after archive.
## Requirements
### Requirement: Supercharger Charge Session Record

The charging capability SHALL maintain a durable record of each Tesla Supercharger charge
session belonging to an account. Each record SHALL identify the owning account, the
vehicle by its durable vehicle identification number, the vehicle by its currently
registered vehicle identifier (which MAY be absent), the session's own identifier, and
the instants at which the session started and stopped. There SHALL be at most one record
per account and session identifier.

The capability SHALL hold one record for EVERY collected Supercharger session, whether or
not that session has verified battery percentages, so that a caller asking for a
vehicle's sessions over a date range receives every session — its time window, its
charging site, and its energy, cost, currency and payment facts — from this capability
alone, with no need to consult the capability that collects Supercharger sessions.

Each record SHALL NOT carry the vendor's raw, unprocessed payload, the account's country,
the vehicle's model designation, the session's billing-type classification, or the
instant at which the vehicle unlatched from the charger. Those remain the responsibility
of the capability that collects them.

#### Scenario: A collected session is recorded
- **GIVEN** the platform has collected a Supercharger session for an account's vehicle
- **WHEN** the charge session records are synchronized
- **THEN** exactly one record exists for that account and session identifier
- **AND** the record carries the session's vehicle identification number, its registered
  vehicle identifier, and its start and stop instants
- **AND** the record carries the session's charging site, and its energy delivered, cost,
  currency and payment status whenever the collected session carries them

#### Scenario: A session with no verified percentages is still recorded
- **GIVEN** a collected Supercharger session for which nobody has verified any battery
  percentage
- **WHEN** the charge session records are synchronized
- **THEN** a record still exists for that session, carrying its time window and its
  charging site, energy, cost, currency and payment facts
- **AND** the record's verified battery percentages are absent

#### Scenario: Re-synchronizing an unchanged session does not duplicate it
- **GIVEN** a record already exists for an account and session identifier
- **WHEN** the charge session records are synchronized again with the same session
- **THEN** exactly one record still exists for that account and session identifier

#### Scenario: A session that disappears from the collected history is retained
- **GIVEN** a record exists for an account and session identifier
- **WHEN** the charge session records are synchronized from a collected history that no
  longer contains that session
- **THEN** the record is retained unchanged

#### Scenario: The same session identifier under two accounts is two records
- **GIVEN** two different accounts each associated with the same session identifier
- **WHEN** the charge session records are synchronized for both accounts
- **THEN** each account has its own record for that session identifier
- **AND** neither account's record is affected by the other's synchronization

#### Scenario: A synchronization that names another account's session is rejected entirely
- **GIVEN** a synchronization scoped to one account
- **WHEN** its supplied set contains a session belonging to a different account
- **THEN** the synchronization is rejected
- **AND** no part of that set is recorded, including the sessions that were correctly
  scoped

#### Scenario: An empty synchronization changes nothing
- **GIVEN** any existing set of records
- **WHEN** a synchronization is performed with an empty set of sessions
- **THEN** the synchronization succeeds
- **AND** no record is created, altered, or removed

### Requirement: The Charging Site Is Fixed On Record; Energy, Cost, Currency And Payment Status Track The Source

Each charge session record SHALL carry the name of the site where the session took place,
which SHALL NOT change once recorded, however many times the record is later
synchronized. Each record SHALL also carry the session's energy delivered, its total
cost, the currency of that cost, and whether every fee on the session has been settled —
each of which MAY be absent when the collected session carries none.

Unlike the charging site, the energy delivered, total cost, currency and payment-settled
status are NOT frozen at first recording: every synchronization SHALL replace each of
them with the collecting capability's current value for that session, even when nothing
else about the session has changed. This capability's synchronization SHALL therefore be
run regularly enough that a session's settling fees stay reflected within one
synchronization cycle of the source updating them.

#### Scenario: The charging site is recorded once and never changes
- **GIVEN** a record already exists for an account and session identifier, carrying a
  charging site name
- **WHEN** the charge session records are synchronized again for that session
- **THEN** the record's charging site name is unchanged, even if the source now reports a
  different name for that session

#### Scenario: Settled fees are reflected on the next synchronization
- **GIVEN** a record whose energy delivered, total cost, currency or payment status were
  recorded before the session's fees fully settled
- **WHEN** the charge session records are synchronized again after those fees settle
- **THEN** the record's energy delivered, total cost, currency and payment status match
  the source's current values for that session

#### Scenario: A session with no fees carries no fee facts
- **GIVEN** a collected Supercharger session that carries no energy delivered, cost,
  currency or payment status
- **WHEN** the charge session records are synchronized
- **THEN** the record's energy delivered, total cost, currency and payment status are all
  absent
- **AND** the record's charging site name is present

### Requirement: Registered Vehicle Identifier Is Refreshed, VIN Is Not

Each charge session record SHALL carry the vehicle identification number as a durable,
write-once value, and the currently registered vehicle identifier as a value refreshed on
every synchronization. The registered vehicle identifier SHALL be absent when the
session's vehicle identification number does not belong to any vehicle currently
registered to the account.

#### Scenario: A vehicle's registered identifier changes
- **GIVEN** a record whose registered vehicle identifier is a given value
- **WHEN** the charge session records are synchronized and that session's vehicle now
  carries a different registered identifier
- **THEN** the record's registered vehicle identifier is updated to the new value
- **AND** the record's vehicle identification number is unchanged

#### Scenario: A vehicle is no longer registered
- **GIVEN** a record whose registered vehicle identifier is present
- **WHEN** the charge session records are synchronized and that session's vehicle
  identification number no longer belongs to a currently registered vehicle
- **THEN** the record's registered vehicle identifier is absent
- **AND** the record itself is retained, still identifiable by its vehicle identification
  number

### Requirement: Battery Percentage Verification On A Charge Session

Each charge session record SHALL carry an optional verified battery percentage at charge
start and at charge stop, an optional provenance stating where those percentages came
from, and an optional frozen pair of estimated percentages captured at the moment of
verification. Any recorded percentage SHALL be between 0 and 100 inclusive. A recorded
provenance SHALL be either "user verified" or "polled". A record carrying either verified
percentage SHALL also carry a provenance.

Synchronizing charge session records SHALL never write, clear, or overwrite any of these
five values — including when the same synchronization pass updates that same record's
registered vehicle identifier or its energy, cost, currency or payment facts. The frozen
estimate pair SHALL never be refreshed after it is first recorded.

#### Scenario: A percentage without a provenance is rejected
- **GIVEN** a charge session record being written
- **WHEN** it carries a verified battery percentage at start or at stop but no provenance
- **THEN** the write is rejected

#### Scenario: A record with no verified percentages needs no provenance
- **GIVEN** a charge session record being written
- **WHEN** it carries no verified battery percentage at start and none at stop
- **THEN** the write is accepted with no provenance

#### Scenario: A percentage outside 0 to 100 is rejected
- **GIVEN** a charge session record being written with a provenance
- **WHEN** it carries a verified battery percentage below 0 or above 100
- **THEN** the write is rejected

#### Scenario: An unrecognized provenance is rejected
- **GIVEN** a charge session record being written
- **WHEN** its provenance is neither "user verified" nor "polled"
- **THEN** the write is rejected

#### Scenario: Synchronization never overwrites a verified percentage
- **GIVEN** a charge session record whose verified percentages, provenance, and frozen
  estimates have been recorded
- **WHEN** the charge session records are synchronized again for that session, including
  when its registered vehicle identifier, energy delivered, cost, currency or payment
  status have changed
- **THEN** all five values are unchanged

### Requirement: One-Time Import Of Previously Collected Sessions

Every Supercharger session collected before this capability existed SHALL be imported
into the charge session records exactly once, carrying its identity, its time window, its
charging site, its energy, cost, currency and payment facts, and whatever battery
percentages had been recorded against it. Importing SHALL be repeatable without altering
any record it has already created.

Where an imported session carries a verified battery percentage but no provenance — a
state the collecting capability permitted and this capability does not — the import SHALL
record its provenance as "user verified", because no automated writer for those
percentages has ever existed and a recorded percentage is therefore necessarily human
entered. A session carrying no verified percentage SHALL NOT be given a provenance.

#### Scenario: A previously collected session is imported
- **GIVEN** a Supercharger session collected before this capability existed
- **WHEN** the import runs
- **THEN** a charge session record exists for it, carrying the same identity, the same
  start and stop instants, and the same charging site, energy, cost, currency and payment
  facts

#### Scenario: A percentage-bearing session with no provenance is resolved on import
- **GIVEN** a previously collected session carrying verified battery percentages and no
  provenance
- **WHEN** the import runs
- **THEN** its charge session record carries the same percentages
- **AND** its provenance is recorded as "user verified"

#### Scenario: A session with no percentages is not given a provenance
- **GIVEN** a previously collected session carrying no verified battery percentage
- **WHEN** the import runs
- **THEN** its charge session record carries no percentage and no provenance

#### Scenario: Re-running the import alters nothing
- **GIVEN** the import has already run, and a percentage has since been verified against
  one of the imported records
- **WHEN** the import runs again
- **THEN** no record is duplicated
- **AND** the verified percentage is unchanged
- **AND** the energy, cost, currency and payment facts already recorded by the import are
  unchanged (import is one-time; a later synchronization, not the import, is what
  reflects settled fees)

### Requirement: Charge Sessions Are Retrievable For A Vehicle Within A Time Window

The charging capability SHALL provide a way to retrieve every charge session record
belonging to a specific vehicle within an account whose stop instant falls within a
caller-specified window of whole calendar days, inclusive of every instant of the
window's first day and every instant of its last day. The retrieved records SHALL be
ordered by their stop instant, earliest first. A retrieval that matches no record SHALL
return an empty result, never an absence or an error.

Each retrieved record SHALL carry every fact the capability holds for that session,
including the session's charging site, energy delivered, cost, currency, payment status,
and its optional verified battery percentages, their provenance, and their frozen
estimated pair — so that a caller needs no further request to another capability to
describe the session completely.

A record whose vehicle is not currently registered to the account SHALL NOT be returned by
this retrieval, for any vehicle requested, even though the record itself continues to
exist and to be retained.

#### Scenario: A session stopping at the very start of the window's first day is included
- **GIVEN** a charge session record for a vehicle, whose stop instant is the first instant
  of a given day
- **WHEN** that vehicle's sessions are retrieved for a window whose first day is that day
- **THEN** the record is included in the result

#### Scenario: A session stopping at the very last instant of the window's last day is included
- **GIVEN** a charge session record for a vehicle, whose stop instant is the last instant
  of a given day
- **WHEN** that vehicle's sessions are retrieved for a window whose last day is that day
- **THEN** the record is included in the result

#### Scenario: A session stopping at the start of the day after the window's last day is excluded
- **GIVEN** a charge session record for a vehicle, whose stop instant is the first instant
  of the day immediately following a given window's last day
- **WHEN** that vehicle's sessions are retrieved for that window
- **THEN** the record is NOT included in the result

#### Scenario: A session that starts before the window but stops within it is included
- **GIVEN** a charge session record whose start instant falls before a given window but
  whose stop instant falls within it
- **WHEN** that vehicle's sessions are retrieved for that window
- **THEN** the record is included in the result
- **AND** its start instant is reported unchanged, even though it precedes the window

#### Scenario: A session that starts within the window but stops after it is excluded
- **GIVEN** a charge session record whose start instant falls within a given window but
  whose stop instant falls after the window's last day
- **WHEN** that vehicle's sessions are retrieved for that window
- **THEN** the record is NOT included in the result
- **AND** this holds even when the record's start instant is itself within the window

#### Scenario: No matching session returns an empty result
- **GIVEN** a vehicle with no charge session record whose stop instant falls within a
  given window
- **WHEN** that vehicle's sessions are retrieved for that window
- **THEN** the result is empty
- **AND** the retrieval succeeds, rather than failing or returning an absence

#### Scenario: Results are ordered earliest-stop-first
- **GIVEN** a vehicle with multiple charge session records whose stop instants fall within
  a given window
- **WHEN** that vehicle's sessions are retrieved for that window
- **THEN** the records are ordered by their stop instant, earliest first

#### Scenario: A retrieved record carries its full session detail
- **GIVEN** a charge session record carrying a charging site, energy delivered, cost,
  currency, payment status, and verified battery percentages with their provenance and
  frozen estimates
- **WHEN** that record is retrieved
- **THEN** the retrieved record carries all of that same detail

#### Scenario: A different vehicle's session within the same account does not leak
- **GIVEN** two charge session records within one account, belonging to two different
  vehicles, both with stop instants inside a given window
- **WHEN** one vehicle's sessions are retrieved for that window
- **THEN** only that vehicle's record is included
- **AND** the other vehicle's record is absent from the result

#### Scenario: A different account's session does not leak
- **GIVEN** two accounts, each holding a charge session record for the same vehicle
  identifier, both with stop instants inside a given window
- **WHEN** one account's sessions for that vehicle are retrieved for that window
- **THEN** only that account's record is included
- **AND** the other account's record is absent from the result

#### Scenario: A session whose vehicle is no longer currently registered is never retrieved by vehicle
- **GIVEN** a charge session record whose vehicle is no longer currently registered to the
  account
- **WHEN** any vehicle's sessions are retrieved for a window containing that record's stop
  instant
- **THEN** the record is not included in the result for any vehicle
- **AND** the record itself continues to exist and to be retained

### Requirement: A Charge Session's Battery Percentages Are Correctable By A Human

The charging capability SHALL provide a way for a human to record or correct the verified
start and end battery percentage of one charge session record belonging to a specific
account, together with the provenance of that percentage data. The correction SHALL be
scoped to the account it is performed for: a correction MAY change only a record that
belongs to that same account, regardless of the record identifier supplied.

Either percentage MAY be recorded independently of the other — a correction that supplies
only one of the two percentages SHALL be accepted, and the percentage not supplied SHALL
be recorded as absent. A correction that supplies neither percentage SHALL clear both
percentages and their provenance to absent, as a single outcome of that one correction.

Whenever a correction results in at least one percentage being present, the record's
provenance SHALL be recorded as verified by a human. The capability SHALL NOT accept
provenance as an input to a correction — provenance is always derived from whether a
percentage is present, never supplied directly.

Each supplied percentage SHALL be within the inclusive range zero to one hundred; a
correction that supplies a percentage outside that range SHALL be rejected, and the
record SHALL remain exactly as it was before the rejected correction.

A correction SHALL change nothing about the record other than its start percentage, end
percentage, and provenance — every other fact the record carries about the session (its
identity, its time window, its charging site, its energy, cost, currency and payment
facts, and its frozen estimated percentages) SHALL be unaffected by any correction, no
matter how many times a correction is performed.

A correction that names a record that does not exist, or that names a record belonging to
a different account than the one the correction is scoped to, SHALL be rejected in the
same way in both cases, and the record (if one exists) SHALL be unaffected.

A successful correction SHALL make the record's full current detail available to whoever
performed it, without requiring a separate retrieval.

#### Scenario: A human records both percentages for a session with none recorded

- **GIVEN** a charge session record belonging to an account, with no verified battery
  percentages recorded
- **WHEN** a correction for that account supplies both a start and an end percentage
- **THEN** the record's start and end percentages match the supplied values
- **AND** the record's provenance shows the percentages were verified by a human

#### Scenario: A human records only the start percentage

- **GIVEN** a charge session record belonging to an account, with no verified battery
  percentages recorded
- **WHEN** a correction for that account supplies only a start percentage
- **THEN** the record's start percentage matches the supplied value
- **AND** the record's end percentage remains absent
- **AND** the record's provenance shows the percentage was verified by a human

#### Scenario: A human records only the end percentage

- **GIVEN** a charge session record belonging to an account, with no verified battery
  percentages recorded
- **WHEN** a correction for that account supplies only an end percentage
- **THEN** the record's end percentage matches the supplied value
- **AND** the record's start percentage remains absent
- **AND** the record's provenance shows the percentage was verified by a human

#### Scenario: A human clears both previously recorded percentages

- **GIVEN** a charge session record belonging to an account, with both a start and an end
  percentage already recorded and their provenance shown as verified by a human
- **WHEN** a correction for that account supplies neither percentage
- **THEN** the record's start and end percentages are both absent
- **AND** the record's provenance is also absent

#### Scenario: A percentage outside the valid range is rejected

- **GIVEN** a charge session record belonging to an account
- **WHEN** a correction for that account supplies a percentage outside zero to one hundred
- **THEN** the correction is rejected
- **AND** the record's percentages and provenance remain exactly as they were before the
  correction was attempted

#### Scenario: A correction never alters the session's other facts

- **GIVEN** a charge session record belonging to an account, carrying a charging site,
  energy delivered, cost, currency, payment status, and frozen estimated percentages
- **WHEN** a correction for that account changes the record's verified percentages
- **THEN** the record's charging site, energy delivered, cost, currency, payment status,
  and frozen estimated percentages are unchanged
- **AND** the record's identity and time window are unchanged

#### Scenario: A correction to a session belonging to a different account is rejected

- **GIVEN** a charge session record belonging to one account
- **WHEN** a correction scoped to a different account names that record
- **THEN** the correction is rejected
- **AND** the record is unaffected

#### Scenario: A correction to a session that does not exist is rejected the same way as a wrong-account correction

- **GIVEN** no charge session record exists for a given record identifier
- **WHEN** a correction for any account names that identifier
- **THEN** the correction is rejected
- **AND** the rejection is indistinguishable from a correction naming a record that exists
  under a different account

#### Scenario: A successful correction returns the record's full current detail

- **GIVEN** a charge session record belonging to an account
- **WHEN** a correction for that account successfully changes the record's verified
  percentages
- **THEN** the party performing the correction receives the record's full current detail,
  including the change just made, without a separate retrieval

### Requirement: Charge Sessions Are Retrievable For A Vehicle By Recency Of Update

The charging capability SHALL provide a way to retrieve every charge session record
belonging to a specific vehicle within an account that has been created or modified at or
after a caller-specified instant. The retrieved records SHALL be ordered by their stop
instant, earliest first. A retrieval that matches no record SHALL return an empty result,
never an absence or an error.

A correction made to a record's verified battery percentages SHALL count as a modification
for the purpose of this retrieval, even when the record's charging time window is
unchanged.

A record whose vehicle is not currently registered to the account SHALL NOT be returned by
this retrieval, for any vehicle requested, even though the record itself continues to exist
and to be retained.

#### Scenario: A record modified after being first recorded becomes visible to a later cursor
- **GIVEN** a charge session record first recorded at one instant
- **AND** its verified battery percentages later corrected at a subsequent instant
- **WHEN** that vehicle's sessions are retrieved for modifications at or after an instant
  between the two
- **THEN** the record is included in the result
- **AND** the retrieved record carries the corrected percentages

#### Scenario: A record modified at exactly the requested instant is included
- **GIVEN** a charge session record last modified at a given instant
- **WHEN** that vehicle's sessions are retrieved for modifications at or after that exact
  instant
- **THEN** the record is included in the result

#### Scenario: A record modified before the requested instant is excluded
- **GIVEN** a charge session record last modified strictly before a given instant
- **WHEN** that vehicle's sessions are retrieved for modifications at or after that instant
- **THEN** the record is NOT included in the result

#### Scenario: No matching record returns an empty result
- **GIVEN** a vehicle with no charge session record modified at or after a given instant
- **WHEN** that vehicle's sessions are retrieved for modifications at or after that instant
- **THEN** the result is empty
- **AND** the retrieval succeeds, rather than failing or returning an absence

#### Scenario: Results are ordered earliest-stop-first
- **GIVEN** a vehicle with multiple charge session records modified at or after a given
  instant, with different stop instants
- **WHEN** that vehicle's sessions are retrieved for modifications at or after that instant
- **THEN** the records are ordered by their stop instant, earliest first

#### Scenario: A different account's record does not leak
- **GIVEN** two accounts, each holding a charge session record for the same vehicle
  identifier, both modified at or after a given instant
- **WHEN** one account's sessions for that vehicle are retrieved for modifications at or
  after that instant
- **THEN** only that account's record is included

#### Scenario: A session whose vehicle is no longer currently registered is never retrieved by vehicle
- **GIVEN** a charge session record whose vehicle is no longer currently registered to the
  account, modified at or after a given instant
- **WHEN** any vehicle's sessions are retrieved for modifications at or after that instant
- **THEN** the record is not included in the result for any vehicle
- **AND** the record itself continues to exist and to be retained

### Requirement: Charge Sessions Are Retrievable For A Vehicle By Recency Of Occurrence, Bounded By Count

The charging capability SHALL provide a way to retrieve the most recent charge session
records belonging to a specific vehicle within an account, up to a caller-specified count,
ordered by their stop instant, most recent first. When the caller specifies no positive
count, the capability SHALL apply its own default count. A retrieval that matches no record
SHALL return an empty result, never an absence or an error.

A record whose vehicle is not currently registered to the account SHALL NOT be returned by
this retrieval, for any vehicle requested, even though the record itself continues to exist
and to be retained.

#### Scenario: Retrieval returns the most recent records up to the requested count, newest first
- **GIVEN** a vehicle with more charge session records than a requested count
- **WHEN** that vehicle's sessions are retrieved for that count
- **THEN** exactly that many records are returned
- **AND** they are the records with the most recent stop instants
- **AND** they are ordered by stop instant, most recent first

#### Scenario: A requested count larger than the available records returns every record, still newest first
- **GIVEN** a vehicle with fewer charge session records than a requested count
- **WHEN** that vehicle's sessions are retrieved for that count
- **THEN** every one of the vehicle's records is returned
- **AND** they are ordered by stop instant, most recent first

#### Scenario: An unspecified or non-positive count applies the capability's own default
- **GIVEN** a vehicle with charge session records
- **WHEN** that vehicle's sessions are retrieved with no positive count specified
- **THEN** the capability applies its own default count
- **AND** the returned records are ordered by stop instant, most recent first

#### Scenario: No matching record returns an empty result
- **GIVEN** a vehicle with no charge session record
- **WHEN** that vehicle's sessions are retrieved for any count
- **THEN** the result is empty
- **AND** the retrieval succeeds, rather than failing or returning an absence

#### Scenario: A different vehicle's record within the same account does not leak
- **GIVEN** two charge session records within one account, belonging to two different
  vehicles
- **WHEN** one vehicle's sessions are retrieved for a count covering both records
- **THEN** only that vehicle's record is included

#### Scenario: A different account's record does not leak
- **GIVEN** two accounts, each holding a charge session record for the same vehicle
  identifier
- **WHEN** one account's sessions for that vehicle are retrieved for a count covering both
  records
- **THEN** only that account's record is included

#### Scenario: A session whose vehicle is no longer currently registered is never retrieved by vehicle
- **GIVEN** a charge session record whose vehicle is no longer currently registered to the
  account
- **WHEN** any vehicle's sessions are retrieved for any count
- **THEN** the record is not included in the result for any vehicle
- **AND** the record itself continues to exist and to be retained

### Requirement: Inferred Pack Capacity Is Recorded On Every Charge Session

The charging capability SHALL record, for each charge session record, the vehicle pack
capacity in kilowatt-hours implied by that session alone — the energy delivered divided by
the fraction of the pack that the session's battery percentages say was replenished.

The capability SHALL keep this recorded value correct at all times, through **both** of the
independent paths that change the values it derives from: it SHALL be brought up to date
when the session's energy delivered is refreshed as the source's fees settle, and it SHALL
be brought up to date when a human corrects the session's verified battery percentages. No
caller of either path SHALL have to supply, request, or trigger the update, and neither
path SHALL be able to leave the recorded value stale. It SHALL also be present for every
session record that already existed before the capability gained the value.

The recorded value SHALL be derived, never supplied. A caller SHALL NOT be able to set,
override, or corrupt it. In particular, the synchronization path that mirrors collected
sessions SHALL have no means of writing it, exactly as it has no means of writing the
verified battery percentages.

The capability SHALL record the value **only** when the session record carries all three of
the values it derives from — the energy delivered, the starting battery percentage, and the
ending battery percentage — **and** the ending battery percentage is strictly greater than
the starting one. In every other case the capability SHALL record the absence of a value.
Recording the absence of a value SHALL NOT be an error, SHALL NOT prevent the session record
from being created or refreshed, and in particular SHALL NOT cause a synchronization pass to
fail or to abandon the other sessions in the same pass.

#### Scenario: A session with energy and verified percentages records its inferred capacity
- **GIVEN** a charge session record with 52.273 kWh delivered, a verified starting battery
  percentage of 29, and a verified ending battery percentage of 100
- **WHEN** the record is read
- **THEN** the record's recorded inferred pack capacity is 73.624 kWh

#### Scenario: A session with no energy figure records no capacity
- **GIVEN** a charge session record that had no kWh fee and therefore carries no energy
  delivered, but does carry verified starting and ending battery percentages
- **WHEN** the record is read
- **THEN** the record records no inferred pack capacity

#### Scenario: A session with no verified percentages records no capacity
- **GIVEN** a collected Supercharger session for which nobody has verified any battery
  percentage
- **WHEN** the charge session records are synchronized
- **THEN** the record exists carrying its time window, site, energy, cost, currency and
  payment facts
- **AND** the record records no inferred pack capacity

#### Scenario: A session whose battery percentage did not change records no capacity
- **GIVEN** a charge session record whose verified starting and ending battery percentages
  are equal
- **WHEN** the record is read
- **THEN** the record records no inferred pack capacity
- **AND** neither recording nor refreshing that session failed

#### Scenario: A session whose battery percentage decreased records no capacity
- **GIVEN** a charge session record whose verified ending battery percentage is lower than
  its verified starting battery percentage
- **WHEN** the record is read
- **THEN** the record records no inferred pack capacity
- **AND** no negative capacity is recorded

#### Scenario: Verifying a session's battery percentages produces its inferred capacity
- **GIVEN** a charge session record with 41.31 kWh delivered and no verified battery
  percentages, recording no inferred pack capacity
- **WHEN** a human corrects that session's battery percentages to a start of 18 and an end
  of 80
- **THEN** the record's recorded inferred pack capacity becomes 66.629 kWh
- **AND** the correction operation reports that value back without a further read
- **AND** the human supplied only the two percentages

#### Scenario: A refreshed energy figure updates the inferred capacity
- **GIVEN** a charge session record with 41.31 kWh delivered and verified battery
  percentages of 18 and 80, recording an inferred pack capacity of 66.629 kWh
- **WHEN** the charge session records are synchronized again and that session's energy
  delivered is refreshed to 44.64 kWh
- **THEN** the record's recorded inferred pack capacity becomes 72.000 kWh

#### Scenario: A re-synchronization cannot overwrite the inferred capacity
- **GIVEN** a charge session record recording an inferred pack capacity
- **WHEN** the charge session records are synchronized again with the session's energy,
  cost, currency, payment status and registered vehicle identifier unchanged
- **THEN** the recorded inferred pack capacity is unchanged
- **AND** the synchronization path had no means of supplying a value for it

#### Scenario: An extreme energy figure does not fail a synchronization pass
- **GIVEN** a collected Supercharger session whose energy delivered is arbitrarily large,
  alongside other sessions in the same synchronization pass
- **WHEN** the charge session records are synchronized
- **THEN** the pass succeeds
- **AND** every session in the pass is recorded, including that one
- **AND** that session's inferred pack capacity is recorded rather than rejected

#### Scenario: Sessions that existed before the value was introduced carry it
- **GIVEN** charge session records that were recorded before the capability recorded
  inferred pack capacity, some carrying energy delivered together with an increasing pair
  of verified battery percentages and some not
- **WHEN** the capability begins recording inferred pack capacity
- **THEN** every such record carrying all three values with an increasing battery percentage
  records its inferred pack capacity
- **AND** every other such record records no inferred pack capacity
- **AND** no caller had to request, trigger, or perform this

#### Scenario: The recorded capacity is returned with the session
- **GIVEN** a charge session record recording an inferred pack capacity
- **WHEN** that record is retrieved by any of the capability's session retrieval operations
- **THEN** the retrieved record carries its recorded inferred pack capacity
- **AND** a record recording no inferred pack capacity is retrieved carrying its absence,
  not a zero

