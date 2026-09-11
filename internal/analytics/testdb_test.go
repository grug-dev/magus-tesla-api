// Package analytics testdb_test.go provisions an isolated, throw-away Postgres
// for the database-backed tests in db_integration_test.go — this module's
// FIRST-EVER DB-backed test file (RM29-analytics-add-vehicle-metrics, Wave 6).
// Mirrors internal/telemetry/testdb_test.go's TestMain/testdb harness pattern
// EXACTLY (a new precedent for this module, not a new pattern for the
// codebase — tasks.md task 6.1).
//
// Behavior (see internal/testdb and ai/go-conventions.md §persistence):
//   - If TEST_DATABASE_URL is set AND reachable, use it (managed/CI Postgres).
//   - Otherwise auto-provision a disposable `postgres:16-alpine` container.
//
// This package's fixtures span THREE modules' schemas: Recalculate reads
// telemetry's vehicle_snapshots and supercharger_sessions and charging's
// manual_charge_entries, then writes this module's own vehicle_metrics. So it
// provisions with testdb.ProvisionDirs, which applies several modules'
// migration DIRECTORIES to one throw-away database, rather than
// testdb.Provision, which takes a single embedded filesystem.
//
// It has to be directories: the //go:embed directive may not contain ".."
// path elements, so this package could only ever embed
// internal/analytics/db/migrations — never telemetry's or charging's. A
// container provisioned that way would contain vehicle_metrics and
// vehicle_metric_watermarks and nothing else, and every cross-module fixture
// would fail with "relation does not exist". Relative paths are safe here
// because `go test` always runs a test binary with its own package directory
// as the working directory (RM29 decision D19).
//
// Production impact: NONE. This is a _test.go file; testcontainers/goose are
// never compiled into the deployed binary, no Docker daemon required in prod.
package analytics

import (
	"context"
	"errors"
	"log"
	"os"
	"testing"

	"github.com/cristianpena/magus-tesla-api/internal/testdb"
)

// migrationDirs are relative to THIS package's directory, which is `go test`'s
// working directory for this package's test binary. The directories are applied
// one goose provider at a time, never merged, because migration versions are
// unique within a module but not across the repo.
//
// ORDER MATTERS, and it must match the Makefile's own MIGRATIONS_DIRS order:
// every other module before analytics. There are still no cross-module foreign
// keys (ai/architecture.md §2), but RM50's TPMS backfill migration
// (20260908000002) reads telemetry.vehicle_snapshots inside its own UPDATE. With
// analytics applied first that table does not exist yet, and the whole provision
// fails with `relation "telemetry.vehicle_snapshots" does not exist`. Production
// never had this problem — Makefile MIGRATIONS_DIRS already ordered telemetry
// before analytics; only this list disagreed. Keep analytics LAST.
var migrationDirs = []string{
	"../telemetry/db/migrations",
	"../charging/db/migrations",
	"db/migrations",
}

// testDSN is the connection string provisioned by TestMain and used by
// newTestPool. It is set once for the whole test binary run.
var testDSN string

var testResult testdb.Result

// TestMain provisions the test database once for the whole package and tears
// it down after the suite runs.
func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	ctx := context.Background()

	result, err := testdb.ProvisionDirs(ctx, migrationDirs...)
	switch {
	case errors.Is(err, testdb.ErrUnavailable):
		// No reachable Postgres and no Docker daemon to provision one. Skip the
		// DB-backed tests rather than killing the whole binary: this module's
		// offline tests (derive_test.go, consumed_test.go, reader_test.go) need
		// no database and must still run. newTestPool turns the empty testDSN
		// into a t.Skip for every DB-backed test.
		log.Printf("analytics testdb: no Postgres available, SKIPPING all DB-backed tests: %v", err)
		return m.Run()
	case err != nil:
		// Anything else — above all a migration that failed to apply — means the
		// schema or the test setup is genuinely broken. Fail LOUDLY: skipping here
		// would let a broken migration pass `make check` in silence.
		log.Fatalf("analytics testdb: provision: %v", err)
	}
	testDSN = result.DSN
	testResult = result
	defer func() { _ = testResult.Terminate(context.Background()) }()

	return m.Run()
}
