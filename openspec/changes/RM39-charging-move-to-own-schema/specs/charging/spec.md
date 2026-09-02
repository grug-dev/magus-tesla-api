## ADDED Requirements

### Requirement: Module-Scoped Database Schema
The charging module's `manual_charge_entries` and `supercharger_sessions` tables SHALL live
in a PostgreSQL schema named `charging`, distinct from the `public` schema and from every
other module's schema. This SHALL be a namespacing change only with respect to
`manual_charge_entries`: it SHALL NOT alter any stored data, any constraint, any index, or
any behavior of the module's public interface (`Writer`, `Reader`) for that table. No other
module SHALL be granted access to the `charging` schema's tables — the module boundary
(`ai/architecture.md` §2, "no cross-module database leaks") is enforced identically before
and after this requirement, now additionally checkable at the database catalog level.

The `vehicle_metric_watermarks.source` vocabulary collision this requirement's companion
table rename (see "Supercharger Sessions Table Renamed" below) creates on
`internal/analytics`'s own table is explicitly OUT OF SCOPE for this requirement — it is
analytics' table, not charging's, and its resolution is specified (but not implemented) in
this change's `design.md` "D8 — Boundary" section.

#### Scenario: The manual_charge_entries and supercharger_sessions tables resolve under the charging schema
- **GIVEN** the charging module's migrations have been applied
- **WHEN** the database catalog is queried for `charging.manual_charge_entries` and
  `charging.supercharger_sessions`
- **THEN** both resolve to their table (a non-null relation)
- **AND** neither `public.manual_charge_entries` nor `public.charge_sessions` nor
  `charging.charge_sessions` resolves to a relation any longer

#### Scenario: Existing manual charge entries survive the schema move unchanged
- **GIVEN** manual charge entries already stored for one or more accounts
- **WHEN** the schema-move migration is applied
- **THEN** every row is preserved unchanged
- **AND** the table's constraints and both indexes
  (`idx_manual_charge_entries_vehicle_time`, `idx_manual_charge_entries_account_time`)
  continue to be enforced exactly as before

#### Scenario: The manual_charge_entries public interface is unaffected by the schema move
- **GIVEN** a caller of `charging.Writer` or `charging.Reader`
- **WHEN** the schema move is applied
- **THEN** every exported type name, method name, and method signature is unchanged
- **AND** the returned data is identical to what the same call returned before the move
- **AND** no caller (`internal/gateway`, `internal/analytics`) needs to change to keep working

### Requirement: Supercharger Sessions Table Renamed
The table previously named `charge_sessions` SHALL be renamed to `supercharger_sessions` in
the same migration that moves it into the `charging` schema, because it is a dense,
Supercharger-only mirror and its old name over-claimed coverage of all charging activity.
Every row, the primary key, and the `UNIQUE (account_id, session_id)` constraint SHALL
survive unchanged (including their catalog names — this rename does NOT extend to
`charge_sessions_pkey` or `charge_sessions_account_session_unique`, a deliberately narrower
scope recorded in `design.md`'s "Rename scope" decision). The index
`idx_charge_sessions_vehicle_stop` SHALL be renamed to
`idx_supercharger_sessions_vehicle_stop`, and the CHECK constraint
`charge_sessions_pct_source_required` SHALL be renamed to
`supercharger_sessions_pct_source_required`, both preserving their exact prior definitions.
The sqlc-generated Go model SHALL be renamed from `ChargeSession` to `SuperchargerSession`
with an identical field list — this is a deliberate exception to this module's general
schema-move-preserves-Go-names rule, because the rename's whole purpose is to retire the
`charge_sessions` name everywhere, including in the code that reads it most. The two sqlc
query names that embedded the old table name, `MirrorChargeSession` and `VerifyChargeSession`,
SHALL be renamed to `MirrorSuperchargerSession` and `VerifySuperchargerSession`, with no
change to either query's parameters, WHERE-scoping, or column effects.

#### Scenario: The renamed table resolves and the old name does not
- **GIVEN** this tier's migration has been applied
- **WHEN** the database catalog is queried for `charging.supercharger_sessions`
- **THEN** it resolves to the same table `public.charge_sessions` (later `charging.charge_sessions`)
  identified before this migration, by unchanged primary key values on every existing row
- **AND** `charge_sessions` resolves to no relation, under `public` or under `charging`

#### Scenario: The renamed index and constraint preserve their definitions
- **GIVEN** this tier's migration has been applied
- **WHEN** the database catalog is queried for indexes and constraints on
  `charging.supercharger_sessions`
- **THEN** `idx_supercharger_sessions_vehicle_stop` exists, covers
  `(account_id, tesla_id, charge_stop_date_time)`, and `idx_charge_sessions_vehicle_stop`
  does not exist
- **AND** `supercharger_sessions_pct_source_required` exists with the identical CHECK
  expression `charge_sessions_pct_source_required` had, which no longer exists
- **AND** the primary key constraint and `UNIQUE (account_id, session_id)` constraint still
  exist, under their original `charge_sessions_pkey` / `charge_sessions_account_session_unique`
  names (per this requirement's deliberately narrower rename scope)

#### Scenario: The renamed Go type carries an identical field set
- **GIVEN** `make sqlc` has regenerated `internal/charging/db/models.go` against this tier's
  migration
- **WHEN** the generated `SuperchargerSession` struct is compared to the pre-migration
  `ChargeSession` struct
- **THEN** every field name, type, and declaration order is identical — only the struct's
  own type identifier changed
- **AND** no other generated struct (`ManualChargeEntry`) changed at all

#### Scenario: Callers reach the renamed table only through unchanged public ports
- **GIVEN** a caller of `charging.SessionWriter`, `charging.SessionReader`,
  `charging.SuperchargerSessionAnalyticsReader`, or `charging.SessionVerifier`
- **WHEN** this tier's rename is applied
- **THEN** every exported type name, method name, and method signature on these ports is
  unchanged
- **AND** the data returned is identical to what the same call returned before the rename
- **AND** no caller (`internal/gateway`, `internal/analytics`, `internal/app`) needs to
  change to keep working, because none of them imports `chargingdb` directly
