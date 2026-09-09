## MODIFIED Requirements

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

## ADDED Requirements

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
