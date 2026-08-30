## ADDED Requirements

### Requirement: Vehicle Activation Status
Every registered vehicle SHALL carry an explicit status of exactly `Active` or `Inactive`. A newly
registered vehicle SHALL default to `Active`. A vehicle registered before this requirement existed
SHALL also read as `Active` once the status exists — unlike an account (which defaults to
`Inactive` with no retroactive change), every existing and newly-seeded vehicle starts `Active`.
`Inactive` on a vehicle is a deliberate retire switch, set only by direct intervention; no read or
write path in this module transitions a vehicle to `Inactive` on its own. An `Inactive` vehicle
SHALL be excluded from every vehicle read this module exposes (the per-account registered-vehicle
read and the all-accounts enumeration).

#### Scenario: A newly seeded vehicle is Active
- **GIVEN** an account with no registered vehicles
- **WHEN** the account module seeds a vehicle from a Tesla `ListVehicles` result
- **THEN** the vehicle is registered with status `Active`

#### Scenario: A vehicle registered before this requirement existed reads as Active
- **GIVEN** a vehicle that was registered before vehicle status was introduced
- **WHEN** vehicle status is introduced into the system
- **THEN** that vehicle's status is `Active`

#### Scenario: An Inactive vehicle is excluded from the per-account registered vehicle read
- **GIVEN** an account with one active registered vehicle and one inactive registered vehicle
- **WHEN** the account module is asked for the account's registered vehicles
- **THEN** only the active vehicle is returned

#### Scenario: Re-seeding never revives an Inactive vehicle's visibility by itself
- **GIVEN** an account with a vehicle whose status has been set to `Inactive`
- **WHEN** the account module is asked to seed vehicles from a Tesla `ListVehicles` result that
  includes that same vehicle's `tesla_id`
- **THEN** the existing row is left untouched (idempotent seeding, unchanged by this requirement)
- **AND** the vehicle remains excluded from the registered-vehicle read

## MODIFIED Requirements

### Requirement: Registered Vehicle Read Access
The account module SHALL expose a public interface to read all **active** vehicles registered to
an account, returning each vehicle's `tesla_id`, `vin`, `display_name`, `access_type`,
`exterior_color`, and `car_type` (and no `state`). The result SHALL reflect whatever is currently
persisted as `Active` for that account — empty when nothing active is registered, even if inactive
rows exist. The same fields, subject to the same active-only filter, SHALL be surfaced through the
cross-account read path used by background collection jobs.

#### Scenario: Reading vehicles for an account that has some registered (updated)
- **GIVEN** an account with two active vehicles registered, one with `exterior_color =
  "PearlWhite"` and `car_type = "modely"` already captured and one with both still `NULL`
- **WHEN** the account module is asked for the account's registered vehicles
- **THEN** it returns both vehicles with their `tesla_id`, `vin`, `display_name`, `access_type`,
  `exterior_color`, and `car_type`
- **AND** the first vehicle's `ExteriorColor` is `"PearlWhite"` and `CarType` is `"modely"`
- **AND** the second vehicle's `ExteriorColor` and `CarType` are `nil` (not an error)

#### Scenario: The cross-account read path also surfaces the two config fields
- **GIVEN** two accounts, each with one active vehicle registered, one vehicle with
  `exterior_color = "SolidBlack"` and `car_type = "modelx"` captured and the other with both `NULL`
- **WHEN** the account module is asked for all registered vehicles across all accounts
- **THEN** the captured vehicle is returned with `ExteriorColor = "SolidBlack"` and
  `CarType = "modelx"`
- **AND** the uncaptured vehicle is returned with `ExteriorColor = nil` and `CarType = nil`

#### Scenario: An account with only inactive vehicles reads as empty
- **GIVEN** an account whose only registered vehicle has status `Inactive`
- **WHEN** the account module is asked for the account's registered vehicles
- **THEN** it returns an empty result, not an error
