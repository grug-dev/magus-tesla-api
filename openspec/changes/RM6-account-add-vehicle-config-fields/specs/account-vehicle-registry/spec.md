## ADDED Requirements

### Requirement: Static Vehicle Config Capture (Conditional Update)
The account module SHALL expose a public port method that persists two static Tesla
`vehicle_config` attributes — `exterior_color` and `car_type` — for a registered vehicle,
identified by `(account_id, tesla_id)`, but ONLY when at least one of the two columns is not yet
captured. The underlying write condition SHALL be `(exterior_color IS NULL OR car_type IS NULL)` —
OR-semantics, not AND — so that a row with only one of the two values already captured is still
eligible for a self-healing write of the missing value, and a row with both values already
captured is never overwritten by a later call. `updated_at` on the vehicle row SHALL change if and
only if the write condition matches and a row is actually updated; a call that matches zero rows
(both values already captured) SHALL leave `updated_at` unchanged.

`NULL` for either column SHALL mean "not yet captured" — the account module SHALL NOT accept or
store an empty string as a substitute for `NULL`; a caller supplying an empty string for either
value is a caller-side contract violation, not a state this port method is responsible for
guarding against (the account module SHALL NOT import `internal/tesla`; the caller — the nightly
telemetry collector — supplies the two observed values as plain, already-validated non-empty
strings).

#### Scenario: Capturing config for a vehicle with neither value yet set
- **GIVEN** a registered vehicle with `exterior_color = NULL` and `car_type = NULL`
- **WHEN** the account module is asked to set `exterior_color = "PearlWhite"` and
  `car_type = "modely"` for that vehicle
- **THEN** both columns are persisted with the supplied values
- **AND** `updated_at` is updated to reflect the write
- **AND** reading that account's registered vehicles returns the vehicle with
  `ExteriorColor = "PearlWhite"` and `CarType = "modely"`

#### Scenario: A vehicle with both values already captured is never overwritten
- **GIVEN** a registered vehicle with `exterior_color = "PearlWhite"` and `car_type = "modely"`
  already persisted
- **WHEN** the account module is asked to set `exterior_color = "SolidBlack"` and
  `car_type = "modelx"` for that same vehicle
- **THEN** the stored values remain `"PearlWhite"` and `"modely"` — the write does not apply
- **AND** `updated_at` on that vehicle row is unchanged

#### Scenario: A partially-captured vehicle self-heals the missing value
- **GIVEN** a registered vehicle with `exterior_color = "PearlWhite"` already persisted but
  `car_type = NULL`
- **WHEN** the account module is asked to set `exterior_color = "PearlWhite"` and
  `car_type = "modely"` for that vehicle
- **THEN** `car_type` is persisted as `"modely"`
- **AND** `exterior_color` remains `"PearlWhite"`
- **AND** `updated_at` is updated to reflect the write

#### Scenario: Reading a vehicle whose config has not yet been captured
- **GIVEN** a registered vehicle that has never had a config-capture write applied
- **WHEN** the account module is asked for that account's registered vehicles
- **THEN** the vehicle is returned with `ExteriorColor = nil` and `CarType = nil` (not an error,
  not an empty string)

## MODIFIED Requirements

### Requirement: Per-Account Vehicle Registry Storage
The account module SHALL persist a registry of Tesla vehicles linked to each account, storing, per
vehicle, the Tesla Fleet API vehicle `id` (`tesla_id`), the `vin`, the `display_name`, the
`access_type`, and two static `vehicle_config` attributes — `exterior_color` and `car_type`. Each
vehicle row SHALL be owned by exactly one account via a foreign key to `accounts(id)`. The volatile
vehicle `state` returned by the Tesla API SHALL NOT be persisted. The account module SHALL own the
registry table; other modules SHALL NOT read it directly and SHALL obtain registered vehicles only
through the account module's public interface. `exterior_color` and `car_type` SHALL be nullable
with no default value: `NULL` means "not yet captured" and is the state of every vehicle at
registration time, since the Tesla `ListVehicles` call that seeds the registry does not return
`vehicle_config` — these two columns are populated later, once per vehicle, through the conditional
update capture contract above.

#### Scenario: A vehicle is registered under an account (updated)
- **GIVEN** an account that has no registered vehicles
- **WHEN** the account module is asked to seed vehicles from a Tesla `ListVehicles` result
- **THEN** one row is inserted per vehicle with its `tesla_id`, `vin`, `display_name`, and
  `access_type`
- **AND** `exterior_color` and `car_type` are stored as `NULL` for the new row, since seeding never
  supplies them
- **AND** each row is linked to the account via its `account_id` foreign key

### Requirement: Registered Vehicle Read Access
The account module SHALL expose a public interface to read all vehicles registered to an account,
returning each vehicle's `tesla_id`, `vin`, `display_name`, `access_type`, `exterior_color`, and
`car_type` (and no `state`). The result SHALL reflect whatever is currently persisted for that
account — empty when nothing is registered. The same fields SHALL be surfaced through the
cross-account read path used by background collection jobs.

#### Scenario: Reading vehicles for an account that has some registered (updated)
- **GIVEN** an account with two vehicles registered, one with `exterior_color = "PearlWhite"` and
  `car_type = "modely"` already captured and one with both still `NULL`
- **WHEN** the account module is asked for the account's registered vehicles
- **THEN** it returns both vehicles with their `tesla_id`, `vin`, `display_name`, `access_type`,
  `exterior_color`, and `car_type`
- **AND** the first vehicle's `ExteriorColor` is `"PearlWhite"` and `CarType` is `"modely"`
- **AND** the second vehicle's `ExteriorColor` and `CarType` are `nil` (not an error)

#### Scenario: The cross-account read path also surfaces the two config fields
- **GIVEN** two accounts, each with one vehicle registered, one vehicle with
  `exterior_color = "SolidBlack"` and `car_type = "modelx"` captured and the other with both `NULL`
- **WHEN** the account module is asked for all registered vehicles across all accounts
- **THEN** the captured vehicle is returned with `ExteriorColor = "SolidBlack"` and
  `CarType = "modelx"`
- **AND** the uncaptured vehicle is returned with `ExteriorColor = nil` and `CarType = nil`
