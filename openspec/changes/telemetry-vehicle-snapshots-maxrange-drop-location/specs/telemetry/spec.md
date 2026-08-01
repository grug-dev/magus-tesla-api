## MODIFIED Requirements

### Requirement: Nightly Vehicle Snapshot Capture
The telemetry capability SHALL capture one immutable snapshot of every registered vehicle
across all accounts on each scheduled run. In addition to the fields already required, for
vehicles that are actively charging or have charge telemetry available at capture time, the
snapshot SHALL also store the following charge-telemetry fields: the energy added to the
battery since the charge session started (in kWh), the charger power (in kW), the charger
voltage (in V), the charger actual current (in A), and the usable battery level (in percent).
These five fields SHALL be stored as nullable values; a NULL value means the vehicle did not
report the field (or the snapshot predates this extraction), and SHALL NOT be interpreted as
zero. The snapshot SHALL also store the vehicle's lifetime max-range charge counter
(`max_range_charge_counter`) as a nullable integer — the count of how many times the vehicle
has been charged to its true 100% Maximum-Battery-Range limit. A NULL value for this field
means the vehicle did not report the counter at capture time or the snapshot predates this
extraction; a stored value of zero (non-NULL) means the vehicle reported zero such charges.
All other snapshot requirements (raw_data, append-only, miles storage, Km-companion
derivation, sentry-mode fidelity) remain unchanged.

#### Scenario: A snapshot for a charging vehicle includes charge-telemetry fields
- **GIVEN** a registered vehicle that is actively charging when the nightly snapshot is
  captured and its vehicle_data reports charge_energy_added, charger_power,
  charger_voltage, charger_actual_current, and usable_battery_level
- **WHEN** a collection cycle captures the snapshot
- **THEN** the stored snapshot includes non-NULL values for all five charge-telemetry
  fields matching the reported values
- **AND** the snapshot's raw_data still contains the full vehicle_data payload

#### Scenario: A snapshot for a parked non-charging vehicle stores NULL charge-telemetry
- **GIVEN** a registered vehicle that is parked and not charging when the snapshot is
  captured, so the vehicle_data reports no meaningful charge-telemetry values
- **WHEN** a collection cycle captures the snapshot
- **THEN** the five charge-telemetry fields on the stored snapshot are NULL
- **AND** all existing snapshot fields (battery level, range, odometer, etc.) are still
  populated normally

#### Scenario: Pre-enrichment snapshot rows have NULL for the charge-telemetry fields
- **GIVEN** a vehicle_snapshots row written before the charge-enrichment migration was
  applied
- **WHEN** a caller reads that snapshot through the telemetry read port
- **THEN** the five charge-telemetry fields are NULL
- **AND** the caller can still read the raw_data to extract the values retroactively
  if needed

#### Scenario: max_range_charge_counter is stored as a non-NULL value when reported
- **GIVEN** a registered vehicle whose vehicle_data reports a max_range_charge_counter
  value (including a value of zero) at capture time
- **WHEN** a collection cycle captures the snapshot
- **THEN** the stored snapshot includes a non-NULL max_range_charge_counter matching
  the reported counter value
- **AND** a reported value of zero is stored as non-NULL zero, distinguishable from
  NULL (which means not reported or row predates extraction)

#### Scenario: max_range_charge_counter is NULL for pre-extraction rows without backfill
- **GIVEN** a vehicle_snapshots row written before the max_range_charge_counter column
  was added AND whose raw_data does not contain a charge_state.max_range_charge_counter
  field (e.g. an older Tesla that did not report it)
- **WHEN** a caller reads that snapshot through the telemetry read port
- **THEN** max_range_charge_counter on the returned snapshot is nil (unknown), distinct
  from a value of zero
- **AND** the caller can still read the raw_data to check the charge_state path directly

#### Scenario: max_range_charge_counter is backfilled for pre-migration rows whose raw_data contains it
- **GIVEN** a vehicle_snapshots row written before the max_range_charge_counter column
  was added, but whose raw_data contains charge_state.max_range_charge_counter as a number
- **WHEN** the migration runs the one-shot backfill UPDATE
- **THEN** the row's max_range_charge_counter column is populated with the value from raw_data
- **AND** rows whose raw_data lacks that path remain NULL

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
  odometer, temperatures, locked, sentry mode, car version, max_range_charge_counter)
  are present and match the stored values for that snapshot
- **AND** max_range_charge_counter is nil when the stored value is NULL (pre-extraction
  row or vehicle did not report it), and non-nil when the stored value is non-NULL
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
