## ADDED Requirements

### Requirement: Charge Gap Ledger
The telemetry capability SHALL provide a durable ledger recording, per vehicle-day, that a
charge record is missing or incomplete — a signal computed by a companion derived-metrics
capability, not by telemetry itself. Each ledger row SHALL identify the owning account, the
vehicle (its Tesla id and VIN), the flagged calendar day, and which charge source is
suspected missing for that day (a manual charge entry not captured by the vehicle API, or a
Supercharger session missing its battery-percentage readings). There SHALL be at most one
ledger row per account/vehicle/day: a day's shortfall is a single aggregate observation and
is never split across multiple rows for the same day.

The ledger SHALL expose a write operation that reconciles a caller-supplied set of
currently-flagged days against a caller-supplied date window for one vehicle: every flagged
day in the set SHALL be stored (inserted if new, or updated in place if its suspected source
changed since a prior reconciliation), and every previously-stored day within that same
window that is absent from the newly-supplied set SHALL be removed from the ledger. The
ledger SHALL NOT retain any record of a removed day — there is no resolved/soft-deleted
state, only present (still flagged) or absent (not flagged, or never flagged). Days outside
the reconciled window SHALL be unaffected by a reconciliation call, regardless of their own
flagged state. A reconciliation call SHALL either fully apply (every insert, update, and
removal it makes) or have no effect at all.

The ledger SHALL reject a reconciliation call, without applying any part of it, if any
supplied flagged day does not belong to the call's own account and vehicle, or if any
supplied flagged day's date falls outside the call's own window.

#### Scenario: A newly-flagged day is stored
- **GIVEN** no ledger row exists for a given account, vehicle, and day
- **WHEN** a reconciliation is performed for a window containing that day, with the day
  present in the flagged set and attributed to a specific suspected charge source
- **THEN** the ledger stores exactly one row for that account, vehicle, and day
- **AND** the stored row's suspected charge source matches what was supplied

#### Scenario: Re-flagging the same day on a later reconciliation does not duplicate it
- **GIVEN** a ledger row already exists for a given account, vehicle, and day
- **WHEN** a later reconciliation is performed for a window containing that day, with the
  day still present in the flagged set
- **THEN** the ledger still contains exactly one row for that account, vehicle, and day
- **AND** the row's suspected charge source reflects the later reconciliation's value

#### Scenario: A day that stops flagging is removed from the ledger
- **GIVEN** a ledger row exists for a given account, vehicle, and day, within some window
- **WHEN** a reconciliation is performed for that same window, with the day absent from the
  flagged set
- **THEN** the ledger no longer contains any row for that account, vehicle, and day
- **AND** no trace of the removed day (such as a resolved marker) remains in the ledger

#### Scenario: An empty flagged set clears every previously-flagged day in the window
- **GIVEN** the ledger holds multiple flagged days for a vehicle within a window
- **WHEN** a reconciliation is performed for that window with an empty flagged set
- **THEN** the ledger contains no rows for that vehicle within that window afterward

#### Scenario: Reconciliation only affects the reconciled window
- **GIVEN** a ledger row exists for a vehicle on a day OUTSIDE a window about to be
  reconciled
- **WHEN** a reconciliation is performed for that window, regardless of what its flagged set
  contains
- **THEN** the row outside the window is unaffected — neither removed nor altered

#### Scenario: A reconciliation attempting to write another account or vehicle's day is rejected entirely
- **GIVEN** a reconciliation call scoped to one account and vehicle
- **WHEN** its flagged set contains an entry belonging to a different account or a different
  vehicle
- **THEN** the reconciliation is rejected
- **AND** no part of that call's flagged set is stored, including entries that were
  correctly scoped

#### Scenario: A reconciliation attempting to flag a day outside its own window is rejected entirely
- **GIVEN** a reconciliation call scoped to a specific window
- **WHEN** its flagged set contains an entry whose day falls outside that window
- **THEN** the reconciliation is rejected
- **AND** no part of that call's flagged set is stored

#### Scenario: Ledger rows for different vehicles are independent
- **GIVEN** two vehicles belonging to the same account, each with a ledger row for the same
  calendar day
- **WHEN** a reconciliation is performed for one vehicle that removes its row for that day
- **THEN** the other vehicle's ledger row for the same day is unaffected

#### Scenario: Ledger rows for different accounts are independent
- **GIVEN** two accounts, each with a vehicle carrying a ledger row for the same calendar
  day
- **WHEN** a reconciliation is performed for one account's vehicle
- **THEN** the other account's ledger row is unaffected, regardless of overlapping days or
  vehicle identifiers

## MODIFIED Requirements

### Requirement: Supercharger Session Read Port
The telemetry capability SHALL expose a read port that allows callers (the gateway, other
consumers) to retrieve Supercharger sessions without accessing the telemetry database
directly. The port SHALL provide three read methods: one returning all sessions for a given
account (scoped to the account, ordered newest-first, with a limit), one returning sessions
for a specific vehicle within an account (also ordered newest-first, with a limit), and one
returning sessions for a specific vehicle within an account whose charge stop time falls
within a caller-supplied date window (ordered oldest-first, with no limit — the window
itself bounds the result). Callers SHALL receive an empty non-nil result when no sessions
exist for the given scope. Every returned session SHALL carry its battery-percentage
verification trio (start percentage, end percentage, source label) exactly as stored — NULL
when no override has been set for that session — and its verification-time snapshot pair
(start percentage estimate, end percentage estimate) exactly as stored — NULL when no
verification has occurred for that session.

The date-windowed method SHALL determine whether a session belongs to the requested window
by comparing the session's charge stop time against the window, regardless of when the
session started — a session that began before the window but whose charging finished within
it SHALL be included, because the charge stop time is what the session's energy and ending
battery percentage are anchored to. The window's end boundary SHALL be treated as inclusive
of the entire final calendar day, not merely its first instant.

#### Scenario: Sessions are returned for an account ordered newest-first
- **GIVEN** an account with multiple Supercharger sessions stored across different dates
- **WHEN** the caller requests sessions for that account
- **THEN** all sessions belonging to that account are returned
- **AND** the sessions are ordered by charge start time, newest first
- **AND** the result is limited to the requested number of rows

#### Scenario: Sessions are returned for a specific vehicle ordered newest-first
- **GIVEN** an account with two vehicles, each having stored Supercharger sessions
- **WHEN** the caller requests sessions for one specific vehicle within that account
- **THEN** only sessions matching both the account id and the vehicle's Tesla id are
  returned
- **AND** the sessions are ordered by charge start time, newest first
- **AND** no sessions belonging to the other vehicle appear in the result

#### Scenario: Empty result when no sessions exist
- **GIVEN** an account with a valid Tesla connection but no stored Supercharger sessions
- **WHEN** the caller requests sessions for that account
- **THEN** an empty collection is returned
- **AND** no error is returned

#### Scenario: A returned session carries its battery-percentage verification trio
- **GIVEN** a stored session whose battery-percentage verification trio has been set to a
  specific start percentage, end percentage, and source label
- **WHEN** the caller requests sessions for that session's account, or for that session's
  specific vehicle
- **THEN** the returned session's start percentage, end percentage, and source label match
  the stored values exactly

#### Scenario: A returned session with no override has a NULL battery-percentage verification trio
- **GIVEN** a stored session whose battery-percentage verification trio has never been set
- **WHEN** the caller requests sessions for that session's account, or for that session's
  specific vehicle
- **THEN** the returned session's start percentage, end percentage, and source label are
  all NULL

#### Scenario: A returned session carries its verification-time snapshot pair
- **GIVEN** a stored session whose verification-time snapshot pair has been set to a
  specific start percentage estimate and end percentage estimate
- **WHEN** the caller requests sessions for that session's account, or for that session's
  specific vehicle
- **THEN** the returned session's snapshot start percentage estimate and end percentage
  estimate match the stored values exactly, independent of whatever a companion estimation
  capability's live computation would currently produce for that session

#### Scenario: A returned session with no verification has a NULL verification-time snapshot pair
- **GIVEN** a stored session whose verification-time snapshot pair has never been set
- **WHEN** the caller requests sessions for that session's account, or for that session's
  specific vehicle
- **THEN** the returned session's snapshot start percentage estimate and end percentage
  estimate are both NULL

#### Scenario: Sessions within a date window are returned ordered oldest-first
- **GIVEN** a vehicle with several stored Supercharger sessions whose charge stop times span
  more than the requested window
- **WHEN** the caller requests sessions for that vehicle within a specific start/end date
  window
- **THEN** only sessions whose charge stop time falls within the window are returned
- **AND** the sessions are ordered by charge stop time, oldest first

#### Scenario: A session stopping exactly at the window's start boundary is included
- **GIVEN** a stored session whose charge stop time is exactly the first instant of the
  window's start day
- **WHEN** the caller requests sessions for that vehicle within that window
- **THEN** the session is included in the result

#### Scenario: A session stopping anywhere within the window's end day is included
- **GIVEN** a stored session whose charge stop time falls within the window's end calendar
  day, including its final moments, not merely its first instant
- **WHEN** the caller requests sessions for that vehicle within that window
- **THEN** the session is included in the result

#### Scenario: A session stopping the day after the window's end boundary is excluded
- **GIVEN** a stored session whose charge stop time falls on the calendar day immediately
  after the window's end day
- **WHEN** the caller requests sessions for that vehicle within that window
- **THEN** the session is NOT included in the result

#### Scenario: A session spanning the window's start boundary is included based on its stop time
- **GIVEN** a stored session whose charge start time is before the window's start day but
  whose charge stop time falls within the window
- **WHEN** the caller requests sessions for that vehicle within that window
- **THEN** the session is included in the result, because its charge stop time — not its
  charge start time — falls within the window

#### Scenario: Date-windowed sessions are isolated to the requested vehicle and account
- **GIVEN** two accounts, each with a vehicle having a Supercharger session whose charge
  stop time falls within the same date window
- **WHEN** the caller requests sessions for one specific account and vehicle within that
  window
- **THEN** only that account's vehicle's session is returned
- **AND** the other account's session does not appear in the result

#### Scenario: Callers never access the telemetry database directly
- **GIVEN** any caller that needs to display Supercharger session data
- **WHEN** it obtains that data
- **THEN** it does so exclusively through the SuperchargerReader port interface
- **AND** it imports no package from internal/telemetry/db
