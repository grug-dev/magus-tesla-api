// File db_integration_test.go holds this module's DATABASE_URL-gated
// DB-integration tests (RM29-analytics-add-vehicle-metrics, Wave 6) — this
// module's FIRST-EVER DB-backed test file, run against a live Postgres
// provisioned by TestMain (testdb_test.go), auto-provisioned via
// testcontainers-go when DATABASE_URL is unset/unreachable
// (ai/go-conventions.md §persistence).
//
// # Status of this file relative to tasks.md Wave 6
//
// Only task 6.4 (the fetch-behaviour coverage rescue: exact lookback window,
// accountID/teslaID scoping, error propagation) is implemented here. Tasks
// 6.1, 6.2 and 6.3 — the TestRecalculate_FixtureA/B/C, TestReconcile_* and
// TestReader_*_ReadsBackWhatRecalculateWrote / ..._ExcludeFixtureCRow tests —
// are NOT implemented. This is reported to the leader as BLOCKED, not
// silently skipped or faked, for two independent, compounding reasons found
// while implementing this file:
//
//  1. No public seeding path exists for two of the three fixture sources.
//     telemetry has no exported writer for a single vehicle_snapshots row —
//     the only write path is Collector.CollectAll, which requires a live (or
//     fully Tesla-DTO-faked) tesla.VehicleService AND a full account.Service
//     fake, and would force this file to import internal/tesla — a package
//     internal/analytics/AGENTS.md explicitly lists under "Must NOT import"
//     ("this module never talks to the Fleet API directly"). Supercharger
//     sessions have NO public writer at all (upsertSuperchargerSession is
//     unexported, reachable only from inside CollectAll's ChargingHistory
//     step). The one source that DOES have a clean public writer —
//     charging.NewWriter(pool).Create — is usable, but is useless alone:
//     every one of Recalculate's Fixture A/B/C rows requires a
//     telemetry.Snapshot to exist first (deriveVehicleMetrics iterates
//     snapshots; with zero snapshots it emits zero rows).
//  2. Even setting aside (1), this package's own auto-provisioned test
//     database cannot contain telemetry's/charging's tables at all. Go's
//     //go:embed directive cannot use ".." to reach outside the embedding
//     file's own directory tree, so testdb_test.go's
//     `//go:embed db/migrations/*.sql` can only ever see
//     internal/analytics/db/migrations — never
//     internal/telemetry/db/migrations or internal/charging/db/migrations.
//     A freshly auto-provisioned container for `go test ./internal/analytics/...`
//     therefore has ONLY vehicle_metrics/vehicle_metric_watermarks — no
//     vehicle_snapshots, supercharger_sessions or manual_charge_entries table
//     exists in it AT ALL. Any seeding strategy for those two tables —
//     direct SQL included — would fail with "relation does not exist" in
//     that (the common local/CI) environment; it could only work by
//     accident against a DATABASE_URL pointing at an already-fully-migrated
//     shared database, which is not something this test file can rely on.
//
// Recommendation to the leader: this is a real gap in the Wave 1/Wave 2 task
// list, not something an analytics-sandboxed worker can resolve alone. Two
// possible fixes, either outside this module's sandbox: (a) a small,
// explicitly test-only exported seeding helper added to telemetry (e.g.
// telemetry.NewTestWriter or an exported insertSnapshot-equivalent, guarded
// the same way testdb itself is guarded to _test.go-only use), or (b) a
// shared cross-module test-harness package (like internal/testdb, but
// embedding and applying every module's migrations together) that
// internal/analytics's DB-integration tests could import. Neither requires
// touching this module's own production code or its AGENTS.md import
// boundary once built.
//
// The tests below (task 6.4) need neither telemetry's nor charging's actual
// stored data — they fake all three of Recalculate's source ports (the same
// fakeTelemetryReader / fakeSuperchargerReader / fakeManualReader already
// defined in reader_test.go, same package, reused rather than redefined) and
// only need vehicle_metrics' OWN schema, which this package's own migrations
// do provide — so they are unaffected by either blocker above.
package analytics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// newTestPool builds a pgxpool.Pool against the test Postgres provisioned by
// TestMain (testdb_test.go). Mirrors internal/telemetry/db_integration_test.go's
// newTestStore skip-when-no-DB convention.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDSN == "" {
		t.Skip("no test Postgres: set DATABASE_URL or start Docker to run the DB-backed tests")
	}
	pool, err := pgxpool.New(context.Background(), testDSN)
	if err != nil {
		t.Fatalf("connecting to Postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// cleanupVehicleMetrics removes any vehicle_metrics/vehicle_metric_watermarks
// rows this test created, scoped to one (accountID, teslaID), so a shared DB
// stays tidy across test runs (mirrors telemetry/db_integration_test.go's
// cleanupVehicle one level up).
func cleanupVehicleMetrics(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, "DELETE FROM vehicle_metrics WHERE account_id = $1 AND tesla_id = $2", accountID, teslaID)
		_, _ = pool.Exec(ctx, "DELETE FROM vehicle_metric_watermarks WHERE account_id = $1 AND tesla_id = $2", accountID, teslaID)
	})
}

// fixtureAPair returns a (predecessor, current) telemetry.Snapshot pair
// shaped like design.md's Test Contract Fixture A, scoped to the given
// vehicle — enough for deriveVehicleMetrics to emit exactly one row (D9/D10),
// so Recalculate's UPSERT+DELETE transaction has real work to do. The exact
// derived values are NOT asserted by the tests in this file (that is
// task 6.1's job, blocked — see the file-level comment above); only that the
// write succeeds and the three source ports were called with the right
// window/scoping, or that an error from one of them propagates.
func fixtureAPair(accountID uuid.UUID, teslaID int64) (prev, cur telemetry.Snapshot) {
	prev = telemetry.Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 10, 3, 30, 0, 0, time.UTC),
		CapturedDate:    time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		OdometerKm:      1000.0,
		BatteryLevelPct: 80,
		BatteryRangeKm:  300.0,
	}
	usedPct := 15
	distanceKm := 50.0
	daysSpanned := 1
	cur = telemetry.Snapshot{
		AccountID:              accountID,
		TeslaID:                teslaID,
		CapturedAt:             time.Date(2026, 8, 11, 3, 30, 0, 0, time.UTC),
		CapturedDate:           time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC),
		OdometerKm:             1050.0,
		BatteryLevelPct:        65,
		BatteryRangeKm:         280.0,
		DistanceTraveledKmCalc: &distanceKm,
		BatteryUsedPctCalc:     &usedPct,
		DaysSpannedCalc:        &daysSpanned,
	}
	return prev, cur
}

// TestRecalculate_FetchWindows_MatchLookbackShape asserts task 6.4(a): the
// exact [start, end] lookback window Recalculate passes to each of the three
// source ports (design.md D11 — telemetry: start-1/end+1; supercharger:
// start-1/end+2; manual: start-1/end, no tail). Uses fakes, not the live DB,
// for the source ports (see file-level comment); only vehicle_metrics itself
// is real, backing the UPSERT/DELETE half of Recalculate.
func TestRecalculate_FetchWindows_MatchLookbackShape(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	const teslaID = int64(910001)
	cleanupVehicleMetrics(t, pool, accountID, teslaID)

	prev, cur := fixtureAPair(accountID, teslaID)
	fakeTelemetry := &fakeTelemetryReader{snapshots: []telemetry.Snapshot{prev, cur}}
	fakeSupercharger := &fakeSuperchargerReader{}
	fakeManual := &fakeManualReader{}

	rec := NewRecalculator(pool, fakeTelemetry, fakeSupercharger, fakeManual)

	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	if err := rec.Recalculate(context.Background(), accountID, teslaID, start, end); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	wantTelemetryStart := start.AddDate(0, 0, -1)
	wantTelemetryEnd := end.AddDate(0, 0, 1)
	if !fakeTelemetry.gotBetweenStart.Equal(wantTelemetryStart) || !fakeTelemetry.gotBetweenEnd.Equal(wantTelemetryEnd) {
		t.Errorf("telemetry window: want [%s, %s], got [%s, %s]",
			wantTelemetryStart, wantTelemetryEnd, fakeTelemetry.gotBetweenStart, fakeTelemetry.gotBetweenEnd)
	}

	wantSuperchargerStart := start.AddDate(0, 0, -1)
	wantSuperchargerEnd := end.AddDate(0, 0, 2)
	if !fakeSupercharger.gotBetweenStart.Equal(wantSuperchargerStart) || !fakeSupercharger.gotBetweenEnd.Equal(wantSuperchargerEnd) {
		t.Errorf("supercharger window: want [%s, %s], got [%s, %s]",
			wantSuperchargerStart, wantSuperchargerEnd, fakeSupercharger.gotBetweenStart, fakeSupercharger.gotBetweenEnd)
	}

	wantManualStart := start.AddDate(0, 0, -1)
	wantManualEnd := end
	if !fakeManual.gotBetweenStart.Equal(wantManualStart) || !fakeManual.gotBetweenEnd.Equal(wantManualEnd) {
		t.Errorf("manual window: want [%s, %s], got [%s, %s]",
			wantManualStart, wantManualEnd, fakeManual.gotBetweenStart, fakeManual.gotBetweenEnd)
	}
}

// TestRecalculate_AccountIDScoping_PassedToEveryPort asserts task 6.4(b):
// accountID/teslaID scoping reaches every one of the three source ports —
// the coverage rescue for the offline test tasks.md records as removed
// (TestConsumedByDay_AccountIDScoping_PassedToEveryPort), now asserted
// against Recalculate (its new fetcher) instead of the old live ConsumedByDay.
func TestRecalculate_AccountIDScoping_PassedToEveryPort(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	const teslaID = int64(910002)
	cleanupVehicleMetrics(t, pool, accountID, teslaID)

	prev, cur := fixtureAPair(accountID, teslaID)
	fakeTelemetry := &fakeTelemetryReader{snapshots: []telemetry.Snapshot{prev, cur}}
	fakeSupercharger := &fakeSuperchargerReader{}
	fakeManual := &fakeManualReader{}

	rec := NewRecalculator(pool, fakeTelemetry, fakeSupercharger, fakeManual)

	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	end := start
	if err := rec.Recalculate(context.Background(), accountID, teslaID, start, end); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	if fakeTelemetry.gotAccountID != accountID || fakeTelemetry.gotTeslaID != teslaID {
		t.Errorf("telemetry scoping: want (%s, %d), got (%s, %d)",
			accountID, teslaID, fakeTelemetry.gotAccountID, fakeTelemetry.gotTeslaID)
	}
	if fakeSupercharger.gotAccountID != accountID || fakeSupercharger.gotTeslaID != teslaID {
		t.Errorf("supercharger scoping: want (%s, %d), got (%s, %d)",
			accountID, teslaID, fakeSupercharger.gotAccountID, fakeSupercharger.gotTeslaID)
	}
	if fakeManual.gotAccountID != accountID || fakeManual.gotTeslaID != teslaID {
		t.Errorf("manual scoping: want (%s, %d), got (%s, %d)",
			accountID, teslaID, fakeManual.gotAccountID, fakeManual.gotTeslaID)
	}
}

// TestRecalculate_TelemetryError_Propagates asserts task 6.4(c) for the
// telemetry source: an error from SnapshotsByVehicleBetween propagates from
// Recalculate rather than being swallowed, and short-circuits before the
// supercharger/manual ports are ever called.
func TestRecalculate_TelemetryError_Propagates(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	const teslaID = int64(910003)
	cleanupVehicleMetrics(t, pool, accountID, teslaID)

	sentinel := errors.New("boom: telemetry unavailable")
	fakeTelemetry := &fakeTelemetryReader{err: sentinel}
	fakeSupercharger := &fakeSuperchargerReader{}
	fakeManual := &fakeManualReader{}

	rec := NewRecalculator(pool, fakeTelemetry, fakeSupercharger, fakeManual)

	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	err := rec.Recalculate(context.Background(), accountID, teslaID, start, start)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Recalculate error: want wrapping %v, got %v", sentinel, err)
	}
	if fakeSupercharger.gotAccountID != uuid.Nil {
		t.Errorf("supercharger port must not be called once telemetry fails, got accountID=%s", fakeSupercharger.gotAccountID)
	}
	if fakeManual.gotAccountID != uuid.Nil {
		t.Errorf("manual port must not be called once telemetry fails, got accountID=%s", fakeManual.gotAccountID)
	}
}

// TestRecalculate_SuperchargerError_Propagates asserts task 6.4(c) for the
// supercharger source: an error from SuperchargerSessionsByVehicleBetween
// propagates, short-circuiting before the manual port is called.
func TestRecalculate_SuperchargerError_Propagates(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	const teslaID = int64(910004)
	cleanupVehicleMetrics(t, pool, accountID, teslaID)

	sentinel := errors.New("boom: supercharger unavailable")
	fakeTelemetry := &fakeTelemetryReader{}
	fakeSupercharger := &fakeSuperchargerReader{err: sentinel}
	fakeManual := &fakeManualReader{}

	rec := NewRecalculator(pool, fakeTelemetry, fakeSupercharger, fakeManual)

	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	err := rec.Recalculate(context.Background(), accountID, teslaID, start, start)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Recalculate error: want wrapping %v, got %v", sentinel, err)
	}
	if fakeManual.gotAccountID != uuid.Nil {
		t.Errorf("manual port must not be called once supercharger fails, got accountID=%s", fakeManual.gotAccountID)
	}
}

// TestRecalculate_ManualError_Propagates asserts task 6.4(c) for the manual
// charge-entries source: an error from ListEntriesByVehicleBetween
// propagates from Recalculate rather than being swallowed.
func TestRecalculate_ManualError_Propagates(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	const teslaID = int64(910005)
	cleanupVehicleMetrics(t, pool, accountID, teslaID)

	sentinel := errors.New("boom: manual entries unavailable")
	fakeTelemetry := &fakeTelemetryReader{}
	fakeSupercharger := &fakeSuperchargerReader{}
	fakeManual := &fakeManualReader{err: sentinel}

	rec := NewRecalculator(pool, fakeTelemetry, fakeSupercharger, fakeManual)

	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	err := rec.Recalculate(context.Background(), accountID, teslaID, start, start)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Recalculate error: want wrapping %v, got %v", sentinel, err)
	}
}

// Note for whoever picks this up after a leader decision: charging.Entry /
// charging.NewWriter(pool).Create IS a clean, in-bounds public writer for
// manual charge entries — the one source of the three that has one. It is
// not exercised in this file because a manual entry alone cannot drive
// Recalculate to a non-empty result without a real telemetry.Snapshot too
// (see the file-level comment); it becomes useful again once the telemetry
// snapshot / supercharger session seeding blocker above is resolved.
