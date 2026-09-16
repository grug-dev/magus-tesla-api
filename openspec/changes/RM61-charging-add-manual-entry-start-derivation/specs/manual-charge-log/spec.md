## ADDED Requirements

### Requirement: A missing starting battery percentage may be derived on write

The manual-charge-log capability SHALL accept a charge entry that carries no starting battery
percentage.

When a caller supplies no starting percentage **and** the entry carries an ending battery
percentage **and** the entry's energy added is known — whether supplied by the caller or itself
derived — the capability SHALL derive the starting percentage from the vehicle's pack capacity,
the energy added, and the ending percentage, using the same derivation the capability already
applies to a Supercharger session missing its own starting percentage. It SHALL store the
derived value at the moment the entry is written, never computing it again on read. When the
derivation cannot produce a value within the valid percentage range, the capability SHALL leave
the starting percentage absent rather than storing an incorrect number. In every other case the
capability SHALL store exactly what the caller supplied, including nothing at all.

A starting battery percentage the caller actually supplied SHALL NEVER be recomputed or
overwritten, under any condition — even when a derivation would otherwise be possible from the
entry's other fields. Clearing a previously stored starting percentage is the only way a caller
can ask for it to be derived again.

The capability SHALL record, alongside the starting percentage, whether it came from the person
or was derived. That provenance SHALL always be determined by the capability itself and SHALL
NOT be settable by any caller; a provenance supplied by a caller SHALL be ignored. The provenance
SHALL be absent whenever the starting percentage itself is absent, since an absent value has no
provenance to record.

Every charge entry that existed before the capability gained this provenance SHALL have its
provenance recorded from its own already-stored starting percentage: an entry that already
carried a starting percentage SHALL be recorded as coming from the person, because no derivation
existed before the capability gained this behavior. An entry with no starting percentage SHALL
have no recorded provenance.

#### Scenario: An entry with no starting percentage and no ending percentage is stored as-is
- **GIVEN** an authenticated user logging a charge that has not finished
- **WHEN** they store the entry supplying no starting percentage and no ending percentage
- **THEN** the entry is stored recording no starting percentage
- **AND** the entry records no starting-percentage provenance

#### Scenario: The starting percentage is derived from the energy added and the ending percentage
- **GIVEN** an authenticated user who supplies no starting percentage, an ending percentage of
  74, and an energy added of 6.20 kWh, for a vehicle whose pack capacity is 62 kWh
- **WHEN** the entry is stored
- **THEN** the stored starting percentage is 64
- **AND** the recorded starting-percentage provenance is "derived"

#### Scenario: No derivation happens when the entry's energy is also unknown
- **GIVEN** an authenticated user who supplies no starting percentage, an ending percentage, but
  no energy added and no other field from which the energy itself could be derived
- **WHEN** the entry is stored
- **THEN** the entry is stored recording no starting percentage
- **AND** the entry records no starting-percentage provenance

#### Scenario: A supplied starting percentage is never overwritten, even when a derivation would
  otherwise succeed
- **GIVEN** an authenticated user who supplies a starting percentage together with an ending
  percentage and an energy added, such that a derivation from the ending percentage and the
  energy would produce a different value
- **WHEN** the entry is stored
- **THEN** the stored starting percentage is exactly what the caller supplied
- **AND** the recorded starting-percentage provenance is "from the person"

#### Scenario: The derivation runs whether the energy was typed or itself derived
- **GIVEN** an authenticated user who supplies no starting percentage and no energy added, but
  supplies a starting-adjacent pair of fields that let the capability derive the energy first
- **WHEN** the entry is stored
- **THEN** the starting percentage is derived from that same energy value
- **AND** the recorded starting-percentage provenance is "derived"

#### Scenario: A caller cannot set the starting-percentage provenance directly
- **GIVEN** a caller that supplies no starting percentage, an ending percentage, and an energy
  added, and also asserts the starting-percentage provenance as "from the person"
- **WHEN** the entry is stored
- **THEN** the recorded starting-percentage provenance is "derived"

#### Scenario: Clearing a stored starting percentage lets it be derived again on an edit
- **GIVEN** an existing entry whose starting percentage was supplied by the person
- **WHEN** the user edits it, removing the starting percentage while keeping the ending
  percentage and the energy added
- **THEN** the stored starting percentage is derived from the ending percentage and the energy
- **AND** the recorded starting-percentage provenance becomes "derived"

#### Scenario: An out-of-range derivation leaves the starting percentage absent
- **GIVEN** an authenticated user who supplies no starting percentage, together with an ending
  percentage and an energy added whose implied starting percentage falls outside the valid
  percentage range
- **WHEN** the entry is stored
- **THEN** the entry is stored recording no starting percentage
- **AND** the entry records no starting-percentage provenance

#### Scenario: Entries that pre-date this provenance are recorded from their own starting
  percentage
- **GIVEN** charge entries that were stored before the capability recorded starting-percentage
  provenance
- **WHEN** those entries are read
- **THEN** every entry that already had a starting percentage has its provenance recorded as
  "from the person"
- **AND** every entry with no starting percentage has no recorded provenance
