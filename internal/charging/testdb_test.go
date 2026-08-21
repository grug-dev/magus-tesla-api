// Package charging_test starts an isolated, throw-away Postgres for the
// database-backed tests in db_integration_test.go.
//
// Behavior (see internal/testdb and ai/go-conventions.md §persistence):
//   - If DATABASE_URL is set AND reachable, use it (managed/CI Postgres).
//   - Otherwise auto-provision a disposable `postgres:16-alpine` container.
// goose migrations are embedded under db/migrations/ and applied before tests.
//
// Production impact: NONE. This file is a _test.go file — Go never compiles
// test-imports into the deployed binary, so testcontainers/goose are not
// shipped to the VM host. No Docker daemon is required in production.
package charging_test

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/testdb"
)

//go:embed db/migrations/*.sql
var migrationsFS embed.FS

var testPool *pgxpool.Pool

// TestMain provisions the test database once for the whole package, runs the
// suite, then tears everything down. Sharing one container + one pool across
// all tests keeps startup overhead to a single ~3s container boot.
func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	ctx := context.Background()

	subFS, err := fs.Sub(migrationsFS, "db/migrations")
	if err != nil {
		log.Fatalf("charging testdb: sub migrations fs: %v", err)
	}

	result, err := testdb.Provision(ctx, subFS)
	if err != nil {
		log.Fatalf("charging testdb: provision: %v", err)
	}
	defer func() { _ = result.Terminate(context.Background()) }()

	pool, err := pgxpool.New(ctx, result.DSN)
	if err != nil {
		log.Fatalf("charging testdb: pgxpool new: %v", err)
	}
	testPool = pool
	defer pool.Close()

	return m.Run()
}