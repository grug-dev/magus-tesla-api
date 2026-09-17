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

- [ ] 2.1 Delete all 10 files in `internal/account/db/migrations/` and write `20260917000001_baseline.sql` with `accounts`, `settings`, `tesla_tokens`, `vehicles` — plus `CREATE SCHEMA account`, every index, constraint, FK, default and table/column `COMMENT` the dump carries
- [ ] 2.2 Same for `internal/telemetry/db/migrations/` — 17 files out, one baseline in, with `poll_attempts`, `poll_runs`, `supercharger_history`, `vehicle_snapshots`
- [ ] 2.3 Same for `internal/charging/db/migrations/` — 15 files out, one baseline in, with `manual_charge_entries`, `mirror_watermarks`, `monthly_effective_capacity`, `supercharger_sessions`
- [ ] 2.4 Same for `internal/analytics/db/migrations/` — 13 files out, one baseline in, with `charge_gaps`, `vehicle_metric_watermarks`, `vehicle_metrics`
- [ ] 2.5 Give all four baselines the identical `Down` block from design D3, raising an exception that names the module
- [ ] 2.6 Confirm each folder holds exactly one file, and that all four carry version `20260917000001`

## 3. The one deliberate schema change

- [ ] 3.1 Write `internal/telemetry/db/migrations/20260917000002_drop_vehicle_snapshots_account_id.sql` — `ALTER TABLE telemetry.vehicle_snapshots DROP COLUMN account_id;`, with a reversible `Down` that adds the column back as nullable `uuid`. No index or constraint references it, so nothing else changes

## 4. Go entry points

- [ ] 4.1 In `cmd/migrate/main.go`, pass `goose.WithTableName("<module>.goose_db_version")` per module and delete `goose.WithAllowOutofOrder(true)` (line 126) with its comment block (lines 102+)
- [ ] 4.2 In `internal/testdb/testdb.go`, the same two edits (`WithAllowOutofOrder` at line 204, comment at 196)
- [ ] 4.3 Check how each entry point maps a migrations directory to its module name — the table name must come from the same single source as the directory, never a second hardcoded list

## 5. Guards

- [ ] 5.1 Delete the `migration-guard` target and `KNOWN_DUPLICATE_MIGRATIONS` from the `Makefile` (lines 627–664), and remove `migration-guard` from `.PHONY` (line 93) and from `check` (line 999)
- [ ] 5.2 Add `migration-boundary-guard`: fail when a file under `internal/<module>/db/migrations/` names a Postgres schema other than `<module>`, ignoring SQL comments. Mirror `money-guard` / `tz-guard` / `boundary-guard` in shape and escape-hatch convention
- [ ] 5.3 Wire the new guard into `.PHONY` and `check`, in `migration-guard`'s old position
- [ ] 5.4 Run `make migration-boundary-guard` — it must pass, because the squash removed all four cross-module migrations
- [ ] 5.5 Run every other guard and confirm none regressed

## 6. Codegen

- [ ] 6.1 Run `sqlc generate`. `sqlc.yaml` needs no edit — `schema:` already points at each module's migrations folder
- [ ] 6.2 Review the `models.go` diff. The only expected change is the `AccountID` field leaving the telemetry snapshot model. Anything else means a baseline does not match the dump
- [ ] 6.3 Fix any Go code that referenced the dropped field, then `go build ./...`, `go vet ./...`, `gofmt -l`, `make lint`

## 7. Reverse-direction check (mandatory, CLAUDE.md)

Record a finding for each. "I did not think about it" is not a finding.

- [ ] 7.1 `make migrate-down` — it walks `MIGRATIONS_DIRS` in reverse and will now hit a raising baseline. Confirm the failure is clean and document the new behaviour
- [ ] 7.2 `make db-setup` / `db-reset` — confirm the role-and-ownership assumptions still hold when a module's schema is created by a baseline rather than by a chain
- [ ] 7.3 `MIGRATIONS_DIRS` — confirm the order no longer matters, and say so where the variable is defined
- [ ] 7.4 `deploy/docker/Dockerfile` — confirm the four `COPY` lines (54–57) still resolve; the folders do not move
- [ ] 7.5 Every `make *-guard` and `make check` phase Claude may run

## 8. Docs (same change, not a follow-up)

- [ ] 8.1 `README.md` — the migration section and "Making a change"
- [ ] 8.2 `ai/architecture.md` — one baseline per module, the per-module ledger, the no-cross-module-schema rule
- [ ] 8.3 `ai/go-conventions.md` — how to add a migration now, and that a migration may only touch its own schema
- [ ] 8.4 `cmd/README.md` — `cmd/migrate`'s behaviour
- [ ] 8.5 The four modules' `AGENTS.md`
- [ ] 8.6 `kkpa/context/architecture/schema-per-module.md` — its file map and any count this change invalidated
- [ ] 8.7 Grep `kkpa/context/` for `migration-guard`, `goose_db_version` and `KNOWN_DUPLICATE` and fix every stale hit. **Do not touch `openspec/changes/archive/`** — a hit there is not ours to fix
- [ ] 8.8 Run `make archive-guard`

## 9. Prove the baselines before any live database is touched **[owner]**

- [ ] 9.1 Build a scratch database and dump it:

```bash
createdb magus_baseline_check
DATABASE_URL="postgres://localhost:5432/magus_baseline_check?sslmode=disable" make migrate-up
pg_dump --schema-only --no-owner --no-privileges \
  -d "postgres://localhost:5432/magus_baseline_check?sslmode=disable" -f /tmp/dump-B.sql
diff /tmp/dump-A.sql /tmp/dump-B.sql
```

- [ ] 9.2 Confirm the diff is **exactly** these three things and nothing else:
  1. `account_id uuid,` gone from `telemetry.vehicle_snapshots`
  2. `public.goose_db_version` in A, not in B
  3. the four `<module>.goose_db_version` tables in B, not in A

  A fourth difference is drift. Stop and report it.

- [ ] 9.3 `dropdb magus_baseline_check`

## 10. Stamp the live databases **[owner]**

One transaction, so all four ledgers appear together or none does.

- [ ] 10.1 Run on **dev**:

```sql
BEGIN;

CREATE TABLE account.goose_db_version (
    id         integer PRIMARY KEY GENERATED BY DEFAULT AS IDENTITY,
    version_id bigint  NOT NULL,
    is_applied boolean NOT NULL,
    tstamp     timestamp NOT NULL DEFAULT now()
);
CREATE TABLE telemetry.goose_db_version (LIKE account.goose_db_version INCLUDING ALL);
CREATE TABLE charging.goose_db_version  (LIKE account.goose_db_version INCLUDING ALL);
CREATE TABLE analytics.goose_db_version (LIKE account.goose_db_version INCLUDING ALL);

INSERT INTO account.goose_db_version   (version_id, is_applied) VALUES (0, true), (20260917000001, true);
INSERT INTO telemetry.goose_db_version (version_id, is_applied) VALUES (0, true), (20260917000001, true);
INSERT INTO charging.goose_db_version  (version_id, is_applied) VALUES (0, true), (20260917000001, true);
INSERT INTO analytics.goose_db_version (version_id, is_applied) VALUES (0, true), (20260917000001, true);

COMMIT;
```

- [ ] 10.2 Verify dev — expect 4 rows, `filas = 2`, `maximo = 20260917000001`:

```sql
            SELECT 'account'   AS modulo, count(*) AS filas, max(version_id) AS maximo FROM account.goose_db_version
  UNION ALL SELECT 'telemetry',           count(*),           max(version_id)          FROM telemetry.goose_db_version
  UNION ALL SELECT 'charging',            count(*),           max(version_id)          FROM charging.goose_db_version
  UNION ALL SELECT 'analytics',           count(*),           max(version_id)          FROM analytics.goose_db_version
  ORDER BY 1;
```

- [ ] 10.3 `make migrate-up` on dev. Only `20260917000002` runs. Re-run 10.2: `telemetry` is now `filas = 3`, `maximo = 20260917000002`
- [ ] 10.4 Confirm `telemetry.vehicle_snapshots` no longer has `account_id`, and that the app still runs against dev

## 11. Prod **[owner]**

Order matters. The stamp lands **before** the deploy, never after (design, Risks).

- [ ] 11.1 Run the 10.1 transaction against prod. Safe to run before the deploy: the running image reads `public.goose_db_version` and cannot see these tables
- [ ] 11.2 Run the 10.2 query against prod and confirm the same four rows
- [ ] 11.3 Confirm `public.goose_db_version` is untouched — 55 rows, max `20260915000001`. This is the rollback path
- [ ] 11.4 Deploy. `migrate` must exit 0, then `web` and `poller` must start and pass their healthchecks
- [ ] 11.5 Confirm prod's `telemetry.goose_db_version` reached `20260917000002`, and that the site serves a page

## 12. Close

- [ ] 12.1 Owner runs the test suite and reports the result. Until then this change is **awaiting the owner's verification**, never "done"
- [ ] 12.2 Archive the change, moving the folder under `openspec/changes/archive/platform/`, and sync `kkpa/context/`
