## ADDED Requirements

### Requirement: Poll Account Election

The telemetry capability SHALL elect exactly one account to poll each
distinct registered vehicle before running its per-account collection loop,
so a vehicle registered to more than one account is fetched at most once per
collection cycle. The election SHALL prefer a candidate account whose access
type for that vehicle is OWNER. When no candidate account has OWNER access
type for a vehicle, the election SHALL select any candidate account that has
registered the vehicle. When more than one candidate account is equally
preferred (two OWNER candidates, or no OWNER candidate and several
non-OWNER candidates), the election SHALL break the tie deterministically,
and the tie-break outcome SHALL NOT depend on the order the account module
returns candidates in. The election SHALL NOT skip a registered vehicle for
any reason, including a vehicle with no OWNER-access candidate at all —
every registered vehicle SHALL be assigned exactly one polling account on
every cycle. The election SHALL determine its outcome using only the vehicle
and access-type information already available from enumerating registered
vehicles; it SHALL NOT query or otherwise depend on whether any candidate
account's stored Tesla connection is currently usable.

#### Scenario: A vehicle registered to two accounts is polled by its OWNER account
- **GIVEN** a vehicle registered to two accounts, one with OWNER access type and one
  with DRIVER access type
- **WHEN** the election runs for a collection cycle
- **THEN** the OWNER account is the one elected to poll that vehicle
- **AND** the DRIVER account is not elected for that vehicle in the same cycle

#### Scenario: A vehicle with no OWNER-access account is still polled
- **GIVEN** a vehicle registered only to accounts whose access type for it is DRIVER, or
  whose access type was never captured
- **WHEN** the election runs for a collection cycle
- **THEN** one of those accounts is elected to poll the vehicle
- **AND** the vehicle is not omitted from the elected set for lacking an OWNER-access
  candidate

#### Scenario: A vehicle registered to exactly one account is always polled by that account
- **GIVEN** a vehicle registered to exactly one account, regardless of that account's
  access type for it
- **WHEN** the election runs for a collection cycle
- **THEN** that account is elected to poll the vehicle

#### Scenario: The election makes no connection-liveness check
- **GIVEN** a vehicle whose only candidate account's Tesla connection state is unknown
  to the election step
- **WHEN** the election runs for a collection cycle
- **THEN** a candidate account is still elected for that vehicle
- **AND** the election does not read or wait on any check of whether that account's
  stored Tesla connection is currently usable

#### Scenario: A tie between equally preferred candidates resolves the same way every time
- **GIVEN** a vehicle registered to two accounts that are equally preferred by the
  election rule (both OWNER, or neither OWNER)
- **WHEN** the election runs for the same input more than once
- **THEN** the same account is elected each time
- **AND** the outcome does not depend on the order the two candidate accounts were
  enumerated in

## MODIFIED Requirements

### Requirement: Per-Vehicle Isolation And Attempt Recording

The telemetry capability SHALL record exactly one attempt per vehicle per collection cycle, with an
outcome of success or failure and a reason. A failure collecting one vehicle SHALL NOT abort the
cycle — every other vehicle SHALL still be processed. The capability SHALL retry a transient
failure at most once per vehicle per cycle before recording it as a failure. Each recorded attempt
SHALL identify the account whose credentials performed the attempt, which — once a vehicle is
polled by exactly one elected account per cycle — is that vehicle's single elected account for the
cycle, not necessarily every account that has ever registered the vehicle.

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
- **AND** each attempt carries the id of the account whose credentials performed the
  attempt, the vehicle's Tesla id, the attempt time, an outcome of success or failure,
  and a reason

### Requirement: Latest Snapshot Read Port

The telemetry capability SHALL expose a read port method that returns the most recently captured
snapshot for each of a caller-supplied batch of vehicles, identified by their Tesla numeric ids, in
a single call. The read port SHALL return snapshots whose values are already expressed in their
display units, and SHALL NOT perform, or require its callers to perform, any unit conversion on
read. The read port SHALL return the TPMS tire pressure fields for all four corners, expressed in
PSI, alongside the existing snapshot fields. The capability SHALL NOT expose companion conversion
methods on the returned snapshot type; every returned field SHALL be directly usable in the unit
its name declares. The pressure fields SHALL remain nullable, preserving the nil/not-reported
distinction from the stored value itself rather than re-deriving it per call.

#### Scenario: Values are returned ready to use, with no conversion on read
- **GIVEN** a stored snapshot for a registered vehicle
- **WHEN** a caller retrieves it through the latest-snapshots-by-vehicle-batch method or
  the since-a-given-instant history method
- **THEN** the odometer and battery range fields are expressed in kilometres, the temperature
  fields in degrees Celsius, and the tire pressure fields in PSI
- **AND** the caller performs no arithmetic and calls no conversion method to obtain those units
- **AND** the returned type exposes no companion conversion method for any of those fields

#### Scenario: Nil fidelity is preserved by the stored pressure value
- **GIVEN** a snapshot row for which TPMS pressure was not reported (NULL in the database)
- **WHEN** a caller retrieves the snapshot through the latest-snapshots-by-vehicle-batch
  method or the since-a-given-instant history method and reads any of the four pressure
  fields
- **THEN** each pressure field is nil
- **AND** the caller can distinguish "not reported" from "reported 0.0 PSI"

#### Scenario: A batch of vehicle ids returns each vehicle's own latest snapshot
- **GIVEN** several registered vehicles, each with at least one stored snapshot, regardless
  of which account or accounts registered them
- **WHEN** a caller requests the latest snapshots for a batch containing those vehicles'
  Tesla ids
- **THEN** exactly one snapshot is returned per requested vehicle id that has a stored
  snapshot
- **AND** each returned snapshot is that vehicle's most recently captured one

#### Scenario: Callers never access the telemetry database directly
- **GIVEN** any caller that needs to display or process TPMS tire pressure data, or a
  vehicle's latest snapshot
- **WHEN** it obtains that data
- **THEN** it does so exclusively through the read port interface, reading the pressure
  fields on the returned Snapshot
- **AND** it imports no package from internal/telemetry/db

### Requirement: Snapshot History Read Port

The telemetry capability SHALL expose a read port through which other modules (the gateway in
particular) can retrieve the **history** of stored snapshots for a single vehicle — all snapshots
captured at or after a caller-supplied instant — without accessing the telemetry module's database
tables directly. The port SHALL identify the vehicle by its Tesla numeric id alone, and SHALL
return the existing `Snapshot` domain type (including all extracted typed fields and the raw
payload), ordered **oldest-first** by capture time. The window boundary is supplied by the caller
as an absolute instant (`since`, inclusive); the port SHALL NOT compute the window itself. Callers
SHALL receive an empty result (not an error) when the vehicle has no snapshots in the window. The
distance and range fields SHALL be returned in kilometres, already converted at capture time, and
the returned type SHALL carry no companion method for deriving them.

#### Scenario: History is returned oldest-first for a vehicle

- **GIVEN** a vehicle with several stored snapshots across different collection runs, all
  captured at or after a given instant
- **WHEN** the caller requests that vehicle's snapshot history since that instant (passing
  the vehicle's Tesla id and the instant)
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

#### Scenario: Snapshots are isolated to the requested vehicle

- **GIVEN** two vehicles, each with stored snapshots
- **WHEN** the caller requests history for one specific vehicle
- **THEN** only snapshots belonging to that vehicle are returned
- **AND** no snapshot belonging to the other vehicle appears in the result

#### Scenario: Callers never access the telemetry database directly

- **GIVEN** any caller that needs to display or use a vehicle's snapshot history
- **WHEN** it obtains that data
- **THEN** it does so exclusively through the telemetry module's read port interface
- **AND** it imports no package from `internal/telemetry/db`

### Requirement: Snapshot Updated-Since Read Port

The telemetry capability SHALL expose a read port through which other modules can retrieve every
stored snapshot for a single vehicle whose last-updated timestamp is at or after a
caller-supplied instant, without accessing the telemetry module's database tables directly. The
port SHALL identify the vehicle by its Tesla numeric id alone, and SHALL return the
existing `Snapshot` domain type. Callers SHALL receive an empty result (not an error) when no
snapshot for that vehicle has been updated at or after the given instant.

#### Scenario: Snapshots updated at or after the given instant are returned
- **GIVEN** a vehicle with snapshots last updated at various instants, some before and some at or
  after a given instant
- **WHEN** the caller requests that vehicle's snapshots updated since that instant
- **THEN** only the snapshots whose `UpdatedAt` is at or after the given instant are returned

#### Scenario: Empty result when nothing has been updated in the window
- **GIVEN** a vehicle with no snapshot updated at or after the requested instant
- **WHEN** the caller requests that vehicle's snapshots updated since that instant
- **THEN** an empty collection is returned, and no error is returned

#### Scenario: Callers never access the telemetry database directly for this port either
- **GIVEN** any caller that needs to detect which of a vehicle's snapshots changed recently
- **WHEN** it obtains that data
- **THEN** it does so exclusively through this read port
- **AND** it imports no package from `internal/telemetry/db`

### Requirement: Preceding-Snapshot Read Port

The telemetry capability SHALL expose a read port through which another module can
retrieve, for one vehicle, the single most recently captured snapshot whose capture
calendar day is strictly before a caller-supplied calendar day — without accessing the
telemetry module's database tables directly. The port SHALL identify the vehicle by its
Tesla numeric id alone, SHALL take the boundary as a whole calendar day (never an
instant), and SHALL return the existing `Snapshot` domain type. When the vehicle has no
snapshot captured before that day, the port SHALL return an absent result and no error —
"no predecessor exists" is a normal answer, not a failure. A genuine lookup failure SHALL
be reported as an error and SHALL NOT be represented as an absent result, so a transient
storage fault can never be mistaken by a caller for "this vehicle has no earlier
snapshot".

The boundary SHALL be evaluated against the snapshot's stored capture calendar day,
not against its precise capture instant. Consequently a snapshot captured on the
boundary day itself is never returned as its own predecessor, regardless of the
timezone the collector runs in.

The port SHALL reach the true predecessor however old it is — there SHALL be no
maximum lookback, no trailing-window limit and no fixed number of days beyond which
the predecessor is reported absent.

#### Scenario: The immediately preceding day's snapshot is returned
- **GIVEN** a vehicle with snapshots captured on three consecutive calendar days
- **WHEN** a caller requests the snapshot preceding the third day
- **THEN** the second day's snapshot is returned

#### Scenario: A predecessor many days older is still returned
- **GIVEN** a vehicle whose most recent snapshot was captured seven calendar days
  after its previous one (the collector missed six nights)
- **WHEN** a caller requests the snapshot preceding the later capture's day
- **THEN** the snapshot from seven days earlier is returned, not an absent result

#### Scenario: A vehicle's first-ever snapshot has no predecessor
- **GIVEN** a vehicle with exactly one stored snapshot
- **WHEN** a caller requests the snapshot preceding that snapshot's own capture day
- **THEN** an absent result is returned
- **AND** no error is returned

#### Scenario: A same-day re-capture is never its own predecessor
- **GIVEN** a vehicle with a snapshot for calendar day N−1
- **AND** a snapshot for calendar day N that was later replaced by a second capture on
  the same day N (the existing "latest capture for a calendar day wins" rule)
- **WHEN** a caller requests the snapshot preceding day N
- **THEN** the day N−1 snapshot is returned
- **AND** neither the replaced nor the replacing day-N snapshot is returned

#### Scenario: A vehicle with no snapshots at all returns an absent result
- **GIVEN** a vehicle for which no snapshot has ever been stored
- **WHEN** a caller requests the snapshot preceding any calendar day
- **THEN** an absent result and no error are returned
