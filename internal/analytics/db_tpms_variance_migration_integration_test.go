// File db_tpms_variance_migration_integration_test.go implements
// RM50-analytics-add-tire-pressure-variance design.md's "Test scope" (task
// B.8) -- proving migration
// 20260908000003_add_tpms_pressure_variance_columns.sql's actual Up/Down SQL
// (the ADD COLUMN, the self-join backfill UPDATE, and the Down DROP COLUMN),
// not merely the post-migration schema state db_integration_test.go's other
// tests already exercise incidentally.
//
// Mirrors db_tpms_migration_integration_test.go's (tier 1) approach exactly:
// a goose.Provider scoped to ONLY this module's own db/migrations directory
// (newAnalyticsMigrationProvider, defined in
// db_watermark_migration_integration_test.go, same package), driven through
// ApplyVersion -- never DownTo/Up -- for the identical reason that file
// documents: this package's test database (testdb_test.go) is provisioned
// across THREE modules' migration directories, all recorded in ONE shared
// goose_db_version table, so Provider.DownTo/.Up would walk every row in it
// and fail the instant it reaches a version this provider's own
// db/migrations-scoped filesystem does not know about. ApplyVersion looks up
// exactly one version_id instead, so it never touches another module's rows.
//
// Unlike tier 1's migration, THIS migration's backfill reads only
// analytics.vehicle_metrics, self-joined -- no telemetry.vehicle_snapshots
// fixture is needed here. Fixtures below seed vehicle_metrics rows directly
// via SQL, since deriveVehicleMetrics/Recalculate has no public entry point
// that leaves the four new columns unpopulated (they exist only pre-this-
// migration) -- mirrors db_integration_test.go's own
// seedPreMigrationVehicleMetric precedent, widened here to also carry the
// four raw tpms_pressure_*_psi values this migration's self-join reads.
//
// This migration is the LAST migration in this module's directory (as of
// this change), so no later migration sits on top of it and no extra
// re-toggle dance is needed in cleanup -- a single re-apply of THIS version
// is enough to leave the schema exactly as every other test in this package
// expects.
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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	// Registers the "pgx" database/sql driver newAnalyticsMigrationProvider's
	// goose.NewProvider needs. Already registered process-wide by
	// db_watermark_migration_integration_test.go's own identical blank
	// import (a Go package's init() runs once regardless of how many files
	// in the binary import it), named here too so this file is
	// self-contained, mirroring that file's and tier 1's own stated
	// reasoning.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// tpmsVarianceMigrationVersion is the goose version_id of the migration
// under test in this file
// (20260908000003_add_tpms_pressure_variance_columns.sql).
const tpmsVarianceMigrationVersion int64 = 20260908000003

// seedVehicleMetricWithTpms inserts a vehicle_metrics row directly via SQL,
// carrying only the columns that exist BEFORE this migration
// (battery_level_pct, odometer_km, battery_range_km, flagged) PLUS the four
// raw tpms_pressure_*_psi columns this migration's self-join backfill reads
// -- those already exist on vehicle_metrics as of tier 1
// (20260908000002_add_tpms_pressure_columns.sql), which TestMain applies
// before this migration is rolled back for this test's "Given" state. A nil
// argument leaves that wheel's raw reading SQL NULL, exercising design.md
// D2's "either day's own raw wheel reading is itself NULL" condition. No
// writer in this codebase can produce this shape (Recalculate always derives
// the _calc columns this migration adds, design D1), so a direct INSERT is
// the only way to construct a genuine pre-migration row -- mirrors this
// package's own D19 precedent (db_integration_test.go).
func seedVehicleMetricWithTpms(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64, metricDate time.Time, batteryLevelPct int, odometerKm, batteryRangeKm float64, flPSI, frPSI, rlPSI, rrPSI *float64) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO analytics.vehicle_metrics (
			account_id, tesla_id, metric_date, battery_level_pct, odometer_km, battery_range_km, flagged,
			tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi
		) VALUES ($1, $2, $3, $4, $5, $6, false, $7, $8, $9, $10)`,
		accountID, teslaID, dateFrom(metricDate), int32(batteryLevelPct), odometerKm, batteryRangeKm,
		flPSI, frPSI, rlPSI, rrPSI,
	)
	if err != nil {
		t.Fatalf("seeding pre-migration vehicle_metrics row with raw TPMS: %v", err)
	}
}

// fetchTpmsDeltaCalc SELECTs one vehicle_metrics row's four
// tpms_pressure_*_psi_calc columns directly by its (account_id, tesla_id,
// metric_date) -- the table's own UNIQUE index. Returns ok=false when no row
// exists. A dedicated, narrow fetch (rather than widening the shared
// fetchVehicleMetric in db_integration_test.go) because design.md's "Files
// touched" list for this change does not include that file.
func fetchTpmsDeltaCalc(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64, metricDate time.Time) (fl, fr, rl, rr *float64, ok bool) {
	t.Helper()
	var flv, frv, rlv, rrv pgtype.Float8
	err := pool.QueryRow(context.Background(), `
		SELECT tpms_pressure_fl_psi_calc, tpms_pressure_fr_psi_calc,
		       tpms_pressure_rl_psi_calc, tpms_pressure_rr_psi_calc
		FROM analytics.vehicle_metrics
		WHERE account_id = $1 AND tesla_id = $2 AND metric_date = $3`,
		accountID, teslaID, dateFrom(metricDate),
	).Scan(&flv, &frv, &rlv, &rrv)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil, nil, false
	}
	if err != nil {
		t.Fatalf("querying vehicle_metrics tpms deltas: %v", err)
	}
	return ptrFloat64FromPg(flv), ptrFloat64FromPg(frv), ptrFloat64FromPg(rlv), ptrFloat64FromPg(rrv), true
}

// TestMigration_TpmsPressureVarianceBackfill implements design.md's Test
// Contract "one migration-backfill integration test" (Test scope) in full:
// the self-join backfill fills a vehicle_metrics row's four delta columns
// from its own predecessor row's raw tpms_pressure_*_psi values, leaves a
// wheel's delta NULL when either day's own raw reading for that wheel is
// NULL (Postgres's NULL - x = NULL rule, matching the Go implementation's
// tpmsDeltaPSI helper exactly), and leaves a row with NO predecessor row
// untouched -- NULL, not an error, not a fabricated 0. A Down/Up round-trip
// leaves the table in the same shape.
func TestMigration_TpmsPressureVarianceBackfill(t *testing.T) {
	pool := newTestPool(t)
	provider := newAnalyticsMigrationProvider(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(990401)
	cleanupVehicleMetrics(t, pool, accountID, teslaID)

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		// Leave the migration APPLIED for every other test in this package,
		// regardless of how this test's own assertions turned out -- mirrors
		// db_tpms_migration_integration_test.go's identical cleanup
		// contract.
		if _, err := provider.ApplyVersion(cleanupCtx, tpmsVarianceMigrationVersion, true); err != nil && !errors.Is(err, goose.ErrAlreadyApplied) {
			t.Errorf("cleanup: re-applying tpms-pressure-variance migration: %v", err)
		}
	})

	// --- Given: migrations applied through the one BEFORE this one only
	// ("apply migrations up to (not including) this one") -- TestMain
	// already applied every migration including this one, so rolling back
	// ONLY this version reaches that state without disturbing anything else
	// (identical technique to db_tpms_migration_integration_test.go's own
	// "Given" step). The four raw tpms_pressure_*_psi columns (tier 1) and
	// the rest of the table stay in place -- only this migration's own four
	// _calc columns are dropped by this rollback.
	if _, err := provider.ApplyVersion(ctx, tpmsVarianceMigrationVersion, false); err != nil {
		t.Fatalf("rolling back tpms-pressure-variance migration to seed the Given state: %v", err)
	}

	predecessorDay := day(2026, 9, 5)
	withPredecessorDay := predecessorDay.AddDate(0, 0, 1) // 2026-09-06
	noPredecessorDay := day(2026, 8, 1)                   // no row exists at this - 1 day

	// Predecessor row: all four wheels reported.
	seedVehicleMetricWithTpms(t, pool, accountID, teslaID, predecessorDay, 80, 1000.0, 300.0,
		floatPtr(35.0), floatPtr(35.5), floatPtr(36.0), floatPtr(36.5))

	// Row WITH a predecessor: FL/FR/RR all reported (deltas compute
	// normally, including a negative delta on FR to prove sign is not
	// clamped); RL absent on THIS day (must yield a NULL delta via
	// Postgres's NULL - x = NULL rule, not an error, not a fabricated 0).
	seedVehicleMetricWithTpms(t, pool, accountID, teslaID, withPredecessorDay, 70, 1050.0, 280.0,
		floatPtr(38.5), floatPtr(34.0), nil, floatPtr(40.0))

	// Row with NO predecessor row anywhere in the table (noPredecessorDay-1
	// has no vehicle_metrics row at all): every wheel reported, but the
	// self-join finds no match, so the UPDATE never touches this row -- all
	// four deltas must stay NULL.
	seedVehicleMetricWithTpms(t, pool, accountID, teslaID, noPredecessorDay, 50, 1500.0, 250.0,
		floatPtr(38.0), floatPtr(36.0), floatPtr(38.1), floatPtr(38.2))

	assertBackfilled := func(t *testing.T) {
		t.Helper()

		fl, fr, rl, rr, ok := fetchTpmsDeltaCalc(t, pool, accountID, teslaID, withPredecessorDay)
		if !ok {
			t.Fatal("expected the with-predecessor vehicle_metrics row to still exist")
		}
		assertFloatPtr(t, "withPredecessorDay FL delta", fl, floatPtr(3.5))  // 38.5 - 35.0
		assertFloatPtr(t, "withPredecessorDay FR delta", fr, floatPtr(-1.5)) // 34.0 - 35.5, negative -- sign not clamped
		assertFloatPtr(t, "withPredecessorDay RL delta", rl, nil)            // absent on cur -- NULL, never 0
		assertFloatPtr(t, "withPredecessorDay RR delta", rr, floatPtr(3.5))  // 40.0 - 36.5

		fl, fr, rl, rr, ok = fetchTpmsDeltaCalc(t, pool, accountID, teslaID, noPredecessorDay)
		if !ok {
			t.Fatal("expected the no-predecessor vehicle_metrics row to still exist")
		}
		if fl != nil || fr != nil || rl != nil || rr != nil {
			t.Errorf("noPredecessorDay: want all four deltas nil (no predecessor row), got fl=%v fr=%v rl=%v rr=%v", fl, fr, rl, rr)
		}
	}

	// --- When: the migration is applied ---
	if _, err := provider.ApplyVersion(ctx, tpmsVarianceMigrationVersion, true); err != nil {
		t.Fatalf("applying tpms-pressure-variance migration: %v", err)
	}

	// --- Then ---
	assertBackfilled(t)

	// --- Down/Up round-trip (mirrors 20260908000002's own precedent): Down
	// only drops the four columns (it does not un-backfill any other
	// table), and Up re-adds them and re-runs the backfill against the
	// SAME, untouched predecessor rows -- so the same assertions must hold
	// again. ---
	if _, err := provider.ApplyVersion(ctx, tpmsVarianceMigrationVersion, false); err != nil {
		t.Fatalf("rolling back tpms-pressure-variance migration (round-trip Down): %v", err)
	}
	if _, err := provider.ApplyVersion(ctx, tpmsVarianceMigrationVersion, true); err != nil {
		t.Fatalf("re-applying tpms-pressure-variance migration (round-trip Up): %v", err)
	}
	assertBackfilled(t)

	// t.Cleanup (registered above) re-applies the migration (a no-op here,
	// since the round-trip above already left it applied) so every other
	// test in this package sees the fully-migrated schema regardless of how
	// this test finished.
}
