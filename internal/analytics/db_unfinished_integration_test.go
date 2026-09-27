// File db_unfinished_integration_test.go holds this module's
// TEST_DATABASE_URL-gated DB-integration tests for
// UnfinishedReader.UnfinishedForDate (reader.go, analytics.go). It runs
// against the live Postgres this package's TestMain provisions
// (testdb_test.go) -- self-skipping when no database is reachable, so
// `go test ./...` stays green without one.
//
// The expected values were fixed before the implementation existed. The
// empty-input case lives in reader_test.go instead: it proves the store is
// never called, which a database-backed test cannot observe.
//
// All fixtures use tesla_id values in the 920xxx range to avoid colliding
// with fixtures other test files in this package already own (mirrors this
// module's existing per-file id-range convention, e.g.
// db_gap_writer_integration_test.go's 910xxx range).
package analytics

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedUnfinishedVehicleMetric inserts one analytics.vehicle_metrics row for
// (teslaID, metricDate), carrying only placeholder values for the raw NOT
// NULL columns every row must have regardless -- mirrors
// db_monthly_sync_integration_test.go's seedMonthlyVehicleMetric. No public
// writer creates a bare vehicle_metrics row on demand.
func seedUnfinishedVehicleMetric(t *testing.T, pool *pgxpool.Pool, teslaID int64, metricDate time.Time) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO analytics.vehicle_metrics (
			tesla_id, metric_date, battery_level_pct, odometer_km, battery_range_km, flagged
		) VALUES ($1, $2, 50, 0, 0, false)`,
		teslaID, dateFrom(metricDate),
	)
	if err != nil {
		t.Fatalf("seeding vehicle_metrics: %v", err)
	}
}

// cleanupUnfinishedVehicleMetrics deletes every analytics.vehicle_metrics
// row in the 920xxx tesla_id range this file's fixtures use, before AND
// after the test -- mirrors db_gap_writer_integration_test.go's
// cleanupChargeGaps: purging on the way in undoes anything an earlier
// interrupted run left behind, and purging on the way out keeps the shared
// database tidy for the next run.
func cleanupUnfinishedVehicleMetrics(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	purge := func() {
		_, _ = pool.Exec(context.Background(),
			"DELETE FROM analytics.vehicle_metrics WHERE tesla_id BETWEEN 920000 AND 920099")
	}
	purge()
	t.Cleanup(purge)
}

// TestUnfinishedForDate_DBIntegration checks UnfinishedForDate against one
// shared fixture, date D = 2026-09-20:
//
//	tesla_id | row for D-1 (2026-09-19) | row for D (2026-09-20)
//	920001   | yes                      | yes
//	920002   | yes                      | no
//	920003   | no                       | no
//
// The fixture is seeded once; every subtest below only reads, so none of
// them interferes with another.
func TestUnfinishedForDate_DBIntegration(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	cleanupUnfinishedVehicleMetrics(t, pool)

	const (
		vehicleDone      = int64(920001) // has a row for D and D-1
		vehicleStale     = int64(920002) // has a row for D-1 only
		vehicleUntracked = int64(920003) // has no row at all
		vehicleUnknown   = int64(920099) // never seeded -- an unregistered/unknown id
	)
	d := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	dMinus1 := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)

	seedUnfinishedVehicleMetric(t, pool, vehicleDone, dMinus1)
	seedUnfinishedVehicleMetric(t, pool, vehicleDone, d)
	seedUnfinishedVehicleMetric(t, pool, vehicleStale, dMinus1)
	// vehicleUntracked and vehicleUnknown get no row at all.

	r := NewUnfinishedReader(pool)

	t.Run("some done some not", func(t *testing.T) {
		got, err := r.UnfinishedForDate(ctx, []int64{vehicleDone, vehicleStale, vehicleUntracked}, d)
		if err != nil {
			t.Fatalf("UnfinishedForDate: %v", err)
		}
		want := []int64{vehicleStale, vehicleUntracked}
		assertInt64Slice(t, got, want)
	})

	t.Run("duplicate input ids are deduplicated", func(t *testing.T) {
		got, err := r.UnfinishedForDate(ctx, []int64{vehicleStale, vehicleStale, vehicleUntracked}, d)
		if err != nil {
			t.Fatalf("UnfinishedForDate: %v", err)
		}
		want := []int64{vehicleStale, vehicleUntracked}
		assertInt64Slice(t, got, want)
	})

	t.Run("unregistered unknown id is reported unfinished", func(t *testing.T) {
		got, err := r.UnfinishedForDate(ctx, []int64{vehicleDone, vehicleUnknown}, d)
		if err != nil {
			t.Fatalf("UnfinishedForDate: %v", err)
		}
		want := []int64{vehicleUnknown}
		assertInt64Slice(t, got, want)
	})

	t.Run("every requested vehicle already done", func(t *testing.T) {
		got, err := r.UnfinishedForDate(ctx, []int64{vehicleDone}, d)
		if err != nil {
			t.Fatalf("UnfinishedForDate: %v", err)
		}
		if got == nil {
			t.Error("want a non-nil empty slice, got nil")
		}
		if len(got) != 0 {
			t.Errorf("want an empty result, got %d entries: %+v", len(got), got)
		}
	})
}

// assertInt64Slice checks got equals want, element by element in order --
// UnfinishedForDate promises ascending tesla_id order, so this never sorts
// either side before comparing.
func assertInt64Slice(t *testing.T, got, want []int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
			return
		}
	}
}
