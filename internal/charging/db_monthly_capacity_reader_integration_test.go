// Package charging_test — integration tests for
// charging.MonthlyCapacityReader.CapacityForMonth. This is the module's
// second read of monthly_effective_capacity: it answers "what did this
// exact vehicle and month measure," never "what is the best capacity to
// use right now" -- a different read, unchanged by this file.
//
// Every fixture row is written with a direct SQL INSERT: no public writer
// in this module creates a single monthly_effective_capacity row on
// demand -- the existing writer always processes a whole month's pool of
// vehicles at once.
//
// Assertions read only CapacityForMonth's own return values, a *float64
// and a bool -- never pgtype (AGENTS.md "Testing Notes").
//
// Each test below uses its own tesla_id, in the 991301-991307 range, kept
// disjoint from every other integration test file in this package, so a
// row left behind by one test can never be read back by another.
package charging_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// seedMonthlyEffectiveCapacity inserts one monthly_effective_capacity row
// directly -- the same fixture shape db_monthly_capacity_integration_test.go
// already uses for a row no public writer builds one at a time.
// capacityKWh nil inserts a month with too little evidence to measure a
// capacity, a SQL NULL, not an absent row.
func seedMonthlyEffectiveCapacity(t *testing.T, pool *pgxpool.Pool, teslaID int64, period time.Time, capacityKWh *float64, candidateCount, sampleCount int) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO charging.monthly_effective_capacity
			(tesla_id, effective_period, effective_capacity_kwh, candidate_count, sample_count)
		VALUES ($1, $2, $3, $4, $5)`,
		teslaID, period, capacityKWh, candidateCount, sampleCount,
	)
	if err != nil {
		t.Fatalf("seeding monthly_effective_capacity: %v", err)
	}
}

// assertCapacity compares CapacityForMonth's (capacityKWh, found) result
// against the expected values, treating a nil pointer on either side as "no
// capacity" and comparing two non-nil pointers by value.
func assertCapacity(t *testing.T, gotCapacity *float64, gotFound bool, wantCapacity *float64, wantFound bool) {
	t.Helper()
	if gotFound != wantFound {
		t.Errorf("found = %v, want %v", gotFound, wantFound)
	}
	switch {
	case wantCapacity == nil && gotCapacity != nil:
		t.Errorf("capacityKWh = %v, want nil", *gotCapacity)
	case wantCapacity != nil && gotCapacity == nil:
		t.Errorf("capacityKWh = nil, want %v", *wantCapacity)
	case wantCapacity != nil && gotCapacity != nil && !floatsAlmostEqual(*gotCapacity, *wantCapacity):
		t.Errorf("capacityKWh = %v, want %v", *gotCapacity, *wantCapacity)
	}
}

func TestCapacityForMonth_NoRowReportsNotFound(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	teslaID := int64(991301)
	cleanupMonthlyEffectiveCapacity(t, pool, teslaID)

	r := charging.NewMonthlyCapacityReader(pool)
	capacityKWh, found, err := r.CapacityForMonth(ctx, teslaID, time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CapacityForMonth: %v", err)
	}
	assertCapacity(t, capacityKWh, found, nil, false)
}

func TestCapacityForMonth_ThinMonthReportsFoundWithNilCapacity(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	teslaID := int64(991302)
	cleanupMonthlyEffectiveCapacity(t, pool, teslaID)

	period := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	seedMonthlyEffectiveCapacity(t, pool, teslaID, period, nil, 2, 2)

	r := charging.NewMonthlyCapacityReader(pool)
	capacityKWh, found, err := r.CapacityForMonth(ctx, teslaID, period)
	if err != nil {
		t.Fatalf("CapacityForMonth: %v", err)
	}
	// A row exists, but the month had too little evidence to measure a
	// capacity. found must still be true here -- this is what tells the
	// caller apart from a month with no row at all.
	assertCapacity(t, capacityKWh, found, nil, true)
}

func TestCapacityForMonth_MeasuredMonthNormalizesAnyDayInMonth(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	teslaID := int64(991303)
	cleanupMonthlyEffectiveCapacity(t, pool, teslaID)

	period := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	seedMonthlyEffectiveCapacity(t, pool, teslaID, period, ptrFloat64(61.8), 5, 4)

	r := charging.NewMonthlyCapacityReader(pool)

	// The first of the month itself.
	capacityKWh, found, err := r.CapacityForMonth(ctx, teslaID, period)
	if err != nil {
		t.Fatalf("CapacityForMonth (first of month): %v", err)
	}
	assertCapacity(t, capacityKWh, found, ptrFloat64(61.8), true)

	// A day in the middle of the month: the caller passes any day inside
	// the month and still reaches the same row.
	capacityKWh, found, err = r.CapacityForMonth(ctx, teslaID, time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CapacityForMonth (mid-month): %v", err)
	}
	assertCapacity(t, capacityKWh, found, ptrFloat64(61.8), true)

	// The last day of the month: normalization must hold at the far
	// boundary too, not only near the start.
	capacityKWh, found, err = r.CapacityForMonth(ctx, teslaID, time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CapacityForMonth (last day of month): %v", err)
	}
	assertCapacity(t, capacityKWh, found, ptrFloat64(61.8), true)
}

func TestCapacityForMonth_DifferentVehicleDoesNotLeak(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	teslaA := int64(991304)
	teslaB := int64(991305)
	cleanupMonthlyEffectiveCapacity(t, pool, teslaA, teslaB)

	period := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	seedMonthlyEffectiveCapacity(t, pool, teslaA, period, ptrFloat64(61.8), 5, 4)
	seedMonthlyEffectiveCapacity(t, pool, teslaB, period, ptrFloat64(70.0), 5, 4)

	r := charging.NewMonthlyCapacityReader(pool)
	capacityKWh, found, err := r.CapacityForMonth(ctx, teslaA, period)
	if err != nil {
		t.Fatalf("CapacityForMonth: %v", err)
	}
	assertCapacity(t, capacityKWh, found, ptrFloat64(61.8), true)
}

func TestCapacityForMonth_DifferentMonthDoesNotLeak(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	teslaID := int64(991306)
	cleanupMonthlyEffectiveCapacity(t, pool, teslaID)

	march := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	february := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	seedMonthlyEffectiveCapacity(t, pool, teslaID, march, ptrFloat64(61.8), 5, 4)
	seedMonthlyEffectiveCapacity(t, pool, teslaID, february, ptrFloat64(55.0), 5, 4)

	r := charging.NewMonthlyCapacityReader(pool)
	capacityKWh, found, err := r.CapacityForMonth(ctx, teslaID, march)
	if err != nil {
		t.Fatalf("CapacityForMonth: %v", err)
	}
	// The same vehicle has a row for both months. Asking for March must
	// never return February's row -- an exact-month match, not "any row
	// this vehicle has."
	assertCapacity(t, capacityKWh, found, ptrFloat64(61.8), true)
}

func TestCapacityForMonth_AdjacentMonthWithNoRowIsNotFound(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	teslaID := int64(991307)
	cleanupMonthlyEffectiveCapacity(t, pool, teslaID)

	march := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	seedMonthlyEffectiveCapacity(t, pool, teslaID, march, ptrFloat64(61.8), 5, 4)

	r := charging.NewMonthlyCapacityReader(pool)
	april := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	capacityKWh, found, err := r.CapacityForMonth(ctx, teslaID, april)
	if err != nil {
		t.Fatalf("CapacityForMonth: %v", err)
	}
	assertCapacity(t, capacityKWh, found, nil, false)
}
