// File db_tpms_migration_integration_test.go implements
// RM50-analytics-add-tire-pressure-columns design.md's Test Contract "DB
// integration: backfill migration round-trip" (task 1.8) -- proving
// migration 20260908000002_add_tpms_pressure_columns.sql's actual Up/Down
// SQL (the ADD COLUMN, the cross-schema UPDATE backfill, and the Down DROP
// COLUMN), not merely the post-migration schema state
// db_integration_test.go's other tests already exercise incidentally.
//
// Mirrors db_watermark_migration_integration_test.go's approach exactly: a
// goose.Provider scoped to ONLY this module's own db/migrations directory
// (newAnalyticsMigrationProvider, defined in that file, same package),
// driven through ApplyVersion -- never DownTo/Up -- for the identical reason
// that file documents: this package's test database (testdb_test.go) is
// provisioned across THREE modules' migration directories, all recorded in
// ONE shared goose_db_version table, so Provider.DownTo/.Up would walk every
// row in it and fail the instant it reaches a version this provider's own
// db/migrations-scoped filesystem does not know about. ApplyVersion looks up
// exactly one version_id instead, so it never touches another module's rows.
//
// This migration is simpler to restore than 20260828000001's own test: it is
// the LAST migration in this module's directory, so no later migration sits
// on top of it and no extra re-toggle dance is needed in cleanup -- a single
// re-apply of THIS version is enough to leave the schema exactly as every
// other test in this package expects it.
//
// Runs strictly sequentially with every other test in this package (no
// t.Parallel anywhere in this file, matching this package's own established
// convention) -- required, because it temporarily drops this migration's
// four columns from the SHARED analytics.vehicle_metrics table partway
// through, then restores them via t.Cleanup regardless of how the test's own
// assertions turn out.
package analytics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"

	"github.com/cristianpena/magus-tesla-api/internal/telemetry"

	// Registers the "pgx" database/sql driver newAnalyticsMigrationProvider's
	// goose.NewProvider needs. Already registered process-wide by
	// db_watermark_migration_integration_test.go's own identical blank
	// import (a Go package's init() runs once regardless of how many files
	// in the binary import it), named here too so this file is
	// self-contained, mirroring that file's own stated reasoning.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// tpmsMigrationVersion is the goose version_id of the migration under test
// in this file (20260908000002_add_tpms_pressure_columns.sql).
const tpmsMigrationVersion int64 = 20260908000002

// TestMigration_TpmsPressureBackfill implements design.md's Test Contract
// "DB integration: backfill migration round-trip" in full: the backfill
// fills a vehicle_metrics row whose (account_id, tesla_id, metric_date)
// matches a vehicle_snapshots row's (account_id, tesla_id,
// captured_date - 1), leaves a row with no matching snapshot NULL (not an
// error, not a fabricated zero), and a Down/Up round-trip leaves the table
// in the same shape. Every pinned value below is copied verbatim from
// design.md's Test Contract, never derived by running the migration first
// and recording what came out.
func TestMigration_TpmsPressureBackfill(t *testing.T) {
	pool := newTestPool(t)
	provider := newAnalyticsMigrationProvider(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(990301)
	cleanupVehicleMetrics(t, pool, accountID, teslaID)

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		// Leave the migration APPLIED for every other test in this package,
		// regardless of how this test's own assertions turned out -- mirrors
		// db_watermark_migration_integration_test.go's identical cleanup
		// contract.
		if _, err := provider.ApplyVersion(cleanupCtx, tpmsMigrationVersion, true); err != nil && !errors.Is(err, goose.ErrAlreadyApplied) {
			t.Errorf("cleanup: re-applying tpms-pressure migration: %v", err)
		}
	})

	// --- Given: migrations applied through the one BEFORE this one only
	// ("apply migrations up to (not including) this one", design.md's Test
	// Contract) -- TestMain already applied every migration including this
	// one, so rolling back ONLY this version reaches that state without
	// disturbing anything else (identical technique to
	// db_watermark_migration_integration_test.go's own "Given" step).
	if _, err := provider.ApplyVersion(ctx, tpmsMigrationVersion, false); err != nil {
		t.Fatalf("rolling back tpms-pressure migration to seed the Given state: %v", err)
	}

	snapshotDay := day(2026, 9, 10)                     // vs.captured_date
	matchingMetricDate := snapshotDay.AddDate(0, 0, -1) // vm.metric_date = vs.captured_date - 1
	unmatchedMetricDate := day(2026, 8, 1)              // no snapshot has captured_date = this + 1

	// One telemetry.vehicle_snapshots row, all four TPMS fields non-NULL.
	// Other NOT NULL columns get plausible values. The four TPMS values are
	// exact binary fractions (39.5, 39.25, 39.75, 39.125) for the identical
	// reason TestRecalculate_TPMS_RoundTrip's own doc comment gives:
	// seedSnapshot narrows them through REAL (float4) on the way in, and a
	// non-power-of-two fraction would pick up a float32 rounding error
	// irrelevant to the migration under test.
	seedSnapshot(t, pool, telemetry.Snapshot{
		AccountID:         accountID,
		TeslaID:           teslaID,
		CapturedAt:        time.Date(2026, 9, 10, 3, 30, 0, 0, time.UTC),
		CapturedDate:      snapshotDay,
		BatteryLevelPct:   65,
		OdometerKm:        2000.0,
		BatteryRangeKm:    260.0,
		ChargingState:     "Disconnected",
		ChargeLimitSocPct: 80,
		InsideTempC:       22.0,
		OutsideTempC:      17.0,
		Locked:            true,
		CarVersion:        "2026.30.1",
		TpmsPressureFLPSI: floatPtr(39.5),
		TpmsPressureFRPSI: floatPtr(39.25),
		TpmsPressureRLPSI: floatPtr(39.75),
		TpmsPressureRRPSI: floatPtr(39.125),
	})

	// One pre-existing vehicle_metrics row at the MATCHING effective day --
	// the four new columns don't exist yet at this point (rolled back
	// above), so seedPreMigrationVehicleMetric's INSERT (which never
	// references them) works unchanged, simulating a row that existed
	// before this migration ran.
	seedPreMigrationVehicleMetric(t, pool, accountID, teslaID, matchingMetricDate, 55, 1900.0, 270.0)

	// A second vehicle_metrics row with NO matching snapshot (a different
	// metric_date; no snapshot has captured_date = this + 1) -- design.md's
	// own "stays NULL, not an error" case.
	seedPreMigrationVehicleMetric(t, pool, accountID, teslaID, unmatchedMetricDate, 50, 1500.0, 250.0)

	assertBackfilled := func(t *testing.T) {
		t.Helper()
		matched, ok := fetchVehicleMetric(t, pool, accountID, teslaID, matchingMetricDate)
		if !ok {
			t.Fatal("expected the matched vehicle_metrics row to still exist")
		}
		if !matched.TpmsPressureFlPsi.Valid || matched.TpmsPressureFlPsi.Float64 != 39.5 {
			t.Errorf("matched row TpmsPressureFlPsi: want 39.5, got %+v", matched.TpmsPressureFlPsi)
		}
		if !matched.TpmsPressureFrPsi.Valid || matched.TpmsPressureFrPsi.Float64 != 39.25 {
			t.Errorf("matched row TpmsPressureFrPsi: want 39.25, got %+v", matched.TpmsPressureFrPsi)
		}
		if !matched.TpmsPressureRlPsi.Valid || matched.TpmsPressureRlPsi.Float64 != 39.75 {
			t.Errorf("matched row TpmsPressureRlPsi: want 39.75, got %+v", matched.TpmsPressureRlPsi)
		}
		if !matched.TpmsPressureRrPsi.Valid || matched.TpmsPressureRrPsi.Float64 != 39.125 {
			t.Errorf("matched row TpmsPressureRrPsi: want 39.125, got %+v", matched.TpmsPressureRrPsi)
		}

		unmatched, ok := fetchVehicleMetric(t, pool, accountID, teslaID, unmatchedMetricDate)
		if !ok {
			t.Fatal("expected the unmatched vehicle_metrics row to still exist")
		}
		if unmatched.TpmsPressureFlPsi.Valid || unmatched.TpmsPressureFrPsi.Valid ||
			unmatched.TpmsPressureRlPsi.Valid || unmatched.TpmsPressureRrPsi.Valid {
			t.Errorf("unmatched row: want all four TPMS columns NULL (no matching snapshot), got %+v", unmatched)
		}
	}

	// --- When: the migration is applied ---
	if _, err := provider.ApplyVersion(ctx, tpmsMigrationVersion, true); err != nil {
		t.Fatalf("applying tpms-pressure migration: %v", err)
	}

	// --- Then ---
	assertBackfilled(t)

	// --- Down/Up round-trip (design.md's own requirement, mirroring
	// 20260828000001's precedent): Down only drops the four columns (it
	// does not un-backfill any other table), and Up re-adds them and
	// re-runs the backfill against the SAME, untouched vehicle_snapshots
	// row -- so the same assertions must hold again. ---
	if _, err := provider.ApplyVersion(ctx, tpmsMigrationVersion, false); err != nil {
		t.Fatalf("rolling back tpms-pressure migration (round-trip Down): %v", err)
	}
	if _, err := provider.ApplyVersion(ctx, tpmsMigrationVersion, true); err != nil {
		t.Fatalf("re-applying tpms-pressure migration (round-trip Up): %v", err)
	}
	assertBackfilled(t)

	// t.Cleanup (registered above) re-applies the migration (a no-op here,
	// since the round-trip above already left it applied) so every other
	// test in this package sees the fully-migrated schema regardless of how
	// this test finished.
}
