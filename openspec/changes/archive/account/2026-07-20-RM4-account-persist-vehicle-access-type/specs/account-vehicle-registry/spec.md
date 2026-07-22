## ADDED Requirements

### Requirement: Vehicle Access-Type Persistence
The account module SHALL persist, per registered vehicle, the Tesla Fleet API `access_type` value
(`OWNER` or `DRIVER`) supplied at seed time. The stored value SHALL be nullable: `NULL` means
"not yet captured" (the vehicle was seeded before `access_type` was tracked, or the caller did not
supply one). The data store SHALL reject any `access_type` value that is neither `OWNER`, `DRIVER`,
nor `NULL`. The volatile distinction between owner and driver is never re-derived from Tesla on a
read path — it is read from the persisted registry row.

#### Scenario: Seeding a vehicle with access_type OWNER persists and returns it
- **GIVEN** an account with no registered vehicles
- **WHEN** the account module is asked to seed a vehicle with `access_type` set to `"OWNER"`
- **THEN** the vehicle is registered with `access_type = 'OWNER'`
- **AND** reading that account's registered vehicles returns the vehicle with `AccessType` equal
  to `"OWNER"`

#### Scenario: Seeding a vehicle with access_type DRIVER persists and returns it
- **GIVEN** an account with no registered vehicles
- **WHEN** the account module is asked to seed a vehicle with `access_type` set to `"DRIVER"`
- **THEN** the vehicle is registered with `access_type = 'DRIVER'`
- **AND** reading that account's registered vehicles returns the vehicle with `AccessType` equal
  to `"DRIVER"`

#### Scenario: Seeding a vehicle without access_type stores NULL
- **GIVEN** an account with no registered vehicles
- **WHEN** the account module is asked to seed a vehicle without supplying `access_type` (nil)
- **THEN** the vehicle is registered with `access_type = NULL`
- **AND** reading that account's registered vehicles returns the vehicle with `AccessType` equal
  to `nil`

#### Scenario: Reads surface access_type through all public read paths
- **GIVEN** two accounts — A with one OWNER vehicle and B with one DRIVER vehicle
- **WHEN** the account module is asked for A's registered vehicles
- **THEN** A's vehicle is returned with `AccessType` equal to `"OWNER"`
- **WHEN** the account module is asked for all registered vehicles across all accounts
- **THEN** A's vehicle carries `AccessType = "OWNER"` and B's vehicle carries `AccessType = "DRIVER"`

#### Scenario: The data store rejects an invalid access_type value
- **GIVEN** a seed attempt that supplies an `access_type` value not in the set {OWNER, DRIVER, NULL}
- **WHEN** the account module attempts to persist that vehicle
- **THEN** the data store rejects the insert with a constraint violation
- **AND** no vehicle row is created for that seed attempt

## MODIFIED Requirements

### Requirement: Per-Account Vehicle Registry Storage
The account module SHALL persist a registry of Tesla vehicles linked to each account, storing, per
vehicle, the Tesla Fleet API vehicle `id` (`tesla_id`), the `vin`, the `display_name`, and the
`access_type`. Each vehicle row SHALL be owned by exactly one account via a foreign key to
`accounts(id)`. The volatile vehicle `state` returned by the Tesla API SHALL NOT be persisted.
The account module SHALL own the registry table; other modules SHALL NOT read it directly and SHALL
obtain registered vehicles only through the account module's public interface.

#### Scenario: A vehicle is registered under an account (updated)
- **GIVEN** an account that has no registered vehicles
- **WHEN** the account module is asked to seed vehicles from a Tesla `ListVehicles` result
- **THEN** one row is inserted per vehicle with its `tesla_id`, `vin`, `display_name`, and
  `access_type`
- **AND** each row is linked to the account via its `account_id` foreign key

### Requirement: Idempotent Vehicle Seeding
Seeding vehicles for an account SHALL be idempotent per `(account_id, tesla_id)`. Inserting
vehicles that already exist for that account SHALL create no duplicate rows and SHALL NOT change the
stored `vin`, `display_name`, or `access_type` of an already-registered vehicle. Uniqueness SHALL
be enforced by the data store (`UNIQUE (account_id, tesla_id)`), so a concurrent or repeated seed
cannot duplicate a vehicle.

#### Scenario: Re-seeding an already-registered vehicle with a different access_type changes nothing
- **GIVEN** an account that already has a vehicle registered with `access_type = 'OWNER'`
- **WHEN** seeding is attempted again with the same `tesla_id` but `access_type = 'DRIVER'`
- **THEN** no new row is created
- **AND** the existing vehicle's `access_type` remains `'OWNER'`

### Requirement: Registered Vehicle Read Access
The account module SHALL expose a public interface to read all vehicles registered to an account,
returning each vehicle's `tesla_id`, `vin`, `display_name`, and `access_type` (and no `state`).
The result SHALL reflect whatever is currently persisted for that account — empty when nothing is
registered.

#### Scenario: Reading vehicles for an account that has some registered (updated)
- **GIVEN** an account with two vehicles registered, one with `access_type = 'OWNER'` and one
  with `access_type = NULL`
- **WHEN** the account module is asked for the account's registered vehicles
- **THEN** it returns both vehicles with their `tesla_id`, `vin`, `display_name`, and `access_type`
- **AND** the second vehicle's `access_type` is `nil` (not an error)
