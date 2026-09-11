# Tasks — platform-isolate-tests-from-dev-database

> **Dependencies / parallelism.**
> - **T1** (`internal/testdb` + `Makefile`) — no dependency. Touches
>   `internal/testdb/testdb.go` and `Makefile` only. MAY run in parallel with
>   T2, T3, T4, T5.
> - **T2** (`analytics` cleanup fix) — no dependency. Touches only
>   `internal/analytics/db_integration_test.go`. MAY run in parallel with T1,
>   T3, T4, T5.
> - **T3** (code-comment sweep) — no dependency. Touches 25 `.go` files,
>   **disjoint from T1 and T2** (excludes `internal/testdb/testdb.go` and
>   `internal/analytics/db_integration_test.go` on purpose — those two files'
>   own stale comments are fixed inside T1 and T2 respectively, to avoid two
>   tasks editing the same file). MAY run in parallel with T1, T2, T4, T5.
> - **T4** (docs sweep) — no dependency. Touches 6 Markdown files, disjoint
>   from every other task. MAY run in parallel with T1, T2, T3, T5.
> - **T5** (SQL script) — no dependency. Adds one new file. MAY run in
>   parallel with everything.
> - **T6** (cheap signals) — depends on **T1, T2, T3, T4** (everything that
>   touches `.go` files or `gofmt`-checked files). Does not depend on T5 (SQL,
>   not part of `go build`/`go vet`/`gofmt`). Final wave for the assistant's
>   own work.
> - **T7** (owner-run: backup + script + verify) — depends on **T1, T2, T5**.
>   Needs the script (T5) and the fix that stops new leaks (T1, T2) landed
>   first, so the cleanup isn't immediately undone by the next test run.
> - **T8** (D7 — `db-setup-test` Makefile target) — depends on **T1**. Both
>   edit `Makefile`. T8 adds a new target in a different section from T1's
>   `test`/`test-with-db` edit, but it is still the same file — two workers
>   touching one file at once is the failure mode this project's own tasks.md
>   convention sequences against (see the archived
>   `platform-harden-docker-deploy` change's T3/T4 note on `compose.yaml`), so
>   T8 waits for T1 to land rather than risk a merge conflict for no reason.
> - **T9** (D7 — document the opt-in `TEST_DATABASE_URL` path) — depends on
>   **T4, T8**. `README.md` and `ai/go-conventions.md` are exactly the two
>   files T4 already sweeps for the `TEST_DATABASE_URL` rename, so T9 waits
>   for T4's pass on those files. It also depends on T8, since it documents
>   `db-setup-test`'s exact final name and behavior.

## T1. `internal/testdb` reads `TEST_DATABASE_URL`; `Makefile` targets updated — no dependency

- [x] T1.1 In `internal/testdb/testdb.go`, change line 138 from
      `dsn := os.Getenv("DATABASE_URL")` to
      `dsn := os.Getenv("TEST_DATABASE_URL")`. Update the log message at line
      141 (`"testdb: DATABASE_URL not usable (%v); provisioning testcontainer"`)
      to name `TEST_DATABASE_URL` instead.
      Acceptance: `grep -n 'DATABASE_URL' internal/testdb/testdb.go` matches
      nothing; `grep -n 'TEST_DATABASE_URL' internal/testdb/testdb.go` matches
      the renamed line and log message.
- [x] T1.2 In the same file, update the 6 doc-comment lines that describe the
      old `DATABASE_URL`-then-container policy by that name (design.md D1):
      lines 13, 16, 49, 59, 104, 133. Name `TEST_DATABASE_URL` in each; the
      described *behavior* (real DB if reachable, else a testcontainer) does
      not change, only which variable triggers it.
      Acceptance: every comment in the file naming the provisioning-policy
      variable says `TEST_DATABASE_URL`; `internal/config`'s own
      `DATABASE_URL` reading is untouched (design.md D2) — this task does not
      touch `internal/config` at all.
- [x] T1.3 In `Makefile`, replace the `test` and `test-with-db` targets
      (currently around lines 503–507) with the text in design.md D3: `test`
      drops `env -u DATABASE_URL` and becomes plain `go test ./...`;
      `test-with-db` becomes `TEST_DATABASE_URL="$(DATABASE_URL)" go test ./...`.
      Rewrite both targets' trailing `##` help text to match the new
      behavior — the current text ("ignores .env DATABASE_URL", "the
      configured DATABASE_URL") would otherwise describe the old behavior.
      Acceptance: `grep -n 'test:\|test-with-db:' Makefile` shows the new
      target bodies; `make -n test` and `make -n test-with-db` print the
      expected commands without executing them.

## T2. Fix the `analytics` test's cleanup leak — no dependency

- [x] T2.1 In `internal/analytics/db_integration_test.go`, add
      `DELETE FROM telemetry.vehicle_snapshots WHERE account_id = $1 AND tesla_id = $2`
      to the existing `t.Cleanup` block inside `cleanupVehicleMetrics`
      (design.md D4), alongside the two `DELETE`s it already runs. Use the
      same `pool.Exec` + ignored-error pattern the two existing statements
      use.
      Acceptance: `cleanupVehicleMetrics` deletes rows from all three tables
      it touches (`analytics.vehicle_metrics`, `analytics.vehicle_metric_watermarks`,
      `telemetry.vehicle_snapshots`) scoped to the same `(accountID, teslaID)`.
- [x] T2.2 Fix this file's own 3 stale lines naming `DATABASE_URL` for test
      gating: the header comment at lines 1 and 5, and the `t.Skip` message
      at line 78 (`"no test Postgres: set DATABASE_URL or start Docker..."`).
      Name `TEST_DATABASE_URL` in all three.
      Acceptance: `grep -n 'DATABASE_URL' internal/analytics/db_integration_test.go`
      matches nothing; `grep -n 'TEST_DATABASE_URL'` matches the 3 updated
      lines.
- [x] T2.3 Note in this file's header comment (or leave a short line near
      `cleanupVehicleMetrics`) that an `analytics` test writing raw SQL into
      `telemetry`'s own table crossed a module boundary, and that this is
      the deeper reason the cleanup was forgotten — fixing that boundary
      crossing itself is a separate ticket (design.md D4), not this one.
      Do not cite this change's own name or decision IDs in the comment
      (project convention) — state the reason plainly instead.

## T3. Sweep stale `DATABASE_URL`-gated comments in Go source — no dependency, disjoint from T1/T2

Update every listed line to name `TEST_DATABASE_URL` instead of
`DATABASE_URL` for describing test provisioning/gating. 31 lines across 25
files (design.md D5). Do **not** touch `internal/testdb/testdb.go` (T1) or
`internal/analytics/db_integration_test.go` (T2) — already covered. Do **not**
touch `cmd/poller/main.go`, `cmd/web/main.go`, `cmd/migrate/main.go`,
`internal/config/config.go`, or `internal/config/config_test.go` — those
describe the application's own `DATABASE_URL` config and stay unchanged
(design.md D2).

- [x] T3.1 `internal/telemetry/service.go:28`
- [x] T3.2 `internal/telemetry/db_poll_run_integration_test.go:17`
- [x] T3.3 `internal/telemetry/testdb_test.go:6`
- [x] T3.4 `internal/telemetry/telemetry.go:668`
- [x] T3.5 `internal/telemetry/db_read_integration_test.go:14`
- [x] T3.6 `internal/telemetry/query_log_test.go:21`
- [x] T3.7 `internal/telemetry/db_change_detection_integration_test.go:9`
- [x] T3.8 `internal/telemetry/service_test.go:20`
- [x] T3.9 `internal/telemetry/db_supercharger_between_integration_test.go:13`
- [x] T3.10 `internal/telemetry/db_supercharger_account_updated_since_integration_test.go:15`
- [x] T3.11 `internal/telemetry/db_integration_test.go:18` and `:27` (the
      `t.Skip` message) — 2 lines
- [x] T3.12 `internal/telemetry/run_writer.go:15`
- [x] T3.13 `internal/telemetry/db_sourcea_integration_test.go:17` and `:20`
      — 2 lines
- [x] T3.14 `internal/telemetry/db_supercharger_integration_test.go:13` and
      `:15` — 2 lines
- [x] T3.15 `internal/account/testdb_test.go:5`
- [x] T3.16 `internal/account/service_integration_test.go:449`, `:586`,
      `:702` — 3 lines
- [x] T3.17 `internal/charging/testdb_test.go:6`
- [x] T3.18 `internal/charging/service.go:52`
- [x] T3.19 `internal/charging/db_integration_test.go:3`
- [x] T3.20 `internal/charging/session_verifier.go:28`
- [x] T3.21 `internal/charging/session_reader.go:18`
- [x] T3.22 `internal/charging/monthly_capacity.go:94`
- [x] T3.23 `internal/analytics/testdb_test.go:9`
- [x] T3.24 `internal/analytics/db_watermark_migration_integration_test.go:104`
      and `:125` — 2 lines
- [x] T3.25 `internal/analytics/db_gap_writer_integration_test.go:14`

Acceptance (whole task): `grep -rn "DATABASE_URL" --include="*.go" internal/`
shows only lines inside `internal/config/` (application config, untouched)
— every other `.go` match today is gone or renamed to `TEST_DATABASE_URL`.

## T4. Sweep stale `DATABASE_URL` references in project docs — no dependency, disjoint from T1/T2/T3

Required by the project rule that a changed build/test workflow is
documented in the same change (`CLAUDE.md` "Workflow & architectural
decisions are documented with their steps"). 15 lines across 6 files.

- [x] T4.1 `README.md` — 5 lines: the `go test ./...` comment block (around
      line 74), the two `>` notes right after it about `make test` /
      `make test-with-db` (lines 78, 80), the `internal/testdb` row in the
      Project Structure tree (line 130), and the `internal/testdb` row in the
      Architecture table (line 222). Rewrite each to describe
      `TEST_DATABASE_URL`-gated provisioning. Do not touch the earlier
      `DATABASE_URL + SESSION_SECRET` / `DATABASE_URL` lines describing how to
      run `cmd/web` / `cmd/poller` (lines 31, 34) or the `make db-setup` line
      (line 172) — those describe the application's own config.
- [x] T4.2 `ai/go-conventions.md` — 3 lines: "DB tests are `DATABASE_URL`-gated"
      (line 108), "`DATABASE_URL`-gated integration tests — write them last"
      (line 145), and the `Provisioning the test database` table's own
      wording (line 160, "a reachable `DATABASE_URL` if there is one"). Do not
      touch line 101 ("`DATABASE_URL` is the single source of truth for the
      DSN") or line 295 (the `cmd/web` run comment) — application config.
- [x] T4.3 `internal/analytics/AGENTS.md` — 2 lines in the Testing section
      (around lines 201–202): "`DATABASE_URL`-gated DB-integration tests" and
      "must pass with `DATABASE_URL` unset."
- [x] T4.4 `internal/account/AGENTS.md` — 1 line (around line 95): "when
      `DATABASE_URL` is set AND reachable, that managed Postgres is used."
- [x] T4.5 `internal/charging/AGENTS.md` — 1 line (around line 477):
      "`testdb_test.go` provisions it: `DATABASE_URL` when set."
- [x] T4.6 `internal/telemetry/AGENTS.md` — 3 lines (around lines 200, 202,
      204): the `env -u DATABASE_URL go test ...` example command (which
      after D1 no longer needs `env -u` at all — `TEST_DATABASE_URL` is
      simply left unset), "`make test-with-db` runs against whatever
      `DATABASE_URL` points at," and "`DATABASE_URL` when set and reachable"
      in the store-tests bullet.

Acceptance (whole task): `grep -rln "DATABASE_URL" --include="*.md"` still
shows `README.md`, `ai/go-conventions.md`, and the four `AGENTS.md` files
(they keep their *application*-config mentions), but none of the 15 lines
above still names `DATABASE_URL` for test provisioning.

## T5. Add the one-off cleanup SQL script — no dependency

- [x] T5.1 Create `scripts/2026-09-11-cleanup-orphan-vehicle-snapshots.sql`
      with the exact content in design.md D6: header comment naming the
      change and stating "back up the database first," a `SELECT count(*)`
      before the delete, the `DELETE ... WHERE NOT EXISTS (...)` statement,
      and a `SELECT count(*)` after. Use `NOT EXISTS` against
      `account.vehicles`, never `NOT IN` and never a hardcoded `tesla_id`
      range (design.md D6 rationale).
      Acceptance: the file exists at that path; running it against a copy of
      the dev database prints 78 before, deletes exactly 78 rows, and prints
      0 after (owner verifies this in T7, not this task).

## T6. Cheap signals — depends on T1, T2, T3, T4

- [ ] T6.1 `go build ./...` — confirms nothing broke compiling.
- [ ] T6.2 `go vet ./...` — compiles every `_test.go` file too, catching any
      leftover reference or signature drift from T1–T4.
- [ ] T6.3 `gofmt -l internal/testdb internal/analytics internal/telemetry internal/account internal/charging` —
      confirms no formatting drift in the touched files.
      Acceptance: all three commands exit clean with no output beyond normal
      build/vet noise.

**Suite commands the assistant does NOT run** (owner's, per
Test-Execution-Policy): `go test ./...`, `make test`, `make test-with-db`,
`make check`.

## T7. OWNER-RUN — back up, run the script, verify counts — depends on T1, T2, T5

- [ ] T7.1 **(Owner)** Back up the dev database.
- [ ] T7.2 **(Owner)** Run
      `scripts/2026-09-11-cleanup-orphan-vehicle-snapshots.sql` against the
      dev database.
- [ ] T7.3 **(Owner)** Confirm the Verification Contract in design.md: orphan
      count 78 → 0, total `vehicle_snapshots` rows 190 → 112, the 112 real
      rows untouched.
- [ ] T7.4 **(Owner)** Run the full suite (`make test`, then optionally
      `make test-with-db` pointed at a throwaway Postgres — never the dev
      database) and report the results back. This is the point where T1–T4
      move from `awaiting-user-verification` to actually verified.

## T8. `db-setup-test` Makefile target (D7) — depends on T1

- [ ] T8.1 In `Makefile`, add the `TEST_DATABASE_URL`, `TEST_DB_NAME`, and
      `TEST_ADMIN_DATABASE_URL` variables (design.md D7), derived from
      `TEST_DATABASE_URL` the same way `DB_NAME` / `ADMIN_DATABASE_URL` are
      derived from `DATABASE_URL`. Default:
      `TEST_DATABASE_URL ?= postgres://localhost:5432/magus_test?sslmode=disable`.
      Acceptance: `make -n db-setup-test DATABASE_URL=...` (see T8.2) prints
      the derived admin URL and DB name correctly for both the default and an
      overridden `TEST_DATABASE_URL`.
- [ ] T8.2 Add the `db-setup-test` target itself (design.md D7 shows the
      shape): create `magus_test` if missing (owned by `$(APP_ROLE)`);
      `ALTER SCHEMA ... OWNER TO $(APP_ROLE)` for each of `account`,
      `analytics`, `charging`, `telemetry` that already exists; then run the
      same `goose up -allow-missing` loop over `$(MIGRATIONS_DIRS)` that
      `db-setup` runs, against `TEST_DATABASE_URL`. Add it to the `.PHONY`
      list alongside `db-setup`/`db-reset`.
      Acceptance: run twice in a row against the owner's actual `magus_test`
      — the first run re-owns all 4 schemas to `magusadmindb` and applies the
      2 pending migrations (`goose_db_version` reaches `20260911000001`); the
      second run prints "already exists" / no schema changes / no pending
      migrations, and exits 0 (design.md D7 "safe to re-run").
- [ ] T8.3 Rewrite `db-setup-test`'s trailing `##` help text to describe what
      it does and when to use it (design.md D7 part 3: faster than the
      container, no Docker daemon needed), matching the terseness of
      `db-setup`'s own help text.

## T9. Document the opt-in `TEST_DATABASE_URL` path (D7) — depends on T4, T8

- [ ] T9.1 In `README.md`, next to the `make test` / `make test-with-db`
      notes T4.1 already rewrites, add the opt-in form:
      `TEST_DATABASE_URL=postgres://localhost:5432/magus_test?sslmode=disable make test`,
      preceded by `make db-setup-test` to bring it current. State plainly why
      a developer would choose it (starts faster, no Docker daemon needed)
      and the trade-off (a second database that can go stale — mitigated by
      re-running `db-setup-test`, and by nothing touching it unless
      `TEST_DATABASE_URL` is set on purpose).
- [ ] T9.2 In `ai/go-conventions.md`'s "Provisioning the test database" table
      (the same section T4.2 already touches), add a row or note for the
      `TEST_DATABASE_URL=<magus_test DSN>` opt-in path and the
      `make db-setup-test` command that keeps it current.
      Acceptance (T9 as a whole): a developer reading either doc alone can
      go from "Docker is slow on my machine" to a working
      `TEST_DATABASE_URL=... make test` command with no other file to read.
