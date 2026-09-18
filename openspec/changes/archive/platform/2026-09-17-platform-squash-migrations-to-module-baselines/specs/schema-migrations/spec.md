## ADDED Requirements

### Requirement: One Self-Contained Schema Baseline Per Module

Each module's migration history SHALL begin with a single baseline that fully defines
that module's own database schema. A baseline SHALL create objects only, and SHALL NOT
read, join against, or copy data from any table. Applying the baselines of every module
to an empty database SHALL produce the platform's complete schema, and the order in
which the modules are applied SHALL NOT change the result.

#### Scenario: A fresh database is built from the baselines alone

- **GIVEN** an empty database
- **WHEN** every module's migrations are applied
- **THEN** each module's schema is created in full
- **AND** the resulting schema matches the schema of the running production database,
  apart from changes made deliberately by a later migration

#### Scenario: The order of modules does not affect the outcome

- **GIVEN** an empty database
- **WHEN** the modules' baselines are applied in any order
- **THEN** the resulting schema is the same in every case
- **AND** no baseline fails because another module's schema is not present yet

### Requirement: A Migration Version Ledger Private To Each Module

Each module SHALL record which of its migration versions have been applied in a ledger
that belongs to that module alone and lives alongside that module's own schema. Two
modules SHALL be able to use the same version number, and each SHALL still apply its
own migration. A module's migration SHALL NOT be skipped because another module already
recorded that version number.

#### Scenario: Two modules use the same version number and both migrations run

- **GIVEN** two modules whose migrations carry the same version number
- **WHEN** the migrations are applied to a database
- **THEN** both migrations run
- **AND** each module's ledger records that version as applied
- **AND** neither migration is reported as successful without having run

#### Scenario: A module's ledger travels with that module's schema

- **GIVEN** a module with applied migrations
- **WHEN** only that module's schema is exported from the database
- **THEN** the export carries both the module's objects and its record of applied
  migration versions

### Requirement: A Migration Never Reads Another Module's Schema

A module's migration SHALL reference only that module's own schema. The project's
verification gate SHALL fail when a migration references a schema belonging to another
module, naming the offending file. This SHALL be enforced automatically and SHALL NOT
depend on a reviewer noticing it.

#### Scenario: A migration that reads another module's table is rejected

- **GIVEN** a migration file in one module that names another module's schema
- **WHEN** the project's verification gate runs
- **THEN** the gate fails
- **AND** the failure names the file and the schema it may not reference

#### Scenario: A migration that touches only its own schema passes

- **GIVEN** every migration file references only its own module's schema
- **WHEN** the project's verification gate runs
- **THEN** the gate passes

### Requirement: Rolling Back A Baseline Is Refused, Not Half-Done

Rolling back a baseline SHALL fail with a message stating why it cannot be reversed. The
attempt SHALL leave both the schema and the module's ledger unchanged, so that a later
forward migration still behaves correctly. A baseline SHALL NOT be reversible by
silently doing nothing, because that would record the baseline as un-applied while every
object it creates still exists, and the next forward run would then fail.

#### Scenario: A rollback attempt fails and changes nothing

- **GIVEN** a database with a module's baseline recorded as applied
- **WHEN** a rollback of that baseline is attempted
- **THEN** the attempt fails with a message naming the reason
- **AND** the module's objects still exist
- **AND** the module's ledger still records the baseline as applied

#### Scenario: A forward run after a refused rollback still succeeds

- **GIVEN** a rollback of a baseline has been attempted and refused
- **WHEN** migrations are applied forward again
- **THEN** the baseline is recognised as already applied and is not re-run
- **AND** any later migration is applied normally

### Requirement: An Existing Database Is Registered Against The Baseline, Not Rebuilt

A database that already holds a module's objects SHALL be recorded as having that
module's baseline applied, without the baseline running against it. The migration runner
SHALL perform that registration itself, before handing the directory to goose, so that no
database requires a hand-run step. Registration SHALL be idempotent, and SHALL NOT occur
on a database that does not yet hold the module's objects. When a baseline is nevertheless
applied to a database that already holds its objects, it SHALL fail and SHALL NOT alter or
delete existing data.

#### Scenario: A database already carrying the schema skips the baseline

- **GIVEN** a database holding a module's objects and its ledger recording the baseline
  as applied
- **WHEN** migrations are applied
- **THEN** the baseline does not run
- **AND** any migration after the baseline is applied

#### Scenario: An unregistered existing database is registered by the runner

- **GIVEN** a database holding a module's objects but with no ledger entry for the
  baseline
- **WHEN** migrations are applied
- **THEN** the runner records the baseline as applied without running it
- **AND** any migration after the baseline is applied
- **AND** no existing table, column, or row is altered or deleted

#### Scenario: A ledger holding only the creation marker is still registered

- **GIVEN** a database holding a module's objects, and a ledger whose only row is the
  version-0 marker written when the ledger was created
- **WHEN** migrations are applied
- **THEN** the runner records the baseline as applied
- **AND** the marker row is not duplicated

#### Scenario: A database without the module's objects is not registered

- **GIVEN** a database where the module's schema holds no tables
- **WHEN** migrations are applied
- **THEN** nothing is recorded ahead of the baseline
- **AND** the baseline runs and creates the module's objects

### Requirement: A Migration Arriving Behind The Current Version Is Rejected

A pending migration SHALL fail the run when its version is lower than the highest
version already applied for that module, and the failure SHALL name that migration
rather than applying it out of order. This protection SHALL be in force for both the
deploy's migration step and the test-database setup.

#### Scenario: A migration added behind the current version stops the run

- **GIVEN** a module whose highest applied version is above the version of a migration
  file not yet applied
- **WHEN** migrations are applied
- **THEN** the run fails
- **AND** the failure names the migration that arrived behind the current version

#### Scenario: Test setup enforces the same protection as the deploy

- **GIVEN** a migration arriving behind a module's current version
- **WHEN** a database-backed test provisions its database
- **THEN** the setup fails for the same reason and with the same protection as the
  deploy's migration step
