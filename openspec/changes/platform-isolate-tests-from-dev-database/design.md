# Design — platform-isolate-tests-from-dev-database

## Context

`internal/testdb` provisions a Postgres database for every DB-backed test in
the repo. Today it decides where tests run by reading `os.Getenv("DATABASE_URL")`
at `internal/testdb/testdb.go:138`. That is the same variable
`internal/config` reads for the running application's own database
connection. One variable does two unrelated jobs: "where does the app
connect" and "where do tests run." That double duty is why tests are
dangerous by default — any shell, `.env` load, or IDE run configuration that
sets `DATABASE_URL` for the app also silently redirects every test onto the
same real database.

That is exactly what happened. `internal/analytics/db_integration_test.go`
inserts fixture rows into `telemetry.vehicle_snapshots` and never deletes
them. Under `make test` this leak is contained inside a disposable
testcontainer and vanishes when the container is torn down. Under
`make test-with-db` — or any bare `go test ./...` / IDE run with
`DATABASE_URL` pointed at the dev database — the leak lands permanently in
`telemetry.vehicle_snapshots`. That is where the 78 orphan rows came from.

This design fixes both problems: which variable tests read (D1), and the
actual leak inside the analytics test (D4). It also brings every doc and
comment that describes the old behavior back in sync (D5), and removes the
78 rows that already exist (D6).

## Goals / Non-Goals

**Goals:**
- Tests never touch a real database unless a developer deliberately points
  them at one, using a variable that cannot be confused with the
  application's own config.
- The `analytics` test that leaked stops leaking, independent of which
  database it runs against.
- Every doc and comment describing the old `DATABASE_URL`-gated behavior is
  corrected in this same change.
- The 78 existing orphan rows are removed from the dev database, with a
  before/after count the owner can verify.

**Non-Goals:**
- No schema change of any kind — no table, column, index, constraint, view,
  or migration. This is a test-infrastructure and cleanup change only.
- No fix for the `analytics` → `telemetry` boundary crossing that made the
  leak possible in the first place (D4) — that is real, but it is a separate,
  larger decision (does `telemetry` need a public writer? does `analytics`
  need a different fixture strategy?) and belongs in its own ticket.
- No CI setup. There is none today, and none is added here.
- No unit tests. The owner's default for this change is no new tests — it
  fixes one existing test's teardown and how tests pick a database.

## Decisions

### D1 — `internal/testdb` reads `TEST_DATABASE_URL`, not `DATABASE_URL`

**Decision:** change the one line at `internal/testdb/testdb.go:138` from
`os.Getenv("DATABASE_URL")` to `os.Getenv("TEST_DATABASE_URL")`. The
following log message at line 141 (`"testdb: DATABASE_URL not usable..."`)
is updated to name the new variable too, since it reports on the same read.

**Rationale:** today one variable serves two purposes — pointing the running
application at its database, and telling tests where to run — so tests are
dangerous by default: whatever points the app at a database also points
tests at it. Splitting the variable flips the default. `DATABASE_URL` is now
ignored by every test, unconditionally. Pointing tests at a real database
becomes a deliberate, separate choice: set `TEST_DATABASE_URL`. This makes
`go test ./...` from any shell, and an IDE's own "run test" button, exactly
as safe as `make test` — the safety moves out of the Makefile and into the
code itself, so it can no longer be bypassed by skipping the Makefile.

**Rejected alternative:** keep reading `DATABASE_URL` but require an opt-in
flag, e.g. `MAGUS_TEST_USE_REAL_DB=1`, before honoring it. Rejected because
it still needs the dangerous variable wired into `testdb` at all — a
developer must still get the flag right, and the moment
`MAGUS_TEST_USE_REAL_DB=1` is set (or forgotten as leftover shell state), the
old danger is back in full: `DATABASE_URL` from `.env` silently becomes the
test target again. Expressing "where do tests run" needs exactly one
variable, not two variables in the right combination.

**Affected file:** `internal/testdb/testdb.go` — the functional line, the log
message, and this file's own package/function doc comments (six lines: 13,
16, 49, 59, 104, 133) that currently describe the `DATABASE_URL`-then-container
policy by that name.

### D2 — `internal/config/config.go` is NOT touched

`internal/config` reads `DATABASE_URL` as the running application's own
configuration (`cmd/web`, `cmd/poller`, `cmd/migrate` all depend on this).
That is correct today and stays correct — it is a different concern from
"where do tests run," and D1 does not change it. This is stated explicitly
so a reviewer does not read the sweep in D5 and wonder why `internal/config`
was skipped: it was skipped on purpose, not missed.

### D3 — The Makefile targets change shape

Current text (`Makefile` lines ~503–507):

```makefile
test: ## Run all tests against disposable testcontainer Postgres (never the real magus DB; ignores .env DATABASE_URL)
	env -u DATABASE_URL go test ./...

test-with-db: ## Run all tests against the configured DATABASE_URL (opt-in; CI with a managed Postgres)
	go test ./...
```

New text:

```makefile
test: ## Run all tests against disposable testcontainer Postgres (TEST_DATABASE_URL is never read from .env here)
	go test ./...

test-with-db: ## Run all tests against TEST_DATABASE_URL (opt-in; point it at a throwaway or CI Postgres, never the real one)
	TEST_DATABASE_URL="$(DATABASE_URL)" go test ./...
```

`test` drops `env -u DATABASE_URL` — it is pointless once `testdb` no longer
reads that variable at all, so `test` becomes a plain `go test ./...`.
`test-with-db` now explicitly forwards the developer's own `$(DATABASE_URL)`
into `TEST_DATABASE_URL` for the test run, since that is the intended use —
pointing tests at a real, managed Postgres on purpose. The `##` help text on
both targets is rewritten to describe the new behavior; the old text
(mentioning `.env DATABASE_URL` and "the configured DATABASE_URL") would
otherwise become a lie about what the target does.

### D4 — `cleanupVehicleMetrics` also deletes the snapshots it inserted

**Decision:** add
`DELETE FROM telemetry.vehicle_snapshots WHERE account_id = $1 AND tesla_id = $2`
to the existing `t.Cleanup` block in `cleanupVehicleMetrics`
(`internal/analytics/db_integration_test.go`), alongside the two `DELETE`s it
already runs against `analytics.vehicle_metrics` and
`analytics.vehicle_metric_watermarks`.

**Rationale:** this is a real defect, independent of D1. The test leaks rows
inside a throwaway testcontainer too — it just doesn't matter there, because
the whole container is discarded. Under `test-with-db` (or before D1
existed, under a bare `go test ./...`), the same leak lands permanently.
D1 stops the leak from ever reaching a real database again; D4 fixes the bug
itself, so the test is correct regardless of which database it runs against.

**A deeper cause, explicitly out of scope:** an `analytics` test writing raw
SQL directly into `telemetry`'s own table is a module-boundary crossing.
`internal/analytics/AGENTS.md` already authorizes this as a deliberate,
test-only concession (`telemetry` exposes no public writer for a single
snapshot), but it is also the reason this specific bug was easy to introduce
and easy to miss: the test owns a table it doesn't own in production, so its
cleanup obligations aren't visible anywhere near `telemetry`'s own test
helpers. Fixing that properly — a public single-snapshot writer on
`telemetry`, or a different fixture strategy for `analytics` — is a separate,
larger decision and belongs in its own ticket.

### D5 — Sweep the stale doc comments and docs

**Decision:** every comment or doc line that describes a test as
"`DATABASE_URL`-gated," tells the reader to "set `DATABASE_URL`" to run a
test, or otherwise documents the old test-provisioning behavior by that
variable's name, is updated to name `TEST_DATABASE_URL` instead. A comment
or doc line describing the *application* reading `DATABASE_URL` for its own
database connection (`cmd/web`, `cmd/poller`, `cmd/migrate`,
`internal/config`) is explicitly excluded — that reading is unchanged (D2).

**Scope found by a repo-wide search**, split by where the sweep touches a
file already being edited for another decision, versus everywhere else:

- **Fixed alongside D1** (same file, same task): `internal/testdb/testdb.go`
  — 6 comment lines (13, 16, 49, 59, 104, 133).
- **Fixed alongside D4** (same file, same task):
  `internal/analytics/db_integration_test.go` — 3 lines (the file's own
  header comment at lines 1 and 5, and the `t.Skip` message at line 78).
- **A dedicated code-comment sweep task** — 31 lines across 25 other `.go`
  files (Go doc comments and `t.Skip` messages), listed in tasks.md.
- **A dedicated docs sweep task** — 15 lines across 6 Markdown files
  (`README.md`, `ai/go-conventions.md`, and the `AGENTS.md` files of
  `analytics`, `account`, `charging`, `telemetry`), listed in tasks.md. This
  is required by the project's own rule that a changed build/test workflow
  is documented in the same change, not as a follow-up.

Total: about 55 lines across 31 files change wording only — no functional
code inside these files changes, except the two lines already covered by D1
(the functional line and its log message) and the one `DELETE` added by D4.

**Not in scope:** `internal/config/AGENTS.md` and `internal/config/config_test.go`
name `DATABASE_URL` too, but both describe the application's own config
loading (`LoadDatabase`, `LoadMigration`), never test provisioning — per D2,
these stay exactly as they are.

### D6 — The 78 existing rows are removed by a one-off SQL script, not a migration

**Decision:** add a new file, `scripts/2026-09-11-cleanup-orphan-vehicle-snapshots.sql`.
No `scripts/` folder exists in this repo today — this change creates it, as
the natural home for a hand-run, one-off maintenance script that is not part
of the schema.

**Rationale:** these 78 rows exist only on the dev database. A goose
migration runs on every environment forever, including production, where
there is nothing to delete — a migration file that does nothing everywhere
except one developer's laptop is the wrong tool. A hand-run script lets the
owner choose when it runs, after taking a backup, and never runs anywhere
else by accident.

**The script:**

```sql
-- One-off cleanup for MAG-72: removes orphan telemetry.vehicle_snapshots
-- rows left behind by a test that leaked into the dev database before this
-- change fixed it (see platform-isolate-tests-from-dev-database).
--
-- BACK UP THE DATABASE BEFORE RUNNING THIS. It deletes rows and cannot be
-- undone without a backup.

-- Count before deleting.
SELECT count(*) AS orphan_count_before
FROM telemetry.vehicle_snapshots s
WHERE NOT EXISTS (
    SELECT 1 FROM account.vehicles v WHERE v.tesla_id = s.tesla_id
);

DELETE FROM telemetry.vehicle_snapshots s
WHERE NOT EXISTS (
    SELECT 1 FROM account.vehicles v WHERE v.tesla_id = s.tesla_id
);

-- Count after deleting — expect 0.
SELECT count(*) AS orphan_count_after
FROM telemetry.vehicle_snapshots s
WHERE NOT EXISTS (
    SELECT 1 FROM account.vehicles v WHERE v.tesla_id = s.tesla_id
);
```

**Why `NOT EXISTS`, not `NOT IN`.** Both return the same answer today. But
`NOT IN` is fragile: if `account.vehicles.tesla_id` ever became nullable, a
single `NULL` in the subquery's result set would make `NOT IN` match
**nothing** — a silent no-op, not an error. `NOT EXISTS` has no such trap.

**Why no hardcoded `tesla_id` range.** The ticket claimed a narrower range
than reality. The real fixture range spans `900001`–`991032` plus the
separate value `987654` — wider than the ticket's own text. Filtering by
"is this row real" (the `NOT EXISTS` join) is correct regardless of what
range the test fixtures happened to use; filtering by a hardcoded range
would have missed `987654` today and any future fixture value tomorrow.

### D7 — give `magus_test` a documented job as the opt-in `TEST_DATABASE_URL` target

**Context.** The owner found an existing local database, `magus_test`, that
this change did not create. It carries all four module schemas, is 2
migrations behind the dev database (47 applied, latest `20260909000002`,
versus `magus`'s `20260911000001`), holds zero rows in
`telemetry.vehicle_snapshots`, and is owned schema-by-schema by the
developer's own Postgres role (`cristianpena`), not by `magusadmindb` — the
app role named in `DATABASE_URL`. Nothing in the repo names it: not the
Makefile, not `.env`, not any Go file, not any doc. It is a leftover scratch
database that drifted out of date. The owner chose to keep it and give it a
real, documented job rather than drop it.

**Decision, in three parts:**

**1. The default does not change.** `TEST_DATABASE_URL` unset still means a
disposable testcontainer (D1). A hand-maintained database must never be the
default target, because `magus_test` itself is the proof of the failure
mode: it is already 2 migrations behind, silently. A testcontainer cannot
go stale — it applies every migration fresh on every single run, so it can
never drift out of sync with the code that tests it. `magus_test` can drift
the moment someone runs a migration against `magus` and forgets to also run
it against `magus_test`. That is exactly the risk a default must not carry.

**2. A new `make` target makes `magus_test` usable on demand.** Name:
**`db-setup-test`** — mirroring `db-setup`'s naming (`db-<verb>`) and adding
the `-test` qualifier the same way `test-with-db` already qualifies `test`.
Following `db-setup`'s own shape (`Makefile` lines ~134–177): a new
`TEST_DATABASE_URL` variable (default `postgres://localhost:5432/magus_test?sslmode=disable`,
overridable exactly like `DATABASE_URL`), with `TEST_DB_NAME` and
`TEST_ADMIN_DATABASE_URL` derived from it the same way `DB_NAME` and
`ADMIN_DATABASE_URL` are derived from `DATABASE_URL` today (strip
`user:pass`, point at the maintenance `postgres` database). Shape:

```makefile
TEST_DATABASE_URL ?= postgres://localhost:5432/magus_test?sslmode=disable
TEST_DB_NAME := $(shell echo "$(TEST_DATABASE_URL)" | sed -E 's|.*/([^/?]+).*|\1|')
TEST_ADMIN_DATABASE_URL := $(shell echo "$(TEST_DATABASE_URL)" | sed -E 's|^(postgres(ql)?://)([^/@]*@)?([^/?]+)/[^/?]+|\1\4/postgres|')

db-setup-test: check-goose ## Create/re-own/migrate magus_test to latest, for the opt-in TEST_DATABASE_URL path (safe to re-run; no Docker needed)
	@set -e; \
	ADMIN="$(TEST_ADMIN_DATABASE_URL)"; DB="$(TEST_DB_NAME)"; ROLE="$(APP_ROLE)"; \
	if psql "$$ADMIN" -tAc "SELECT 1 FROM pg_database WHERE datname='$$DB'" | grep -q 1; then \
		echo "Database '$$DB' already exists."; \
	else \
		echo "Creating database $$DB owned by $$ROLE..."; \
		psql "$$ADMIN" -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"$$DB\" OWNER \"$$ROLE\""; \
	fi; \
	echo "Re-owning module schemas to '$$ROLE' (a schema that doesn't exist yet is skipped)..."; \
	for schema in account analytics charging telemetry; do \
		if psql "$(TEST_DATABASE_URL)" -tAc "SELECT 1 FROM pg_namespace WHERE nspname='$$schema'" | grep -q 1; then \
			psql "$$ADMIN" -d "$$DB" -v ON_ERROR_STOP=1 -c "ALTER SCHEMA \"$$schema\" OWNER TO \"$$ROLE\""; \
		fi; \
	done; \
	echo "Migrating '$$DB' to latest..."; \
	for dir in $(MIGRATIONS_DIRS); do \
		echo "goose up: $$dir"; \
		$(GOOSE) -dir $$dir postgres "$(TEST_DATABASE_URL)" up -allow-missing; \
	done; \
	echo; echo "✓ magus_test ready. Point tests at it with:"; \
	echo "    TEST_DATABASE_URL=$(TEST_DATABASE_URL) make test"
```

Two things this target does that `db-setup` does not need to: it always
re-owns the four schemas (`db-setup` only warns about a wrong owner and
tells the developer to run `db-reset`, since a fresh app database is never
owned by the wrong role in the first place), and it always runs the full
`goose up` loop even when the database already exists, so an
already-provisioned `magus_test` is brought current every time. It does
**not** create the `APP_ROLE` role itself — `db-setup` already owns that
job, and a developer using `db-setup-test` has necessarily run `db-setup`
already (the app role must exist to be granted schema ownership).

**Safe to re-run.** `CREATE DATABASE` is skipped when the database already
exists; `ALTER SCHEMA ... OWNER TO` is a no-op when the schema is already
owned by `$(APP_ROLE)`; `goose up -allow-missing` is a no-op for every
migration already recorded in `goose_db_version`. Running `db-setup-test`
against an already-current, already-correctly-owned `magus_test` changes
nothing and prints no error.

**3. Document the opt-in path where a developer will look.** Add it to the
root `README.md`'s test instructions, right beside the existing
`make test` / `make test-with-db` notes, and to `ai/go-conventions.md`'s
"Provisioning the test database" table — the same two doc locations D5's
sweep already touches for the `TEST_DATABASE_URL` rename, so this is one
more edit in the same place, not a new doc surface. Show the concrete form:

```bash
make db-setup-test                                              # one-time / re-run anytime
TEST_DATABASE_URL=postgres://localhost:5432/magus_test?sslmode=disable make test
```

State plainly when to choose it over the container: it starts faster (no
image pull, no container boot) and needs no Docker daemon running at all —
useful on a machine where Docker is slow to start or not installed. State
the trade-off just as plainly: this is a second database that can go stale
between runs, exactly like `magus_test` already had. The mitigation is
`db-setup-test` itself — re-run it before a test session to bring the
schema current — and the fact that nothing touches `magus_test` unless a
developer sets `TEST_DATABASE_URL` on purpose.

## Verification Contract

Orphan-count query (same shape the script uses):

```sql
SELECT count(*) FROM telemetry.vehicle_snapshots s
WHERE NOT EXISTS (SELECT 1 FROM account.vehicles v WHERE v.tesla_id = s.tesla_id);
```

| Check | Before script | After script |
|---|---|---|
| Orphan row count | **78** | **0** |
| Total `vehicle_snapshots` rows | **190** | **112** |
| Real (non-orphan) rows | 112 | 112 — untouched |

Additional checks, after D1 and D4 are both applied:

- Running the `analytics` integration tests against a real database adds
  **zero** new orphan rows to `telemetry.vehicle_snapshots`.
- With `DATABASE_URL` set to the real database and `TEST_DATABASE_URL`
  unset, `go test ./...` provisions a disposable testcontainer and does
  **not** connect to the real database at all.
- `telemetry.poll_attempts` and `analytics.vehicle_metrics` have, and keep,
  zero orphan rows — this change's cleanup is scoped to
  `telemetry.vehicle_snapshots` only, since that is the only table the leak
  touched.
- After D7: `make db-setup-test` run twice in a row is a no-op the second
  time (safe-to-re-run, above); run once against today's `magus_test`, it
  brings `goose_db_version` to `20260911000001` (matching `magus`) and makes
  every one of the four schemas owned by `$(APP_ROLE)`.

## Database objects

None. No table, column, index, constraint, view, or migration is added,
changed, or removed by this change.

## Rollout

1. Merge D1 (`testdb`), D3 (`Makefile`), and D4 (`analytics` test fix) —
   these stop the leak from recurring.
2. Merge D5 (the comment and doc sweep) — same change, so no doc is ever
   left describing the old variable.
3. The owner backs up the dev database, runs the D6 script, and confirms the
   Verification Contract counts (owner-run task in tasks.md).
4. Any developer who currently sets `DATABASE_URL` to run tests against a
   real database switches to `TEST_DATABASE_URL` — called out in the
   proposal's Impact section so nobody is surprised by tests silently using
   a testcontainer after this merges.
5. D7's `db-setup-test` target and its doc notes land in the same change.
   The default stays the testcontainer; `magus_test` is documented as the
   opt-in path a developer reaches for only by setting `TEST_DATABASE_URL`
   themselves.
