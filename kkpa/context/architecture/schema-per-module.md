# Schema per module — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `schema per module`, `module schema`, `per-module Postgres schema`
- **Internal name:** one PostgreSQL schema per `internal/` module that owns persistence, named
  after the module — `account.*`, `analytics.*`, `charging.*`, `telemetry.*`

## Component map

Deliberately empty. This guide was assembled from OpenSpec capability specs, which carry
behavior and rules but no file paths, so populating a file map here would mean inventing one.
The per-module files this invariant governs — each module's `db/migrations/`, `db/query.sql`,
`sqlc.yaml` entry, and `_test.go` raw SQL — are named by role in *How maintenance works* below;
ask CodeGraph for their current contents.

## How maintenance works

The modular-monolith boundary — historically enforced only by Go import guards and convention —
is made visible in the database catalog. Each module's tables move into a schema named after the
module, so a cross-module database read has to spell the other module's schema out loud in its own
`query.sql`, where it is greppable and reviewable.

Migration status, one row per module:

| Module | Schema | Tables | Status |
|---|---|---|---|
| `account` | `account` | `accounts`, `tesla_tokens`, `vehicles` | moved (RM39 tier 1) |
| `analytics` | `analytics` | `vehicle_metrics`, `vehicle_metric_watermarks`, `charge_gaps` | moved (RM39 tier 2) |
| `charging` | `charging` | `manual_charge_entries`, `supercharger_sessions` (renamed from `charge_sessions`) | moved + renamed (RM39 tier 3) |
| `telemetry` | `telemetry` | `vehicle_snapshots`, `supercharger_history` (renamed from `supercharger_sessions`), `poll_attempts`, `poll_runs` | moved + renamed (RM39 tier 4) |

RM39 is complete: every persistence-owning module has its own schema, and the roadmap is archived
at `openspec/roadmaps/archive/RM39-schema-per-module/`.

To move a module's tables, in this order:

1. **One additive goose migration** — `CREATE SCHEMA IF NOT EXISTS <module>` plus one
   `ALTER TABLE … SET SCHEMA <module>` per owned table. Never edit a historic migration; they run
   before the move and must keep resolving through `search_path` to `public`. The `-- +goose Down`
   reverses in the opposite order, then drops the schema.
2. **Schema-qualify every table reference in that module's `db/query.sql`.**
3. **Schema-qualify the raw SQL in that module's `_test.go` files.**
4. **Add `gen.go.rename` entries** to that module's `sqlc.yaml` entry, then run `make sqlc` and
   diff `models.go`.

When the same migration also **renames** a table, the statement order is mandatory, not stylistic:
`CREATE SCHEMA` → `ALTER TABLE ... SET SCHEMA` → `ALTER TABLE ... RENAME TO`. Renaming first
collides with the identically-named table another module still owns in `public`. The Down
migration reverses in the exact mirror order.

## Conventions & gotchas

- **Every query is schema-qualified — this is forced, not stylistic.** sqlc resolves table names
  statically from the migration files and fails codegen on a bare name once a table leaves
  `public`. A `search_path` on the role cannot rescue it: the failure is at *generate* time, not
  run time. _Source: spec account — Requirement: Module-Scoped Database Schema._
- **The move is namespacing only.** No stored data, constraint (primary key, foreign key, unique,
  check), index, or public-interface behavior changes. `ALTER TABLE … SET SCHEMA` is catalog-only —
  constraints and indexes reference the table by OID, so nothing is rebuilt and no row moves.
  _Source: spec account — Requirement: Module-Scoped Database Schema._
- **`gen.go.rename` keys must be SINGULARIZED, and a wrong key fails SILENTLY at exit 0.**
  `account_tesla_token` works; `account_tesla_tokens` is ignored with no error. Without the rename
  block sqlc prefixes the schema onto every generated struct (`Account` → `AccountAccount`),
  churning every call site. Never trust the config — verify by diffing `models.go`.
  _Source: spec account — Requirement: Module-Scoped Database Schema._
- **Raw SQL in `_test.go` files is the trap no automated signal catches.** Integration tests
  hand-write `DELETE FROM …`, `UPDATE … SET …` and `SELECT count(*) FROM …`. sqlc never parses
  those strings and `go vet` compiles the test while treating the SQL as opaque. On RM39 tier 1,
  `go build`, `go vet`, `gofmt` and both guards were all clean while ten integration tests failed
  on `relation "accounts" does not exist`. Only the test suite catches it.
  _Source: RM39 roadmap decision D9 (learned during tier 1)._
- **goose is untouched, and `make migration-guard` is NOT retired.** The shared
  `public.goose_db_version` stays; goose stores version numbers, not table names, so every applied
  record remains valid and no `db-reset` is needed. Version collisions across module directories
  are independent of table schemas. _Source: RM39 roadmap decision D4._
- **No module is granted access to another module's schema.** The boundary in
  `ai/architecture.md` §2 ("no cross-module database leaks") is enforced identically before and
  after the move — it is now additionally checkable at the database catalog level.
  _Source: spec account — Requirement: Module-Scoped Database Schema._
- **`vehicles` lives in the `account` schema, not a `vehicle` one.** The registry has its own
  OpenSpec capability but no module of its own — `internal/account` owns it. Schemas follow module
  ownership, not capability boundaries, so the table sits beside `accounts` and `tesla_tokens`.
  _Source: spec account-vehicle-registry — Requirement: Module-Scoped Database Schema._
- **The registry's constraints are the concrete proof that the move is catalog-only.** After the
  schema move, `UNIQUE (account_id, tesla_id)`, the `access_type` and `status` check constraints,
  the foreign key to `accounts`, and the `vin` index all continue to be enforced exactly as
  before, and every stored `access_type` / `exterior_color` / `car_type` / `status` value is
  preserved. _Source: spec account-vehicle-registry — Requirement: Module-Scoped Database Schema._
- **`vehicle_metric_watermarks.source` holds table names as DATA, and they are never
  schema-qualified.** The column stores the literal strings `'vehicle_snapshots'`,
  `'supercharger_sessions'` and `'manual_charge_entries'`, a CHECK constraint depends on them, and
  `analytics.Recalculator` keys its recompute cursor on them. Qualifying one would silently break
  the cursor. The rule: a string inside a column is data; only a table reference in a `FROM` /
  `INTO` / `UPDATE` / `JOIN` clause gets a schema.
  _Source: spec analytics — Requirement: Module-Scoped Database Schema._
- **A test that replays a historic migration out of chronological order needs `search_path` on
  its own connection, never a migration edit.** `db_watermark_migration_integration_test.go`
  drives `goose.Provider.ApplyVersion` after the schema move has already applied; the historic
  file names its table bare, correctly, so it must be given
  `?search_path=public,<module>` on that one connection. Keep `public` first, so the test still
  passes against a database that has not applied the move.
  _Source: RM39 roadmap decision D12 (found during tier 2)._
- **Hunt raw test SQL by TABLE NAME, never by an opening quote.** A quote-anchored grep misses
  backtick-delimited multi-line SQL entirely — on tier 2 it predicted 5 statements and the real
  number was 18, in a file the pattern never surfaced. Use
  `grep -rnE '(FROM|INTO|UPDATE|JOIN)[[:space:]]+(<table>|<table>)\b' --include='*_test.go' internal/<module>`.
  _Source: RM39 roadmap decision D13 (found during tier 2)._
- **Only qualify the module's OWN tables.** Analytics' tests seed other modules' tables directly
  (`vehicle_snapshots`, `supercharger_sessions`); those stay bare until their own tier moves them.
  Over-qualifying a table that has not moved breaks the test just as surely as under-qualifying
  one that has. _Source: spec analytics — Requirement: Module-Scoped Database Schema._
- **A rename's completeness criterion is the CATALOG, never a hand-written object list.** Postgres
  auto-names one CHECK constraint per inline column constraint, so those names exist ONLY in
  `pg_constraint` — no grep of the source tree finds them. Tier 3 shipped, passed a review round
  and archived with five `charge_sessions_*_check` constraints still carrying the retired name,
  because everyone verified "the four objects we listed were renamed", which was true and
  insufficient. Verify with a catalog query, and treat "no name beginning `<old_table>` remains"
  as the acceptance criterion.
  _Source: spec charging — Requirement: Supercharger Sessions Table Renamed._
- **`ALTER TABLE ... RENAME TO` renames nothing else.** Indexes, the primary key, unique
  constraints and every CHECK keep their old names silently. Left behind, they surface the retired
  name in a duplicate-key or check-violation error message against a table the rest of the system
  calls by its new name — a debugging trap, not a cosmetic one. Tier 3 renamed nine objects for
  one table.
  _Source: spec charging — Requirement: Supercharger Sessions Table Renamed._
- **A rename escapes the module sandbox; a schema move does not.** Other modules' `_test.go` files
  seed this module's table by bare name, and a module-scoped worker cannot see them. Tier 3 broke
  two `internal/analytics` tests this way. Before dispatching a rename tier, the leader must grep
  the WHOLE repo for the old table name in test SQL, not just the owning module.
  _Source: RM39 roadmap decision D17 (found during tier 3)._
- **`search_path` is UNSAFE as a test fix whenever the new name collides with another module's
  table.** D12's trick — giving one test connection `?search_path=<module>,public` — silently
  redirects a query meant for another module's identically-named table to your own, and the test
  passes while asserting nothing. During tiers 3 and 4 both `public.supercharger_sessions`
  (telemetry) and `charging.supercharger_sessions` exist. Qualify explicitly instead.
  _Source: RM39 roadmap decision D12, corrected during tier 3._
- **A generated Go type name follows the table when the rename's PURPOSE is to retire the old
  word.** The general RM39 rule freezes Go names via `gen.go.rename` to avoid churn (D3), but
  tier 3 deliberately did the opposite: `ChargeSession` → `SuperchargerSession`, with an identical
  field list, plus the two sqlc query names that embedded the old table name
  (`MirrorChargeSession` → `MirrorSuperchargerSession`, `VerifyChargeSession` →
  `VerifySuperchargerSession`). Freezing them would have half-fixed the confusion in the code that
  is read most.
  _Source: spec charging — Requirement: Supercharger Sessions Table Renamed._
- **The rename changed no public port and no behavior.** `charging.Writer`, `charging.Reader` and
  the session ports keep every exported type, method name and signature; `internal/gateway` and
  `internal/analytics` needed no change to keep working. If a rename tier forces a caller edit,
  something outside its scope moved.
  _Source: spec charging — Requirement: Supercharger Sessions Table Renamed; Requirement:
  Module-Scoped Database Schema._

## Related KB

All KB links are relative to `kkpa/context/`, never to this file — so a guide can be moved
without recounting `../..` segments.

- Architecture: `architecture/telemetry-ingest-only.md` — telemetry's own schema, its
  `supercharger_history` table, and why that is NOT charging's `supercharger_sessions`
- Architecture: `architecture/nightly-cycle.md` — the cycle that writes across these schemas
- Entities: `entities/vehicle-metrics/guide.md` — the watermark rows tier 3b rewrote
