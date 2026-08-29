## ADDED Requirements

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
