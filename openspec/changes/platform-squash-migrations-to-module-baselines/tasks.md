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
createdb magus_baseline_check
make migrate-up DATABASE_URL="$SCRATCH"          # variable AFTER the target
pg_dump --schema-only --no-owner --no-privileges -d "$SCRATCH" -f /tmp/dump-B.sql
diff "$DUMP_A" /tmp/dump-B.sql
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

## 10. Stamp dev, then apply the drop **[owner]**

Still on your laptop. `psql "$DATABASE_URL"` reads the DSN from the variable you loaded
above, so nothing is typed out.

- [ ] 10.1 Stamp dev:

```bash
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 \
  -f openspec/changes/platform-squash-migrations-to-module-baselines/runbook/stamp-baseline.sql
```

  Expect `BEGIN`, four `CREATE TABLE`, four `INSERT 0 2`, `COMMIT`. Any error rolls the
  whole thing back — the file is one transaction.

- [ ] 10.2 Check the ledgers:

```bash
psql "$DATABASE_URL" \
  -f openspec/changes/platform-squash-migrations-to-module-baselines/runbook/verify-ledgers.sql
```

  Expect five rows: the four modules at `filas = 2`, `maximo = 20260917000001`, and
  `public (old)` at `55` / `20260915000001`. The second query returns **one** row
  (`account_id`), because the drop has not run yet.

- [ ] 10.3 Apply the one real migration. Only `20260917000002` should run:

```bash
make migrate-up
```

- [ ] 10.4 Re-run 10.2. Now `telemetry` reads `filas = 3`, `maximo = 20260917000002`, the
  other three are unchanged, and the second query returns **zero** rows — `account_id` is
  gone. Then start the app (`make up`) and confirm the dashboard still loads.

## 11. Run the tests **[owner]**

- [ ] 11.1 `make test` (or `make check` for the full gate). Report the result — until you
  do, this change is **awaiting your verification**, never "done"

## 12. Merge **[owner]**

Prod deploys by pulling `main`, so the branch has to land first.

- [ ] 12.1 Merge the branch into `main` and push it. Do **not** deploy yet — prod is stamped
  in group 13, before the deploy

## 13. Stamp prod — BEFORE the deploy **[owner]**

On the VPS. The stamp is safe to run any time before the deploy: the image currently
running reads `public.goose_db_version` and cannot see these new tables.

```bash
ssh <your VPS>
cd /home/magus/magus-tesla-api
git pull                     # brings the runbook SQL files with it

set -a; . ./.env; set +a
DC="docker compose --project-directory . -f deploy/docker/compose.yaml"
```

- [ ] 13.1 Stamp prod. `exec -T` feeds the file in over stdin, so the SQL runs inside the
  `db` container without a copy step:

```bash
$DC exec -T db psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 \
  < openspec/changes/platform-squash-migrations-to-module-baselines/runbook/stamp-baseline.sql
```

- [ ] 13.2 Check prod's ledgers — same five rows as dev at 10.2:

```bash
$DC exec -T db psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
  < openspec/changes/platform-squash-migrations-to-module-baselines/runbook/verify-ledgers.sql
```

- [ ] 13.3 Confirm `public (old)` still reads `55` / `20260915000001`. **That is the
  rollback path** — the previous image reads it, so it must stay untouched

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

  Expect four `applied 0 migration(s)` lines and one `applied 1 migration(s)` for
  `telemetry`. A `relation already exists` here means the stamp did not land — see below.

- [ ] 14.3 Confirm `web` and `poller` are up and healthy: `$DC ps`
- [ ] 14.4 Re-run 13.2. `telemetry` reads `maximo = 20260917000002`, and the second query
  returns zero rows
- [ ] 14.5 Open the site and confirm a page renders

**If 14.2 fails:** prod was not stamped, so a baseline tried to run against a populated
database. Nothing is damaged — it refused rather than writing. `web` and `poller` will not
have started, because both wait for the migration step to succeed. Either run group 13 and
redeploy, or redeploy the previous image: it reads `public.goose_db_version`, which is
untouched and at max version with nothing pending, so there is no database step to undo.

## 15. Close

- [ ] 15.1 Decide whether you want a test for `config.moduleForDir` — its error path is
  uncovered, and unit tests were excluded before that function existed
- [ ] 15.2 Archive the change, moving the folder under
  `openspec/changes/archive/platform/`, and sync `kkpa/context/`
