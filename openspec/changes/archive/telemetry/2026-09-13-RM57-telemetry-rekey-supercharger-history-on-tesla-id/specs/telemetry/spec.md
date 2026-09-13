## MODIFIED Requirements

### Requirement: Supercharger Session Ledger

The telemetry capability SHALL, on each nightly collection cycle, fetch the complete
Tesla-billed Supercharger and DC fast-charging session history for each connected account
and persist it as a durable session ledger. For each session the ledger SHALL store the
session identifier, the vehicle identifier, the vehicle VIN, the site name, the country
code, the charge start and stop times, the billing type, the vehicle make type, the full
raw session payload (fees and invoices), and the following derived summaries: total energy
in kWh (derived from fees where unit of measure is kWh), total cost (sum of totalDue
across all fees), the session currency, and the overall paid status (true when all fees
are paid, false when any fee is unpaid). Derived summaries SHALL be NULL when no relevant
fees exist. A session that has already been stored SHALL be refreshed in place (upserted)
rather than duplicated, so that billing state changes (e.g. an unpaid session later
becomes paid) are reflected on the next nightly run.

Every stored session SHALL carry a vehicle identifier; the ledger SHALL NOT store a
session without one. A session whose VIN is not a currently registered vehicle SHALL be
skipped rather than stored, and each skip SHALL be counted in the cycle report.
**This is a CHANGE from the prior revision of this requirement, under which the ledger
also stored an owning account identifier on every session, and under which a session for
an unrecognized VIN was stored with a NULL vehicle identifier.** The ledger no longer
records an account: which vehicles a user may see is already recorded by the account
capability's own vehicle registry, so repeating it on every session added nothing. With
the account identifier gone, a session with no vehicle identifier could not be reached by
any read the capability offers, so storing one would only keep rows nobody can read.

The ledger SHALL also carry a human-owned battery-percentage verification/override trio for
each session — a start battery percentage, an end battery percentage (each 0-100
inclusive), and a source label identifying why the percentages are set. This trio is NEVER
computed or written by the nightly collection cycle: it starts NULL for every newly
inserted session and SHALL remain exactly as previously stored across every subsequent
nightly refresh of that session, even when the session's billing fields or raw payload
change on that same refresh. A source label, when set, SHALL be one of a closed, explicitly
extensible set of values identifying a human-owned or measured origin; the ledger SHALL
NEVER store a label identifying a computed estimate — an estimate is a different
capability's read-time concern, never persisted here.

#### Scenario: A newly verified session records its battery-percentage verification trio
- **GIVEN** a session whose battery-percentage verification/override trio is NULL
- **WHEN** the trio is set to a specific start percentage, end percentage, and source label
- **THEN** the ledger stores the verified start and end percentages and the source label
  exactly as set

#### Scenario: A later re-verification of the same session overwrites the trio with the new verification's own values
- **GIVEN** a session whose verification/override trio was set by a prior verification
- **WHEN** the session is verified again, setting a new trio
- **THEN** the ledger's trio reflects only the most recent verification's values
- **AND** the prior verification's values are not separately retained

#### Scenario: A new account's full Supercharger history is ingested on first run
- **GIVEN** an account with a valid Tesla connection and several historical Supercharger
  sessions not yet stored in the ledger, every one of them for a currently registered
  vehicle
- **WHEN** the nightly collection cycle runs
- **THEN** every session returned by the Tesla charging-history endpoint is stored in the
  ledger
- **AND** each stored session carries the vehicle identifier, the VIN, the site name, the
  country code, the charge start and stop times, the billing type, and the vehicle make
  type
- **AND** no stored session carries an account identifier
- **AND** each stored session carries the full raw session payload including fees and
  invoices
- **AND** each stored session carries derived energy kWh, total cost, currency, and paid
  status computed from its fees
- **AND** each stored session's battery-percentage verification trio (start percentage, end
  percentage, source label) is NULL

#### Scenario: A previously stored session whose billing status changed is updated
- **GIVEN** a session already in the ledger that was stored as unpaid on a prior run
- **WHEN** the nightly cycle runs and the Tesla endpoint now returns that session as paid
- **THEN** the ledger row for that session is updated to reflect the paid status
- **AND** no duplicate row is created for that session
- **AND** immutable fields (session identifier, VIN, charge start and stop times, site
  name, country code, billing type, vehicle make type, first-seen timestamp) are
  unchanged

#### Scenario: A nightly refresh never overwrites an already-set battery-percentage verification trio
- **GIVEN** a session already in the ledger whose battery-percentage verification trio has
  been set (by some means outside the nightly collection cycle) to specific start and end
  percentages and a source label
- **WHEN** the nightly cycle runs and re-fetches that session, with its billing fields
  (e.g. paid status) or raw payload having changed since the prior run
- **THEN** the ledger row's billing fields and raw payload reflect the newly fetched values
- **AND** the ledger row's battery-percentage verification trio (start percentage, end
  percentage, source label) is exactly what it was before this nightly run — unchanged in
  every respect, not merely still non-NULL

#### Scenario: Energy and cost derivation from fees
- **GIVEN** a session with multiple fees where one fee has a unit of measure of kWh and
  another has a unit of measure of minutes
- **WHEN** the session is stored
- **THEN** energy_kwh is the sum of usage tiers from the kWh-denominated fee only
- **AND** total_cost is the sum of totalDue across all fees regardless of unit
- **AND** currency is taken from the currencyCode of the first fee
- **AND** is_paid reflects whether all fees are paid

#### Scenario: A session with no fees stores NULL derived summaries
- **GIVEN** a session returned by the Tesla endpoint with an empty fees array
- **WHEN** the session is stored
- **THEN** energy_kwh, total_cost, currency, and is_paid are all NULL
- **AND** the raw session payload is still stored

#### Scenario: A session for an unrecognized VIN is skipped, not stored
- **GIVEN** a session in the Tesla history whose VIN is not a currently registered vehicle
  on this account (e.g. the vehicle was sold)
- **WHEN** the nightly cycle processes that session
- **THEN** no ledger row is written for it
- **AND** the skip is counted in the cycle report
- **AND** the remaining sessions in the same history response are still stored normally

#### Scenario: A charging-history fetch failure is isolated from snapshot collection
- **GIVEN** an account for which the Tesla charging-history endpoint returns an error
- **WHEN** the nightly cycle runs
- **THEN** the failure is counted in the cycle report's charging fetch failures counter
- **AND** the snapshot collection for that account's vehicles continues normally
- **AND** no Supercharger sessions are stored for that account in this cycle

### Requirement: Supercharger Session Read Port

The telemetry capability SHALL expose a read port that allows other modules to retrieve
Supercharger sessions without accessing the telemetry database directly. The port SHALL
provide two read methods, both scoped to a single vehicle and identifying that vehicle by
its Tesla numeric identifier alone: one returning that vehicle's sessions ordered
newest-first with a limit, and one returning that vehicle's sessions whose charge stop
time falls within a caller-supplied date window, ordered oldest-first with no limit — the
window itself bounds the result. Callers SHALL receive an empty non-nil result when no
sessions exist for the given scope. Every returned session SHALL carry its
battery-percentage verification trio (start percentage, end percentage, source label)
exactly as stored — NULL when no override has been set for that session.

**This is a CHANGE from the prior revision of this requirement, under which the port
provided three methods and every method identified its scope by an account identifier
first.** The account-scoped method returning all of an account's sessions is REMOVED: the
ledger no longer stores an account identifier, so the method has nothing to filter on, and
it had no caller. The two surviving methods identify their vehicle by its Tesla numeric
identifier alone.

The date-windowed method SHALL determine whether a session belongs to the requested window
by comparing the session's charge stop time against the window, regardless of when the
session started — a session that began before the window but whose charging finished within
it SHALL be included, because the charge stop time is what the session's energy and ending
battery percentage are anchored to. The window's end boundary SHALL be treated as inclusive
of the entire final calendar day, not merely its first instant.

#### Scenario: Sessions are returned for a specific vehicle ordered newest-first
- **GIVEN** two vehicles, each having stored Supercharger sessions
- **WHEN** the caller requests sessions for one specific vehicle by its Tesla numeric
  identifier
- **THEN** only sessions matching that vehicle identifier are returned
- **AND** the sessions are ordered by charge start time, newest first
- **AND** the result is limited to the requested number of rows
- **AND** no sessions belonging to the other vehicle appear in the result

#### Scenario: Empty result when no sessions exist
- **GIVEN** a registered vehicle with no stored Supercharger sessions
- **WHEN** the caller requests sessions for that vehicle
- **THEN** an empty collection is returned
- **AND** no error is returned

#### Scenario: A session whose charging finished inside the window is included
- **GIVEN** a session for a vehicle that started before the requested window and finished
  charging inside it
- **WHEN** the caller requests that vehicle's sessions for the window
- **THEN** the session is included in the result

#### Scenario: The window's final calendar day is inclusive
- **GIVEN** a session for a vehicle that finished charging late on the window's final
  calendar day, and another that finished on the first instant of the following day
- **WHEN** the caller requests that vehicle's sessions for the window
- **THEN** the session finishing on the final day is included
- **AND** the session finishing on the following day is not

### Requirement: Supercharger Session Updated-Since Read Port

The telemetry capability SHALL expose a read port through which other modules can retrieve every
stored Supercharger session for a single vehicle whose `updated_at` is at or after a
caller-supplied instant, without accessing the telemetry module's database tables directly. The
port SHALL identify the vehicle by its Tesla numeric id alone, and SHALL return the existing
`SuperchargerHistory` domain type, ordered oldest-first by `updated_at`. Callers SHALL receive an
empty result (not an error) when no session for that vehicle has been updated at or after the
given instant.

**This is a CHANGE from the prior revision of this requirement, under which the port identified
the vehicle by its account AND its Tesla numeric id.** The ledger no longer stores an account
identifier, so the account is no longer part of the vehicle's identity for this read.

This port is the capability's bounded "what changed recently" read, and the persistence layer
SHALL keep an index matching it exactly, so that the vehicle filter, the instant predicate and
the ordering are all satisfied by a single index scan with no separate sort step.

#### Scenario: A revised session's billing state is detected by this port
- **GIVEN** a Supercharger session originally stored weeks ago, whose billing fields are revised
  today (its stored `updated_at` refreshes to today)
- **WHEN** the caller requests that vehicle's sessions updated since a recent instant
- **THEN** the revised session is included in the result, even though its `ChargeStartDateTime`/
  `ChargeStopDateTime` are weeks in the past

#### Scenario: A session updated at exactly the requested instant is included
- **GIVEN** a Supercharger session last modified at a given instant
- **WHEN** the caller requests that vehicle's sessions updated since that exact instant
- **THEN** the session is included in the result

#### Scenario: Results are ordered earliest-updated-first
- **GIVEN** a vehicle with several Supercharger sessions modified at or after a given instant,
  with different last-modified instants
- **WHEN** the caller requests that vehicle's sessions updated since that instant
- **THEN** the records are ordered by their last-modified instant, earliest first

#### Scenario: Another vehicle's session does not appear
- **GIVEN** two vehicles, each holding a Supercharger session modified at or after a given
  instant
- **WHEN** one vehicle's sessions are requested for modifications at or after that instant
- **THEN** only that vehicle's session is included

#### Scenario: Empty result when nothing has been updated in the window
- **GIVEN** a vehicle with no Supercharger session updated at or after the requested instant
- **WHEN** the caller requests that vehicle's sessions updated since that instant
- **THEN** an empty collection is returned, and no error is returned

#### Scenario: Callers never access the telemetry database directly for this port either
- **GIVEN** any caller that needs to detect which of a vehicle's Supercharger sessions changed
  recently
- **WHEN** it obtains that data
- **THEN** it does so exclusively through this read port
- **AND** it imports no package from `internal/telemetry/db`

### Requirement: Cycle Report Charging Counters

The telemetry capability's cycle report SHALL include a count of Supercharger sessions
successfully upserted in the cycle, a count of accounts for which the charging-history
fetch failed, and a count of sessions skipped because their VIN was not a currently
registered vehicle. All three counters SHALL be zero when no sessions were processed, no
sessions were skipped and no failures occurred. The three counters SHALL be independent: a
skipped session SHALL NOT count as an upsert or as a fetch failure, and a fetch failure
SHALL NOT count as a skip. The capability's per-cycle operational log line SHALL report all
three.

**This is a CHANGE from the prior revision of this requirement, which defined two counters.
The skipped-session counter is ADDED**, because the ledger no longer stores a session whose
VIN is not a registered vehicle (see "Supercharger Session Ledger"), and a silent skip would
hide data the Tesla endpoint returned and the capability chose not to keep.

#### Scenario: Cycle report reflects sessions upserted across all accounts
- **GIVEN** two accounts each with three new or updated Supercharger sessions, all for
  registered vehicles
- **WHEN** the collection cycle completes
- **THEN** the cycle report's upserted count is six
- **AND** the cycle report's charging fetch failure count is zero
- **AND** the cycle report's skipped-session count is zero

#### Scenario: Cycle report counts a charging fetch failure per account
- **GIVEN** two accounts where one account's charging-history fetch fails
- **WHEN** the collection cycle completes
- **THEN** the cycle report's charging fetch failure count is one
- **AND** the cycle report's skipped-session count is zero
- **AND** snapshot collection succeeded normally for both accounts

#### Scenario: Cycle report counts each skipped unregistered session
- **GIVEN** an account whose charging history returns three sessions, one of them for a VIN
  that is not a currently registered vehicle
- **WHEN** the collection cycle completes
- **THEN** the cycle report's skipped-session count is one
- **AND** the cycle report's upserted count is two
- **AND** the cycle report's charging fetch failure count is zero

#### Scenario: The cycle log line reports the skipped-session count
- **GIVEN** a completed cycle whose report holds a non-zero skipped-session count
- **WHEN** the cycle's operational summary line is emitted
- **THEN** the line reports the skipped-session count alongside the upserted count and the
  charging fetch failure count

## REMOVED Requirements

### Requirement: Supercharger History Account-Wide Updated-Since Read Port

**Reason**: MAG-67 / roadmap `RM57-rekey-supercharger-pair-on-tesla-id` tier 1. This port
had two parts, and this change removes both at once.

Its predicate is gone: the port filtered on the ledger's account identifier, and the
ledger no longer stores one.

Its purpose is gone too. The port existed so a caller could recover a session whose vehicle
is not currently registered — such a session had no vehicle identifier, so a per-vehicle
read could never surface it, and only an account-wide read could. The ledger now skips such
a session instead of storing it without a vehicle identifier, so no unreachable session is
ever written and there is nothing left to recover. Removing the port and removing the
orphan row are the same decision.

**Migration**: The one consumer — the nightly Supercharger mirror — reads through the
surviving per-vehicle updated-since port instead, once per vehicle of the account it is
processing. The set of sessions it sees is the same set as before, minus the orphans, which
no longer exist. Roadmap tier 3 then re-keys the mirror's own cursor on the vehicle
identifier and drops the account grouping entirely.
