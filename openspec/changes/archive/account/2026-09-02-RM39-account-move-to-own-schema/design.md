## Context

`internal/account` owns three tables, all currently in the `public` schema:

- `accounts` (`20260707000001_init_account.sql`, plus `language`
  `20260813000001_accounts_add_language.sql` and `status`
  `20260830000001_accounts_vehicles_add_status.sql`): `id` (PK), `email`, `provider`,
  `provider_id`, `display_name`, `created_at`, `updated_at`, `language`, `status`, plus
  `UNIQUE (provider, provider_id)` and `CHECK (status IN ('Active','Inactive'))`.
- `tesla_tokens` (`20260707000001_init_account.sql`, collapsed to one row per account by
  `20260709000001_tesla_tokens_one_per_account.sql`): `id` (PK), `account_id` (FK →
  `accounts(id) ON DELETE CASCADE`), `tesla_email`, `access_token`, `refresh_token`,
  `access_expires_at`, `created_at`, `updated_at`, plus `UNIQUE (account_id)`
  (`tesla_tokens_account_id_key`).
- `vehicles` (`20260710000001_vehicles.sql`, plus `access_type`
  `20260720000001_vehicles_add_access_type.sql`, `exterior_color`/`car_type`
  `20260803000001_vehicles_add_config_fields.sql`, `status`
  `20260830000001_accounts_vehicles_add_status.sql`): `id` (PK), `account_id` (FK →
  `accounts(id) ON DELETE CASCADE`), `tesla_id`, `vin`, `display_name`, `created_at`,
  `updated_at`, `access_type`, `exterior_color`, `car_type`, `status`, plus
  `UNIQUE (account_id, tesla_id)`, `CHECK (access_type IS NULL OR access_type IN
  ('OWNER','DRIVER'))`, `CHECK (status IN ('Active','Inactive'))`, and index `idx_vehicles_vin`.

Roadmap `RM39-schema-per-module.md` establishes, by running the real toolchain (not by reasoning),
that:

1. sqlc **tracks** `ALTER TABLE … SET SCHEMA` across an additive second migration — the generated
   struct name changes to `<Schema><Table>` unless told otherwise.
2. sqlc **rejects a bare table name** once that table has left `public` — codegen fails at
   generate time, not run time, so a `search_path` cannot paper over it.
3. `gen.go.rename` needs the **singularized** `<schema>_<table>` key form. The plural form is
   silently ignored — no error, exit 0 — so every tier must verify `models.go` by diff, not trust
   the config.

This tier (tier 1 of 5) is the pilot: `account` has **zero cross-module entanglement** — no other
module imports `accountdb`, and none of `account`'s own migrations read another module's table —
so it establishes the additive-migration + schema-qualify + `rename:` pattern every later tier
mirrors, without any of the complications later tiers carry (D5a/D5b/D5c table renames, D6's
cross-module backfill block). None of those apply here.

Performance profile: **read-heavy** (`ai/architecture.md` §7). This migration is a one-time DDL
event, not a recurring read/write path — see the Index Plan section for why it costs nothing on
the steady-state read paths.

## Goals / Non-Goals

**Goals:**
- Move `accounts`, `tesla_tokens`, `vehicles` into a dedicated `account` Postgres schema via one
  additive goose migration, with a real reversible `-- +goose Down`.
- Preserve every row, every constraint (PK/FK/UNIQUE/CHECK), and every index unchanged.
- Keep `Account`, `TeslaToken`, `Vehicle` and every exported `Service` method's name and signature
  byte-for-byte unchanged — this is pure namespacing, invisible to every consumer.
- Make the module boundary (`ai/architecture.md` §2) checkable at the database catalog level, not
  just by Go import guards.

**Non-Goals:**
- No table, column, index, or constraint is renamed. D5a/D5b/D5c (the table-rename decisions) are
  scoped to `telemetry` and `charging` — they do not touch `account`.
- No data is deleted, backfilled, or transformed. Unlike tier 3's D8 (which deletes specific
  `vehicle_metric_watermarks` rows as a side effect of a CHECK-set collision that does not exist
  here), this tier's migration has zero data-mutation statements — every statement is `CREATE
  SCHEMA` or `ALTER TABLE … SET SCHEMA`.
- No `search_path`, role, or `GRANT` change — the roadmap's Makefile audit (see
  `RM39-schema-per-module.md` §"The Makefile needs no changes") already established that migrations
  run as the app role, so `CREATE SCHEMA account` inside a migration makes that role the schema
  owner automatically. This tier's own review (below) confirms nothing here invalidates that
  finding.
- No `db-reset`. D4/D1 deliberately avoid needing one; the roadmap's stopper gate (an owner-run
  reset) sits after tier 3, not here.
- goose itself is untouched. `public.goose_db_version` stays exactly where it is (D4).

## Decisions

### D1 — One additive migration: `CREATE SCHEMA` + `ALTER TABLE … SET SCHEMA` (restated, binding)

Exact DDL — one new goose migration file,
`internal/account/db/migrations/20260902000001_move_account_to_own_schema.sql`:

```sql
-- +goose Up
-- RM39 tier 1 (account-move-to-own-schema, MAG-31): move this module's three tables into a
-- dedicated `account` Postgres schema, additive to existing history (roadmap D1). No table,
-- column, index, or constraint is renamed or altered — this migration is pure namespacing so the
-- modular-monolith boundary (ai/architecture.md §2 "no cross-module database leaks") becomes
-- visible in the database catalog, not just enforced by Go import guards. `ALTER TABLE … SET
-- SCHEMA` is catalog-only (see design.md's index plan for the proof: no row, index, or
-- constraint is rewritten). Because migrations run as the app role (Makefile `db-setup` exports
-- PGUSER=$(APP_ROLE)), CREATE SCHEMA here makes that role the schema owner — no GRANT needed.

CREATE SCHEMA IF NOT EXISTS account;

ALTER TABLE accounts     SET SCHEMA account;
ALTER TABLE tesla_tokens SET SCHEMA account;
ALTER TABLE vehicles     SET SCHEMA account;

-- +goose Down
-- Reverse in the OPPOSITE order of Up: move every table back to public first, then drop the
-- now-empty schema. A non-empty schema cannot be dropped without CASCADE, and this ordering means
-- CASCADE is never needed — DROP SCHEMA only ever runs against an empty schema.
ALTER TABLE account.vehicles     SET SCHEMA public;
ALTER TABLE account.tesla_tokens SET SCHEMA public;
ALTER TABLE account.accounts     SET SCHEMA public;

DROP SCHEMA IF EXISTS account;
```

**Why additive, not rewriting the six existing migrations to `CREATE TABLE account.accounts (...)`
directly.** Rewriting history requires `make db-reset` on every environment that already applied
those migrations (goose tracks applied versions by checksum/number, not content — editing an
applied migration desyncs `goose_db_version` from reality) and it would silently break
`charging`'s historic backfill migration (`20260823000001_add_charge_sessions.sql`), which reads
`public.supercharger_sessions` and would need its own coordinated edit for no benefit — the
backfill's `to_regclass` guard already tolerates the *forward* additive move cleanly. Rejected:
rewriting the six pre-existing `account` migration files to create tables directly under
`account.` from the start.

**Why one migration for all three tables, not three.** All three tables move together in one
deploy; there is no independent-deployability reason to split them (goose applies one module's
migrations in file order regardless), and D7 (tier 3's statement-ordering requirement) does not
apply here — `account` has no colliding table name with another module's still-`public` table, so
statement order within this migration is not load-bearing the way it is for `charging`.

**Why `IF NOT EXISTS` / `IF EXISTS`.** `CREATE SCHEMA IF NOT EXISTS` and `DROP SCHEMA IF EXISTS`
follow this module's own precedent (`DROP TABLE IF EXISTS`, `DROP COLUMN IF EXISTS` throughout its
migration history) — idempotent DDL that tolerates being re-run against a partially-applied state
without erroring, consistent with goose's own re-run-safety expectations.

### D2 — Every table reference in `query.sql` becomes schema-qualified (restated, binding)

Not a style choice — sqlc resolves table names statically against the migration files at
**generate** time and fails codegen on a bare name once a table has left `public` (roadmap
"Findings" table, verified by the owner's own scratch repro). Every `FROM accounts`, `FROM
tesla_tokens`, `FROM vehicles`, `INSERT INTO accounts`, `INSERT INTO tesla_tokens`, `INSERT INTO
vehicles`, `UPDATE accounts`, `UPDATE tesla_tokens`, `UPDATE vehicles` in
`internal/account/db/query.sql` becomes `account.accounts` / `account.tesla_tokens` /
`account.vehicles`, **including the two `EXISTS (SELECT 1 FROM accounts a WHERE …)` subqueries**
inside `GetLatestTeslaTokenByAccount`, `GetLatestTeslaTokenByAccountForUpdate`,
`ListVehiclesByAccount`, and `ListAllVehicles` — those are still table references and sqlc
resolves them the same way. `RETURNING *` clauses need no change (they reference the target table
already named in the preceding `INSERT`/`UPDATE`).

### D3 — `gen.go.rename` keeps `Account`, `TeslaToken`, `Vehicle` unchanged (restated, binding)

Added under the account entry's existing `gen.go` block in the root `sqlc.yaml` — **not** the
top-level `overrides:` block, which sqlc ignores for this purpose:

```yaml
        rename:
          account_account:      "Account"
          account_tesla_token:  "TeslaToken"
          account_vehicle:      "Vehicle"
```

Key form is the **singularized** `<schema>_<table>` — `accounts` → `account`, `tesla_tokens` →
`tesla_token`, `vehicles` → `vehicle`, each prefixed with the schema name `account`. Without this
block, sqlc would generate `AccountAccount`, `AccountTeslaToken`, `AccountVehicle` from the
schema-qualified tables (mirroring the roadmap's own repro:
`telemetry.vehicle_snapshots` → `TelemetryVehicleSnapshot`).

**Verification is mandatory, not optional** — the roadmap's Findings table states a wrong key
produces no error and exit 0. After `make sqlc` runs, `internal/account/db/models.go` MUST be
diffed against its pre-migration version (see Test Contract below for the exact byte-for-byte
expectation) before this tier can be considered implemented.

### D4 — goose is unchanged (restated, binding)

`public.goose_db_version` is untouched by this migration — the Makefile's `migrate-up`/`down`/
`status` targets pass no `-table` flag, so goose's own bookkeeping table keeps resolving through
`search_path` to `public` regardless of where `account`'s own tables live. `make migration-guard`
remains required (its check — no two modules' migrations share a version number — is orthogonal to
table schema). No `db-reset` is needed by this tier; the roadmap's stopper gate is scheduled after
tier 3, not here.

### D-index-plan — index and constraint preservation through `ALTER TABLE … SET SCHEMA`

**Claim: `ALTER TABLE … SET SCHEMA` is a catalog-only operation. It does not rewrite the table, its
indexes, or its constraints, and it does not require an `ACCESS EXCLUSIVE` table rewrite the way
`ALTER TABLE … TYPE` would.**

How this is known, not assumed: PostgreSQL's `ALTER TABLE` documentation states that `SET SCHEMA`
"changes the schema of the table" and is grouped with the other lightweight, catalog-only forms
(`RENAME`, `OWNER TO`) rather than the data-rewriting forms (`ALTER COLUMN … TYPE`, `ADD COLUMN …
DEFAULT` pre-PG11 semantics). Every object that references the table by OID rather than by
qualified name — every index, every PK/FK/UNIQUE/CHECK constraint, every sequence backing a
`DEFAULT gen_random_uuid()` — continues to reference the same OID after the schema changes; only
the table's entry in `pg_class`/`pg_namespace` changes which schema OID it belongs to. This is the
same mechanism the roadmap's own scratch repro exercised (two migrations, second one moves the
table; the generated model updated cleanly with all prior data intact) — this design restates that
finding for `account`'s specific objects rather than re-deriving it:

| Object | Type | Preserved because |
|---|---|---|
| `accounts_pkey` | PK on `id` | catalog-only OID reference, unaffected by schema |
| `accounts_provider_provider_id_key` | UNIQUE on `(provider, provider_id)` | same |
| `accounts_status_check` (and the `CHECK` on `vehicles.status`, `vehicles.access_type`) | CHECK | catalog-only; CHECK constraints have no schema-qualified body referencing the table by name |
| `tesla_tokens_account_id_fkey` | FK → `accounts(id)` | FK constraints reference the target table's OID, not its qualified name — survives the target moving schema too |
| `tesla_tokens_account_id_key` | UNIQUE on `account_id` | catalog-only |
| `vehicles_account_id_fkey` | FK → `accounts(id)` | same as `tesla_tokens`' FK |
| `vehicles_account_id_tesla_id_key` | UNIQUE on `(account_id, tesla_id)` | catalog-only |
| `idx_vehicles_vin` | plain index on `vin` | catalog-only |
| Every row in all three tables | data | untouched — no `INSERT`/`UPDATE`/`DELETE` in this migration |

**No new index is added by this tier**, and none is needed: this migration changes zero query
predicates, zero access patterns, and zero row volume. The existing read paths
(`GetAccountByProviderID` via the `UNIQUE (provider, provider_id)` index,
`GetLatestTeslaTokenByAccount`/`…ForUpdate` via `UNIQUE (account_id)`, `ListVehiclesByAccount` via
the FK/PK-backed access pattern, `ListAllVehicles` via a full-table scan already accepted as a
nightly-cadence cost) all continue to use exactly the same physical indexes after the move — only
the fully-qualified name used to address the table in `query.sql` changes.

**Lock consideration** (write-path cost, evaluated on its own terms per the roadmap's Context
note, separately from steady-state read guidance): `ALTER TABLE … SET SCHEMA` takes an `ACCESS
EXCLUSIVE` lock on the table for the duration of the catalog update, which is a fast metadata-only
operation (no page rewrite), so the lock is held briefly. This runs once, during a deploy's
migration step, against a table sized in the tens-to-low-thousands of rows (an `accounts` /
`tesla_tokens` / `vehicles` table scoped to this project's user base) — not a steady-state read
path, so it does not need to be evaluated against the read-heavy performance profile.

**Rejected alternative:** re-creating each table under the new schema with `CREATE TABLE account.X
AS SELECT * FROM public.X` plus manually re-adding every constraint and index. This is strictly
worse — it needs an explicit index/constraint rebuild step (more ways to silently drop one), a
window where two copies of the data exist, and a follow-up `DROP TABLE public.X`. `ALTER TABLE …
SET SCHEMA` achieves the identical end state in three single-statement, constraint-preserving,
lock-brief operations.

## Test Contract (authored before implementation, per `ai/go-conventions.md` §Testing)

This tier writes and repairs tests but does not run them (`Test-Execution-Policy`). The following
are the concrete expected values any test — new or existing — must assert against, decided now, so
implementation cannot quietly redefine "correct":

1. **`internal/account/db/models.go` after `make sqlc` — byte-for-byte type names.** The generated
   `Account`, `TeslaToken`, and `Vehicle` struct names and every field name/type MUST be identical
   to the pre-migration file quoted below (only the file's internal SQL-string embeddings, if any,
   and the `//   sqlc v1.31.1` version comment may legitimately differ — no struct name, field
   name, or field type may change):
   ```go
   type Account struct {
       ID          uuid.UUID
       Email       string
       Provider    string
       ProviderID  string
       DisplayName pgtype.Text
       CreatedAt   pgtype.Timestamptz
       UpdatedAt   pgtype.Timestamptz
       Language    string
       Status      string
   }
   type TeslaToken struct {
       ID              uuid.UUID
       AccountID       uuid.UUID
       TeslaEmail      pgtype.Text
       AccessToken     string
       RefreshToken    string
       AccessExpiresAt pgtype.Timestamptz
       CreatedAt       pgtype.Timestamptz
       UpdatedAt       pgtype.Timestamptz
   }
   type Vehicle struct {
       ID            uuid.UUID
       AccountID     uuid.UUID
       TeslaID       int64
       Vin           string
       DisplayName   pgtype.Text
       CreatedAt     pgtype.Timestamptz
       UpdatedAt     pgtype.Timestamptz
       AccessType    pgtype.Text
       ExteriorColor pgtype.Text
       CarType       pgtype.Text
       Status        string
   }
   ```
   A diff producing ANY change to these three struct bodies means the `rename:` key form was wrong
   (most likely: a plural key was used) — per the roadmap, this fails silently at exit 0, so the
   diff step itself, not `sqlc generate`'s exit code, is the pass/fail signal.

2. **Catalog resolution after `make migrate-up`.** Run against the applied database:
   ```sql
   SELECT to_regclass('account.accounts'), to_regclass('account.tesla_tokens'),
          to_regclass('account.vehicles');
   ```
   Expected: all three return their table's OID (non-`NULL`). And:
   ```sql
   SELECT to_regclass('public.accounts'), to_regclass('public.tesla_tokens'),
          to_regclass('public.vehicles');
   ```
   Expected: all three return `NULL` — none of the three tables resolves under `public` any
   longer.

3. **`internal/account`'s existing integration suite — zero assertion changes.** Every existing
   test in `service_integration_test.go` (and any sibling `_test.go` file exercising the DB) MUST
   continue to pass with **the exact same expected values it asserts today** — this tier changes
   where the tables physically live, not what any port method returns. Concretely:
   `TestUpsertFromOAuth_Idempotent` still expects `Account.Status == account.StatusInactive` on
   first and second call; `TestLanguagePreference_RoundTrip` still requires its test account
   activated (`status = 'Active'`) via direct SQL before exercising `LanguageFor`/`SetLanguage`,
   and still expects the same round-tripped language values; vehicle-registry tests still expect
   `ListVehiclesByAccount`/`ListAllVehicles` to return only `status = 'Active'` rows gated by an
   `Active` owning account, per RM34 D14/D15 — all unchanged, because those predicates and their
   underlying indexes are unaffected by the schema move (see D-index-plan). If any existing
   assertion needs to change to pass, that is a signal this migration did more than namespace —
   investigate before proceeding, do not adjust the test to match.

4. **Constraint/row counts before vs. after — used as the manual verification step, not a new
   automated test.** `SELECT count(*) FROM account.accounts`, `account.tesla_tokens`,
   `account.vehicles` immediately after migration MUST equal the same three counts taken against
   `public.accounts`/`public.tesla_tokens`/`public.vehicles` immediately before it — same row
   counts, because no row is touched.

No new `_test.go` test is required by this tier specifically to prove the schema move (points 1–4
above are diff/catalog/count checks a human or the leader performs at verification time, per
`ai/go-conventions.md`'s "Authoring order" — a `DATABASE_URL`-gated integration test asserting
`to_regclass` is optional polish, not required, since `internal/testdb` already provisions from
this same migrations directory and any pre-existing test failing to compile/run against the new
schema is itself the signal).

## Makefile / tooling re-check (per `CLAUDE.md`'s "reverse direction" docs rule)

The roadmap's own audit (`RM39-schema-per-module.md` §"The Makefile needs no changes") already
found zero `schema`/`public`/`GRANT`/`search_path` occurrences in the `Makefile`, zero bare table
names, and confirmed migrations run as `$(APP_ROLE)`. This tier's own check, scoped to `account`,
confirms nothing here invalidates that finding: `MIGRATIONS_DIRS` orders `account` first already
(it is the first module `internal/account/db/migrations` created), this migration adds no new
directory, no new guard target, and no new `sqlc.yaml` structural entry (the existing account `sql:`
block is edited in place, not duplicated). `internal/testdb`'s `Provision`/`ProvisionDirs` helpers
need no change — they apply whatever is in `db/migrations/*.sql` for a module, and this tier adds
one file there.
