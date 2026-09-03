// File db_watermark_supercharger_migration_integration_test.go implements
// design.md §9 Test Contract T1 of
// RM39-analytics-fix-watermark-vocabulary (roadmap tier 3b, task 2.3) --
// proving migration
// 20260902000004_migrate_vehicle_metric_watermarks_source_supercharger.sql's
// actual Up/Down SQL, mirroring
// db_watermark_migration_integration_test.go's own shape and helpers exactly
// (same package, same ApplyVersion-only rationale documented in that file's
// header comment -- not repeated here). This is a separate file, not a
// second function appended to that one, purely for file-size locality: one
// file per migration under test.
//
// Runs strictly sequentially with every other test in this package (no
// t.Parallel anywhere in this file, matching the package's documented
// convention) -- it temporarily rolls the SHARED vehicle_metric_watermarks.source
// CHECK constraint back to its pre-this-migration vocabulary partway through,
// then restores it via t.Cleanup regardless of how the test's own assertions
// turn out, so every other test in this package still sees the fully-migrated
// schema.
package analytics

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestMigration_WatermarkSourceSuperchargerVocabulary implements design.md §9
// Test Contract T1 in full: the migration DELETEs only the charge_sessions
// watermark row (not renaming it), leaves its sibling sources' rows
// byte-identical, re-points the CHECK constraint's vocabulary back to
// supercharger_sessions, and a Down round-trip restores the OLD (RM31)
// constraint/comments WITHOUT resurrecting the deleted row. Every pinned
// value below (account/tesla IDs, timestamps, row counts) is copied verbatim
// from design.md §9 T1 -- never derived by running the migration first and
// recording what came out (ai/go-conventions.md §Testing "Authoring order").
func TestMigration_WatermarkSourceSuperchargerVocabulary(t *testing.T) {
	pool := newTestPool(t)
	provider := newAnalyticsMigrationProvider(t)
	ctx := context.Background()

	accountID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	const teslaID = int64(777)
	otherAccountID := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	const otherTeslaID = int64(888)

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM analytics.vehicle_metric_watermarks WHERE account_id = $1 AND tesla_id = $2`, accountID, teslaID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM analytics.vehicle_metric_watermarks WHERE account_id = $1 AND tesla_id = $2`, otherAccountID, otherTeslaID)
		// This test's own final action (the Down round-trip below) leaves the
		// migration in the "not applied" bookkeeping state, so a direct
		// re-application (no prior toggle needed) restores the fully-migrated
		// state every other test in this package expects -- unlike
		// db_watermark_migration_integration_test.go's own cleanup, which
		// requires the explicit false-then-true force sequence precisely
		// because that file's test never itself un-applies THIS migration's
		// version (design.md §9 "Cleanup").
		if _, err := provider.ApplyVersion(cleanupCtx, superchargerVocabMigrationVersion, true); err != nil {
			t.Errorf("cleanup: re-applying the RM39 tier 3b migration: %v", err)
		}
	})

	// --- Given: migrations applied through 20260828000001 only ---
	if _, err := provider.ApplyVersion(ctx, superchargerVocabMigrationVersion, false); err != nil {
		t.Fatalf("rolling back the RM39 tier 3b migration to seed the Given state: %v", err)
	}

	// Three rows, one per source, for the SAME vehicle, using the
	// pre-this-migration (RM31) vocabulary -- legal again now that the
	// migration is rolled back.
	seedWatermarkRow(t, pool, accountID, teslaID, "charge_sessions", time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC))
	seedWatermarkRow(t, pool, accountID, teslaID, "vehicle_snapshots", time.Date(2026, 8, 31, 3, 30, 0, 0, time.UTC))
	seedWatermarkRow(t, pool, accountID, teslaID, "manual_charge_entries", time.Date(2026, 8, 29, 18, 0, 0, 0, time.UTC))

	// --- When: the migration is applied ---
	if _, err := provider.ApplyVersion(ctx, superchargerVocabMigrationVersion, true); err != nil {
		t.Fatalf("applying the RM39 tier 3b migration: %v", err)
	}

	// --- Then: design.md §9 T1's pinned post-migration assertions ---

	if n := countWatermarkRows(t, pool, accountID, teslaID, "charge_sessions"); n != 0 {
		t.Errorf("charge_sessions watermark count: want 0 (the row is GONE, not renamed), got %d", n)
	}
	if got := fetchWatermarkUpdatedAt(t, pool, accountID, teslaID, "vehicle_snapshots"); !got.Equal(time.Date(2026, 8, 31, 3, 30, 0, 0, time.UTC)) {
		t.Errorf("vehicle_snapshots source_updated_at: want unchanged 2026-08-31T03:30:00Z, got %s", got)
	}
	if got := fetchWatermarkUpdatedAt(t, pool, accountID, teslaID, "manual_charge_entries"); !got.Equal(time.Date(2026, 8, 29, 18, 0, 0, 0, time.UTC)) {
		t.Errorf("manual_charge_entries source_updated_at: want unchanged 2026-08-29T18:00:00Z, got %s", got)
	}
	if n := countWatermarkRows(t, pool, accountID, teslaID, ""); n != 2 {
		t.Errorf("total watermark row count for this vehicle: want 2 (down from 3 -- exactly vehicle_snapshots and manual_charge_entries survive), got %d", n)
	}

	if err := insertWatermarkSource(t, pool, otherAccountID, otherTeslaID, "charge_sessions"); !isCheckViolation(err, "vehicle_metric_watermarks_source_check") {
		t.Errorf("INSERT with source='charge_sessions' post-migration: want a CHECK violation on vehicle_metric_watermarks_source_check, got %v", err)
	}
	if err := insertWatermarkSource(t, pool, otherAccountID, otherTeslaID, "supercharger_sessions"); err != nil {
		t.Errorf("INSERT with source='supercharger_sessions' post-migration: want success, got %v", err)
	}

	// Semantic consequence, asserted via Reconcile's own watermark method (not
	// raw SQL, design.md §9 T1): the absence of a row IS the epoch signal
	// (design D7). This runs in the FULLY post-migration state -- contrast
	// with db_watermark_migration_integration_test.go's own analogous
	// assertion, which correctly uses the same renamed constant but for ITS
	// own migration's mid-state (see that file and design.md §8).
	rec, ok := newRealRecalculator(pool).(*recalculator)
	if !ok {
		t.Fatal("newRealRecalculator did not return a *recalculator")
	}
	gotEpoch, err := rec.watermark(ctx, accountID, teslaID, sourceSuperchargerSessions)
	if err != nil {
		t.Fatalf("watermark(supercharger_sessions) post-migration: %v", err)
	}
	if !gotEpoch.IsZero() {
		t.Errorf("watermark(supercharger_sessions) post-migration: want the zero-value epoch (no row = epoch, design D7), got %s", gotEpoch)
	}

	// --- Down round-trip ---
	if _, err := provider.ApplyVersion(ctx, superchargerVocabMigrationVersion, false); err != nil {
		t.Fatalf("rolling back the RM39 tier 3b migration (round-trip): %v", err)
	}

	if err := insertWatermarkSource(t, pool, otherAccountID, otherTeslaID, "charge_sessions"); err != nil {
		t.Errorf("INSERT with source='charge_sessions' after Down: want success (old vocabulary restored), got %v", err)
	}
	// The negative assertion IS the point of this test (design.md §9's Down
	// step, mirroring 20260828000001's own T1 test): Down restores the SCHEMA
	// (constraint, comments), never the deleted DATA -- a Down that
	// resurrected the row would be a bug.
	if n := countWatermarkRows(t, pool, accountID, teslaID, "supercharger_sessions"); n != 0 {
		t.Errorf("supercharger_sessions watermark count after Down: want STILL 0 (Down does not resurrect the deleted row), got %d", n)
	}
	// Down's own DELETE, the mirror image of Up's. A supercharger_sessions
	// row was inserted above (the "Then" step's second insertWatermarkSource
	// call) to prove the new vocabulary was accepted; Down must clear it, or
	// restoring the old CHECK fails with SQLSTATE 23514 and leaves the table
	// unconstrained -- exactly the failure mode this migration's own Down
	// comment (design.md §3) documents as load-bearing, not tidying.
	if n := countWatermarkRows(t, pool, otherAccountID, otherTeslaID, "supercharger_sessions"); n != 0 {
		t.Errorf("supercharger_sessions watermark count after Down: want 0 (Down clears the rows written under the new vocabulary, mirroring Up), got %d", n)
	}

	// t.Cleanup (registered above) removes both vehicles' rows and
	// re-applies the migration so every other test in this package sees the
	// fully-migrated schema regardless of how this test finished.
}
