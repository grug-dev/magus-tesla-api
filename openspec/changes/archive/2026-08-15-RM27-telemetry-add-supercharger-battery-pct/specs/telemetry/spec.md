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
directly. The port SHALL provide two read methods: one returning all sessions for a given
account (scoped to the account, ordered newest-first, with a limit), and one returning
sessions for a specific vehicle within an account (also ordered newest-first, with a
limit). Callers SHALL receive an empty non-nil result when no sessions exist for the given
scope. Every returned session SHALL carry its battery-percentage verification trio (start
percentage, end percentage, source label) exactly as stored — NULL when no override has
been set for that session — and its verification-time snapshot pair (start percentage
estimate, end percentage estimate) exactly as stored — NULL when no verification has
occurred for that session.

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

#### Scenario: Callers never access the telemetry database directly
- **GIVEN** any caller that needs to display Supercharger session data
- **WHEN** it obtains that data
- **THEN** it does so exclusively through the SuperchargerReader port interface
- **AND** it imports no package from internal/telemetry/db
