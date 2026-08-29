## ADDED Requirements

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
