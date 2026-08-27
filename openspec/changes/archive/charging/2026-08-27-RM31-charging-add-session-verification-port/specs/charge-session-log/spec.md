## ADDED Requirements

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
