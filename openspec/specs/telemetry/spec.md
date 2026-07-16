# telemetry Specification

## Purpose
TBD - created by archiving change telemetry-add-nightly-snapshots. Update Purpose after archive.
## Requirements
### Requirement: Nightly Vehicle Snapshot Capture
The telemetry capability SHALL capture one immutable snapshot of every registered vehicle
across all accounts on each scheduled run. In addition to the fields already required, for
vehicles that are actively charging or have charge telemetry available at capture time, the
snapshot SHALL also store the following charge-telemetry fields: the energy added to the
battery since the charge session started (in kWh), the charger power (in kW), the charger
voltage (in V), the charger actual current (in A), the usable battery level (in percent),
and the fast-charger type string. These six fields SHALL be stored as nullable values; a
NULL value means the vehicle did not report the field (or the snapshot predates this
extraction), and SHALL NOT be interpreted as zero. All other snapshot requirements
(raw_data, append-only, miles storage, Km-companion derivation, sentry-mode fidelity)
remain unchanged.

#### Scenario: A snapshot for a charging vehicle includes charge-telemetry fields
- **GIVEN** a registered vehicle that is actively charging when the nightly snapshot is
  captured and its vehicle_data reports charge_energy_added, charger_power,
  charger_voltage, charger_actual_current, usable_battery_level, and fast_charger_type
- **WHEN** a collection cycle captures the snapshot
- **THEN** the stored snapshot includes non-NULL values for all six charge-telemetry
  fields matching the reported values
- **AND** the snapshot's raw_data still contains the full vehicle_data payload

#### Scenario: A snapshot for a parked non-charging vehicle stores NULL charge-telemetry
- **GIVEN** a registered vehicle that is parked and not charging when the snapshot is
  captured, so the vehicle_data reports no meaningful charge-telemetry values
- **WHEN** a collection cycle captures the snapshot
- **THEN** the six charge-telemetry fields on the stored snapshot are NULL
- **AND** all existing snapshot fields (battery level, range, odometer, etc.) are still
  populated normally

#### Scenario: Pre-enrichment snapshot rows have NULL for the new fields
- **GIVEN** a vehicle_snapshots row written before the charge-enrichment migration was
  applied
- **WHEN** a caller reads that snapshot through the telemetry read port
- **THEN** the six charge-telemetry fields are NULL
- **AND** the caller can still read the raw_data to extract the values retroactively
  if needed

### Requirement: Waking Sleeping Vehicles Within A Bounded Timeout
The telemetry capability SHALL wake a sleeping vehicle before capturing it, polling for the vehicle
to come online within a bounded, configurable timeout. If the vehicle comes online within the
timeout, the capability SHALL capture its snapshot. If the vehicle does not come online before the
timeout expires, the capability SHALL record the attempt as a failure with a wake-timeout reason
and SHALL NOT keep waking that vehicle for the rest of the run.

#### Scenario: A sleeping vehicle wakes in time and is captured
- **GIVEN** a registered vehicle that is asleep at the start of the cycle and comes online within
  the wake timeout
- **WHEN** the collection cycle processes it
- **THEN** the vehicle is asked to wake
- **AND** once it reports online its snapshot is captured and stored
- **AND** its attempt is recorded as a success

#### Scenario: A vehicle that never wakes in time is recorded and skipped
- **GIVEN** a registered vehicle that is asleep and does not come online before the wake timeout
  expires
- **WHEN** the collection cycle processes it
- **THEN** no snapshot is stored for that vehicle
- **AND** its attempt is recorded as a failure with a wake-timeout reason
- **AND** the cycle continues to the other vehicles

### Requirement: Per-Vehicle Isolation And Attempt Recording
The telemetry capability SHALL record exactly one attempt per vehicle per collection cycle, with an
outcome of success or failure and a reason. A failure collecting one vehicle SHALL NOT abort the
cycle — every other vehicle SHALL still be processed. The capability SHALL retry a transient
failure at most once per vehicle per cycle before recording it as a failure.

#### Scenario: One vehicle's failure does not stop the others
- **GIVEN** three registered vehicles where the second fails to be captured
- **WHEN** the collection cycle runs
- **THEN** the first and third vehicles are captured and stored
- **AND** exactly one attempt is recorded for each of the three vehicles
- **AND** the second vehicle's attempt is recorded as a failure with its reason

#### Scenario: Every attempt is recorded regardless of outcome
- **GIVEN** a collection cycle over a set of registered vehicles
- **WHEN** the cycle completes
- **THEN** each vehicle has exactly one recorded attempt for that cycle
- **AND** each attempt carries the owning account id, the vehicle's Tesla id, the attempt time, an
  outcome of success or failure, and a reason

### Requirement: Expired Or Revoked Connections Are Isolated, Not Fatal
The telemetry capability SHALL treat an account with no usable Tesla connection (missing, expired
beyond refresh, or revoked) as a per-vehicle failure, not a fatal error. For each affected vehicle
it SHALL record the attempt as a failure with an unauthorized reason, and it SHALL continue
processing the remaining vehicles and accounts. It SHALL NOT abort the cycle, and it SHALL NOT reach
into the account module's internal state to alter it.

#### Scenario: An account with no usable connection is recorded and skipped
- **GIVEN** an account whose Tesla connection is missing or revoked, owning one or more registered
  vehicles, alongside other accounts with valid connections
- **WHEN** the collection cycle runs
- **THEN** each of that account's vehicles has an attempt recorded as a failure with an unauthorized
  reason
- **AND** no snapshot is stored for those vehicles
- **AND** the vehicles of the other, validly-connected accounts are still captured

### Requirement: Collection Spans All Accounts And Vehicles
The telemetry capability SHALL enumerate every registered vehicle across all accounts through the
account module's public interface, and SHALL process every enumerated vehicle in a cycle. It SHALL
obtain each account's Tesla credentials through the account module's public interface and SHALL NOT
read the account module's tables directly.

#### Scenario: Vehicles from multiple accounts are all processed
- **GIVEN** two accounts each owning registered vehicles, all with valid connections and online
- **WHEN** a collection cycle runs
- **THEN** every vehicle of both accounts has a snapshot stored
- **AND** every vehicle of both accounts has an attempt recorded
- **AND** the work list and the credentials were obtained only through the account module's public
  interface

### Requirement: Scheduled Unattended Collection
The telemetry capability SHALL run collection cycles automatically on an in-application schedule,
by default once per day at 03:30 local time, with the hour, minute, and timezone configurable. The
schedule SHALL be part of the running application (not an external OS scheduler). The scheduler
SHALL shut down gracefully when the process receives a termination signal.

#### Scenario: A cycle runs at the scheduled time
- **GIVEN** the poller running with its default daily 03:30-local schedule
- **WHEN** the scheduled time is reached
- **THEN** a collection cycle runs
- **AND** the scheduler arms itself for the next day's run

#### Scenario: The scheduler stops gracefully on shutdown
- **GIVEN** the poller running and waiting for the next scheduled time
- **WHEN** the process receives a termination signal
- **THEN** the scheduler stops waiting and the poller exits cleanly
- **AND** no new collection cycle is started after the shutdown begins

### Requirement: Latest Snapshot Read Port

The telemetry capability SHALL expose a read port through which other modules (the
gateway in particular) can retrieve the most recently stored snapshot for each vehicle
belonging to a given account, without accessing the telemetry module's database tables
directly. The read port SHALL return the existing `Snapshot` domain type (including all
extracted typed fields and the raw payload). Callers SHALL receive an empty result (not
an error) when the account has no stored snapshots.

#### Scenario: Latest snapshot is returned for each vehicle of an account

- **GIVEN** an account that has stored snapshots for two vehicles, where each vehicle
  has at least two snapshots from different collection runs
- **WHEN** the caller requests the latest snapshots for that account
- **THEN** exactly one snapshot is returned per vehicle
- **AND** the returned snapshot for each vehicle is the one with the most recent
  capture time
- **AND** all extracted fields (battery level, range, charging state, charge limit,
  odometer, temperatures, locked, sentry mode, car version, latitude, longitude) are
  present and match the stored values for that snapshot
- **AND** the distance and range fields are returned in miles (the Tesla-native unit)
  with no kilometre field on the returned type

#### Scenario: Empty result for an account with no snapshots

- **GIVEN** an account that has never had a snapshot stored (e.g. the nightly
  collection has not run yet, or the account just connected)
- **WHEN** the caller requests the latest snapshots for that account
- **THEN** an empty collection is returned
- **AND** no error is returned

#### Scenario: Per-account scoping — another account's data is not returned

- **GIVEN** two accounts each owning vehicles with stored snapshots
- **WHEN** the caller requests the latest snapshots for account A
- **THEN** only snapshots belonging to account A are returned
- **AND** no snapshot belonging to account B appears in the result

#### Scenario: Sentry-mode nil fidelity is preserved on read

- **GIVEN** a stored snapshot whose sentry-mode value was recorded as absent (unknown)
  because the vehicle did not report it at capture time
- **WHEN** that snapshot is returned through the read port
- **THEN** the sentry-mode field on the returned snapshot is nil (unknown), distinct
  from a value of off

#### Scenario: Callers never access the telemetry database directly

- **GIVEN** any caller that needs to display or use telemetry snapshot data
- **WHEN** it obtains that data
- **THEN** it does so exclusively through the telemetry module's read port interface
- **AND** it imports no package from `internal/telemetry/db`

### Requirement: Supercharger Session Ledger
The telemetry capability SHALL, on each nightly collection cycle, fetch the complete
Tesla-billed Supercharger and DC fast-charging session history for each connected account
and persist it as a durable session ledger. For each session the ledger SHALL store the
session identifier, the owning account id, the vehicle VIN, the site name, the country
code, the charge start and stop times, the billing type, the vehicle make type, the full
raw session payload (fees and invoices), and the following derived summaries: total energy
in kWh (derived from fees where unit of measure is kWh), total cost (sum of totalDue
across all fees), the session currency, and the overall paid status (true when all fees
are paid, false when any fee is unpaid). Derived summaries SHALL be NULL when no relevant
fees exist. A session that has already been stored SHALL be refreshed in place (upserted)
rather than duplicated, so that billing state changes (e.g. an unpaid session later
becomes paid) are reflected on the next nightly run. Sessions for VINs no longer
registered to the account SHALL still be stored with a NULL vehicle id.

#### Scenario: A new account's full Supercharger history is ingested on first run
- **GIVEN** an account with a valid Tesla connection and several historical Supercharger
  sessions not yet stored in the ledger
- **WHEN** the nightly collection cycle runs
- **THEN** every session returned by the Tesla charging-history endpoint is stored in the
  ledger
- **AND** each stored session carries the account id, the VIN, the site name, the country
  code, the charge start and stop times, the billing type, and the vehicle make type
- **AND** each stored session carries the full raw session payload including fees and
  invoices
- **AND** each stored session carries derived energy kWh, total cost, currency, and paid
  status computed from its fees

#### Scenario: A previously stored session whose billing status changed is updated
- **GIVEN** a session already in the ledger that was stored as unpaid on a prior run
- **WHEN** the nightly cycle runs and the Tesla endpoint now returns that session as paid
- **THEN** the ledger row for that session is updated to reflect the paid status
- **AND** no duplicate row is created for that session
- **AND** immutable fields (session identifier, VIN, charge start and stop times, site
  name, country code, billing type, vehicle make type, first-seen timestamp) are
  unchanged

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

#### Scenario: A session for an unrecognized VIN is stored with NULL vehicle id
- **GIVEN** a session in the Tesla history whose VIN is no longer a registered vehicle
  on this account (e.g. the vehicle was sold)
- **WHEN** the session is stored
- **THEN** the session is persisted with the VIN set to the session's VIN
- **AND** the vehicle id (tesla_id) is NULL
- **AND** all other session fields are stored normally

#### Scenario: A charging-history fetch failure is isolated from snapshot collection
- **GIVEN** an account for which the Tesla charging-history endpoint returns an error
- **WHEN** the nightly cycle runs
- **THEN** the failure is counted in the cycle report's charging fetch failures counter
- **AND** the snapshot collection for that account's vehicles continues normally
- **AND** no Supercharger sessions are stored for that account in this cycle

### Requirement: Supercharger Session Read Port
The telemetry capability SHALL expose a read port that allows callers (the gateway, other
consumers) to retrieve Supercharger sessions without accessing the telemetry database
directly. The port SHALL provide two read methods: one returning all sessions for a given
account (scoped to the account, ordered newest-first, with a limit), and one returning
sessions for a specific vehicle within an account (also ordered newest-first, with a
limit). Callers SHALL receive an empty non-nil result when no sessions exist for the given
scope.

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

#### Scenario: Callers never access the telemetry database directly
- **GIVEN** any caller that needs to display Supercharger session data
- **WHEN** it obtains that data
- **THEN** it does so exclusively through the SuperchargerReader port interface
- **AND** it imports no package from internal/telemetry/db

### Requirement: Cycle Report Charging Counters
The telemetry capability's cycle report SHALL include a count of Supercharger sessions
successfully upserted in the cycle and a count of accounts for which the charging-history
fetch failed. Both counters SHALL be zero when no sessions were processed and no failures
occurred.

#### Scenario: Cycle report reflects sessions upserted across all accounts
- **GIVEN** two accounts each with three new or updated Supercharger sessions
- **WHEN** the collection cycle completes
- **THEN** the cycle report's ChargingSessionsUpserted is six
- **AND** the cycle report's ChargingFetchFailures is zero

#### Scenario: Cycle report counts a charging fetch failure per account
- **GIVEN** two accounts where one account's charging-history fetch fails
- **WHEN** the collection cycle completes
- **THEN** the cycle report's ChargingFetchFailures is one
- **AND** snapshot collection succeeded normally for both accounts

