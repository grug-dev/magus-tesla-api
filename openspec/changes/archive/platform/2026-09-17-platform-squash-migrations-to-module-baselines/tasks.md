# Tasks

Every task marked **[owner]** is a database or deploy command. The owner runs those.
The assistant runs none of them, and writes no backup.

Task groups 1–8 touch only files. Nothing reaches a database before group 9.

## 1. Branch

- [x] 1.1 Bump `openspec/.work-counter` from `64` to `66`, and commit the bump with the artifacts. `CH64` was already used by MAG-84 (branch and merged PR #72) without the counter being bumped, so this change takes `CH65` — a number is never reused
- [x] 1.2 Create and switch to `ft/CH65-MAG-83-platform-squash-migrations-to-module-baselines`

## 2. The four baselines

Source of truth: the `pg_dump --schema-only --no-owner --no-privileges` of dev taken
2026-09-17. Transcribe it, including `telemetry.vehicle_snapshots.account_id` — the drop
is task 3.1, not an omission here (design D4).

- [x] 2.1 Delete all 10 files in `internal/account/db/migrations/` and write `20260917000001_baseline.sql` with `accounts`, `settings`, `tesla_tokens`, `vehicles` — plus `CREATE SCHEMA account`, every index, constraint, FK, default and table/column `COMMENT` the dump carries
- [x] 2.2 Same for `internal/telemetry/db/migrations/` — 17 files out, one baseline in, with `poll_attempts`, `poll_runs`, `supercharger_history`, `vehicle_snapshots`
- [x] 2.3 Same for `internal/charging/db/migrations/` — 15 files out, one baseline in, with `manual_charge_entries`, `mirror_watermarks`, `monthly_effective_capacity`, `supercharger_sessions`
- [x] 2.4 Same for `internal/analytics/db/migrations/` — 13 files out, one baseline in, with `charge_gaps`, `vehicle_metric_watermarks`, `vehicle_metrics`
- [x] 2.5 Give all four baselines the identical `Down` block from design D3, raising an exception that names the module
- [x] 2.6 Confirm each folder holds exactly one file, and that all four carry version `20260917000001`

## 3. The one deliberate schema change

- [x] 3.1 Write `internal/telemetry/db/migrations/20260917000002_drop_vehicle_snapshots_account_id.sql` — `ALTER TABLE telemetry.vehicle_snapshots DROP COLUMN account_id;`, with a reversible `Down` that adds the column back as nullable `uuid`. No index or constraint references it, so nothing else changes

## 4. Go entry points

- [x] 4.1 In `cmd/migrate/main.go`, pass `goose.WithTableName("<module>.goose_db_version")` per module and delete `goose.WithAllowOutofOrder(true)` (line 126) with its comment block (lines 102+)
- [x] 4.2 In `internal/testdb/testdb.go`, the same two edits (`WithAllowOutofOrder` at line 204, comment at 196)
- [x] 4.3 Check how each entry point maps a migrations directory to its module name — the table name must come from the same single source as the directory, never a second hardcoded list

## 4b. Automatic baseline stamp (removes the manual prod step)

A database built before the squash already has every table a baseline creates, so goose
would run the baseline and fail on `relation already exists`, the `migrate` service would
exit non-zero, and compose would never start `web` or `poller`. The original plan fixed
that with a hand-run SQL file on dev and on prod. That put a manual step in front of an
automatic deploy, with nothing enforcing the order — so the migration runner does it
itself instead.

- [x] 4b.1 `internal/config`: add `MigrationDir.BaselineVersion()`, which reads the module's
  lowest numbered migration from disk rather than hardcoding `20260917000001`, so the stamp
  cannot drift from the files it claims to record
- [x] 4b.2 `internal/config`: add `MigrationDir.StampBaselineSQL(version)`, returning the two
  statements that create the ledger and record the baseline. Two statements, not one string:
  the pgx driver uses the extended protocol, which rejects several statements in one `Exec`
- [x] 4b.3 Guard the INSERT on **tables** existing in the schema, never on the schema itself —
  the runner creates the schema one line earlier, so a schema check would wrongly skip the
  baseline on a brand-new database. Second guard: an empty ledger, which makes it idempotent
- [x] 4b.4 Copy the ledger DDL from goose's own postgres dialect and use
  `CREATE TABLE IF NOT EXISTS`. goose calls `TableExists` before creating the ledger
  (`provider_run.go`, `tryEnsureVersionTable`), so pre-creating it cannot collide
- [x] 4b.5 `cmd/migrate`: call `stampBaseline` in `applyDir`, right after `EnsureSchemaSQL`
  and before goose. Keep the reasoning in `internal/config` so `cmd/` stays wiring
- [x] 4b.6 `cmd/migrate`: add `-stamp-only`, for the `Makefile` targets that migrate with the
  goose CLI and so cannot reach the Go path any other way
- [x] 4b.7 `Makefile`: run `go run ./cmd/migrate -stamp-only` before the goose loop in
  `migrate-up`, `db-setup` and `db-setup-test`. One implementation, three callers — a second
  `psql -c` here would be free to drift from the Go one
- [x] 4b.8 Tests: `internal/config` for version discovery and the generated SQL;
  `cmd/migrate` for the four database shapes — pre-squash (stamps), repeated (idempotent),
  fresh schema (records nothing), already migrated (unchanged). Each DB test builds its own
  throw-away schema, so none depends on the order the others ran in
- [x] 4b.9 This is one-time code. When dev and prod are both past the squash, delete
  `BaselineVersion`, `StampBaselineSQL`, `stampBaseline`, the `-stamp-only` flag and the
  three `Makefile` lines
- [x] 4b.10 `.gitignore`: add `/migrate` and `/monthly-capacity` to the root-built-binary
  list. It named only four of the six `cmd/` runnables, so `go build ./cmd/migrate` — which
  this work runs constantly — leaves an untracked 15 MB binary at the repo root. Found by
  producing one

## 5. Guards

- [x] 5.1 Delete the `migration-guard` target and `KNOWN_DUPLICATE_MIGRATIONS` from the `Makefile` (lines 627–664), and remove `migration-guard` from `.PHONY` (line 93) and from `check` (line 999)
- [x] 5.2 Add `migration-boundary-guard`: fail when a file under `internal/<module>/db/migrations/` names a Postgres schema other than `<module>`, ignoring SQL comments. Mirror `money-guard` / `tz-guard` / `boundary-guard` in shape and escape-hatch convention
- [x] 5.3 Wire the new guard into `.PHONY` and `check`, in `migration-guard`'s old position
- [x] 5.4 Run `make migration-boundary-guard` — it must pass, because the squash removed all four cross-module migrations
- [x] 5.5 Run every other guard and confirm none regressed

## 6. Codegen

- [x] 6.1 Run `sqlc generate`. `sqlc.yaml` needs no edit — `schema:` already points at each module's migrations folder
- [x] 6.2 Review the `models.go` diff. The only expected change is the `AccountID` field leaving the telemetry snapshot model. Anything else means a baseline does not match the dump
- [x] 6.3 Fix any Go code that referenced the dropped field, then `go build ./...`, `go vet ./...`, `gofmt -l`, `make lint`

## 7. Reverse-direction check (mandatory, CLAUDE.md)

Findings, one per item. "I did not think about it" is not a finding.

- [x] 7.1 `make migrate-down` — **changed, deliberately.** It now walks `MIGRATION_MODULES` in
  reverse and hits a baseline that raises. The failure is clean: the transaction aborts, so the
  schema is untouched AND the ledger row is not deleted, which means a later `migrate-up` still
  behaves. Documented in the target's own comment, in `ai/go-conventions.md`, and in
  `kkpa/docs/0-set-up/running-the-server.md`. Backlog item 28 (a `down` subcommand for
  `cmd/migrate`) was updated to say a down path must stop at the baseline.
- [x] 7.2 `make db-setup` / `db-reset` — **unaffected, and verified why.** Both create the role
  and database, then run the same goose loop, which now also passes `-table`. Ownership still
  works because the baseline creates each schema while connected as the app role, exactly as the
  old `CREATE SCHEMA` migration did. `db-setup-test` additionally re-owned four schemas from a
  **second hardcoded list**; that list now comes from `MIGRATION_MODULES`, so adding a module
  cannot leave it behind.
- [x] 7.3 `MIGRATIONS_DIRS` — **changed.** It is now derived from `MIGRATION_MODULES` rather
  than written out, and the goose CLI loops iterate modules, not dirs, because each invocation
  needs both the directory and the ledger name. Overriding `MIGRATIONS_DIRS` alone therefore no
  longer changes those loops — `MIGRATION_MODULES` is the knob. Verified with
  `make -n migrate-up` and `make -n migrate-run`: the first prints `-table $m.goose_db_version`
  per module with no `-allow-missing`, the second still exports the four derived paths for
  `cmd/migrate`. The order is now meaningless to the result and is documented as kept only for
  comparable logs.
- [x] 7.4 `deploy/docker/Dockerfile` — **unaffected, verified.** The four `COPY` lines (54-57)
  still resolve: the folders did not move, only their contents changed. `cmd/migrate`'s
  image-default path (`MIGRATIONS_ROOT` + the four module names) also still works, and now
  yields the module name for the ledger from the same list that builds the path.
- [x] 7.5 Guards and `sqlc` — all 13 guards pass, including the new one, verified in both
  directions and with its escape hatch. `sqlc.yaml` needed no change; its `schema:` already
  points at each module's migrations folder, and it parses a baseline the same way.
  `gofmt`, `go build ./...`, `go vet ./...` and `make lint` (0 issues) are clean.
- [x] 7.6 **Unplanned finding — `make -n` on any migrate target prints `DATABASE_URL`,
  password included.** Pre-existing, not introduced here: the DSN is a command-line argument, so
  it is visible in `ps` too. The `Makefile` already documents avoiding this for `migrate-run`
  specifically. Not fixed here — it is unrelated to this change and would widen it.
- [x] 7.7 **Unplanned finding — `config.moduleForDir` is new logic with no test.** Unit tests
  were excluded by the owner's decision, taken before this function existed. It resolves a
  `MIGRATIONS_DIRS` path to its module and errors when no segment names one. Its happy path is
  covered indirectly by the three existing `TestLoadMigration_MigrationsDirs*` tests; its error
  path is not covered at all. Worth a small test if the owner wants one.

## 8. Docs (same change, not a follow-up)

- [x] 8.1 `README.md` — the migration section and "Making a change"
- [x] 8.2 `ai/architecture.md` — one baseline per module, the per-module ledger, the no-cross-module-schema rule
- [x] 8.3 `ai/go-conventions.md` — how to add a migration now, and that a migration may only touch its own schema
- [x] 8.4 `cmd/README.md` — `cmd/migrate`'s behaviour
- [x] 8.5 The module `AGENTS.md` files — `analytics` (its `MIGRATIONS_DIRS` line and the
  no-cross-module-schema rule, since it is the module that broke it most), `charging` (its
  ledger name in the testdb paragraph), and **`config`**, which the original list missed:
  it documents `MigrationsDirs` field by field and every word of that had changed.
  `account` and `telemetry` needed nothing — their only migration mentions are generic
  and still true
- [x] 8.6 `kkpa/context/architecture/schema-per-module.md` — its file map and any count this change invalidated
- [x] 8.7 Grep `kkpa/context/` for `migration-guard`, `goose_db_version` and `KNOWN_DUPLICATE`
  and fix every stale hit. One live guide needed it: `architecture/schema-per-module.md`, whose
  RM39 decision D4 said the shared ledger stays and the guard is NOT retired — both now false.
  The four `location_kind` mentions across the KB were checked and are **correct as they
  stand**: they all describe it as required at the application level, which it still is, and
  none claims a database `NOT NULL`. `kkpa/context/pending-spec-to-sync/applied/` was left
  alone — those are records of what was applied at the time, like the archive.
  Also swept and fixed outside the planned list: `kkpa/docs/1-deploy/docker.md`,
  `kkpa/docs/0-set-up/deployment.md`, `kkpa/docs/0-set-up/running-the-server.md`,
  `CLAUDE.md`, and `openspec/roadmaps/backlog.md` (items 21 and 23 removed as picked up,
  item 28's rollback SQL corrected to the per-module ledger, item 16's ordering blocker
  recorded as removed by this change). **`openspec/changes/archive/` untouched** —
  `make archive-guard` confirms it
- [x] 8.8 Run `make archive-guard`

## 9. Prove the baselines before any live database is touched **[owner]**

On your laptop, on the feature branch. Nothing here touches dev or prod.

```bash
cd ~/cpena/sw/github/magus-tesla-api
git checkout ft/CH65-MAG-83-platform-squash-migrations-to-module-baselines

# Load .env into this shell. Do it this way so the password never reaches your
# shell history, and avoid `make -n` on any migrate target: it prints the DSN in full.
set -a; . ./.env; set +a

# The scratch DSN is your own, with the database name swapped.
SCRATCH=$(printf '%s' "$DATABASE_URL" | sed 's#/[^/?]*?#/magus_baseline_check?#')

# DUMP_A is the pg_dump --schema-only of dev. Re-take it if the old one is gone:
DUMP_A=/tmp/dump-A.sql
pg_dump --schema-only --no-owner --no-privileges -d "$DATABASE_URL" -f "$DUMP_A"
```

- [ ] 9.1 Build a scratch database from the new migrations and dump it.

  **The variable MUST go after the target.** `make migrate-up DATABASE_URL=...` is a make
  command-line variable and wins. The env form `DATABASE_URL=... make migrate-up` does
  **not** work here and is dangerous: the `Makefile` does `include .env` at line 22, and in
  make a file assignment beats an environment variable, so the env form silently keeps the
  real dev DSN and runs the baselines against your live dev database. Verified with
  `make -n` both ways.

```bash
# db-reset, NOT `createdb` + migrate-up. createdb makes your OS role the owner, and since
# Postgres 15 a non-owner has no CREATE on a database — the first CREATE SCHEMA then fails
# with "permission denied for database magus_baseline_check" and goose reports the missing
# schema afterwards. db-reset creates it owned by APP_ROLE, the role the DSN connects as.
# It is destructive, which is correct for a scratch database and nothing else.
make db-reset DATABASE_URL="$SCRATCH"            # variable AFTER the target
pg_dump --schema-only --no-owner --no-privileges -d "$SCRATCH" -f /tmp/dump-B.sql

# Filter the \restrict / \unrestrict lines: pg_dump writes a fresh random token on
# every run, so two dumps of the SAME schema always differ on those two lines. Left in,
# they look like drift and would stop a run that is actually correct.
diff <(grep -vE '^\\(un)?restrict ' "$DUMP_A") \
     <(grep -vE '^\\(un)?restrict ' /tmp/dump-B.sql)
```

  `make migrate-up` needs **`psql` on PATH as well as `goose`** now: it creates each
  module's schema before goose, because goose builds its version table inside that schema
  before running anything. If the run fails with `schema "<module>" does not exist`, the
  schema step did not happen — the database is untouched and the command is safe to re-run.

- [ ] 9.2 Confirm the diff is **exactly** these three things and nothing else:
  1. `account_id uuid,` gone from `telemetry.vehicle_snapshots`
  2. `public.goose_db_version` in A, not in B
  3. the four `<module>.goose_db_version` tables in B, not in A

  A fourth difference is drift between the migrations and the live database. **Stop and
  report it** — this diff is the only proof the baselines are faithful.

- [ ] 9.3 Drop the scratch database: `dropdb magus_baseline_check`

## 10. Bring dev up to date **[owner]**

Still on your laptop. There is no stamp step to run by hand any more: `make migrate-up`
runs `cmd/migrate -stamp-only` first, which records a baseline as applied only on a
database that already has that module's tables. See
`config.MigrationDir.StampBaselineSQL` for why running it unconditionally is safe on a
fresh database and on one already past the squash.

- [ ] 10.1 Stamp and migrate in one command:

```bash
make migrate-up
```

  Expect four `pre-existing schema — recorded baseline 20260917000001 as applied without
  running it` lines, then `20260917000002` applying in `telemetry` and nothing in the
  other three. A `relation already exists` here means the stamp did not fire — stop and
  report it rather than reaching for the SQL file.

- [ ] 10.2 Check the ledgers:

```bash
psql "$DATABASE_URL" \
  -f openspec/changes/platform-squash-migrations-to-module-baselines/runbook/verify-ledgers.sql
```

  Expect five rows: `telemetry` at `filas = 3` / `maximo = 20260917000002`, the other
  three modules at `filas = 2` / `maximo = 20260917000001`, and `public (old)` at `55` /
  `20260915000001`. The second query returns **zero** rows — `account_id` is gone.

- [ ] 10.3 Start the app (`make up`) and confirm the dashboard still loads.

## 11. Run the tests **[owner]**

- [ ] 11.1 `make test` (or `make check` for the full gate). Report the result — until you
  do, this change is **awaiting your verification**, never "done"

## 12. Merge **[owner]**

Prod deploys by pulling `main`, so the branch has to land first.

- [ ] 12.1 Merge the branch into `main` and push it. Merging does not deploy — this repo has
  no CI. The deploy is group 14, by hand, and it stamps prod itself

## 13. Prod — nothing to stamp by hand **[owner]**

This group used to be the manual prod stamp. It is gone: the `migrate` service runs the
same stamp inside the deploy, before goose, so prod fixes itself in group 14. That removes
the trap this plan used to carry — a third hand-run command that had to come before the
usual `git pull && make docker-up`, once and only once, with nothing enforcing the order.

`runbook/stamp-baseline.sql` is kept as a fallback for a database the runner cannot
reach (a restored dump, a manual recovery). It is not part of the normal path.

**13.1 is REQUIRED, not optional.** The stamp fires on "this module's schema already has
tables". It does **not** check that those tables are at the pre-squash head, because it has
no cheap way to. A database that stopped short of `20260915000001` gets stamped anyway, and
then `20260917000002` fails against a schema that never got the column it drops.

That is not theoretical: `magus_test` was three migrations behind (`20260908000002`,
`20260908000003`, `20260914000002`), got stamped, and `DROP COLUMN account_id` failed
because the column had never been added. Its `max(version_id)` was `20260915000001`, the
same as dev's — only the **count** differed, 52 against 55. So check the count, not the max.

- [ ] 13.1 **Confirm prod applied every pre-squash migration.** On the VPS:

```bash
ssh <your VPS>
cd /home/magus/magus-tesla-api
git pull

set -a; . ./.env; set +a
DC="docker compose --project-directory . -f deploy/docker/compose.yaml"

$DC exec -T db psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
  < openspec/changes/platform-squash-migrations-to-module-baselines/runbook/verify-ledgers.sql
```

  Before the deploy the four module rows are absent and `public (old)` reads `55` /
  `20260915000001`. **That row is the rollback path** — the previous image reads it, and
  nothing in this change writes to it.

- [ ] 13.2 Confirm the count precisely — `55` is the number that matters:

```bash
$DC exec -T db psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc \
  "SELECT count(DISTINCT version_id) FROM public.goose_db_version WHERE is_applied"
```

  **If it is not 55, STOP — do not merge.** Prod is behind, and stamping it would record
  baselines it does not match. Bring prod current on the old image first, or report the
  number and stop.

## 14. Deploy prod, then verify **[owner]**

- [ ] 14.1 Deploy:

```bash
$DC up -d --build
```

- [ ] 14.2 Confirm the one-shot migration step exited 0 — it applies `20260917000002` and
  skips the four baselines:

```bash
$DC logs migrate | tail -20
```

  Expect four `pre-existing schema — recorded baseline 20260917000001 as applied without
  running it` lines, then four `applied 0 migration(s)` and one `applied 1 migration(s)`
  for `telemetry`. A `relation already exists` here means the stamp did not fire — see
  below.

- [ ] 14.3 Confirm `web` and `poller` are up and healthy: `$DC ps`
- [ ] 14.4 Re-run 13.2. `telemetry` reads `maximo = 20260917000002`, and the second query
  returns zero rows
- [ ] 14.5 Open the site and confirm a page renders

**If 14.2 fails:** the stamp did not fire and a baseline tried to run against a populated
database. Nothing is damaged — it refused rather than writing. `web` and `poller` will not
have started, because both wait for the migration step to succeed. Two ways out: apply
`runbook/stamp-baseline.sql` by hand (the fallback in group 13) and redeploy, or redeploy
the previous image, which reads `public.goose_db_version` — untouched and at max version
with nothing pending, so there is no database step to undo. Report the failure either way:
the stamp firing is the thing this change is relying on.

## 15. Close

- [ ] 15.1 Decide whether you want a test for `config.moduleForDir` — its error path is
  uncovered, and unit tests were excluded before that function existed
- [ ] 15.2 Archive the change, moving the folder under
  `openspec/changes/archive/platform/`, and sync `kkpa/context/`

---

## Outcome — recorded at archive time (2026-09-17)

The checkboxes above are left as they stood. This section is what actually happened, so
the record is not read off a half-ticked list.

**Done and verified.**

- The baselines were proved against a scratch database, compared structurally (columns,
  indexes, constraints, relations, functions, triggers) rather than by text diff. 47
  relations matched. Exactly the three expected differences, no fourth.
- Dev was migrated through the new path. The three untouched modules recorded
  `20260917000001` without running it, `telemetry` applied `20260917000002`,
  `public.goose_db_version` stayed at 55 rows, and no row was lost.
- Prod's pre-squash ledger was confirmed at 55 applied versions before the merge.
- PR #73 merged. Prod deployed with `git pull && make docker-up`. The `migrate` service
  logged four stamp lines, `applied 1 migration(s)` for `telemetry`, `applied 0` for the
  other three, and exited 0. `web` and `poller` started.

**Changed after the plan was written.**

- The manual stamp (originally groups 10 and 13) was replaced by an automatic one in
  `cmd/migrate`, added as group 4b. See design.md D9.
- Two bugs were found and fixed before the deploy. The guard first refused a ledger
  holding only goose's version-0 marker, which is exactly the state a failed first run
  leaves. And the guard cannot tell a stale database from a current one — `magus_test`
  was three migrations behind, got stamped, and the column drop then failed. The owner
  accepted that limitation; the control is the required count check in 13.2.
- design.md originally said prod deploys automatically on a merge to `main`. It does not
  — this repo has no CI. Corrected before archiving.

**Not done at archive time.**

- `make test` had not passed on the owner's machine. Docker was down, and
  `internal/account` and `internal/charging` abort rather than skip without it. The
  no-Docker route is `make db-setup-test` plus `TEST_DATABASE_URL`.
- 14.3, 14.4 and 14.5 (prod `ps`, ledger re-check, page render) were not recorded.
- 15.1, a test for `config.moduleForDir`, was not taken up.
