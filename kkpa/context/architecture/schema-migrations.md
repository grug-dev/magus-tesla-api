# schema-migrations — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `schema migrations`, `goose per module`, `migration baseline`, `baseline`,
  `migration ledger`, `goose_db_version`
- **Internal name:** `schema-migrations` capability — `internal/<module>/db/migrations/` +
  `<module>.goose_db_version`, applied by `cmd/migrate` and the `Makefile` goose loops

## Component map

Not curated from the spec — a capability `spec.md` carries behavior, not file paths. Resolve
symbols through CodeGraph, or re-run
`/kkpa-context-curate from-spec … --with-filemap` if a file map is wanted here.

## How maintenance works

Each module owns its own migration directory **and** its own version ledger. The two always
travel together: the ledger lives inside the Postgres schema that module already owns.

1. **A module's history starts with one baseline** that defines its whole schema. It creates
   objects and reads nothing, so it depends on no other module and may be applied in any order
   relative to them.
2. **Every goose call names the module's ledger** (`-table <module>.goose_db_version`, or
   `goose.WithTableName`). Leave it off and the module's history lands in `public` and disagrees
   with what the other entry point recorded.
3. **The runner creates the module's schema before goose**, because goose builds its version
   table inside that schema before running any migration.
4. **The runner then registers the baseline** on a database that already holds the module's
   objects, so the baseline never runs against a populated database. It records nothing on a
   database that does not yet hold those objects.
5. **goose applies whatever is left** — for a pre-existing database, only the migrations written
   after the baseline.

Ordinary change after the baseline: add a numbered `.sql` file to the owning module's directory.
Never edit a baseline to reach dev or prod — a baseline is recorded as applied there without ever
executing, so an edit inside it reaches new databases only.

## Conventions & gotchas

Non-obvious rules that make a change correct. Cite the source for each (a CLAUDE rule, ADR, or
symbol) so it can be re-checked.

- **A module's migration may name only that module's schema.** A migration that reads another
  module's tables reintroduces the ordering dependency the per-module ledger removed.
  `make migration-boundary-guard` enforces it.
  _Source: spec schema-migrations — Requirement: A Migration Never Reads Another Module's Schema._
- **Two modules may use the same version number, and the directory order is free.** Each module
  has its own ledger, so there is no cross-module uniqueness rule. All four baselines are
  `20260917000001`.
  _Source: spec schema-migrations — Requirement: A Migration Version Ledger Private To Each Module._
- **A baseline refuses to roll back.** Reversing one would mean dropping the module's whole
  schema and its data, and an empty rollback would mark it un-applied while every object still
  exists — the next up would then fail on "relation already exists". Recreate the database
  (`make db-reset`) instead.
  _Source: spec schema-migrations — Requirement: Rolling Back A Baseline Is Refused, Not Half-Done._
- **An existing database is registered against the baseline, never rebuilt by it** — and the
  runner does that itself, so no database needs a hand-run step. Registration is idempotent, and
  a ledger holding only goose's version-0 creation marker is still registered.
  _Source: spec schema-migrations — Requirement: An Existing Database Is Registered Against The Baseline, Not Rebuilt._
- **Registration proves the schema EXISTS, not that it is CURRENT.** It fires on "this module's
  schema already holds a table". A database that stopped short of the pre-squash head gets
  registered anyway, and the next migration then fails against a schema it does not match. Check
  `count(DISTINCT version_id)` in `public.goose_db_version`, never `max(version_id)` — a stale
  database can carry the same max as a current one.
  _Source: spec schema-migrations — Requirement: An Existing Database Is Registered Against The Baseline, Not Rebuilt._
- **A migration arriving behind a module's current version fails the run.** Within one module a
  late migration is a real mistake, so goose's own check is on and nothing passes its
  allow-missing option. This holds for the deploy's migration step and for test-database setup.
  _Source: spec schema-migrations — Requirement: A Migration Arriving Behind The Current Version Is Rejected._
- **`public.goose_db_version` is history, not state.** It holds the 54 versions applied before
  the squash. Nothing reads it, and it is the rollback path for the previous image — never write
  to it.
  _Source: spec schema-migrations — Requirement: A Migration Version Ledger Private To Each Module._

## Related KB

All KB links are relative to `kkpa/context/`, never to this file — so a guide can be moved
without recounting `../..` segments.

- Architecture: `architecture/schema-per-module.md` — the schema-per-module invariant this
  capability's ledger placement follows from.
- Architecture: `architecture/deployment-stack.md` — where the migration step runs in the deploy.
