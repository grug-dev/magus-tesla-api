// Package account testdb_test.go provisions an isolated, throw-away Postgres
// for the database-backed tests in service_integration_test.go.
//
// Behavior (see internal/testdb and ai/go-conventions.md §persistence):
//   - If TEST_DATABASE_URL is set AND reachable, use it (managed/CI Postgres).
//   - Otherwise auto-provision a disposable `postgres:16-alpine` container.
//
// goose migrations are embedded under db/migrations/ and applied before tests.
//
// Production impact: NONE. This is a _test.go file; testcontainers/goose are
// never compiled into the deployed binary, no Docker daemon required in prod.
package account

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"os"
	"testing"

	"github.com/cristianpena/magus-tesla-api/internal/testdb"
)

//go:embed db/migrations/*.sql
var migrationsFS embed.FS

// testDSN is the connection string provisioned by TestMain and used by
// newTestService. It is set once for the whole test binary run.
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
		log.Fatalf("account testdb: sub migrations fs: %v", err)
	}

	result, err := testdb.Provision(ctx, subFS)
	if err != nil {
		log.Fatalf("account testdb: provision: %v", err)
	}
	testDSN = result.DSN
	testResult = result
	defer func() { _ = testResult.Terminate(context.Background()) }()

	return m.Run()
}
