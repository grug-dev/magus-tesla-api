# account-vehicle-registry Specification

## Purpose

The `account-vehicle-registry` capability is the platform's per-account durable registry of Tesla
vehicles. It persists, for each account, the vehicles returned by a one-time `tesla.ListVehicles`
call — keyed by the Tesla Fleet API vehicle `id` (`tesla_id`) — so downstream domain modules
(battery, charging, …) can key historical metrics off stable per-vehicle records, and so the
dashboard no longer needs to call Tesla on every page load. The registry is owned by the
`account` module; other modules obtain registered vehicles only through the account module's public
interface, never by reading the table directly.

The volatile vehicle `state` (online/asleep) returned by the Tesla API is intentionally NOT
persisted: once vehicles are registered the dashboard reads from the DB, so any stored "state"
would be stale and mislead users about vehicle liveness.

## Requirements

### Requirement: Per-Account Vehicle Registry Storage
The account module SHALL persist a registry of Tesla vehicles linked to each account, storing, per
vehicle, the Tesla Fleet API vehicle `id` (`tesla_id`), the `vin`, and the `display_name`. Each
vehicle row SHALL be owned by exactly one account via a foreign key to `accounts(id)`. The volatile
vehicle `state` returned by the Tesla API SHALL NOT be persisted. The account module SHALL own the
registry table; other modules SHALL NOT read it directly and SHALL obtain registered vehicles only
through the account module's public interface.

#### Scenario: A vehicle is registered under an account
- **GIVEN** an account that has no registered vehicles
- **WHEN** the account module is asked to seed vehicles from a Tesla `ListVehicles` result
- **THEN** one row is inserted per vehicle with its `tesla_id`, `vin`, and `display_name`
- **AND** each row is linked to the account via its `account_id` foreign key

#### Scenario: Volatile state is not stored
- **GIVEN** a Tesla vehicle listing whose vehicles carry a `state` value (e.g. "online")
- **WHEN** those vehicles are registered
- **THEN** no `state` field is stored on the vehicle rows

#### Scenario: Another module cannot read the registry table directly
- **GIVEN** a module outside `internal/account` that wants an account's vehicles
- **WHEN** it needs the account's registered vehicles
- **THEN** it obtains them by calling the account module's public interface
- **AND** it does not query the registry table directly

### Requirement: Idempotent Vehicle Seeding
Seeding vehicles for an account SHALL be idempotent per `(account_id, tesla_id)`. Inserting vehicles
that already exist for that account SHALL create no duplicate rows and SHALL NOT change the stored
`vin` or `display_name` of an already-registered vehicle. Uniqueness SHALL be enforced by the data
store (`UNIQUE (account_id, tesla_id)`), so a concurrent or repeated seed cannot duplicate a
vehicle.

#### Scenario: Re-seeding an already-registered vehicle inserts nothing
- **GIVEN** an account that already has a vehicle registered with `tesla_id` T
- **WHEN** seeding is attempted again with the same `tesla_id` T for that account
- **THEN** no new row is created
- **AND** the existing vehicle's `display_name` and `vin` are left unchanged

#### Scenario: A truly new vehicle is added; existing ones are untouched
- **GIVEN** an account with vehicles T1 and T2 already registered
- **WHEN** a Tesla listing returns T1, T2, and a new T3 with a changed display name for T1
- **THEN** only T3 is inserted
- **AND** T1's stored `display_name` is not overwritten with the changed name

#### Scenario: Concurrent seeds do not duplicate a vehicle
- **GIVEN** two concurrent seed attempts for the same account and the same `tesla_id`
- **WHEN** both attempt to insert that vehicle
- **THEN** the data store rejects the duplicate via its unique constraint
- **AND** the account ends up with exactly one row for that `tesla_id`

### Requirement: Registered Vehicle Read Access
The account module SHALL expose a public interface to read all vehicles registered to an account,
returning each vehicle's `tesla_id`, `vin`, and `display_name` (and no `state`). The result SHALL
reflect whatever is currently persisted for that account — empty when nothing is registered.

#### Scenario: Reading vehicles for an account that has some registered
- **GIVEN** an account with two vehicles registered
- **WHEN** the account module is asked for the account's registered vehicles
- **THEN** it returns both vehicles with their `tesla_id`, `vin`, and `display_name`

#### Scenario: Reading vehicles for an account that has none registered
- **GIVEN** an account with no vehicles registered
- **WHEN** the account module is asked for the account's registered vehicles
- **THEN** it returns an empty result (not an error)