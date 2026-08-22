// Package charging_test starts an isolated, throw-away Postgres for the
// database-backed tests in db_integration_test.go,
// db_session_integration_test.go and db_backfill_integration_test.go.
//
// Behavior (see internal/testdb and ai/go-conventions.md §persistence):
//   - If DATABASE_URL is set AND reachable, use it (managed/CI Postgres).
//   - Otherwise auto-provision a disposable `postgres:16-alpine` container.
//
// TWO migration DIRECTORIES are applied, in this order: internal/telemetry's
// first, then this module's own (design.md D8c,
// RM29-charging-add-charge-sessions). Telemetry must go first because this
// module's 20260823000001 migration ships a backfill that reads telemetry's
// supercharger_sessions table — db_backfill_integration_test.go seeds that
// table and needs it to already exist. testdb.ProvisionDirs is the sanctioned
// form for a package whose fixtures span more than one module's schema
// (ai/go-conventions.md §Testing: "more than one module's tables →
// ProvisionDirs"); internal/analytics already does the same. This is a path
// dependency on a migration DIRECTORY, not a Go import — no _test.go file in
// this package imports internal/telemetry.
//
// migrationsFS stays embedded (rather than switching entirely to os.DirFS)
// because db_backfill_integration_test.go reads the shipped migration file
// through it to extract the backfill statement between the
// BACKFILL-BEGIN/BACKFILL-END sentinels at runtime, so the test can never
// drift from the statement that actually ships to production (design.md D8c).
//
// Production impact: NONE. This file is a _test.go file — Go never compiles
// test-imports into the deployed binary, so testcontainers/goose are not
// shipped to the VM host. No Docker daemon is required in production.
package charging_test

import (
	"context"
	"embed"
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

	// Telemetry's directory first, this module's own second — see the package
	// doc comment above for why the order matters (design.md D8c).
	result, err := testdb.ProvisionDirs(ctx, "../telemetry/db/migrations", "db/migrations")
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
