## ADDED Requirements

### Requirement: Nightly Vehicle Snapshot Capture
The telemetry capability SHALL, on a scheduled server-side run, capture one immutable snapshot of
every registered vehicle across all accounts and store it. For each successfully captured vehicle
the stored snapshot SHALL include the owning account id, the vehicle's Tesla id, the capture time,
the full raw `vehicle_data` payload, AND extracted typed fields (battery level, rated battery
range, charging state, charge limit, odometer, inside temperature, outside temperature, locked,
sentry mode, car version, latitude, and longitude). Distance and range fields SHALL be stored in
the Tesla-native unit (miles); the capability SHALL NOT store a kilometre value as a field — the
kilometre equivalent is derived on read. Snapshots SHALL be append-only: a new capture SHALL never
overwrite or delete an earlier snapshot for the same vehicle.

#### Scenario: An online vehicle is captured with raw and typed data
- **GIVEN** a registered vehicle whose Tesla connection is valid and whose vehicle is online
- **WHEN** a collection cycle runs
- **THEN** a snapshot is stored for that vehicle carrying its owning account id, its Tesla id, and
  the capture time
- **AND** the snapshot stores the full raw `vehicle_data` payload
- **AND** the snapshot stores the extracted typed fields (battery level, rated range, charging
  state, charge limit, odometer, inside/outside temperature, locked, sentry mode, car version,
  latitude, longitude)
- **AND** the distance and range fields are stored in miles, with no kilometre field stored

#### Scenario: Sentry-mode fidelity is preserved
- **GIVEN** a captured vehicle whose `vehicle_data` does not report sentry mode at all
- **WHEN** its snapshot is stored
- **THEN** the stored sentry-mode value is recorded as absent (unknown), distinct from a reported
  value of off
- **AND** a vehicle that reports sentry mode off stores off, and one that reports on stores on

#### Scenario: Snapshots accumulate as history
- **GIVEN** a vehicle that already has a stored snapshot from a previous run
- **WHEN** a later collection cycle captures it again
- **THEN** a new snapshot row is added
- **AND** the earlier snapshot is unchanged and still present

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
