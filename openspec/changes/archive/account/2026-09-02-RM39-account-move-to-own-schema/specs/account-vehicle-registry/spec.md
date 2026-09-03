## ADDED Requirements

### Requirement: Module-Scoped Database Schema
The vehicle registry's `vehicles` table SHALL live in a PostgreSQL schema named `account`,
distinct from the `public` schema and from every other module's schema — the same schema that
holds the account module's `accounts` and `tesla_tokens` tables, since the registry is owned by
`internal/account`. This SHALL be a namespacing change only: it SHALL NOT alter any stored data,
any constraint (primary key, foreign key, unique, or check), any index, or any behavior of the
registry's read/write interface.

#### Scenario: The vehicles table resolves under the account schema
- **GIVEN** the account module's migrations have been applied
- **WHEN** the database catalog is queried for `account.vehicles`
- **THEN** it resolves to its table (a non-null relation)
- **AND** `public.vehicles` no longer resolves to a relation

#### Scenario: Existing vehicle registrations, constraints, and indexes survive the schema move
- **GIVEN** vehicles already registered under one or more accounts, including their
  `access_type`, `exterior_color`, `car_type`, and `status` values
- **WHEN** the schema-move migration is applied
- **THEN** every registered vehicle row is preserved unchanged
- **AND** the `UNIQUE (account_id, tesla_id)` constraint, the `access_type`/`status` check
  constraints, the foreign key to `accounts`, and the `vin` index continue to be enforced exactly
  as before

#### Scenario: The registry's read interface is unaffected by the schema move
- **GIVEN** a caller of the registry's read interface (e.g. `RegisteredVehicles`,
  `AllRegisteredVehicles`)
- **WHEN** the schema move is applied
- **THEN** every exported type name, method name, and method signature is unchanged
- **AND** the returned vehicle data is identical to what the same call returned before the move
