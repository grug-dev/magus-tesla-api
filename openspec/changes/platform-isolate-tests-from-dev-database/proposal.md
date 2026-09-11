# platform-isolate-tests-from-dev-database

Source: MAG-72 — https://linear.app/magus-monitor/issue/MAG-72/clean-up-telemetry-integration-test-fixtures-in-vehicle-snapshots

## Why

The dev database's `telemetry.vehicle_snapshots` table holds 78 orphan rows —
rows whose `tesla_id` does not match any real vehicle in `account.vehicles` —
against 112 real ones. The owner confirmed this count on 2026-09-11.

The Linear ticket blames telemetry's integration tests. That is wrong.
Telemetry's tests clean up correctly: a helper called `cleanupVehicle` deletes
every row a telemetry test creates, in both `vehicle_snapshots` and
`poll_attempts`. It has done this since 2026-07-11 and runs 40 times across
five test files.

The real cause is one file in a different module: `internal/analytics`.

- `internal/analytics/db_integration_test.go` inserts rows into
  `telemetry.vehicle_snapshots` as input fixtures for its own metric tests.
- Its cleanup helper, `cleanupVehicleMetrics`, deletes only two `analytics`
  tables. It never deletes the `telemetry` rows the same file inserted.
- The file's own doc comment claims it mirrors telemetry's cleanup pattern
  "one level up". It copied the shape but forgot the rows it inserted itself.

All 21 distinct orphan `tesla_id` values in the dev database appear in this one
file. It is also the only test file in the repo that inserts into
`telemetry.vehicle_snapshots` — every other match for that phrase is
generated `sqlc` code, the real write path.

**Why the rows reached a real database at all.** `internal/testdb`, the shared
test-database helper, reads `DATABASE_URL` to decide where tests run. That is
the same variable the running application reads for its own database
connection. When a developer's shell has `DATABASE_URL` set — from `.env`, an
IDE run configuration, or a direct `export` — tests silently run against the
real database instead of a disposable one. `make test` works around this today
by unsetting the variable before running `go test`, but that guard lives only
in one Makefile target. `go test ./...` from a plain shell, or an IDE's own
"run test" button, walks straight past it.

All 78 orphan rows were inserted between 2026-07-31 and 2026-08-21, then
stopped — because the owner stopped using `make test-with-db`, not because the
bug was fixed. The leak is still live: any developer or IDE run with
`DATABASE_URL` set in the environment reintroduces it today.

There is no CI in this repository (no `.github/workflows`), so nothing
external depends on today's behavior.

## What Changes

- **`internal/testdb` starts reading `TEST_DATABASE_URL` instead of
  `DATABASE_URL`.** This is the functional fix: it makes tests safe by
  default. `DATABASE_URL` (the application's own database config) is left
  completely alone.
- **The `Makefile`'s `test` and `test-with-db` targets change shape** to match
  the new variable, and their help text is corrected.
- **The `analytics` test's cleanup is fixed** to also delete the
  `telemetry.vehicle_snapshots` rows it inserts, closing the actual leak
  inside the test itself (not just stopping it from reaching a real
  database).
- **Every comment across the codebase that describes a test as
  "`DATABASE_URL`-gated" or tells the reader to set `DATABASE_URL`** is swept
  to name `TEST_DATABASE_URL` instead — in Go doc comments and in the
  `README.md` / `ai/go-conventions.md` / module `AGENTS.md` docs that describe
  the same test-provisioning behavior.
- **A one-off SQL script** (not a migration — see design.md D6) is added for
  the owner to run once, deleting the 78 existing orphan rows from the dev
  database after a backup.

## Impact

- **Affected modules:** `internal/testdb` (behavior change), `internal/analytics`
  (test-only fix), plus doc-only edits across `internal/telemetry`,
  `internal/account`, `internal/charging`, and the root docs. No production
  code path changes — nothing outside `_test.go` files and test-provisioning
  docs is touched.
- **Not breaking.** No public interface, database schema, table, column, or
  API changes. `DATABASE_URL` keeps working exactly as before for the running
  application (`cmd/web`, `cmd/poller`, `cmd/migrate`).
- **One environment variable rename for developers:** anyone who currently
  sets `DATABASE_URL` to point tests at a real database must set
  `TEST_DATABASE_URL` instead. This is announced in the same change, in every
  doc that named the old variable for that purpose.
- **Spec delta:** adds one requirement, `Test Database Isolation`, to the
  existing `platform` capability (`specs/platform/spec.md`).

## Out of Scope

- Fixing the boundary violation of an `analytics` test writing raw SQL into a
  `telemetry` table (see design.md D4). That is a real issue, tracked as its
  own future ticket.
- Any schema, table, column, index, or migration change.
- Unit tests (owner's default: excluded — this change fixes one existing
  test's teardown and a test-database chooser; it adds no new tests).
