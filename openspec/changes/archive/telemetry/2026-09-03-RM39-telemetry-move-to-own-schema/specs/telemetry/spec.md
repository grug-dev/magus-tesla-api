## ADDED Requirements

### Requirement: Module-Scoped Database Schema
The telemetry module's four tables SHALL live in a PostgreSQL schema named `telemetry`,
distinct from the `public` schema and from every other module's schema — `vehicle_snapshots`,
`supercharger_history` (renamed by the companion requirement below), `poll_attempts` and
`poll_runs`.
With respect to `vehicle_snapshots`, `poll_attempts` and `poll_runs` this SHALL be a
namespacing change only: it SHALL NOT alter any stored data, any constraint, any index, or
any behavior of the module's public interfaces (`Collector`, `Reader`, `SuperchargerReader`,
`RunWriter`). No other module SHALL be granted access to the `telemetry` schema's tables —
the module boundary (`ai/architecture.md` §2, "no cross-module database leaks") is enforced
identically before and after this requirement, now additionally checkable at the database
catalog level.

The goose version-tracking table SHALL remain outside this schema, in `public`, so that every
module continues to record its migrations against one shared ledger.

#### Scenario: All four telemetry tables resolve under the telemetry schema
- **GIVEN** the telemetry module's migrations have been applied
- **WHEN** the database catalog is queried for `telemetry.vehicle_snapshots`,
  `telemetry.supercharger_history`, `telemetry.poll_attempts` and `telemetry.poll_runs`
- **THEN** all four resolve to their table (a non-null relation)
- **AND** none of `public.vehicle_snapshots`, `public.supercharger_sessions`,
  `public.poll_attempts` or `public.poll_runs` resolves to a relation any longer

#### Scenario: The charging module's identically-named table is untouched
- **GIVEN** `charging.supercharger_sessions` exists (renamed into that name by the charging
  module's own earlier change)
- **WHEN** this change's schema move and table rename are applied
- **THEN** `charging.supercharger_sessions` still resolves to its own table, with its own rows
- **AND** it is a different relation from `telemetry.supercharger_history` — no statement in
  this module resolves a telemetry table name through a search path that could reach the
  charging schema

#### Scenario: Existing telemetry rows survive the schema move unchanged
- **GIVEN** snapshots, Supercharger rows, poll attempts and poll runs already stored for one
  or more accounts
- **WHEN** the schema-move migration is applied
- **THEN** every row in every one of the four tables is preserved unchanged, with the same
  row count per table as immediately before the migration
- **AND** every constraint and every index continues to be enforced with its exact prior
  definition, column set and column order

#### Scenario: The telemetry public interfaces are unaffected by the schema move
- **GIVEN** a caller of `telemetry.Collector`, `telemetry.Reader`,
  `telemetry.SuperchargerReader` or `telemetry.RunWriter`
- **WHEN** the schema move is applied
- **THEN** every exported method name and method signature on those ports is unchanged
- **AND** the data returned by every read method is identical to what the same call returned
  before the move
- **AND** no caller (`internal/analytics`, `internal/app`, `cmd/*`) needs to change to keep
  working, because none of them imports `telemetrydb` directly

#### Scenario: The schema move is reversible
- **GIVEN** the schema-move migration has been applied
- **WHEN** it is rolled back by one step
- **THEN** all four tables resolve under `public` again under their pre-migration names
- **AND** every renamed catalog object carries its pre-migration name again
- **AND** the `telemetry` schema no longer exists, having been dropped without needing to
  cascade over any object it did not create

### Requirement: Supercharger History Table Renamed
The table previously named `supercharger_sessions` and owned by this module SHALL be renamed
to `supercharger_history` in the same migration that moves it into the `telemetry` schema.
The old name over-claimed kinship with `vehicle_snapshots`: `vehicle_snapshots` is
append-only, one row per poll, whereas this table is **upserted** — Tesla settles fees after
a session ends, so a row is never final on first insert. `_history` also names the Fleet API
endpoint the rows come from. The name is additionally freed for correctness reasons outside
this module: the charging module already holds a table named `supercharger_sessions`, and two
live tables one schema apart sharing a base name is the ambiguity this rename retires.

Every row SHALL survive unchanged, and every constraint and index SHALL preserve its exact
prior definition, column set and column order. EVERY catalog object still carrying the old
table name SHALL be renamed to follow it — the primary key, the `session_id` unique
constraint, the five CHECK constraints PostgreSQL auto-named from inline column constraints,
and the two standalone indexes. **The completeness criterion is the catalog, not a list**:
after this migration no relation, index or constraint owned by this module SHALL have a name
beginning `supercharger_sessions`. PostgreSQL renames none of these automatically, and a
constraint name is read in exactly one place — an error message, under pressure — so a
surviving old name would print retired vocabulary against a table the whole system calls
`supercharger_history`.

The module's own domain type SHALL be renamed from `SuperchargerSession` to
`SuperchargerHistory`, and the sqlc-generated model for the table SHALL follow the table's new
name with an identical field list — a deliberate exception to this change's general
schema-move-preserves-Go-names rule, because the rename's whole purpose is to retire the old
vocabulary everywhere, including in the code that reads it most. The five sqlc query names
that embedded the old table name SHALL be renamed to name `SuperchargerHistory` instead, with
no change to any query's parameters, predicates, ordering or column effects.

The public port SHALL be left **deliberately half-renamed**: `SuperchargerReader`, its four
`SuperchargerSessions*` method names and its constructor keep their current names while
returning `[]SuperchargerHistory`. This mismatch is the designed outcome of this change, not
an omission — the port has a large cross-module footprint and its rename is a separate,
sequenced change. A reviewer SHALL NOT flag the mismatch as incomplete work, and no
implementer SHALL rename the port as part of this change.

#### Scenario: The renamed table resolves and the old name does not
- **GIVEN** this change's migration has been applied
- **WHEN** the database catalog is queried for `telemetry.supercharger_history`
- **THEN** it resolves to the same table previously identified as
  `public.supercharger_sessions`, by unchanged primary key values on every existing row
- **AND** neither `public.supercharger_sessions` nor `telemetry.supercharger_sessions`
  resolves to a relation

#### Scenario: No catalog object survives under the old table name
- **GIVEN** this change's migration has been applied
- **WHEN** the database catalog is queried for the constraints and indexes on
  `telemetry.supercharger_history`
- **THEN** the primary key, the `session_id` unique constraint, the five auto-named column
  CHECK constraints, and the two standalone indexes all carry names beginning
  `supercharger_history`
- **AND** each preserves the exact definition it had under its old name — the same checked
  expression for each CHECK, the same column set for each key, and the same column order and
  sort direction for each index
- **AND** a catalog query for any constraint or index name beginning `supercharger_sessions`
  within this module's schema returns no rows — the completeness criterion is the catalog,
  not a fixed list

#### Scenario: The renamed Go type carries an identical field set
- **GIVEN** the module's generated database models have been regenerated against this
  change's migration
- **WHEN** the generated `SuperchargerHistory` struct is compared to the pre-change
  `SuperchargerSession` struct
- **THEN** every field name, type and declaration order is identical — only the struct's own
  type identifier changed
- **AND** no other generated struct (`VehicleSnapshot`, `PollAttempt`, `PollRun`) changed at
  all, in name, field set or field order

#### Scenario: Generated documentation names only live objects
- **GIVEN** the module's generated database models have been regenerated against this
  change's migration
- **WHEN** the generated doc comments carried over from the database's table and column
  comments are read
- **THEN** none of them names a table, index, constraint or query that no longer exists under
  that name

#### Scenario: The public Supercharger port is deliberately left half-renamed
- **GIVEN** a caller of `telemetry.SuperchargerReader`
- **WHEN** this change is applied
- **THEN** the port's name, its four method names, its constructor name and every method
  signature's parameter list are unchanged
- **AND** each method's returned element type is `SuperchargerHistory` rather than
  `SuperchargerSession`
- **AND** the data returned is identical to what the same call returned before the rename
- **AND** the resulting name mismatch between the port and its element type is the specified
  end state of this change, to be resolved by a separate later change

#### Scenario: A caller outside the module is broken only by the type name, and visibly
- **GIVEN** code outside `internal/telemetry` that names the type
  `telemetry.SuperchargerSession`
- **WHEN** this change is applied
- **THEN** that code fails to compile, rather than compiling against a silently different
  type
- **AND** the failure is reported by the standard Go build and vet signals, which compile test
  files as well as production files
