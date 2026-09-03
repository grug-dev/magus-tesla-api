## MODIFIED Requirements

### Requirement: Battery Percentage Verification On A Charge Session

Each charge session record SHALL carry an optional verified battery percentage at charge
start and at charge stop, and an optional provenance stating where those percentages came
from. **This is a CHANGE from the prior revision of this requirement, under which each
record also carried an optional frozen pair of estimated percentages captured at the
moment of verification — that frozen pair is REMOVED by this revision
(`RM41-charging-drop-estimate-columns`), because no code path in the platform has ever
written it and the estimator that was originally intended to eventually populate it
(`derivedStartBatteryPct`) instead writes the real verified start percentage.** Any
recorded percentage SHALL be between 0 and 100 inclusive. A recorded provenance SHALL be
either "user verified" or "polled". A record carrying either verified percentage SHALL
also carry a provenance.

Synchronizing charge session records SHALL never write, clear, or overwrite any of these
three values — including when the same synchronization pass updates that same record's
registered vehicle identifier or its energy, cost, currency or payment facts.

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
- **GIVEN** a charge session record whose verified percentages and provenance have been
  recorded
- **WHEN** the charge session records are synchronized again for that session, including
  when its registered vehicle identifier, energy delivered, cost, currency or payment
  status have changed
- **THEN** all three values are unchanged

### Requirement: Charge Sessions Are Retrievable For A Vehicle Within A Time Window

The charging capability SHALL provide a way to retrieve every charge session record
belonging to a specific vehicle within an account whose stop instant falls within a
caller-specified window of whole calendar days, inclusive of every instant of the
window's first day and every instant of its last day. The retrieved records SHALL be
ordered by their stop instant, earliest first. A retrieval that matches no record SHALL
return an empty result, never an absence or an error.

Each retrieved record SHALL carry every fact the capability holds for that session,
including the session's charging site, energy delivered, cost, currency, payment status,
and its optional verified battery percentages and their provenance — so that a caller
needs no further request to another capability to describe the session completely. **This
is a CHANGE from the prior revision of this requirement, under which a retrieved record
also carried a frozen estimated pair alongside the verified percentages and their
provenance — the frozen pair is REMOVED by this revision
(`RM41-charging-drop-estimate-columns`), because the capability no longer stores it (see
"Battery Percentage Verification On A Charge Session" above).**

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
  currency, payment status, and verified battery percentages with their provenance
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
only one of the two percentages SHALL be accepted. A correction that supplies only a start
percentage, leaving the end percentage unsupplied, SHALL record the end percentage as
absent; the capability SHALL NOT derive an end percentage under any circumstance.

A correction that supplies only an end percentage, leaving the start percentage unsupplied,
SHALL have its start percentage derived from the record's energy delivered and the supplied
end percentage, using the vehicle's pack capacity, WHENEVER the record's energy delivered is
present AND that derivation yields a percentage within the inclusive range zero to one
hundred. In every other case — the record carries no energy delivered, or the derivation
would yield a percentage outside that range — the start percentage SHALL be recorded as
absent, exactly as before this behavior existed. In neither case SHALL the correction be
rejected or fail: a derivation that cannot produce a valid percentage records the absence of
one, silently, the same way an unsupplied percentage always has.

A correction that supplies neither percentage SHALL clear both percentages and their
provenance to absent, as a single outcome of that one correction — this SHALL NOT trigger a
derivation.

A correction SHALL NEVER derive, override, or alter a start percentage the correction itself
supplies. Only a start percentage the correction leaves unsupplied is ever a candidate for
derivation.

Whenever a correction results in at least one percentage being present — whether supplied
directly or derived — the record's provenance SHALL be recorded as verified by a human. The
capability SHALL NOT accept provenance as an input to a correction, and SHALL NOT distinguish
a derived percentage from a directly supplied one in the recorded provenance — provenance is
always derived from whether a percentage is present, never from how it came to be present.

Each percentage the correction itself supplies SHALL be within the inclusive range zero to
one hundred; a correction that supplies such a percentage outside that range SHALL be
rejected, and the record SHALL remain exactly as it was before the rejected correction. This
range validity check applies only to a percentage the correction supplies directly — a start
percentage the capability derives that would fall outside that range is never a rejection
cause; it is simply not recorded (above).

A correction SHALL change nothing about the record other than its start percentage, end
percentage, and provenance — every other fact the record carries about the session (its
identity, its time window, its charging site, its energy, cost, currency and payment facts)
SHALL be unaffected by any correction, no matter how many times a correction is performed,
and regardless of whether a start percentage was supplied or derived. **This is a CHANGE
from the prior revision of this requirement, under which that list of unaffected facts also
named the record's frozen estimated percentages — REMOVED by this revision
(`RM41-charging-drop-estimate-columns`) because the capability no longer stores them.**

A correction that names a record that does not exist, or that names a record belonging to a
different account than the one the correction is scoped to, SHALL be rejected in the same way
in both cases, and the record (if one exists) SHALL be unaffected.

A successful correction SHALL make the record's full current detail available to whoever
performed it, without requiring a separate retrieval — including a start percentage the
correction itself derived.

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

#### Scenario: A human records only the end percentage, and a start percentage can be derived

- **GIVEN** a charge session record belonging to an account, with no verified battery
  percentages recorded and an energy delivered figure present
- **WHEN** a correction for that account supplies only an end percentage, and the vehicle's
  pack capacity together with the record's energy delivered and the supplied end percentage
  yields a start percentage within zero to one hundred
- **THEN** the record's end percentage matches the supplied value
- **AND** the record's start percentage is recorded as the derived value
- **AND** the record's provenance shows the percentage was verified by a human
- **AND** the party performing the correction did not have to compute or supply the start
  percentage themselves

#### Scenario: A human records only the end percentage, and no start percentage can be derived

- **GIVEN** a charge session record belonging to an account, with no verified battery
  percentages recorded and no energy delivered figure present for the session
- **WHEN** a correction for that account supplies only an end percentage
- **THEN** the correction succeeds
- **AND** the record's end percentage matches the supplied value
- **AND** the record's start percentage remains absent
- **AND** the record's provenance shows the percentage was verified by a human

#### Scenario: A derived start percentage outside the valid range is recorded as absent, not rejected

- **GIVEN** a charge session record belonging to an account, with no verified battery
  percentages recorded and an energy delivered figure present
- **WHEN** a correction for that account supplies only an end percentage, and the vehicle's
  pack capacity together with the record's energy delivered and the supplied end percentage
  would yield a percentage outside zero to one hundred
- **THEN** the correction succeeds
- **AND** the record's end percentage matches the supplied value
- **AND** the record's start percentage remains absent
- **AND** no error is reported to the party performing the correction

#### Scenario: A supplied start percentage is never overridden by derivation

- **GIVEN** a charge session record belonging to an account
- **WHEN** a correction for that account supplies both a start percentage and an end
  percentage
- **THEN** the record's start percentage matches exactly the value the correction supplied
- **AND** no derivation is attempted, regardless of the record's energy delivered figure

#### Scenario: A human clears both previously recorded percentages

- **GIVEN** a charge session record belonging to an account, with both a start and an end
  percentage already recorded and their provenance shown as verified by a human
- **WHEN** a correction for that account supplies neither percentage
- **THEN** the record's start and end percentages are both absent
- **AND** the record's provenance is also absent
- **AND** no derivation is attempted

#### Scenario: A percentage outside the valid range that was supplied directly is rejected

- **GIVEN** a charge session record belonging to an account
- **WHEN** a correction for that account supplies a percentage outside zero to one hundred
- **THEN** the correction is rejected
- **AND** the record's percentages and provenance remain exactly as they were before the
  correction was attempted

#### Scenario: A correction never alters the session's other facts, whether a start percentage was supplied or derived

- **GIVEN** a charge session record belonging to an account, carrying a charging site,
  energy delivered, cost, currency, and payment status
- **WHEN** a correction for that account changes the record's verified percentages, whether
  the resulting start percentage was supplied directly or derived
- **THEN** the record's charging site, energy delivered, cost, currency, and payment status
  are unchanged
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

#### Scenario: A successful correction returns the record's full current detail, including a derived start percentage

- **GIVEN** a charge session record belonging to an account
- **WHEN** a correction for that account successfully changes the record's verified
  percentages, deriving a start percentage in the process
- **THEN** the party performing the correction receives the record's full current detail,
  including the derived start percentage, without a separate retrieval
