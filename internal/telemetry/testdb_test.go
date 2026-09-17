// Package telemetry testdb_test.go provisions an isolated, throw-away Postgres
// for the database-backed tests in db_integration_test.go, db_read_integration_test.go,
// db_sourcea_integration_test.go, and db_supercharger_integration_test.go.
//
// Behavior (see internal/testdb and ai/go-conventions.md §persistence):
//   - If TEST_DATABASE_URL is set AND reachable, use it (managed/CI Postgres).
//   - Otherwise auto-provision a disposable `postgres:16-alpine` container.
//
// goose migrations are embedded under db/migrations/ and applied before tests.
//
// Production impact: NONE. This is a _test.go file; testcontainers/goose are
// never compiled into the deployed binary, no Docker daemon required in prod.
package telemetry

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log"
	"os"
	"testing"

	"github.com/cristianpena/magus-tesla-api/internal/testdb"
)

//go:embed db/migrations/*.sql
var migrationsFS embed.FS

// testDSN is the connection string provisioned by TestMain and used by
// newTestStore. It is set once for the whole test binary run.
var testDSN string

var testResult testdb.Result

// TestMain provisions the test database once for the whole package and tears
// it down after the suite runs. Tests create per-test pools on top of testDSN
// (preserving their existing t.Cleanup(pool.Close) pattern).
func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	ctx := context.Background()

	subFS, err := fs.Sub(migrationsFS, "db/migrations")
	if err != nil {
		log.Fatalf("telemetry testdb: sub migrations fs: %v", err)
	}

	result, err := testdb.Provision(ctx, "telemetry", subFS)
	switch {
	case errors.Is(err, testdb.ErrUnavailable):
		// No reachable Postgres and no Docker daemon to provision one. Skip the
		// DB-backed tests rather than killing the whole binary: the package's
		// offline tests (clock.CalendarDay, snapshotFrom, scheduler) need no database and
		// must still run. deriveConsumption and dayStart moved to
		// internal/analytics and were deleted here (RM29-telemetry-drop-derived-
		// columns tier 4, design D8). newTestStore turns the empty testDSN into a
		// t.Skip for every DB-backed test.
		log.Printf("telemetry testdb: no Postgres available, SKIPPING all DB-backed tests: %v", err)
		return m.Run()
	case err != nil:
		// Anything else — above all a migration that failed to apply — means the
		// schema or the test setup is genuinely broken. Fail LOUDLY: skipping here
		// would let a broken migration pass `make check` in silence.
		log.Fatalf("telemetry testdb: provision: %v", err)
	}
	testDSN = result.DSN
	testResult = result
	defer func() { _ = testResult.Terminate(context.Background()) }()

	return m.Run()
}
