## Context

`internal/charging` owns two tables, both currently in the `public` schema:

- `charge_sessions` (`20260823000001_add_charge_sessions.sql`, plus `inferred_capacity_kwh_calc`
  from `20260829000001_add_inferred_capacity.sql`): `id` (PK), `account_id`, `vin`, `tesla_id`,
  `session_id`, `charge_start_date_time`, `charge_stop_date_time`, `site_location_name`,
  `energy_kwh`, `total_cost`, `currency`, `is_paid`, `start_battery_pct`, `end_battery_pct`,
  `battery_pct_source`, `start_battery_pct_est`, `end_battery_pct_est`, `created_at`,
  `updated_at`, `inferred_capacity_kwh_calc` (GENERATED). Constraints:
  `charge_sessions_account_session_unique UNIQUE (account_id, session_id)`,
  `charge_sessions_pct_source_required CHECK`. Index: `idx_charge_sessions_vehicle_stop
  (account_id, tesla_id, charge_stop_date_time)`.
- `manual_charge_entries` (`20260718000001_add_manual_charge_entries.sql`, plus
  `20260720000001_require_location_kind.sql`, `20260829000001_add_inferred_capacity.sql`,
  `20260829000002_add_entry_status.sql`): `id` (PK), `account_id`, `tesla_id`, `vin`,
  `charged_on`, `energy_added_kwh`, `price`, `currency`, `started_at`, `ended_at`,
  `start_battery_pct`, `end_battery_pct`, `charging_type`, `location_kind`, `location_label`,
  `notes`, `status`, `energy_source`, `odometer_km`, `inferred_capacity_kwh_calc`, `created_at`,
  `updated_at`. Indexes: `idx_manual_charge_entries_vehicle_time`,
  `idx_manual_charge_entries_account_time`.

Roadmap `RM39-schema-per-module.md` establishes, by running the real toolchain (not by
reasoning), the same four findings tiers 1–2 already used (D1–D3, D9). This tier is the first
to also carry **D5a/D5b/D5c** (table-rename decisions — the owner reopened D5 on 2026-09-02
and kept MAG-31's rename request in a sharpened form) and **D7** (statement order inside this
tier's one migration) and **D8** (the watermark vocabulary collision the rename creates).

Performance profile: **read-heavy** (`ai/architecture.md` §7). This migration is a one-time
DDL event, not a recurring read/write path.

## Goals / Non-Goals

**Goals:**
- Move `charge_sessions` and `manual_charge_entries` into a dedicated `charging` Postgres
  schema via one additive goose migration, with a real reversible `-- +goose Down`.
- In the SAME migration, rename `charge_sessions` → `supercharger_sessions` (D5b), in the D7
  statement order that avoids colliding with telemetry's still-`public`
  `supercharger_sessions`.
- Rename the two catalog objects the roadmap names explicitly:
  `idx_charge_sessions_vehicle_stop` → `idx_supercharger_sessions_vehicle_stop`, and the
  `charge_sessions_pct_source_required` CHECK constraint →
  `supercharger_sessions_pct_source_required`.
- Preserve every row, the remaining constraints (PK, `UNIQUE (account_id, session_id)`), and
  the renamed index's coverage unchanged.
- Keep `ManualChargeEntry` — and every exported `Writer`/`Reader`/`SessionWriter`/
  `SessionReader`/`SuperchargerSessionAnalyticsReader`/`SessionVerifier` method's name and
  signature — byte-for-byte unchanged.
- Let the Go db model `ChargeSession` become `SuperchargerSession` (D5c), and the two sqlc
  query names that embed the old table name (`MirrorChargeSession`, `VerifyChargeSession`)
  become `MirrorSuperchargerSession`/`VerifySuperchargerSession`.
- State, precisely, where the D8 watermark-vocabulary cleanup belongs and why this change's
  own sandbox cannot complete it (see "D8 — Boundary" below).

**Non-Goals:**
- No column is added, dropped, or retyped on either table.
- `20260823000001_add_charge_sessions.sql` (the historic migration with the
  `public.supercharger_sessions` backfill read) is **not edited** — see "Do NOT touch" below.
  Its cross-module read is D6's problem, owned by a separate ticket, not this tier.
- Nothing beyond the **four** catalog objects listed in "Rename scope" is renamed. Roadmap
  **D16** (owner-confirmed 2026-09-02) expanded this change from two objects to four: the
  index, the CHECK, the primary key and the unique constraint all follow the table's new
  name. This design originally proposed the narrower two-object scope; the owner rejected it.
- No `db-reset`. D4/D1 deliberately avoid needing one; the roadmap's stopper gate (an
  owner-run reset) sits immediately AFTER this tier.
- goose itself is untouched. `public.goose_db_version` stays exactly where it is (D4).
- The `vehicle_metric_watermarks` CHECK rewrite + DELETE (roadmap D8) is **out of this
  change entirely**. Roadmap **D15** (owner-confirmed 2026-09-02) moved it into its own
  analytics-owned tier, `RM39-analytics-fix-watermark-vocabulary` (roadmap tier 3b), which
  `depends_on` this one. It is described below only as downstream context — see
  "D8 — Boundary."

## Decisions

### D1/D7 — One additive migration: `CREATE SCHEMA` + `SET SCHEMA` + `RENAME`, in the mandatory order

Exact DDL — one new goose migration file,
`internal/charging/db/migrations/20260902000003_move_charging_to_own_schema.sql` (next free
chronological timestamp — see "Migration filename" below):

```sql
-- +goose Up
-- RM39 tier 3 (charging-move-to-own-schema, MAG-31): move this module's two tables into a
-- dedicated `charging` Postgres schema (roadmap D1), AND rename charge_sessions to
-- supercharger_sessions (roadmap D5b) in the SAME migration.
--
-- STATEMENT ORDER IS MANDATORY (roadmap D7). Tier 3 runs BEFORE tier 4
-- (RM39-telemetry-move-to-own-schema, blocked on a separate boundary ticket, D6), so
-- internal/telemetry still owns `public.supercharger_sessions` at this point. Renaming
-- charge_sessions to the bare name `supercharger_sessions` while both tables sit in
-- `public` would collide with telemetry's table. Moving this table into the `charging`
-- schema FIRST, then renaming it there, means the two same-named tables coexist under
-- different schema qualifiers (`charging.supercharger_sessions` vs
-- `public.supercharger_sessions`) until tier 4 moves telemetry's copy too. This also frees
-- D5b from D6's block — this rename does not wait for the boundary ticket.
--
-- `ALTER TABLE … SET SCHEMA` and `ALTER TABLE … RENAME TO` / `ALTER INDEX … RENAME TO` /
-- `ALTER TABLE … RENAME CONSTRAINT` are all catalog-only operations (see this file's Index
-- Plan section for the proof: no row, index page, or constraint definition is rewritten).
-- Because migrations run as the app role (Makefile db-setup exports PGUSER=$(APP_ROLE)),
-- CREATE SCHEMA here makes that role the schema owner — no GRANT needed.
CREATE SCHEMA IF NOT EXISTS charging;

ALTER TABLE charge_sessions       SET SCHEMA charging;
ALTER TABLE manual_charge_entries SET SCHEMA charging;

ALTER TABLE charging.charge_sessions RENAME TO supercharger_sessions;

-- Rename ALL FOUR catalog objects that still carry the old table name (roadmap D16 —
-- see design.md "Rename scope"). Postgres does NOT auto-rename the index, the CHECK, the
-- implicit primary key, or the unique constraint when the table is renamed.
ALTER INDEX charging.idx_charge_sessions_vehicle_stop
    RENAME TO idx_supercharger_sessions_vehicle_stop;

ALTER TABLE charging.supercharger_sessions
    RENAME CONSTRAINT charge_sessions_pct_source_required
    TO supercharger_sessions_pct_source_required;

ALTER TABLE charging.supercharger_sessions
    RENAME CONSTRAINT charge_sessions_pkey
    TO supercharger_sessions_pkey;

ALTER TABLE charging.supercharger_sessions
    RENAME CONSTRAINT charge_sessions_account_session_unique
    TO supercharger_sessions_account_session_unique;

-- +goose Down
-- Reverse in the EXACT opposite order of Up (D7's ordering logic in reverse): undo the
-- constraint/index renames, undo the table rename, THEN SET SCHEMA public for both tables,
-- THEN drop the now-empty schema. A non-empty schema cannot be dropped without CASCADE, and
-- this ordering means CASCADE is never needed.
ALTER TABLE charging.supercharger_sessions
    RENAME CONSTRAINT supercharger_sessions_account_session_unique
    TO charge_sessions_account_session_unique;

ALTER TABLE charging.supercharger_sessions
    RENAME CONSTRAINT supercharger_sessions_pkey
    TO charge_sessions_pkey;

ALTER TABLE charging.supercharger_sessions
    RENAME CONSTRAINT supercharger_sessions_pct_source_required
    TO charge_sessions_pct_source_required;

ALTER INDEX charging.idx_supercharger_sessions_vehicle_stop
    RENAME TO idx_charge_sessions_vehicle_stop;

ALTER TABLE charging.supercharger_sessions RENAME TO charge_sessions;

ALTER TABLE charging.manual_charge_entries SET SCHEMA public;
ALTER TABLE charging.charge_sessions       SET SCHEMA public;

DROP SCHEMA IF EXISTS charging;
```

**Why additive, not rewriting the five existing migrations.** Same reasoning as tiers 1–2:
rewriting history desyncs `goose_db_version` from reality on every environment that already
applied those migrations. Rejected: rewriting `20260823000001` (and the other four) to create
tables directly under `charging.` and already named `supercharger_sessions` — this would also
require reworking the backfill's `to_regclass('public.supercharger_sessions')` guard mid-file,
which D6 explicitly defers to a separate ticket.

**Why one migration for both tables, and both the schema move and the rename.** `charge_sessions`
and `manual_charge_entries` move together in one deploy; splitting the schema move from the
rename into two migrations would create an intermediate state
(`charging.charge_sessions`, not yet renamed) that no environment needs to pass through and
that would need its own D7-equivalent reasoning for no benefit.

**Why `IF NOT EXISTS` / `IF EXISTS`.** Follows tiers 1–2's own precedent — idempotent DDL that
tolerates being re-run against a partially-applied state without erroring.

### D2 — Every query is schema-qualified (restated, binding)

Not a style choice — sqlc resolves table names statically against the migration files at
**generate** time and fails codegen on a bare name once a table has left `public`. Measured
directly on `internal/charging/db/query.sql`: **13 table references** across 10 `-- name:`
blocks —

- `manual_charge_entries`: 7 (`CreateEntry` INSERT, `UpdateEntry` UPDATE, `DeleteEntry`
  DELETE, `ListEntriesByVehicle`/`ListEntriesByAccount`/`ListEntriesByVehicleBetween`/
  `ListEntriesByVehicleUpdatedSince` SELECT).
- `charge_sessions` (becomes `supercharger_sessions`): 6 (`MirrorChargeSession` INSERT,
  `ListSessionsByVehicleBetween` SELECT, `LockSessionForVerification` SELECT,
  `VerifyChargeSession` UPDATE, `ListSessionsByVehicleUpdatedSince` SELECT,
  `ListSessionsByVehicle` SELECT).

Every `manual_charge_entries` reference becomes `charging.manual_charge_entries`. Every
`charge_sessions` reference becomes `charging.supercharger_sessions` — both the schema
qualification AND the new table name, since D5b renames the physical table this same
migration.

### D3/D5c — `gen.go.rename`: preserve one type, let the other follow its table

Added under the charging entry's existing `gen.go` block in the root `sqlc.yaml` — **not**
the top-level `overrides:` block, which sqlc ignores for this purpose:

```yaml
        rename:
          charging_manual_charge_entry:  "ManualChargeEntry"
          charging_supercharger_session: "SuperchargerSession"
```

Key form is the singularized `<schema>_<table>` — `manual_charge_entries` →
`manual_charge_entry`, `supercharger_sessions` (the table's name AFTER this migration's
rename) → `supercharger_session`, each prefixed with the schema name `charging`.

**`charging_manual_charge_entry` preserves `ManualChargeEntry` — restating D3.** This is pure
namespacing for that table; without the entry, sqlc would generate
`ChargingManualChargeEntry`.

**`charging_supercharger_session` is deliberately a NEW mapping, not a preservation of
`ChargeSession` — this is D5c, the exception to D3.** D3 freezes names because the schema move
alone is pure namespacing, where a rename would be churn for nothing. D5a/D5b are the reverse
case: their entire purpose is to retire stale vocabulary (`charge_sessions` over-claiming what
is really a Supercharger-only mirror), so leaving the Go type on the old name would half-fix
the confusion in exactly the code — `session_reader.go`, `session_writer.go`,
`session_verifier.go`, `service.go` — that gets read most.

**Verification is mandatory, not optional** — a wrong key produces no error and exit 0
(the roadmap's own headline finding). After `make sqlc`, `internal/charging/db/models.go`
must be diffed: expect `ManualChargeEntry`'s struct body byte-identical, and `ChargeSession`
replaced by `SuperchargerSession` with an identical field list (see Test Contract below for
the exact expected shape).

### D5c-query — the two query-name renames are a SEPARATE mechanism from `gen.go.rename`

`gen.go.rename` only remaps **table-derived** struct names (the ones sqlc infers from a
`schema_table` catalog name). `MirrorChargeSession` and `VerifyChargeSession` are
**query-derived** names — they come from the `-- name: <Name> :exec/:one/:many` comment in
`query.sql`, a completely different sqlc mechanism with no `rename:` equivalent. Renaming them
means editing the `-- name:` line itself:

```sql
-- name: MirrorSuperchargerSession :exec
...
-- name: VerifySuperchargerSession :one
...
```

This changes the generated Go function name (`Queries.MirrorChargeSession` →
`Queries.MirrorSuperchargerSession`) AND the generated params struct
(`MirrorChargeSessionParams` → `MirrorSuperchargerSessionParams`,
`VerifyChargeSessionParams` → `VerifySuperchargerSessionParams`) automatically — sqlc derives
both from the query name. Call sites in `session_writer.go` (`qtx.MirrorChargeSession(...)`)
and `session_verifier.go` (`q.VerifyChargeSession(...)`) update to match; this is a compile
error if missed, not a silent drift, unlike the `gen.go.rename` key-typo failure mode.

`LockSessionForVerification` has no "ChargeSession" in its name and is unaffected by this
decision — only its `FROM charge_sessions` line needs D2's schema+rename qualification.

### Rename scope (D16) — all FOUR catalog objects that carry the old table name

The roadmap's tier-3 row originally named only two objects: the index
(`idx_charge_sessions_vehicle_stop`) and the CHECK constraint
(`charge_sessions_pct_source_required`). This design first proposed that narrower scope and
flagged the gap for the owner. **The owner rejected it (roadmap D16, 2026-09-02).** Scope is
now all nine — the four D16 named, plus five found later in the catalog (see below):

| Object | Kind | New name |
|---|---|---|
| `idx_charge_sessions_vehicle_stop` | index | `idx_supercharger_sessions_vehicle_stop` |
| `charge_sessions_pct_source_required` | CHECK (named) | `supercharger_sessions_pct_source_required` |
| `charge_sessions_pkey` | primary key | `supercharger_sessions_pkey` |
| `charge_sessions_account_session_unique` | UNIQUE `(account_id, session_id)` | `supercharger_sessions_account_session_unique` |
| `charge_sessions_battery_pct_source_check` | CHECK (auto-named) | `supercharger_sessions_battery_pct_source_check` |
| `charge_sessions_start_battery_pct_check` | CHECK (auto-named) | `supercharger_sessions_start_battery_pct_check` |
| `charge_sessions_end_battery_pct_check` | CHECK (auto-named) | `supercharger_sessions_end_battery_pct_check` |
| `charge_sessions_start_battery_pct_est_check` | CHECK (auto-named) | `supercharger_sessions_start_battery_pct_est_check` |
| `charge_sessions_end_battery_pct_est_check` | CHECK (auto-named) | `supercharger_sessions_end_battery_pct_est_check` |

**How the last five were found, and why D16's list missed them.** D16 was written from the
constraints this module *names explicitly* in `20260823000001`. Postgres also auto-names one
CHECK per inline column constraint as `<table>_<column>_check` — five of them here
(`battery_pct_source`, `start/end_battery_pct`, and their `_est` siblings). No grep finds
these: the names exist only in the catalog, never in the repo. They surfaced only when
`tasks.md`'s T5.4 catalog-verification query was actually run against a migrated database,
after the change had already passed review. Nothing in Go, SQL or docs references them, so
the rename is catalog-only and consumer-free — but D16's rationale applies verbatim: a CHECK
violation on a battery percentage would otherwise print `charge_sessions_end_battery_pct_check`
against a table the whole system calls `supercharger_sessions`.

**Why the owner expanded it.** Postgres auto-renames none of these when a table is renamed.
Leaving the last two behind means a duplicate-key violation prints
`charge_sessions_account_session_unique` — or `charge_sessions_pkey` — in the error text,
against a table the rest of the system now calls `supercharger_sessions`. That is precisely
the stale-vocabulary confusion D5b exists to remove, preserved in the one place a developer
reads under pressure: an error message.

**Cost.** Seven extra `ALTER TABLE … RENAME CONSTRAINT` statements in Up and seven in Down —
the same catalog-only mechanism already used for the named CHECK. No table rewrite, no lock
beyond the `ACCESS EXCLUSIVE` the migration already takes, no data touched.

**Still explicit, but the list is DERIVED from the catalog — not from reading the DDL.** The
migration names each object rather than looping over `pg_constraint` for `charge_sessions%`,
because a literal statement fails loudly if its object is absent while a pattern loop silently
renames whatever it happens to match.

An earlier version of this paragraph justified the literal list with "a literal list fails
loudly if an object is missing." **That claim was wrong, and this change is the counterexample.**
A literal list fails loudly only about objects it *names*; it is silent about objects it never
knew existed. The first four-object list was built by reading `20260823000001`'s `CREATE TABLE`
for explicitly-named constraints, so it never saw the five CHECKs Postgres auto-names
`<table>_<column>_check` — names that exist only in the catalog and appear nowhere in this
repo. That list passed review and archived before a catalog query found the gap.

The correction is not "stop using a list", it is **where the list comes from**: enumerate
`pg_constraint` and `pg_indexes` on a migrated database, then write those names into the
migration explicitly. Keep the loud-failure property; drop the assumption that the DDL text is
a complete inventory of the catalog.

### D8 — Boundary: the watermark CHECK rewrite + DELETE is analytics-owned work, not this change's

> **RESOLVED — roadmap D15 (owner-confirmed 2026-09-02).** This section's recommendation was
> accepted. The watermark CHECK rewrite + DELETE and the `sourceChargeSessions` update are
> **no longer part of this change in any form**. They are their own roadmap tier 3b,
> `RM39-analytics-fix-watermark-vocabulary`, owned by `internal/analytics` and depending on
> this tier. Everything below is retained as the analysis that produced that split, and as the
> hand-off context tier 3b starts from. **No task in this change implements any of it**, and
> no worker on this change may write to `internal/analytics/` or to
> `vehicle_metric_watermarks`.

**The collision, restated precisely.** Before this tier, `analytics.vehicle_metric_watermarks`'s
CHECK constraint (rewritten once already by `20260828000001`) allows
`('vehicle_snapshots', 'charge_sessions', 'manual_charge_entries')`, and
`internal/analytics/recalculate.go` line 44 defines `sourceChargeSessions = "charge_sessions"`
— the label naming the table this tier is about to rename. After this tier's migration
applies, the table analytics' Supercharger watermark cursor should be naming is
`supercharger_sessions` — the SAME literal string the vocabulary held BEFORE
`20260828000001` (back when it named telemetry's table). Two different tables, same string,
different eras — and nothing in the database or the Go type system catches the mismatch,
because `source` is a free-standing `TEXT` label, not a foreign key.

**Precedent, copied not invented (roadmap D8's own instruction).**
`20260828000001_migrate_vehicle_metric_watermarks_source.sql` performed the mirror-image
operation once already: `DROP CONSTRAINT` → `DELETE FROM … WHERE source = <retiring value>`
→ `ADD CONSTRAINT` with the new vocabulary, then a Down that reverses the same three steps
with the values swapped. The migration this tier needs is that exact shape, with the roles
reversed (the value being retired is `'charge_sessions'`, the value being (re)admitted is
`'supercharger_sessions'`):

```sql
-- +goose Up
ALTER TABLE analytics.vehicle_metric_watermarks
    DROP CONSTRAINT IF EXISTS vehicle_metric_watermarks_source_check;

DELETE FROM analytics.vehicle_metric_watermarks
WHERE source = 'charge_sessions';

ALTER TABLE analytics.vehicle_metric_watermarks
    ADD CONSTRAINT vehicle_metric_watermarks_source_check
    CHECK (source IN ('vehicle_snapshots', 'supercharger_sessions', 'manual_charge_entries'));

-- +goose Down
ALTER TABLE analytics.vehicle_metric_watermarks
    DROP CONSTRAINT IF EXISTS vehicle_metric_watermarks_source_check;

DELETE FROM analytics.vehicle_metric_watermarks
WHERE source = 'supercharger_sessions';

ALTER TABLE analytics.vehicle_metric_watermarks
    ADD CONSTRAINT vehicle_metric_watermarks_source_check
    CHECK (source IN ('vehicle_snapshots', 'charge_sessions', 'manual_charge_entries'));
```

DELETE, not UPDATE, for the identical reason `20260828000001` chose DELETE: an absent
`(account_id, tesla_id, source)` watermark row is DEFINED as epoch by
`Recalculator.watermark()`'s own contract, so the next nightly `Reconcile` backfills that
vehicle's Supercharger history from `charging.supercharger_sessions` in one pass —
self-healing, and data loss here is an accepted, named trade-off (MAG-31, restated by roadmap
D8).

This companion migration must also be paired with a one-line Go change:
`internal/analytics/recalculate.go:44`, `sourceChargeSessions = "charge_sessions"` →
`sourceChargeSessions = "supercharger_sessions"` (the constant name itself is not required to
change, only its value — though renaming the constant too, e.g. to
`sourceSuperchargerSessions`, would match the vocabulary and avoid a stale identifier; that is
an analytics-side style call, not specified further here).

**The boundary question, answered directly.**

1. **Which module's migration directory should this live in?** `internal/analytics/db/migrations/`
   — NOT `internal/charging/db/migrations/`. `vehicle_metric_watermarks` is
   `internal/analytics`'s table (moved into the `analytics` schema by tier 2); a migration
   script that runs `ALTER TABLE`/`DELETE FROM` against another module's schema is a
   cross-module database write regardless of whether it executes at deploy-time DDL or at
   runtime — `ai/architecture.md` §2's "no cross-module database leaks" does not carve out an
   exception for migration files, and this project has a direct, load-bearing precedent for
   the alternative: `20260828000001` itself — triggered by a `charging`-side change
   (`RM31-analytics-read-sessions-from-charging`, moving the Supercharger READ from telemetry
   to charging) — already lives in `internal/analytics/db/migrations/`, not in
   `internal/telemetry`'s or `internal/charging`'s directory, precisely because the table it
   touches is analytics'.

2. **Would putting it in charging's directory be a boundary violation?** Yes. It would mean
   this change's own migration file — nominally "charging's" — contains DDL/DML against a
   table this module (i) never queries through any of its own ports, (ii) does not own per
   `internal/charging/AGENTS.md`'s Data Ownership section, and (iii) whose Go-side vocabulary
   constant lives in a file (`internal/analytics/recalculate.go`) this change's sandbox does
   not include. That is exactly the "module may not read or write another module's tables
   directly" rule the architecture doc states, applied to a migration script instead of a Go
   query — the fact that goose migrations aren't compiled Go doesn't exempt them; they are
   still executable DDL/DML this repository ships, and sqlc's own §D2 finding ("every query is
   schema-qualified, forced not chosen") already establishes that this project treats a bare
   or foreign-schema table reference in generated SQL as a boundary signal worth enforcing
   mechanically, not just as a style nicety.

3. **How does `MIGRATIONS_DIRS` ordering resolve the sequencing?** `MIGRATIONS_DIRS` is
   `account → telemetry → charging → analytics` (confirmed by the roadmap's own audit and by
   this tier's own reading of the Makefile — unchanged by this tier). `db-reset`/`db-setup`
   apply each directory **to completion** before starting the next — NOT merged by timestamp
   across directories (the roadmap's own "Replay is coherent" section states this explicitly
   for the analogous telemetry→charging backfill case). Because `charging` is applied
   strictly before `analytics` in that ordering, an analytics-owned migration with ANY valid,
   globally-unique timestamp will still run after this tier's `charge_sessions` →
   `supercharger_sessions` rename has fully applied — the dependency is satisfied by
   directory order, not by the companion file's timestamp being numerically close to this
   tier's. This is the same reasoning the roadmap's own D-watermark-replay note (tier 2) and
   this module's `db_backfill_integration_test.go` already rely on for the telemetry→charging
   direction.

**Consequence for this change.** This worker's sandbox is `internal/charging/` plus this
artifacts folder — it explicitly excludes `internal/analytics/`. The D8 cleanup is therefore
**out of scope for this change to implement**, by design, not by oversight. `tasks.md` records
it as a task this change specifies in full (the SQL above, ready to hand to an analytics
worker) but cannot check off itself, and recommends the leader either (a) open a small
analytics-owned companion change/tier sequenced immediately after this one, or (b) grant an
explicit, narrow sandbox exception for this one migration file plus the one-line
`recalculate.go` edit if the leader judges a whole extra tier is disproportionate for a
2-file, ~15-line change. Either way, **not landing this cleanup in the same wave as this
tier is safe, not merely tolerable**: until it lands, the CHECK constraint still accepts the
now-stale `'charge_sessions'` label, and `recalculate.go` still writes it — nothing breaks,
the vocabulary is just temporarily out of step with the renamed table, exactly the kind of
"133 doc files still say `supercharger_sessions` meaning telemetry's table" era-ambiguity the
roadmap already accepts for D5b's table rename itself.

### D9 — Raw SQL in `_test.go` files is schema- and name-qualified (re-measured, confirmed)

The roadmap's dispatch instructions state a corrected count of 32 statements across 10 files
for this module, using a quote-agnostic grep (catching backtick multi-line SQL the original
quote-anchored grep missed). This worker re-ran the exact command independently:

```
grep -rnE '(FROM|INTO|UPDATE|JOIN)[[:space:]]+(charge_sessions|manual_charge_entries)\b' \
  --include='*_test.go' internal/charging
```

**Result: 32 statements, confirmed exactly — not a correction, the roadmap's number was
already right for this module.** Across these 10 files:

- `db_entry_status_integration_test.go`
- `db_session_integration_test.go`
- `db_integration_test.go`
- `db_inferred_capacity_sessions_integration_test.go`
- `db_session_reader_updated_since_integration_test.go`
- `db_session_reader_integration_test.go`
- `db_session_verifier_integration_test.go`
- `db_session_reader_by_vehicle_integration_test.go`
- `db_inferred_capacity_entries_integration_test.go`
- `db_backfill_integration_test.go`

Every `manual_charge_entries` reference becomes `charging.manual_charge_entries`; every
`charge_sessions` reference becomes `charging.supercharger_sessions` (schema AND name).
**`db_backfill_integration_test.go`'s references to `supercharger_sessions` (bare, no
`charging.` prefix) are telemetry's OWN table and stay exactly as they are** — this is the
one file in the list that also touches a second table by design (it seeds telemetry's
pre-migration source data to prove the backfill), and only its `charge_sessions`-referencing
lines (assertions against the mirrored rows, not the seeded source rows) qualify.

One additional consequence beyond a bare-name fix:
`db_session_reader_by_vehicle_integration_test.go`'s T-Order2 case asserts literal `EXPLAIN`
plan text: `strings.Contains(planText, "Index Scan Backward using
idx_charge_sessions_vehicle_stop")`. This must become
`"Index Scan Backward using idx_supercharger_sessions_vehicle_stop"` — a **value** change
inside the test's expected string, not merely a table-name qualification, because this
tier renames the index itself.

**Migration files stay bare** — `20260823000001_add_charge_sessions.sql` (and every other
historic migration in this module) ran before the schema move and must keep resolving
through `search_path` to `public`, per D9's own rule.

### D12 — Checked: the out-of-order migration-replay pattern is NOT present in this module

Tier 2 found `internal/analytics/db_watermark_migration_integration_test.go` driving
`goose.Provider.ApplyVersion` to replay a historic migration after the schema move had
already applied, requiring a test-connection-scoped `search_path` fix. This tier checked
`internal/charging` for the same pattern before assuming it does not apply:

```
grep -rn "ApplyVersion\|goose\.Provider\|goose\.NewProvider\|NewProvider" internal/charging/
```

**Result: not present.** The only hit repo-wide inside this module is a prose mention in
`internal/charging/AGENTS.md` ("`goose.NewProvider` records applied versions in
`goose_db_version`...") — documentation, not a test driving a provider directly.
`internal/charging/testdb_test.go` provisions via `testdb.ProvisionDirs(ctx,
"../telemetry/db/migrations", "db/migrations")` — the standard forward, in-order,
whole-directory provisioning every other module uses, with no `ApplyVersion` call anywhere in
the package. No `search_path` fix is needed here.

### Do NOT touch — `20260823000001_add_charge_sessions.sql`

Per roadmap D6, this file's one-time backfill reads `public.supercharger_sessions` directly
(a genuine cross-module boundary violation, owned by a separate ticket). It still works at
this tier because `internal/telemetry` has not moved schema yet — the
`to_regclass('public.supercharger_sessions')` guard still resolves. This change makes zero
edits to that file. Confirmed by inspection: this design's migration is purely additive
(a new file), and no statement in it references `20260823000001`'s content.

### Index Plan — preservation through `SET SCHEMA` + `RENAME`, proof for the one new consideration

**Claim: `ALTER TABLE … SET SCHEMA`, `ALTER TABLE … RENAME TO`, `ALTER INDEX … RENAME TO`, and
`ALTER TABLE … RENAME CONSTRAINT` are all catalog-only operations** — same PostgreSQL
documentation basis as tiers 1–2 for `SET SCHEMA`; the three `RENAME` forms are equally
catalog-only by Postgres documentation (they update `pg_class.relname` /
`pg_constraint.conname`, never touch heap or index pages). Every object referencing the table
by OID — every remaining constraint, the renamed index, the sequence backing
`DEFAULT gen_random_uuid()` — continues to reference the same OID; only names and the
`pg_namespace` entry change.

| Object | Type | Preserved because |
|---|---|---|
| `charge_sessions_pkey` → `supercharger_sessions_pkey` | PK on `id` | catalog-only rename (D16); same OID, same backing index, same uniqueness enforcement |
| `charge_sessions_account_session_unique` → `supercharger_sessions_account_session_unique` | UNIQUE `(account_id, session_id)` | catalog-only rename (D16); same OID, same backing index, same uniqueness enforcement |
| `idx_charge_sessions_vehicle_stop` → `idx_supercharger_sessions_vehicle_stop` | index on `(account_id, tesla_id, charge_stop_date_time)` | catalog-only rename; same physical index, same column order, same scan behavior |
| `charge_sessions_pct_source_required` → `supercharger_sessions_pct_source_required` | CHECK | catalog-only rename; identical CHECK expression |
| `inferred_capacity_kwh_calc`'s `GENERATED ALWAYS AS (...) STORED` definition | generated column | catalog-only; the generation expression references columns by attnum, not by table/schema name |
| `manual_charge_entries`'s PK, both indexes | PK, 2 indexes | catalog-only, no rename |
| Every row in both tables | data | untouched — no `INSERT`/`UPDATE`/`DELETE` in this migration |

**No cross-table FK exists** on either table (no cross-module FK precedent,
`ai/architecture.md` §2), so there is no FK-to-a-renamed-table case to reason about.

**The renamed index serves the identical query plans.** `idx_supercharger_sessions_vehicle_stop`
is the same physical B-tree that `idx_charge_sessions_vehicle_stop` was — only its
`pg_class.relname` changed. Every read this table is shaped for
(`ListSessionsByVehicleBetween`'s ascending range scan, `ListSessionsByVehicle`'s backward
scan for DESC + LIMIT, `ListSessionsByVehicleUpdatedSince`'s residual-filter scan) continues
to use the same index by OID; only the name a human or an `EXPLAIN` output sees changes —
which is exactly what `db_session_reader_by_vehicle_integration_test.go`'s T-Order2 case
must now assert against the new name (see D9).

**No new index is added by this tier**, and none is needed: this migration changes zero
query predicates, zero access patterns, and zero row volume.

**Lock consideration:** each `ALTER TABLE`/`ALTER INDEX` statement takes a brief `ACCESS
EXCLUSIVE` lock during its catalog update (metadata-only, no page rewrite). Five short
statements run once, during a deploy's migration step, against tables sized for this
project's user base — not a steady-state read path.

**Rejected alternative:** re-creating `charge_sessions` under the new schema and name with
`CREATE TABLE charging.supercharger_sessions AS SELECT * FROM public.charge_sessions` plus
manually re-adding every constraint and index. Strictly worse for the same reasons as tiers
1–2: an explicit index/constraint rebuild step, a window with two copies of the data, and a
follow-up `DROP TABLE`.

## Migration filename

Latest migration timestamp across ALL module migration directories at the time of this
change (checked directly, not assumed): `20260902000002`
(`internal/analytics/db/migrations/20260902000002_move_analytics_to_own_schema.sql`, tier
2's own migration). `internal/charging`'s own latest is `20260829000002_add_entry_status.sql`.
`20260902000003` collides with neither, confirmed by listing every module's `db/migrations/`
directory immediately before creating this design.

The D8 companion migration this design recommends for `internal/analytics` (not created by
this change) should use the next free timestamp after this one is claimed —
`20260902000004` at minimum, re-verified at the time it is actually created since this
change's own migration will by then be the new highest.

## Test Contract (authored before implementation, per `ai/go-conventions.md` §Testing)

1. **`internal/charging/db/models.go` after `make sqlc` — exact expected diff, not a "no
   diff" claim (unlike tiers 1–2).** `ManualChargeEntry`'s struct body: byte-identical to
   pre-migration (name, every field name, every field type unchanged). `ChargeSession`'s
   struct MUST be entirely replaced by a struct named `SuperchargerSession` with the IDENTICAL
   field list, field types, and field order as `ChargeSession` had (only the type name
   changes — `TeslaID pgtype.Int8`, `SessionID int64`, ... `InferredCapacityKwhCalc
   pgtype.Numeric` unchanged). `git diff internal/charging/db/models.go` MUST show a rename
   of the type identifier only, with zero field-level changes.

2. **Catalog resolution after `make migrate-up`.** Run against the applied database:
   ```sql
   SELECT to_regclass('charging.supercharger_sessions'), to_regclass('charging.manual_charge_entries');
   ```
   Expected: both return their table's OID (non-`NULL`). And:
   ```sql
   SELECT to_regclass('public.charge_sessions'), to_regclass('public.manual_charge_entries'),
          to_regclass('charging.charge_sessions');
   ```
   Expected: all three return `NULL` — neither the old public location nor the old name
   under the new schema resolves.

3. **Renamed catalog objects resolve under their new names.**
   ```sql
   SELECT indexname FROM pg_indexes WHERE schemaname = 'charging' AND tablename = 'supercharger_sessions';
   ```
   Expected: includes `idx_supercharger_sessions_vehicle_stop`, does NOT include
   `idx_charge_sessions_vehicle_stop`.
   ```sql
   SELECT conname FROM pg_constraint WHERE conrelid = 'charging.supercharger_sessions'::regclass;
   ```
   Expected (D16 — all nine renamed): the set is exactly
   `supercharger_sessions_pct_source_required`, `supercharger_sessions_pkey`,
   `supercharger_sessions_account_session_unique`,
   `supercharger_sessions_battery_pct_source_check`,
   `supercharger_sessions_start_battery_pct_check`,
   `supercharger_sessions_end_battery_pct_check`,
   `supercharger_sessions_start_battery_pct_est_check` and
   `supercharger_sessions_end_battery_pct_est_check`.

   **The binding assertion is the negative one**, because it cannot go stale as the table
   gains constraints: a single `conname LIKE 'charge\\_sessions%'` match is a failure. The
   positive list above is a reading aid; the catalog is the criterion.

4. **`internal/charging`'s existing test suite — zero assertion changes EXCEPT the one
   documented EXPLAIN-text case.** Every existing test in `db_entry_status_integration_test.go`,
   `db_session_integration_test.go`, `db_integration_test.go`,
   `db_inferred_capacity_sessions_integration_test.go`,
   `db_session_reader_updated_since_integration_test.go`,
   `db_session_reader_integration_test.go`, `db_session_verifier_integration_test.go`,
   `db_inferred_capacity_entries_integration_test.go`, `db_backfill_integration_test.go` MUST
   continue to pass with the exact same expected values. The ONE documented exception:
   `db_session_reader_by_vehicle_integration_test.go`'s T-Order2 case, whose expected EXPLAIN
   substring changes from `"Index Scan Backward using idx_charge_sessions_vehicle_stop"` to
   `"Index Scan Backward using idx_supercharger_sessions_vehicle_stop"` — this is a rename
   this tier makes on purpose, not a regression signal.

5. **Row counts before vs. after — manual verification, not a new automated test.**
   `SELECT count(*) FROM charging.supercharger_sessions`,
   `charging.manual_charge_entries` immediately after migration MUST equal the same two
   counts taken against `public.charge_sessions`/`public.manual_charge_entries` immediately
   before it.

6. **D6's backfill is unaffected by this tier.** `20260823000001`'s backfill guard
   (`to_regclass('public.supercharger_sessions')`) still resolves against telemetry's
   still-`public` table; this tier's migration runs strictly AFTER `20260823000001` within
   charging's own directory (later timestamp), so by the time this tier's rename applies,
   the backfill has already run and its result is already committed data in what is about to
   become `charging.supercharger_sessions`. No re-ordering risk.

No new `_test.go` test is required specifically to prove the schema move/rename, for the same
reasoning as tiers 1–2: `internal/testdb` already provisions from this migrations directory,
and any pre-existing test failing to run against the new schema/name is itself the signal.

## Makefile / tooling re-check (per `CLAUDE.md`'s "reverse direction" docs rule)

The roadmap's own audit (`RM39-schema-per-module.md` §"The Makefile needs no changes") found
zero `schema`/`public`/`GRANT`/`search_path` occurrences in the `Makefile`. This tier's own
check, scoped to `charging`, confirms nothing here invalidates that finding: `MIGRATIONS_DIRS`
already includes `internal/charging/db/migrations`, this migration adds no new directory, no
new guard target, and no new `sqlc.yaml` structural entry (the existing charging `sql:` block
is edited in place). `internal/testdb`'s `ProvisionDirs` needs no change — it applies whatever
is in `db/migrations/*.sql`, and this tier adds one file there;
`internal/charging/testdb_test.go`'s existing `ProvisionDirs(ctx,
"../telemetry/db/migrations", "db/migrations")` call is unaffected. `make migration-guard`
remains required (D4) — this tier's migration timestamp (`20260902000003`) was checked for
global uniqueness across every module directory, not just this module's own (see "Migration
filename" above).
