# charge-session-log Specification Delta

## MODIFIED Requirements

### Requirement: Charge Sessions Are Retrievable For A Vehicle By Recency Of Update

The charging capability SHALL provide a way to retrieve every charge session record
belonging to a specific vehicle within an account that has been created or modified at or
after a caller-specified instant. The retrieved records SHALL be ordered by their stop
instant, earliest first. A retrieval that matches no record SHALL return an empty result,
never an absence or an error.

A correction made to a record's verified battery percentages SHALL count as a modification
for the purpose of this retrieval, even when the record's charging time window is
unchanged.

**A synchronization pass that leaves every one of a record's synchronized facts
unchanged SHALL NOT count as a modification for the purpose of this retrieval — even
though the synchronization still ran and still touched the record.** Only a
synchronization that actually changes at least one synchronized fact (the site's
energy, cost, currency or payment facts, or the registered vehicle identifier) SHALL
count as a modification. This is what lets a caller of this retrieval trust that "no
result" means "nothing worth recalculating," not "nobody has synchronized since."

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

#### Scenario: A synchronization that changes nothing does not make a record newly visible
- **GIVEN** a charge session record last modified at a given instant, whose energy,
  cost, currency, payment status and registered vehicle identifier are already
  identical to what the source currently reports
- **WHEN** the charge session records are synchronized again, and afterward that
  vehicle's sessions are retrieved for modifications at or after an instant strictly
  after the given instant but before the new synchronization
- **THEN** the record is NOT included in the result
- **AND** the record itself, and its data, are unchanged by the synchronization

#### Scenario: A synchronization that changes one fact makes a record newly visible
- **GIVEN** a charge session record last modified at a given instant
- **WHEN** the charge session records are synchronized again and the source now
  reports a different energy delivered, total cost, currency, payment status, or
  registered vehicle identifier for that session
- **AND** that vehicle's sessions are retrieved for modifications at or after an
  instant strictly after the given instant
- **THEN** the record IS included in the result
- **AND** the retrieved record carries the changed value

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

**Replacing a value with an identical value is not a change.** When a synchronization
writes the same energy delivered, total cost, currency, and payment status the record
already carries, that write SHALL NOT count as a modification of the record — see
"Charge Sessions Are Retrievable For A Vehicle By Recency Of Update." A record's
human-verified battery percentages and lifecycle status SHALL play no part in this
comparison in either direction: this capability's synchronization SHALL neither read
nor be influenced by them, so a human correction is never mistaken for a
synchronization change, and a synchronization pass is never mistaken for a human
correction.

**The source reporting a different value for the charging site — a fact this
capability never updates after first recording — SHALL NOT count as a modification
either, even though the source and the stored record now disagree.** The record
keeps its original site name, and the disagreement itself is not treated as new
information, on every later synchronization, not just the first one where it
appears.

#### Scenario: The charging site is recorded once and never changes
- **GIVEN** a record already exists for an account and session identifier, carrying a
  charging site name
- **WHEN** the charge session records are synchronized again for that session
- **THEN** the record's charging site name is unchanged, even if the source now reports a
  different name for that session

#### Scenario: A source disagreement on the charging site does not count as a modification, on any night
- **GIVEN** a record whose charging site name the source now reports differently,
  while the record's energy delivered, total cost, currency, payment status, and
  registered vehicle identifier all already match the source
- **WHEN** the charge session records are synchronized again, twice in a row, and
  afterward that vehicle's sessions are retrieved for modifications at or after an
  instant before either synchronization
- **THEN** the record is NOT included in the result after either synchronization
- **AND** the record's charging site name still reads its original value after both

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

#### Scenario: A verified session's data does not look like it changed on an unchanged re-synchronization
- **GIVEN** a record whose battery percentages have been human-verified and whose
  energy, cost, currency, payment status and registered vehicle identifier already
  match the source
- **WHEN** the charge session records are synchronized again with no real change on
  either side
- **THEN** the record's verified percentages and lifecycle status are unchanged
- **AND** the synchronization does not count as a modification of the record

### Requirement: Registered Vehicle Identifier Is Refreshed, VIN Is Not

Each charge session record SHALL carry the vehicle identification number as a durable,
write-once value, and the currently registered vehicle identifier as a value refreshed on
every synchronization. The registered vehicle identifier SHALL be absent when the
session's vehicle identification number does not belong to any vehicle currently
registered to the account.

**Refreshing the registered vehicle identifier to the same value it already holds is
not a change.** Only a synchronization that actually changes this value — including
from absent to present, which SHALL count as a change — makes the record a
modification for the purpose of "Charge Sessions Are Retrievable For A Vehicle By
Recency Of Update."

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

#### Scenario: A vehicle that reappears in the account's registration makes its record visible again
- **GIVEN** a record whose registered vehicle identifier is absent
- **WHEN** the charge session records are synchronized and that session's vehicle
  identification number now belongs to a currently registered vehicle
- **THEN** the record's registered vehicle identifier is set to that value
- **AND** this counts as a modification of the record for "Charge Sessions Are
  Retrievable For A Vehicle By Recency Of Update"
