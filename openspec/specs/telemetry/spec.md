# telemetry Specification

## Purpose
TBD - created by archiving change telemetry-add-nightly-snapshots. Update Purpose after archive.
## Requirements
### Requirement: Nightly Vehicle Snapshot Capture
The telemetry capability SHALL capture one snapshot of every registered vehicle across all
accounts on each scheduled run, and SHALL store every unit-bearing value in the unit it is
displayed in — distance and range in kilometres, temperature in degrees Celsius, and tire
pressure in PSI — converting at capture time. Every stored unit-bearing value SHALL be identified
by a name that carries its unit, and the capability SHALL NOT store a unit-bearing value under a
name that leaves its unit implicit. In addition to the fields already required, the snapshot SHALL
also store the tire pressure monitoring system (TPMS) readings for all four corners of the vehicle
(front-left, front-right, rear-left, rear-right). These four fields SHALL be stored as nullable
values; a NULL value means the vehicle did not report TPMS at capture (e.g. the vehicle has no
TPMS sensors, or the field was absent in the response) OR the row predates this extraction, and a
NULL value SHALL NOT be interpreted as zero pressure. A truthfully reported zero pressure SHALL be
stored as a non-NULL value. The capability SHALL continue to store the complete raw payload
unconverted, in the units the Fleet API reported, so that no information is lost by the
conversion. All other snapshot requirements (daily-replace upsert semantics, sentry-mode fidelity)
remain unchanged.

#### Scenario: A snapshot captures TPMS pressure for all four corners
- **GIVEN** a registered vehicle that is parked and its vehicle_data reports tire pressure values
  for all four corners when the nightly snapshot is captured
- **WHEN** a collection cycle captures the snapshot
- **THEN** the stored snapshot includes non-NULL values for all four TPMS pressure fields
- **AND** each stored value is the corresponding value reported by the vehicle converted to PSI
- **AND** the snapshot's raw_data still contains the full vehicle_data payload with the pressure
  values in the unit the Fleet API reported

#### Scenario: Distance and range are stored in kilometres
- **GIVEN** a registered vehicle whose vehicle_data reports an odometer reading and an estimated
  battery range
- **WHEN** a collection cycle captures the snapshot
- **THEN** the stored odometer and battery range are expressed in kilometres
- **AND** each stored value is the reported value converted from the Fleet API's native distance
  unit exactly once, at capture time
- **AND** the snapshot's raw_data still contains the values in the unit the Fleet API reported

#### Scenario: Temperature is stored as reported, without conversion
- **GIVEN** a registered vehicle whose vehicle_data reports inside and outside temperatures
- **WHEN** a collection cycle captures the snapshot
- **THEN** the stored inside and outside temperatures are expressed in degrees Celsius
- **AND** the stored values equal the reported values, because the Fleet API already reports
  temperature in degrees Celsius and no conversion is applied

#### Scenario: A snapshot stores NULL TPMS pressure when the vehicle does not report TPMS
- **GIVEN** a registered vehicle whose vehicle_data does not include TPMS pressure values
  at capture time (e.g. the vehicle has no TPMS sensors)
- **WHEN** a collection cycle captures the snapshot
- **THEN** the stored TPMS pressure fields on the snapshot are NULL for all four corners
- **AND** all existing snapshot fields (battery level, range, odometer, etc.) are still
  populated normally

#### Scenario: A truthfully reported zero pressure is stored as non-NULL
- **GIVEN** a registered vehicle that reports a tire pressure of exactly zero for one or more
  corners (e.g. a flat tire)
- **WHEN** a collection cycle captures the snapshot
- **THEN** the stored TPMS pressure value for the affected corner is non-NULL
- **AND** the stored value is 0.0 PSI (not NULL — zero is a truthful reading)

#### Scenario: Pre-extraction snapshot rows have NULL for the TPMS pressure fields
- **GIVEN** a vehicle_snapshots row written before the TPMS column migration was applied
- **WHEN** a caller reads that snapshot through the telemetry read port
- **THEN** the four TPMS pressure fields on the Snapshot are NULL
- **AND** the caller can still access the raw_data payload to extract the TPMS values
  retroactively if needed

#### Scenario: Rows captured before the unit change are converted, not left mixed
- **GIVEN** snapshot rows that were captured while values were stored in the Fleet API's native
  units
- **WHEN** the display-unit change is applied
- **THEN** those rows' distance, range, and tire-pressure values are converted in place to the
  display units
- **AND** no row remains stored in a different unit from any other row
- **AND** rows whose tire-pressure values were NULL remain NULL rather than becoming zero

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
The telemetry capability's read port SHALL return snapshots whose values are already expressed in
their display units, and SHALL NOT perform, or require its callers to perform, any unit conversion
on read. The read port SHALL return the TPMS tire pressure fields for all four corners, expressed
in PSI, alongside the existing snapshot fields. The capability SHALL NOT expose companion
conversion methods on the returned snapshot type; every returned field SHALL be directly usable in
the unit its name declares. The pressure fields SHALL remain nullable, preserving the
nil/not-reported distinction from the stored value itself rather than re-deriving it per call. No
new read method is introduced; the four TPMS fields ride on the existing Snapshot returned by the
existing read port methods.

#### Scenario: Values are returned ready to use, with no conversion on read
- **GIVEN** a stored snapshot for a registered vehicle
- **WHEN** a caller retrieves it through LatestSnapshotsByAccount or SnapshotsByVehicleSince
- **THEN** the odometer and battery range fields are expressed in kilometres, the temperature
  fields in degrees Celsius, and the tire pressure fields in PSI
- **AND** the caller performs no arithmetic and calls no conversion method to obtain those units
- **AND** the returned type exposes no companion conversion method for any of those fields

#### Scenario: Nil fidelity is preserved by the stored pressure value
- **GIVEN** a snapshot row for which TPMS pressure was not reported (NULL in the database)
- **WHEN** a caller retrieves the snapshot through LatestSnapshotsByAccount or
  SnapshotsByVehicleSince and reads any of the four pressure fields
- **THEN** each pressure field is nil
- **AND** the caller can distinguish "not reported" from "reported 0.0 PSI"

#### Scenario: Callers never access the telemetry database directly for TPMS data
- **GIVEN** any caller that needs to display or process TPMS tire pressure data
- **WHEN** it obtains that data
- **THEN** it does so exclusively through the Reader port interface (LatestSnapshotsByAccount
  or SnapshotsByVehicleSince), reading the pressure fields on the returned Snapshot
- **AND** it imports no package from internal/telemetry/db

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

The ledger SHALL additionally carry a frozen verification-time snapshot pair for each
session — a start battery percentage estimate and an end battery percentage estimate (each
0-100 inclusive) — recording what a companion estimation capability's live, on-read
computation produced at the moment the verification/override trio above was set. This
snapshot pair is a permanent, point-in-time observation, not an ongoing estimate: it SHALL
be written at most once per verification, in the same operation that sets the trio, and
SHALL NEVER be recomputed, refreshed, or otherwise modified afterward by the nightly
collection cycle or by any other read of a live estimate — remaining unchanged even after
the estimation method that originally produced it changes. It starts NULL for every newly
inserted session and, like the trio, SHALL remain exactly as previously stored across every
subsequent nightly refresh of that session. This snapshot pair SHALL NEVER be treated as an
input to, or a cache of, the companion estimation capability's live computation.

NOTE (2026-08-15): no such companion estimation capability exists on this platform — it was
descoped before implementation. The snapshot pair is therefore reserved and NULL in every
stored session today; the requirements above bind whenever an estimation capability is
introduced, and until then nothing writes the pair.

#### Scenario: A newly verified session records a frozen snapshot of the estimate alongside the override

- **GIVEN** a session whose battery-percentage verification/override trio and
  verification-time snapshot pair are both NULL
- **WHEN** the trio is set to a specific start and end percentage and a source label, in a
  single write that also records the start and end percentage a companion estimation
  capability's live computation produced at that same moment
- **THEN** the ledger stores both the verified start and end percentages and the source
  label
- **AND** the ledger stores the snapshot start and end percentages exactly as they were
  computed at that moment, independent of and not necessarily equal to the verified values

#### Scenario: A later re-verification of the same session overwrites the frozen snapshot with the new verification's own snapshot
- **GIVEN** a session whose verification/override trio and verification-time snapshot pair
  were both set by a prior verification
- **WHEN** the session is verified again, setting a new trio and a new snapshot pair in one
  write
- **THEN** the ledger's trio and snapshot pair both reflect only the most recent
  verification's values
- **AND** the prior verification's snapshot values are not separately retained

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

#### Scenario: A nightly refresh never overwrites an already-set verification-time snapshot pair
- **GIVEN** a session already in the ledger whose verification-time snapshot pair has been
  set (in the same write as its verification trio, by some means outside the nightly
  collection cycle) to specific start and end percentage estimates
- **WHEN** the nightly cycle runs and re-fetches that session one or more times, with its
  billing fields or raw payload having changed since the prior run
- **THEN** the ledger row's billing fields and raw payload reflect the newly fetched values
- **AND** the ledger row's verification-time snapshot pair is exactly what it was before
  this nightly run — unchanged in every respect, across every subsequent nightly refresh,
  regardless of how many nightly cycles have run since the snapshot was recorded

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

### Requirement: Snapshot History Read Port
The telemetry capability SHALL expose a read port through which other modules (the gateway in
particular) can retrieve the **history** of stored snapshots for a single vehicle — all snapshots
captured at or after a caller-supplied instant — without accessing the telemetry module's database
tables directly. The port SHALL identify the vehicle by its account and its Tesla numeric id, and
SHALL return the existing `Snapshot` domain type (including all extracted typed fields and the raw
payload), ordered **oldest-first** by capture time. The window boundary is supplied by the caller
as an absolute instant (`since`, inclusive); the port SHALL NOT compute the window itself. Callers
SHALL receive an empty result (not an error) when the vehicle has no snapshots in the window. The
distance and range fields SHALL be returned in kilometres, already converted at capture time, and
the returned type SHALL carry no companion method for deriving them.

#### Scenario: History is returned oldest-first for a vehicle within its account

- **GIVEN** an account that has stored several snapshots for one vehicle across different
  collection runs, all captured at or after a given instant
- **WHEN** the caller requests that vehicle's snapshot history since that instant (passing the
  account id, the vehicle's Tesla id, and the instant)
- **THEN** every snapshot captured at or after the instant is returned
- **AND** the snapshots are ordered oldest-first by capture time
- **AND** all extracted fields (including odometer, battery level, and battery range) match the
  stored values for each snapshot
- **AND** the distance and range fields are returned in kilometres with no companion conversion
  method on the returned type

#### Scenario: The `since` boundary is inclusive

- **GIVEN** a vehicle with a snapshot whose capture time is exactly the requested `since` instant
- **WHEN** the caller requests that vehicle's history since that instant
- **THEN** the snapshot captured exactly at `since` is included in the result

#### Scenario: Snapshots older than the window are excluded

- **GIVEN** a vehicle with snapshots both before and after the requested `since` instant
- **WHEN** the caller requests that vehicle's history since that instant
- **THEN** only snapshots captured at or after `since` are returned
- **AND** snapshots captured before `since` do not appear in the result

#### Scenario: Empty result when the vehicle has no snapshots in the window

- **GIVEN** a vehicle that has no stored snapshots at or after the requested `since` instant
- **WHEN** the caller requests that vehicle's history since that instant
- **THEN** an empty collection is returned
- **AND** no error is returned

#### Scenario: Per-account and per-vehicle scoping

- **GIVEN** two accounts that each own vehicles with stored snapshots, and an account that owns two
  vehicles
- **WHEN** the caller requests history for one account and one vehicle
- **THEN** only snapshots belonging to that account AND that vehicle are returned
- **AND** no snapshot belonging to another account, or to another vehicle of the same account,
  appears in the result

#### Scenario: Callers never access the telemetry database directly

- **GIVEN** any caller that needs to display or use a vehicle's snapshot history
- **WHEN** it obtains that data
- **THEN** it does so exclusively through the telemetry module's read port interface
- **AND** it imports no package from `internal/telemetry/db`

### Requirement: Static Vehicle Config Capture During Nightly Collection
The telemetry capability SHALL, during each collection cycle, capture the two static
`vehicle_config` attributes (`exterior_color`, `car_type`) from the `VehicleData` response already
fetched for a vehicle, and SHALL persist them through the account module's public port method
(`SetVehicleConfigIfEmpty`) — WITHOUT making any additional Tesla Fleet API call beyond the
`VehicleData` fetch every vehicle capture already performs. The capability SHALL skip calling that
port method when the vehicle's already-known registry record has both `exterior_color` and
`car_type` non-nil (already captured in a prior cycle). The capability SHALL also skip calling that
port method when either value observed from `VehicleData` this cycle is an empty string — whether
because Tesla reported an empty string, or because the vehicle's capture attempt did not reach
`VehicleData` this cycle (e.g. a wake timeout, an unauthorized connection, or a persistent API
error). At most one write-back attempt SHALL occur per vehicle per collection cycle, even when the
vehicle's snapshot capture was internally retried once for a transient error.

#### Scenario: A vehicle with both config values already captured is never re-written
- **GIVEN** a registered vehicle whose registry record already has non-nil `exterior_color` and
  `car_type`
- **WHEN** a collection cycle captures that vehicle's snapshot successfully
- **THEN** the capability does not call the account port's config write-back method for that
  vehicle
- **AND** the vehicle's snapshot is still captured normally

#### Scenario: An uncaptured vehicle is written back after a successful capture
- **GIVEN** a registered vehicle whose registry record has `exterior_color` and `car_type` both nil
- **AND** that vehicle's `VehicleData` response reports non-empty values for both
- **WHEN** a collection cycle captures that vehicle's snapshot successfully
- **THEN** the capability calls the account port's config write-back method exactly once, passing
  the two observed values

#### Scenario: An empty observed value is never written back
- **GIVEN** a registered vehicle whose registry record has `exterior_color` and `car_type` both nil
- **AND** that vehicle's `VehicleData` response reports an empty string for at least one of the two
  values
- **WHEN** a collection cycle captures that vehicle's snapshot
- **THEN** the capability does not call the account port's config write-back method for that
  vehicle

#### Scenario: A vehicle whose capture attempt fails observes no config this cycle
- **GIVEN** a registered vehicle whose snapshot capture attempt fails for the cycle (wake timeout,
  unauthorized connection, or a persistent API error that exhausts the bounded retry)
- **WHEN** the cycle completes
- **THEN** the capability does not call the account port's config write-back method for that
  vehicle
- **AND** the vehicle's recorded attempt outcome and reason reflect the actual capture failure,
  unaffected by config capture

#### Scenario: A retried capture attempts config write-back at most once
- **GIVEN** a registered vehicle whose snapshot capture fails transiently on the first attempt this
  cycle and succeeds on the single bounded retry, with the retry's `VehicleData` response reporting
  non-empty config values
- **WHEN** the collection cycle completes
- **THEN** the capability calls the account port's config write-back method at most once for that
  vehicle, using the retry's observed values, never the first (failed) attempt's

### Requirement: Cycle Report Config Capture Failures Counter
The telemetry capability's cycle report SHALL include a count of static vehicle-config write-back
attempts that failed (the account port's write-back method returned an error). A failed write-back
SHALL NOT alter the vehicle's recorded attempt outcome or reason, and SHALL NOT abort collection for
any other vehicle. The counter SHALL be zero when no write-back was attempted, or every attempted
write-back succeeded.

#### Scenario: A failed config write-back is counted without affecting the recorded attempt
- **GIVEN** a registered vehicle whose snapshot is captured successfully and whose config
  write-back is attempted (the vehicle's registry record has at least one nil config field, and
  both observed values are non-empty)
- **AND** the account port's write-back call returns an error
- **WHEN** the collection cycle completes
- **THEN** the cycle report's config-capture-failures count is one
- **AND** the vehicle's recorded attempt still reflects a successful snapshot capture, unaffected
  by the write-back failure

#### Scenario: Cycle report's config capture counter is zero when nothing needed writing back
- **GIVEN** a collection cycle where every vehicle's registry record already has both config
  values captured
- **WHEN** the collection cycle completes
- **THEN** the cycle report's config-capture-failures count is zero

### Requirement: Snapshot Effective Date

The `Snapshot` domain type SHALL carry an `EffectiveDate time.Time` field representing the
calendar day the nightly snapshot **describes** — computed as `CapturedAt.AddDate(0, 0, -1)`
(calendar-day arithmetic, not a 24-hour duration, so DST does not shift it). `EffectiveDate` is
populated exactly once, in the single DB→domain mapper (`rowToSnapshot`), so both
`LatestSnapshotsByAccount` and `SnapshotsByVehicleSince` return `Snapshot`s that carry it without
any per-method duplication. `EffectiveDate` is a **full `time.Time`** preserving the
time-of-day component (same type and shape as `CapturedAt`); it is NOT a pre-formatted display
string — formatting (e.g. MM-DD) is a caller concern.

`EffectiveDate` is a read-derived field. There is NO matching database column and NO migration: it
is computed from `CapturedAt` at the read boundary and never persisted. The write path
(`insertSnapshot` / the poller) does not set `EffectiveDate`; a write-built `Snapshot` carries the
zero `time.Time` value, which is never read before storage. `CapturedAt` remains the authoritative
"when we read it"; `CapturedDate` remains the dedupe calendar day (capture's own date). `EffectiveDate`
is a distinct, separate concept from both: the day the row *represents*.

#### Scenario: EffectiveDate is one calendar day before CapturedAt on every returned Snapshot

- **GIVEN** a stored `vehicle_snapshots` row whose `captured_at` is `2026-08-08 03:30:00 UTC`
- **WHEN** a caller retrieves it through `LatestSnapshotsByAccount` or `SnapshotsByVehicleSince`
- **THEN** the returned `Snapshot.EffectiveDate` equals `2026-08-07 03:30:00 UTC`
- **AND** the field carries the full `time.Time` (year, month, day, and time-of-day), not a
  pre-formatted string
- **AND** `Snapshot.CapturedAt` is unchanged (`2026-08-08 03:30:00 UTC`)

#### Scenario: Both read methods populate EffectiveDate from the single mapper

- **GIVEN** a `telemetry.Reader` implementation backed by the production store
- **WHEN** `LatestSnapshotsByAccount(ctx, accountID)` and `SnapshotsByVehicleSince(ctx, accountID, teslaID, since)` return non-empty `Snapshot` slices
- **THEN** every `Snapshot` in both results has a non-zero `EffectiveDate`
- **AND** that `EffectiveDate` equals `CapturedAt.AddDate(0, 0, -1)` for that row
- **AND** the population happens in `rowToSnapshot` (no per-method duplication)

#### Scenario: EffectiveDate is read-derived, never persisted

- **GIVEN** the `vehicle_snapshots` table schema
- **WHEN** the nightly poller writes a snapshot via the write path
- **THEN** no `effective_date` column is written (no such column exists)
- **AND** no migration is introduced by this change
- **AND** the write-built `Snapshot`'s `EffectiveDate` stays the zero `time.Time` (it is not read
  before storage)

#### Scenario: Existing callers that ignore EffectiveDate are unaffected

- **GIVEN** any existing caller of `telemetry.Reader` (e.g. the dashboard card render path) that
  does not read `EffectiveDate`
- **WHEN** it receives `Snapshot`s after this change
- **THEN** its behavior is unchanged
- **AND** it is not forced to handle the new field
- **AND** the `telemetry.Reader` interface method signatures are unchanged

#### Scenario: DST does not shift EffectiveDate relative to CapturedAt

- **GIVEN** a `CapturedAt` that falls on a DST boundary day in the poller's timezone
- **WHEN** `EffectiveDate` is computed via `CapturedAt.AddDate(0, 0, -1)`
- **THEN** `EffectiveDate` is one calendar day earlier than `CapturedAt`'s calendar day
- **AND** the offset is a calendar-day step (not a fixed 24-hour duration), so a 23- or 25-hour DST
  day does not move `EffectiveDate` off by an hour

### Requirement: Derived Consumption Metrics

The telemetry capability SHALL, when capturing a nightly snapshot for a vehicle, compute
and store five derived consumption values against that vehicle's previous stored snapshot
(the row for the same account and vehicle with the greatest capture time strictly before
the start of the incoming snapshot's local calendar day): the distance traveled in
kilometres, the battery percentage consumed, the number of calendar days the comparison
spans, and — only when the battery percentage consumed is strictly greater than zero — the
resulting kilometres-per-percent efficiency and the equivalent estimated full-charge range
in kilometres. Distance traveled and battery percentage consumed SHALL always be stored as
the true, signed difference between the two records, without averaging, clamping, or
discarding a negative value. The two efficiency values SHALL be NULL whenever the battery
percentage consumed is zero or negative, and SHALL also be NULL, along with the distance
traveled, battery percentage consumed, and days-spanned values, when the vehicle has no
prior stored snapshot. A snapshot that replaces an existing same-day snapshot (a same-day
re-capture) SHALL have all five derived values recomputed against the correct predecessor
and refreshed, not left at their previously stored values. The capability SHALL also
compute these five values for every snapshot stored before this capability existed, in the
same schema change that introduces the columns, producing the same results the ongoing
capture-time computation would produce for the same pair of consecutive records.

#### Scenario: A normal drive day computes positive distance, positive battery used, and both efficiency values

- **GIVEN** a vehicle with a previously stored snapshot reporting an odometer of 42,350 km
  and a battery level of 82%
- **AND** its next nightly snapshot reports an odometer of 42,390 km and a battery level of
  70%, captured the following calendar day
- **WHEN** the new snapshot is captured and stored
- **THEN** the stored distance traveled is 40 km
- **AND** the stored battery used is 12%
- **AND** the stored days spanned is 1
- **AND** the stored km-per-percent value is approximately 3.33
- **AND** the stored estimated range is approximately 333 km

#### Scenario: A charging day stores a negative battery-used value and leaves both efficiency values NULL

- **GIVEN** a vehicle with a previously stored snapshot reporting a battery level of 60%
- **AND** its next nightly snapshot reports a battery level of 75% (the vehicle was charged
  overnight, net battery increased) and some non-negative distance traveled
- **WHEN** the new snapshot is captured and stored
- **THEN** the stored battery used is -15 (a negative value, stored as reported, not clamped)
- **AND** the stored km-per-percent value is NULL
- **AND** the stored estimated range value is NULL
- **AND** the stored distance traveled reflects the true odometer difference, unaffected by
  the negative battery-used value

#### Scenario: A parked day with zero battery change leaves both efficiency values NULL

- **GIVEN** a vehicle with a previously stored snapshot and a next nightly snapshot whose
  battery level is identical to the previous snapshot's battery level
- **WHEN** the new snapshot is captured and stored
- **THEN** the stored battery used is 0
- **AND** the stored km-per-percent value is NULL
- **AND** the stored estimated range value is NULL
- **AND** the stored distance traveled is still computed and stored normally (it may be
  zero or non-zero independent of the battery-used value)

#### Scenario: A multi-day gap stores the true multi-day delta and the number of days it spans

- **GIVEN** a vehicle whose previous stored snapshot is from two calendar days before the
  next nightly capture (the poller missed an intervening night)
- **AND** the vehicle traveled 80 km and consumed 20% battery across that whole gap
- **WHEN** the new snapshot is captured and stored
- **THEN** the stored distance traveled is 80 km (the true total across the gap, not
  averaged into a per-day figure)
- **AND** the stored battery used is 20%
- **AND** the stored days spanned is 2
- **AND** the stored km-per-percent and estimated range values are computed from the full
  80 km / 20% figures, not from any per-day approximation

#### Scenario: The first-ever snapshot of a vehicle has all five derived values NULL

- **GIVEN** a vehicle with no previously stored snapshot
- **WHEN** its first nightly snapshot is captured and stored
- **THEN** all five derived values (distance traveled, battery used, days spanned,
  km-per-percent, estimated range) are NULL on the stored snapshot
- **AND** this is not treated as a capture failure — the snapshot is stored successfully
  and the attempt is recorded as a success

#### Scenario: A same-day re-capture recomputes and refreshes all five derived values

- **GIVEN** a vehicle with a stored snapshot for calendar day N (itself computed against a
  predecessor from day N-1) and a stored snapshot for day N-1 before that
- **WHEN** a second capture for day N runs later the same day, replacing day N's stored
  snapshot with different odometer and battery readings
- **THEN** the replaced day-N row's five derived values are recomputed against the day N-1
  predecessor (not against the first day-N capture that was just replaced)
- **AND** the stored derived values reflect the second capture's odometer and battery
  readings, not the first capture's

#### Scenario: Existing history is backfilled when the capability is introduced

- **GIVEN** a sequence of previously stored snapshots for a vehicle across several
  consecutive calendar days, captured before this capability existed
- **WHEN** the schema change that introduces the five derived columns is applied
- **THEN** every row except the vehicle's oldest stored snapshot has all five derived
  values populated
- **AND** each populated row's values equal what capture-time computation would produce for
  that row and its immediate predecessor
- **AND** the vehicle's oldest stored snapshot has all five derived values NULL (it has no
  predecessor)

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

