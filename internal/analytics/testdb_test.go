// Package analytics testdb_test.go provisions an isolated, throw-away Postgres
// for the database-backed tests in db_integration_test.go — this module's
// FIRST-EVER DB-backed test file (RM29-analytics-add-vehicle-metrics, Wave 6).
// Mirrors internal/telemetry/testdb_test.go's TestMain/testdb harness pattern
// EXACTLY (a new precedent for this module, not a new pattern for the
// codebase — tasks.md task 6.1).
//
// Behavior (see internal/testdb and ai/go-conventions.md §persistence):
//   - If DATABASE_URL is set AND reachable, use it (managed/CI Postgres).
//   - Otherwise auto-provision a disposable `postgres:16-alpine` container.
//
// goose migrations are embedded under db/migrations/ and applied before
// tests. IMPORTANT: this embeds ONLY internal/analytics/db/migrations —
// Go's //go:embed cannot reach outside this package's own directory tree
// (embed patterns may not contain ".." path elements), so a freshly
// auto-provisioned container for this package contains ONLY the
// vehicle_metrics/vehicle_metric_watermarks schema. It does NOT contain
// telemetry's vehicle_snapshots/supercharger_sessions or charging's
// manual_charge_entries tables — see db_integration_test.go's top-of-file
// comment for what this means for cross-module fixture seeding.
//
// Production impact: NONE. This is a _test.go file; testcontainers/goose are
// never compiled into the deployed binary, no Docker daemon required in prod.
package analytics

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

	subFS, err := fs.Sub(migrationsFS, "db/migrations")
	if err != nil {
		log.Fatalf("analytics testdb: sub migrations fs: %v", err)
	}

	result, err := testdb.Provision(ctx, subFS)
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
