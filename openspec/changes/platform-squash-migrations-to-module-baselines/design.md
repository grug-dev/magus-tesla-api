# Design — squash migrations to one baseline per module

## Context

Four module folders hold 55 migration files. `cmd/migrate` and `internal/testdb`
apply them one folder at a time, in the `MIGRATIONS_DIRS` order: `account`,
`telemetry`, `charging`, `analytics`. All four share one `public.goose_db_version`
table, so a version number used twice is recorded once and the second file is
skipped in silence.

State measured on 2026-09-17:

| | |
|---|---|
| Migration files on disk | 55 |
| Unique version numbers | 54 (`20260720000001` used twice) |
| Versions applied on prod | 54, max `20260915000001`, 0 pending |
| Versions applied on dev | 54, max `20260915000001`, 0 pending |
| Postgres schemas | 4 — `account`, `telemetry`, `charging`, `analytics` |
| Domain tables | 15 — account 4, telemetry 4, charging 4, analytics 3 |
| Migrations reading another module's schema | 4 |
| Dead columns | 1 — `telemetry.vehicle_snapshots.account_id` |

The ground truth for the baselines is a `pg_dump --schema-only --no-owner
--no-privileges` of dev, taken by the owner on 2026-09-17: 1224 lines. Dev and prod
report the same version ledger, so dev's schema stands for both.

The ticket's own counts said "telemetry 5" and "16 tables". The dump says telemetry
has 4 (`poll_attempts`, `poll_runs`, `supercharger_history`, `vehicle_snapshots`)
and the domain total is 15. The ticket's 16 counted `public.goose_db_version`.

Two constraints shape everything below.

**The assistant never runs a database command.** Every `CREATE`, `INSERT`, `psql`,
`pg_dump` and `make migrate-*` in this document is run by the owner. The change
ships the exact text to paste.

**A failed migrate step stops the site.** `web` and `poller` both declare
`depends_on: migrate: condition: service_completed_successfully`. If the migration
container exits non-zero, neither starts.

## Goals / Non-Goals

**Goals:**

- One migration file per module, so no ordering between modules can be wrong.
- A version ledger private to each module, inside the Postgres schema that module
  already owns, so a module's schema travels complete when it becomes its own service.
- No migration reads another module's schema, enforced by a guard rather than by luck.
- Drop `telemetry.vehicle_snapshots.account_id`, which MAG-65 could not drop.
- Dev, prod, and a freshly built database end with the same schema, provably.

**Non-Goals:**

- `charging.manual_charge_entries.location_kind` nullability. Waived by the owner: no
  row is NULL today and the Go insert path always sends a string. The baseline records
  the column exactly as the database has it.
- Any change to the deploy path's shape. The four `COPY` lines in
  `deploy/docker/Dockerfile`, the `migrate` service, and `MIGRATIONS_DIRS` all keep
  working unchanged.
- Data migration of any kind. A baseline creates objects and backfills nothing.
- New unit tests. `internal/testdb` builds every test container's schema from these
  folders, so a wrong baseline fails the whole suite at setup. That existing signal is
  stronger than any test this change could add.

## Decisions

### D1 — One baseline per module, not one global version order

Rejected alternative: MAG-76's plan, sorting all 55 files into a single global order
and ignoring the folder.

A global order declares the four modules to be one timeline. That is the opposite of
the independence this project is built for, and it would have to be undone when a
module becomes its own service. It also only re-orders the cross-module SQL instead of
removing it, so the next backfill reintroduces the problem.

A baseline removes the cross-module SQL outright: it creates tables and reads nothing.
One file per folder has no order left to get wrong.

### D2 — All four baselines carry the same version, `20260917000001`

Rejected alternative: a distinct number per module.

With a version table per module, the same number in four folders is legal. Using it
deliberately is the clearest possible statement in the repo that the collision class is
gone — the exact thing that was impossible before is now the normal case. A distinct
number per module works identically but leaves the impression that the numbers still
have to be coordinated across modules.

### D3 — Each baseline's `Down` raises an exception

```sql
-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    RAISE EXCEPTION
        'this baseline is not reversible: rolling it back would mean dropping the whole % schema. Recreate the database instead.',
        '<module>';
END
$$;
-- +goose StatementEnd
```

Rejected alternative A — an empty `Down`. This is a trap. `goose down` would mark the
baseline un-applied without touching a table. The next `migrate-up` then tries to run
the baseline against a database that still has every object, fails on `relation already
exists`, and the migration container exits non-zero. On prod that keeps `web` and
`poller` from starting. A rollback command that arms a later outage is worse than one
that refuses.

Rejected alternative B — `DROP SCHEMA <module> CASCADE`. Honest and truly reversible,
but it turns `make migrate-down` into a button that deletes a module's data. The dev
database holds snapshot history and hand-entered charges Tesla cannot backfill.

Raising makes the refusal loud, deterministic, and free of side effects: the
transaction aborts, so the version row is not deleted either. `make migrate-down`
across modules was already not a usable operation; this states that plainly instead of
pretending.

### D4 — The baselines mirror today's schema; the deliberate drop is a separate migration

The four baselines transcribe the 2026-09-17 dump exactly, **including**
`telemetry.vehicle_snapshots.account_id`. A fifth file,
`telemetry/20260917000002_drop_vehicle_snapshots_account_id.sql`, does the drop as an
ordinary migration.

Rejected alternative: simply leaving the column out of the baseline.

That does not work, and the reason is the whole mechanism of this change. Dev and prod
are *stamped*: their version tables are seeded with `20260917000001` and the baseline
**never runs there**. So anything a baseline says that differs from the live database is
a statement about fresh databases only. Omitting the column would leave it on dev and
prod forever while a fresh database lacks it — and `pg_dump A` vs `pg_dump B` would
then never match again, destroying the one verification that makes this change safe.

A normal migration after the baseline runs everywhere: dev, prod, test containers, and
fresh databases. That keeps all four identical and keeps the dump diff usable for every
future change.

The drop is clean. In the live schema, `telemetry.vehicle_snapshots` has a primary key
on `(id)` and a unique constraint `vehicle_snapshots_tesla_date_unique` on `(tesla_id,
captured_date)`. **No index and no constraint references `account_id`.** The index
lines mentioning it in `20260911000002` are inside that migration's `Down` block,
recreating the pre-re-key indexes. So there is no index to rebuild and no read path to
re-plan.

### D5 — One goose version table per module, inside the module's own schema

`goose.WithTableName("<module>.goose_db_version")` in both `cmd/migrate/main.go` and
`internal/testdb/testdb.go`. Confirmed supported in the pinned
`github.com/pressly/goose/v3 v3.27.3`: the option's documented form is
`goose.WithTableName("schema.my_migrations")`, and it cannot be combined with
`WithStore`, which this project does not use.

Full schema of each of the four new tables — this is goose's own Postgres DDL, copied
from `internal/dialects/postgres.go:19` of the pinned version, so the table goose
would have created and the table we create by hand are identical:

```sql
CREATE TABLE <module>.goose_db_version (
    id         integer PRIMARY KEY GENERATED BY DEFAULT AS IDENTITY,
    version_id bigint  NOT NULL,
    is_applied boolean NOT NULL,
    tstamp     timestamp NOT NULL DEFAULT now()
);
```

**Index plan: the primary key on `id`, and nothing else.** This is deliberate and it
is what goose itself ships. The table is read once per process start, with a full scan
of at most a handful of rows, to compute the applied set and the maximum version. It is
written once per applied migration. Adding an index on `version_id` would serve no
query that exists and would cost a write on the one path that is already the rarest in
the system. The project's read-heavy profile argues for aggressive indexing on tables
the gateway reads per request; this table is read by a migration process, never by a
request.

Why inside the module's schema rather than four differently-named tables in `public`:
each module already owns a Postgres schema, and its version ledger is part of what that
module owns. When a module is extracted into its own service, `pg_dump --schema=telemetry`
then carries the schema *and* its migration history in one piece.

### D6 — `public.goose_db_version` is left exactly as it is

Rejected alternative: dropping it once the four new tables exist.

It holds 55 rows recording which of the 54 versions were applied and when. After the
squash that is the only surviving record of that history in a database, and the
accepted cost of this change is precisely the loss of replayable history. Keeping the
ledger costs nothing — no code reads it after this change — and recovers part of what
the squash gives up. It also makes rollback to the previous image trivial (see below).

### D7 — Delete `migration-guard`, turn off `WithAllowOutofOrder`

`make migration-guard` and `KNOWN_DUPLICATE_MIGRATIONS` exist only to hold back a
collision that becomes impossible once each module has one file and its own ledger. A
guard kept past the death of its failure mode is a maintenance cost that teaches a
future reader the wrong lesson about what can go wrong.

`goose.WithAllowOutofOrder(true)` is on in both entry points for the same single
reason: the shared table made a later folder's lower version look like a missing
migration. With per-module ledgers each module's versions are monotonic on their own,
so goose's default protection against a migration arriving late can be restored. That
protection is currently disabled repo-wide, which is a real loss — turning it back on
is a gain this change makes almost for free.

### D8 — A guard forbids cross-module schema references in migrations

This is the root cause, and it is the one part of the change that prevents recurrence.
Without it, the next cross-module backfill reintroduces the ordering dependency and the
baselines only bought time.

Shape: a grep guard in the `Makefile`, following `money-guard` / `tz-guard` /
`boundary-guard` — same structure, same escape-hatch comment convention. It fails when
a file under `internal/<module>/db/migrations/` names a Postgres schema other than
`<module>`, ignoring SQL comments. The current four offenders disappear with the
squash, so the guard starts green.

A module that may not read another module's tables at runtime must not read them in a
migration either. The guard makes that rule the same rule in both places.

## Risks / Trade-offs

**The stamp is wrong or missing on prod, and the deploy carries the baselines.** →
The baseline runs against a populated database and fails on `relation already exists`.
`migrate` exits non-zero and `web` and `poller` never start: the site goes down. This
is the one genuinely dangerous step. Mitigations, in order: the stamp lands on prod
*before* the deploy, never after; the runbook below verifies it with a query whose
expected output is written down; and the failure mode is loud and non-destructive — it
refuses to run rather than damaging data.

**A baseline drifts from the live schema.** → A fresh database, including every test
container, differs from prod in a way nothing notices until a query fails. Mitigation:
the dump diff in step 2 of the runbook, whose expected output is stated exactly. An
unexpected line in that diff is the drift, before anything merges.

**`make migrate-down` now fails on a baseline.** → Accepted, by D3. It was never a
usable cross-module operation; the alternative arms a later outage or deletes
irreplaceable data.

**Replayable history is lost.** → Accepted. `git log` keeps all 55 files, and
`public.goose_db_version` keeps the applied-and-when record (D6).

**The old image and the new image disagree about where the ledger lives.** → This is
a mitigation, not a risk. The previous image reads `public.goose_db_version`, which
this change never touches, and prod is at max version with nothing pending there. So
rolling back to the previous image is a working rollback with no database step at all.

## Migration Plan

Every step marked **[owner]** is a database or deploy command the owner runs. The
assistant runs none of them.

**Step 1 — build the branch.** Baselines, the drop migration, `WithTableName`,
`WithAllowOutofOrder` off, `migration-guard` deleted, new guard added, `sqlc generate`,
docs. Nothing touches a database.

**Step 2 — prove the baselines against a scratch database. [owner]**

```bash
# A = the live dev schema (already taken 2026-09-17)
# B = an empty database + the new migrations
createdb magus_baseline_check
DATABASE_URL="postgres://localhost:5432/magus_baseline_check?sslmode=disable" make migrate-up
pg_dump --schema-only --no-owner --no-privileges \
  -d "postgres://localhost:5432/magus_baseline_check?sslmode=disable" -f /tmp/dump-B.sql
diff /tmp/dump-A.sql /tmp/dump-B.sql
```

The diff is expected to be **exactly** these three things, and nothing else:

1. `account_id uuid,` gone from `telemetry.vehicle_snapshots` — the deliberate drop.
2. `public.goose_db_version` present in A, absent in B.
3. `account.goose_db_version`, `telemetry.goose_db_version`,
   `charging.goose_db_version`, `analytics.goose_db_version` absent in A, present in B.

Any fourth difference is drift. Stop and report it rather than explaining it away.

**Step 3 — stamp dev, then apply the drop. [owner]** Run for each of the four modules,
then `make migrate-up` to apply `20260917000002` only.

**Step 4 — stamp prod. [owner]** The same SQL against prod, **before** the deploy.
Safe to run early: the running image reads `public.goose_db_version` and cannot see the
new tables.

**Step 5 — verify prod's ledger before deploying. [owner]** Four rows expected, one
per module, each with `max(version_id) = 20260917000001`.

**Step 6 — deploy. [owner]** `migrate` finds each module's ledger at
`20260917000001`, skips the baseline, applies `20260917000002`, exits 0. `web` and
`poller` start.

**Rollback.** Before step 6, redeploy the previous image: it uses
`public.goose_db_version`, which is untouched and at max version with nothing pending.
After step 6, the only schema change made is the dropped `account_id`; the previous
image does not read that column, so the previous image still runs. The four new ledger
tables are inert to it.

The exact SQL for steps 3, 4 and 5 is written out in `tasks.md`, as text to paste.

## Open Questions

**Does the stamp insert a `version_id = 0` row?** The plan says yes, for both databases
and all four tables, so the stamped ledger matches the shape the goose CLI produces and
the shape `public.goose_db_version` already has on prod (55 rows for 54 versions).

This is cosmetic, and worth recording as a known inconsistency of goose rather than of
this change: the Provider API used by `cmd/migrate` creates the table **without** a `0`
row, while the CLI used by `make migrate-up` creates it **with** one. So a fresh
database's row count already depends on which entry point touched it first. No goose
code path reads that row — the applied set is keyed on `version_id` — so neither choice
changes behaviour.
