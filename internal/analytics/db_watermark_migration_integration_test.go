// File db_watermark_migration_integration_test.go implements design.md §5
// Test Contract T1 (RM31-analytics-read-sessions-from-charging, tier 3, task
// 2.3) -- proving migration
// 20260828000001_migrate_vehicle_metric_watermarks_source.sql's actual Up/Down
// SQL, not merely the post-migration schema state db_integration_test.go's
// other tests already exercise incidentally. This is a dedicated file
// (tasks.md 2.3) because it drives goose directly against this module's own
// migration rather than through analyticsdb.Queries -- it exercises the
// migration ITSELF, not Recalculator/Reader.
//
// Why a scoped goose.Provider using ONLY ApplyVersion, never DownTo/Up: this
// package's test database (testdb_test.go) is provisioned via
// testdb.ProvisionDirs across THREE modules' migration directories, all
// recorded in ONE shared goose_db_version table (telemetry's and charging's
// migrations are intermixed with analytics' own by insertion order, not by
// version number). goose's Provider.DownTo/.Up compute "what's pending" by
// walking every row in that SHARED table (see goose's Provider.down/.up,
// which query database.Store.ListMigrations -- ALL recorded migrations,
// ordered by insertion, not scoped to one module) and fail the instant they
// reach a version this provider's own db/migrations-scoped filesystem does
// not know about (goose.ErrVersionNotFound) -- which happens immediately
// here, because telemetry's and charging's own latest migrations sit at
// version numbers BETWEEN this module's 20260822000002 cutoff and its
// 20260828000001 (the migration under test). Provider.ApplyVersion has no
// such traversal: it looks up exactly ONE version_id via a targeted
// `WHERE version_id = $1` query (goose's GetMigrationByVersion), so it never
// touches another module's rows at all. This is the only combination that is
// both correct against the shared table and faithful to the migration file's
// OWN Up/Down SQL (not a hand-duplicated copy of it in this test file).
//
// Runs strictly sequentially with every other test in this package (no
// t.Parallel anywhere in this file, matching db_integration_test.go's own
// documented convention -- required, because it temporarily rolls the SHARED
// vehicle_metric_watermarks.source CHECK constraint back to its PRE-migration
// vocabulary partway through, then restores it via t.Cleanup regardless of
// how the test's own assertions turn out, so every other test in this
// package still sees the fully-migrated schema.
package analytics

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	// Registers the "pgx" database/sql driver goose.NewProvider needs. Already
	// registered process-wide by testdb_test.go's own dependency chain (a Go
	// package's init() runs once regardless of how many files in the binary
	// import it), but named explicitly here so this file is self-contained.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// watermarkSourceMigrationVersion is the goose version_id of the migration
// under test in this file
// (20260828000001_migrate_vehicle_metric_watermarks_source.sql).
const watermarkSourceMigrationVersion int64 = 20260828000001

// newAnalyticsMigrationProvider returns a goose.Provider scoped to ONLY this
// module's own db/migrations directory, against the SAME database
// newTestPool connects to. See the file-level comment for why this provider
// is driven exclusively through ApplyVersion, never DownTo/Up.
func newAnalyticsMigrationProvider(t *testing.T) *goose.Provider {
	t.Helper()
	if testDSN == "" {
		t.Skip("no test Postgres: set DATABASE_URL or start Docker to run the DB-backed tests")
	}
	db, err := sql.Open("pgx", testDSN)
	if err != nil {
		t.Fatalf("opening database/sql connection for goose: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("db/migrations"))
	if err != nil {
		_ = db.Close()
		t.Fatalf("constructing goose provider: %v", err)
	}
	t.Cleanup(func() { _ = provider.Close() }) // Close() closes the underlying *sql.DB too.
	return provider
}

// seedWatermarkRow inserts one vehicle_metric_watermarks row directly -- this
// table has no public writer at all (Recalculator.advanceWatermark is
// unexported and always computed, never handed an arbitrary
// source_updated_at literal), mirroring db_integration_test.go's established
// D19 direct-SQL convention.
func seedWatermarkRow(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64, source string, sourceUpdatedAt time.Time) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO vehicle_metric_watermarks (account_id, tesla_id, source, source_updated_at) VALUES ($1, $2, $3, $4)`,
		accountID, teslaID, source, pgtype.Timestamptz{Time: sourceUpdatedAt, Valid: true},
	)
	if err != nil {
		t.Fatalf("seeding vehicle_metric_watermarks: %v", err)
	}
}

// fetchWatermarkUpdatedAt reads one watermark row's source_updated_at,
// failing the test outright when the row is missing -- unlike
// db_integration_test.go's fetchWatermark (which returns an ok bool for
// callers that legitimately expect "no row" as one of two valid outcomes),
// every call site in this file always expects the row to exist.
func fetchWatermarkUpdatedAt(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64, source string) time.Time {
	t.Helper()
	ts, ok := fetchWatermark(t, pool, accountID, teslaID, source)
	if !ok {
		t.Fatalf("expected a %s watermark row for (%s, %d), found none", source, accountID, teslaID)
	}
	return ts
}

// countWatermarkRows counts vehicle_metric_watermarks rows matching the given
// scope -- source == "" matches every source for the vehicle.
func countWatermarkRows(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64, source string) int {
	t.Helper()
	var n int
	var err error
	if source == "" {
		err = pool.QueryRow(context.Background(),
			`SELECT count(*) FROM vehicle_metric_watermarks WHERE account_id = $1 AND tesla_id = $2`,
			accountID, teslaID,
		).Scan(&n)
	} else {
		err = pool.QueryRow(context.Background(),
			`SELECT count(*) FROM vehicle_metric_watermarks WHERE account_id = $1 AND tesla_id = $2 AND source = $3`,
			accountID, teslaID, source,
		).Scan(&n)
	}
	if err != nil {
		t.Fatalf("counting vehicle_metric_watermarks: %v", err)
	}
	return n
}

// insertWatermarkSource attempts a bare INSERT with the given source value,
// for the CHECK-constraint-vocabulary assertions below. Returns the error
// (nil on success) and registers its own cleanup for a successful insert, so
// this helper never leaves a stray row behind regardless of the caller's own
// outcome.
func insertWatermarkSource(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64, source string) error {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`INSERT INTO vehicle_metric_watermarks (account_id, tesla_id, source, source_updated_at) VALUES ($1, $2, $3, now())`,
		accountID, teslaID, source,
	)
	if err == nil {
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(),
				`DELETE FROM vehicle_metric_watermarks WHERE account_id = $1 AND tesla_id = $2 AND source = $3`,
				accountID, teslaID, source,
			)
		})
	}
	return err
}

// isCheckViolation reports whether err is specifically a Postgres CHECK
// constraint violation (SQLSTATE 23514) on the named constraint -- precise
// enough to distinguish "the vocabulary rejected this value" from any other
// failure mode.
func isCheckViolation(err error, constraintName string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23514" && pgErr.ConstraintName == constraintName
}

// TestMigration_WatermarkSourceVocabulary implements design.md §5 Test
// Contract T1 in full: the migration DELETEs only the supercharger_sessions
// watermark row (not renaming it), leaves its sibling sources' rows
// byte-identical, re-points the CHECK constraint's vocabulary, and a Down
// round-trip restores the OLD constraint/comments WITHOUT resurrecting the
// deleted row. Every pinned value below (account/tesla IDs, timestamps, row
// counts) is copied verbatim from design.md §5 T1 -- never derived by
// running the migration first and recording what came out.
func TestMigration_WatermarkSourceVocabulary(t *testing.T) {
	pool := newTestPool(t)
	provider := newAnalyticsMigrationProvider(t)
	ctx := context.Background()

	accountID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	const teslaID = int64(555)
	otherAccountID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	const otherTeslaID = int64(1)

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM vehicle_metric_watermarks WHERE account_id = $1 AND tesla_id = $2`, accountID, teslaID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM vehicle_metric_watermarks WHERE account_id = $1 AND tesla_id = $2`, otherAccountID, otherTeslaID)
		// Leave the migration APPLIED for every other test in this package,
		// regardless of how this test's own assertions turned out.
		if _, err := provider.ApplyVersion(cleanupCtx, watermarkSourceMigrationVersion, true); err != nil && !errors.Is(err, goose.ErrAlreadyApplied) {
			t.Errorf("cleanup: re-applying watermark-source migration: %v", err)
		}
	})

	// --- Given: migrations applied through 20260822000002 only ---
	if _, err := provider.ApplyVersion(ctx, watermarkSourceMigrationVersion, false); err != nil {
		t.Fatalf("rolling back watermark-source migration to seed the Given state: %v", err)
	}

	// Three rows, one per source, for the SAME vehicle, using the OLD
	// (pre-migration) vocabulary -- legal again now that the migration is
	// rolled back.
	seedWatermarkRow(t, pool, accountID, teslaID, "supercharger_sessions", time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC))
	seedWatermarkRow(t, pool, accountID, teslaID, "vehicle_snapshots", time.Date(2026, 8, 21, 3, 30, 0, 0, time.UTC))
	seedWatermarkRow(t, pool, accountID, teslaID, "manual_charge_entries", time.Date(2026, 8, 19, 18, 0, 0, 0, time.UTC))

	// --- When: the migration is applied ---
	if _, err := provider.ApplyVersion(ctx, watermarkSourceMigrationVersion, true); err != nil {
		t.Fatalf("applying watermark-source migration: %v", err)
	}

	// --- Then: design.md §5 T1's pinned post-migration assertions ---

	if n := countWatermarkRows(t, pool, accountID, teslaID, "supercharger_sessions"); n != 0 {
		t.Errorf("supercharger_sessions watermark count: want 0 (the row is GONE, not renamed), got %d", n)
	}
	if got := fetchWatermarkUpdatedAt(t, pool, accountID, teslaID, "vehicle_snapshots"); !got.Equal(time.Date(2026, 8, 21, 3, 30, 0, 0, time.UTC)) {
		t.Errorf("vehicle_snapshots source_updated_at: want unchanged 2026-08-21T03:30:00Z, got %s", got)
	}
	if got := fetchWatermarkUpdatedAt(t, pool, accountID, teslaID, "manual_charge_entries"); !got.Equal(time.Date(2026, 8, 19, 18, 0, 0, 0, time.UTC)) {
		t.Errorf("manual_charge_entries source_updated_at: want unchanged 2026-08-19T18:00:00Z, got %s", got)
	}
	if n := countWatermarkRows(t, pool, accountID, teslaID, ""); n != 2 {
		t.Errorf("total watermark row count for this vehicle: want 2 (down from 3 -- exactly vehicle_snapshots and manual_charge_entries survive), got %d", n)
	}

	if err := insertWatermarkSource(t, pool, otherAccountID, otherTeslaID, "supercharger_sessions"); !isCheckViolation(err, "vehicle_metric_watermarks_source_check") {
		t.Errorf("INSERT with source='supercharger_sessions' post-migration: want a CHECK violation on vehicle_metric_watermarks_source_check, got %v", err)
	}
	if err := insertWatermarkSource(t, pool, otherAccountID, otherTeslaID, "charge_sessions"); err != nil {
		t.Errorf("INSERT with source='charge_sessions' post-migration: want success, got %v", err)
	}

	// Semantic consequence, asserted via Reconcile's own watermark method
	// (not a raw SQL check, design.md §5 T1): the absence of a row IS the
	// epoch signal (design D7).
	rec, ok := newRealRecalculator(pool).(*recalculator)
	if !ok {
		t.Fatal("newRealRecalculator did not return a *recalculator")
	}
	gotEpoch, err := rec.watermark(ctx, accountID, teslaID, sourceChargeSessions)
	if err != nil {
		t.Fatalf("watermark(charge_sessions) post-migration: %v", err)
	}
	if !gotEpoch.IsZero() {
		t.Errorf("watermark(charge_sessions) post-migration: want the zero-value epoch (no row = epoch, design D7), got %s", gotEpoch)
	}

	// --- Down round-trip ---
	if _, err := provider.ApplyVersion(ctx, watermarkSourceMigrationVersion, false); err != nil {
		t.Fatalf("rolling back watermark-source migration (round-trip): %v", err)
	}

	if err := insertWatermarkSource(t, pool, otherAccountID, otherTeslaID, "supercharger_sessions"); err != nil {
		t.Errorf("INSERT with source='supercharger_sessions' after Down: want success (old vocabulary restored), got %v", err)
	}
	// The negative assertion IS the point of this test (design.md §2e's Down
	// comment): Down restores the SCHEMA (constraint, comments), never the
	// deleted DATA -- a Down that resurrected the row would be a bug.
	if n := countWatermarkRows(t, pool, accountID, teslaID, "supercharger_sessions"); n != 0 {
		t.Errorf("supercharger_sessions watermark count after Down: want STILL 0 (Down does not resurrect the deleted row), got %d", n)
	}
	// Down's own DELETE, the mirror image of Up's. A charge_sessions row was
	// inserted above to prove the new vocabulary was accepted; Down must clear
	// it, or restoring the old CHECK fails with SQLSTATE 23514 and leaves the
	// table unconstrained. This is not a hypothetical: Reconcile writes a
	// charge_sessions cursor on every pass, so production has these rows from
	// the first nightly run after Up. This assertion is what caught it.
	if n := countWatermarkRows(t, pool, otherAccountID, otherTeslaID, "charge_sessions"); n != 0 {
		t.Errorf("charge_sessions watermark count after Down: want 0 (Down clears the rows written under the new vocabulary, mirroring Up), got %d", n)
	}

	// t.Cleanup (registered above) removes both vehicles' rows and
	// re-applies the migration so every other test in this package sees the
	// fully-migrated schema regardless of how this test finished.
}
