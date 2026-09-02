## ADDED Requirements

### Requirement: Module-Scoped Database Schema
The account module's `accounts` and `tesla_tokens` tables SHALL live in a PostgreSQL schema named
`account`, distinct from the `public` schema and from every other module's schema. This SHALL be a
namespacing change only: it SHALL NOT alter any stored data, any constraint (primary key, foreign
key, unique, or check), any index, or any behavior of the module's public interface. No other
module SHALL be granted access to the `account` schema's tables — the module boundary
(`ai/architecture.md` §2, "no cross-module database leaks") is enforced identically before and
after this requirement, now additionally checkable at the database catalog level.

#### Scenario: The accounts and tesla_tokens tables resolve under the account schema
- **GIVEN** the account module's migrations have been applied
- **WHEN** the database catalog is queried for `account.accounts` and `account.tesla_tokens`
- **THEN** both resolve to their table (a non-null relation)
- **AND** neither `public.accounts` nor `public.tesla_tokens` resolves to a relation any longer

#### Scenario: Existing data, constraints, and indexes survive the schema move
- **GIVEN** accounts and Tesla connections that existed before the schema move
- **WHEN** the schema-move migration is applied
- **THEN** every account and Tesla connection row is preserved unchanged
- **AND** every primary key, foreign key, unique constraint, and check constraint on both tables
  continues to be enforced exactly as before

#### Scenario: The module's public interface is unaffected by the schema move
- **GIVEN** a caller of the account module's public interface (e.g. `AccessTokenFor`,
  `SaveTeslaTokens`, `UpsertFromOAuth`)
- **WHEN** the schema move is applied
- **THEN** every exported type name, method name, and method signature is unchanged
- **AND** no caller needs to change to keep working
