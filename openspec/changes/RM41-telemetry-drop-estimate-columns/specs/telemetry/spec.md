## MODIFIED Requirements

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
capability's read-time concern, never persisted here. **This is a CHANGE from the prior
revision of this requirement, under which the ledger also carried a frozen
verification-time snapshot pair — a start battery percentage estimate and an end battery
percentage estimate, recording what a companion estimation capability's live computation
produced at the moment the trio above was set. That snapshot pair is REMOVED by this
revision (`RM41-telemetry-drop-estimate-columns`), because the companion estimation
capability it was reserved for was descoped before it ever shipped, and the estimator
MAG-36 eventually shipped instead writes the trio's own start percentage directly — there
is no longer anything for a snapshot to capture.**

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
when no override has been set for that session. **This is a CHANGE from the prior revision
of this requirement, under which a retrieved session also carried its verification-time
snapshot pair (start percentage estimate, end percentage estimate) exactly as stored — that
snapshot pair is REMOVED by this revision (`RM41-telemetry-drop-estimate-columns`), because
the capability no longer stores it (see "Supercharger Session Ledger" above).**

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
