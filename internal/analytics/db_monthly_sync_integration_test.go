// File db_monthly_sync_integration_test.go holds this module's
// TEST_DATABASE_URL-gated DB-integration tests for MonthlySyncer.SyncMonth
// (monthly_sync.go). It runs against the live Postgres this package's
// TestMain provisions (testdb_test.go) -- self-skipping when no database is
// reachable, so `go test ./...` stays green without one.
//
// Every test seeds analytics.vehicle_metrics and charging's
// monthly_effective_capacity with a direct SQL INSERT. Neither table has a
// public writer that creates one row on demand -- this module's own
// established fixture pattern for that case (see db_integration_test.go).
// The charging-aggregate tests below seed one more way each: an external
// charge through the real charging.NewWriter(pool).Create (a public writer
// exists for it), and a bare Supercharger session with the same
// seedChargeSession helper db_integration_test.go already declares in this
// package -- charge_sessions has no writer that can set a specific ending
// battery percentage on demand.
//
// The database is shared across test runs, so every test purges its own
// tesla_id both before and after running -- a row left behind by an earlier
// interrupted run must never be counted by a later one.
//
// Three tests share tesla_id 555001, because they share one seeded month by
// design. That is safe only while they run in order. Do NOT add t.Parallel()
// here: the shared id would let one test purge another's rows mid-run.
package analytics

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// seedMonthlyVehicleMetric inserts one analytics.vehicle_metrics row with
// the three columns SyncMonth actually reads (metric_date,
// distance_traveled_km_calc, consumed_pct), plus the raw NOT NULL columns
// every row must carry regardless. The raw values are placeholders -- this
// derivation never reads them.
func seedMonthlyVehicleMetric(t *testing.T, pool *pgxpool.Pool, teslaID int64, metricDate time.Time, distanceKm, consumedPct float64) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO analytics.vehicle_metrics (
			tesla_id, metric_date, battery_level_pct, odometer_km, battery_range_km,
			distance_traveled_km_calc, consumed_pct, flagged
		) VALUES ($1, $2, 50, 0, 0, $3, $4, false)`,
		teslaID, dateFrom(metricDate), distanceKm, consumedPct,
	)
	if err != nil {
		t.Fatalf("seeding vehicle_metrics: %v", err)
	}
}

// seedMonthlyEffectiveCapacity inserts one charging.monthly_effective_capacity
// row directly, following this module's existing cross-module fixture
// convention. capacityKWh nil seeds a real row that measured nothing this
// month -- not the same thing as no row at all.
func seedMonthlyEffectiveCapacity(t *testing.T, pool *pgxpool.Pool, teslaID int64, period time.Time, capacityKWh *float64) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO charging.monthly_effective_capacity (tesla_id, effective_period, effective_capacity_kwh)
		VALUES ($1, $2, $3)`,
		teslaID, dateFrom(period), capacityKWh,
	)
	if err != nil {
		t.Fatalf("seeding monthly_effective_capacity: %v", err)
	}
}

// cleanupMonthlySyncFixtures removes every row this file's tests can write
// for one teslaID, across this module's own schema and every charging table
// a fixture here seeds directly or through charging's own writer. Called
// before AND after each test, mirroring db_integration_test.go's
// cleanupVehicleMetrics: purging on the way in undoes anything an earlier
// interrupted run left behind, and purging on the way out keeps the shared
// database tidy for the next run.
func cleanupMonthlySyncFixtures(t *testing.T, pool *pgxpool.Pool, teslaID int64) {
	t.Helper()
	purge := func() {
		ctx := context.Background()
		for _, stmt := range []string{
			"DELETE FROM analytics.vehicle_monthly_metrics WHERE tesla_id = $1",
			"DELETE FROM analytics.vehicle_metrics WHERE tesla_id = $1",
			"DELETE FROM charging.monthly_effective_capacity WHERE tesla_id = $1",
			"DELETE FROM charging.manual_charge_entries WHERE tesla_id = $1",
			"DELETE FROM charging.supercharger_sessions WHERE tesla_id = $1",
		} {
			_, _ = pool.Exec(ctx, stmt, teslaID)
		}
	}
	purge()
	t.Cleanup(purge)
}

// newTestMonthlySyncer builds a MonthlySyncer over the shared test pool and
// real charging readers -- the same construction NewMonthlySyncer performs
// in production, minus the composition root.
func newTestMonthlySyncer(pool *pgxpool.Pool) MonthlySyncer {
	return NewMonthlySyncer(pool, charging.NewMonthlyCapacityReader(pool), charging.NewReader(pool), charging.NewSuperchargerSessionAnalyticsReader(pool))
}

// almostEqual compares two float64 results with a small tolerance, so a
// harmless floating-point rounding difference from a database round-trip
// never fails a test that would pass on the exact math.
func almostEqual(a, b float64) bool {
	const epsilon = 1e-9
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < epsilon
}

// march2026MixedFixture seeds the five vehicle_metrics rows the "mixed
// weekday/weekend month" scenario below is built on: two weekdays, two
// weekend days, and one more weekday with real distance but zero
// consumption (a day that must count toward the day count but not toward
// the efficiency ratio).
func march2026MixedFixture(t *testing.T, pool *pgxpool.Pool, teslaID int64) {
	t.Helper()
	seedMonthlyVehicleMetric(t, pool, teslaID, time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC), 100, 20) // Monday
	seedMonthlyVehicleMetric(t, pool, teslaID, time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC), 50, 10)  // Tuesday
	seedMonthlyVehicleMetric(t, pool, teslaID, time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC), 80, 16)  // Saturday
	seedMonthlyVehicleMetric(t, pool, teslaID, time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC), 40, 8)   // Sunday
	seedMonthlyVehicleMetric(t, pool, teslaID, time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC), 15, 0)   // Monday, zero consumption
}

// wantMixedFixtureFigures is what march2026MixedFixture's five rows must
// derive to: distance and consumption sums include all five days, but the
// efficiency ratio (KmPerPctCalc) excludes the zero-consumption day, so it
// divides 270/54, not 285/54.
var wantMixedFixtureFigures = VehicleMonthlyMetrics{
	AllDistanceKm: 285, AllConsumedPct: 54, AllKmPerPctCalc: 5.0, AllDayCount: 5,
	WeekdayDistanceKm: 165, WeekdayConsumedPct: 30, WeekdayKmPerPctCalc: 5.0, WeekdayDayCount: 3,
	WeekendDistanceKm: 120, WeekendConsumedPct: 24, WeekendKmPerPctCalc: 5.0, WeekendDayCount: 2,
}

// assertCoreFigures checks the twelve distance/consumption/efficiency/count
// fields SyncMonth derives from vehicle_metrics, against a caller-supplied
// expectation. It never looks at capacity, currency, or the charging-source
// fields -- those have their own assert helpers below.
func assertCoreFigures(t *testing.T, got, want VehicleMonthlyMetrics) {
	t.Helper()

	floats := []struct {
		name      string
		got, want float64
	}{
		{"AllDistanceKm", got.AllDistanceKm, want.AllDistanceKm},
		{"AllConsumedPct", got.AllConsumedPct, want.AllConsumedPct},
		{"AllKmPerPctCalc", got.AllKmPerPctCalc, want.AllKmPerPctCalc},
		{"WeekdayDistanceKm", got.WeekdayDistanceKm, want.WeekdayDistanceKm},
		{"WeekdayConsumedPct", got.WeekdayConsumedPct, want.WeekdayConsumedPct},
		{"WeekdayKmPerPctCalc", got.WeekdayKmPerPctCalc, want.WeekdayKmPerPctCalc},
		{"WeekendDistanceKm", got.WeekendDistanceKm, want.WeekendDistanceKm},
		{"WeekendConsumedPct", got.WeekendConsumedPct, want.WeekendConsumedPct},
		{"WeekendKmPerPctCalc", got.WeekendKmPerPctCalc, want.WeekendKmPerPctCalc},
	}
	for _, f := range floats {
		if !almostEqual(f.got, f.want) {
			t.Errorf("%s = %v, want %v", f.name, f.got, f.want)
		}
	}

	counts := []struct {
		name      string
		got, want int
	}{
		{"AllDayCount", got.AllDayCount, want.AllDayCount},
		{"WeekdayDayCount", got.WeekdayDayCount, want.WeekdayDayCount},
		{"WeekendDayCount", got.WeekendDayCount, want.WeekendDayCount},
	}
	for _, c := range counts {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}

// assertCapacityFields checks the two capacity fields against an expected
// kWh figure and measured flag.
func assertCapacityFields(t *testing.T, got VehicleMonthlyMetrics, wantKWh float64, wantMeasured bool) {
	t.Helper()
	if got.CapacityMeasured != wantMeasured {
		t.Errorf("CapacityMeasured = %v, want %v", got.CapacityMeasured, wantMeasured)
	}
	if !almostEqual(got.CapacityKWh, wantKWh) {
		t.Errorf("CapacityKWh = %v, want %v", got.CapacityKWh, wantKWh)
	}
}

// assertZeroChargingFields checks that Currency reads its default and every
// charging-source field (ExtAC*, ExtDC*, SC*) still holds its documented
// zero value -- the state every row must be in until a later change computes
// real charging aggregates.
func assertZeroChargingFields(t *testing.T, got VehicleMonthlyMetrics) {
	t.Helper()
	if got.Currency != "COP" {
		t.Errorf("Currency = %q, want COP", got.Currency)
	}
	zeroDist := EndingBatteryDist{}
	groups := []struct {
		name           string
		energyKWh      float64
		cost           float64
		count          int
		endingBattDist EndingBatteryDist
	}{
		{"ExtAC", got.ExtACEnergyKWh, got.ExtACCost, got.ExtACEntryCount, got.ExtACEndingBatteryDist},
		{"ExtDC", got.ExtDCEnergyKWh, got.ExtDCCost, got.ExtDCEntryCount, got.ExtDCEndingBatteryDist},
		{"SC", got.SCEnergyKWh, got.SCCost, got.SCSessionCount, got.SCEndingBatteryDist},
	}
	for _, g := range groups {
		if g.energyKWh != 0 || g.cost != 0 || g.count != 0 || g.endingBattDist != zeroDist {
			t.Errorf("%s* fields are not zero-filled: energyKWh=%v cost=%v count=%v endingBatteryDist=%+v",
				g.name, g.energyKWh, g.cost, g.count, g.endingBattDist)
		}
	}
}

// TestSyncMonth_MixedWeekdayWeekendMonth_MatchesPureDerivation proves the
// full path -- SQL fetch, Go derivation, SQL upsert, SQL read-back --
// reproduces the same answer as the pure-math derivation, for a month with
// both weekday and weekend days and one zero-consumption day. No capacity
// row exists, so capacity must read as zero/not-measured.
func TestSyncMonth_MixedWeekdayWeekendMonth_MatchesPureDerivation(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	teslaID := int64(555001)
	cleanupMonthlySyncFixtures(t, pool, teslaID)

	march2026MixedFixture(t, pool, teslaID)

	syncer := newTestMonthlySyncer(pool)
	got, err := syncer.SyncMonth(ctx, teslaID, time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("SyncMonth: %v", err)
	}

	wantPeriod := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if !got.Period.Equal(wantPeriod) {
		t.Errorf("Period = %v, want %v", got.Period, wantPeriod)
	}
	assertCoreFigures(t, got, wantMixedFixtureFigures)
	assertCapacityFields(t, got, 0, false)
	assertZeroChargingFields(t, got)
}

// TestSyncMonth_EmptyMonth_WritesZeroRow proves a vehicle with no
// vehicle_metrics rows at all for the month still gets a stored row, with
// every count and figure visibly at zero -- never a missing row, which
// would be indistinguishable from "not synced yet".
func TestSyncMonth_EmptyMonth_WritesZeroRow(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	teslaID := int64(555002)
	cleanupMonthlySyncFixtures(t, pool, teslaID)

	syncer := newTestMonthlySyncer(pool)
	got, err := syncer.SyncMonth(ctx, teslaID, time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("SyncMonth: %v", err)
	}

	wantPeriod := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	if !got.Period.Equal(wantPeriod) {
		t.Errorf("Period = %v, want %v", got.Period, wantPeriod)
	}
	assertCoreFigures(t, got, VehicleMonthlyMetrics{})
	assertCapacityFields(t, got, 0, false)
	assertZeroChargingFields(t, got)
}

// TestSyncMonth_MeasuredCapacity_CopiedThroughFromMidMonthDay proves a
// measured pack capacity is copied through correctly, and that passing a
// mid-month day (not the first) still normalizes to the month's first day
// -- both the period the row is stored under and the figures computed for
// it.
func TestSyncMonth_MeasuredCapacity_CopiedThroughFromMidMonthDay(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	teslaID := int64(555001)
	cleanupMonthlySyncFixtures(t, pool, teslaID)

	march2026MixedFixture(t, pool, teslaID)
	measuredKWh := 61.8
	seedMonthlyEffectiveCapacity(t, pool, teslaID, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), &measuredKWh)

	syncer := newTestMonthlySyncer(pool)
	got, err := syncer.SyncMonth(ctx, teslaID, time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("SyncMonth: %v", err)
	}

	wantPeriod := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if !got.Period.Equal(wantPeriod) {
		t.Errorf("Period = %v, want %v", got.Period, wantPeriod)
	}
	assertCoreFigures(t, got, wantMixedFixtureFigures)
	assertCapacityFields(t, got, 61.8, true)
	assertZeroChargingFields(t, got)
}

// TestSyncMonth_CapacityRowWithNoMeasurement_CollapsesToZeroNotMeasured
// proves a month where charging's own capacity table has a row but no
// measured number (too little evidence that month) reads the same as no
// row at all: capacity zero, not measured. This loss of distinction was
// confirmed and accepted at the database design gate -- it is not a bug.
func TestSyncMonth_CapacityRowWithNoMeasurement_CollapsesToZeroNotMeasured(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	teslaID := int64(555003)
	cleanupMonthlySyncFixtures(t, pool, teslaID)

	seedMonthlyEffectiveCapacity(t, pool, teslaID, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), nil)

	syncer := newTestMonthlySyncer(pool)
	got, err := syncer.SyncMonth(ctx, teslaID, time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("SyncMonth: %v", err)
	}
	assertCapacityFields(t, got, 0, false)
}

// TestSyncMonth_NoCapacityRowAtAll_CollapsesSameAsNoMeasurement proves the
// two "nothing measured" cases charging can report -- no row, and a row
// with a NULL measurement -- produce the exact same analytics result. This
// confirms the collapse above is symmetric, not a coincidence of one case.
func TestSyncMonth_NoCapacityRowAtAll_CollapsesSameAsNoMeasurement(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	teslaID := int64(555004)
	cleanupMonthlySyncFixtures(t, pool, teslaID)

	syncer := newTestMonthlySyncer(pool)
	got, err := syncer.SyncMonth(ctx, teslaID, time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("SyncMonth: %v", err)
	}
	assertCapacityFields(t, got, 0, false)
}

// TestSyncMonth_RerunSameMonth_IsIdempotentAndUpsertsInPlace proves running
// SyncMonth twice for the same vehicle and month rewrites one row rather
// than appending a second one, and that the two results agree on every
// field except UpdatedAt, which must advance.
func TestSyncMonth_RerunSameMonth_IsIdempotentAndUpsertsInPlace(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	teslaID := int64(555001)
	cleanupMonthlySyncFixtures(t, pool, teslaID)

	march2026MixedFixture(t, pool, teslaID)

	syncer := newTestMonthlySyncer(pool)
	period := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	first, err := syncer.SyncMonth(ctx, teslaID, period)
	if err != nil {
		t.Fatalf("first SyncMonth: %v", err)
	}
	second, err := syncer.SyncMonth(ctx, teslaID, period)
	if err != nil {
		t.Fatalf("second SyncMonth: %v", err)
	}

	for _, got := range []VehicleMonthlyMetrics{first, second} {
		assertCoreFigures(t, got, wantMixedFixtureFigures)
		assertCapacityFields(t, got, 0, false)
		assertZeroChargingFields(t, got)
	}

	if first.TeslaID != second.TeslaID {
		t.Errorf("TeslaID changed between calls: %d -> %d", first.TeslaID, second.TeslaID)
	}
	if !first.Period.Equal(second.Period) {
		t.Errorf("Period changed between calls: %v -> %v", first.Period, second.Period)
	}
	if !first.CreatedAt.Equal(second.CreatedAt) {
		t.Errorf("CreatedAt changed between calls -- it must record only the first sync: %v -> %v", first.CreatedAt, second.CreatedAt)
	}
	if !second.UpdatedAt.After(first.UpdatedAt) {
		t.Errorf("UpdatedAt did not advance on the second call: first=%v, second=%v", first.UpdatedAt, second.UpdatedAt)
	}

	var rowCount int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM analytics.vehicle_monthly_metrics WHERE tesla_id = $1 AND period = $2`,
		teslaID, dateFrom(period),
	).Scan(&rowCount)
	if err != nil {
		t.Fatalf("counting vehicle_monthly_metrics rows: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("row count for (tesla_id, period) = %d, want 1", rowCount)
	}
}

// chargingSourceWant groups the four figures SyncMonth derives for one
// charging source (ExtAC, ExtDC, or SC), so assertChargingSourceFields can
// compare all four in one call instead of four separate positional floats.
type chargingSourceWant struct {
	energyKWh float64
	cost      float64
	count     int
	dist      EndingBatteryDist
}

// assertChargingSourceFields checks one charging source's four SyncMonth
// output fields against a want computed by hand from the seeded fixture,
// never from the code under test.
func assertChargingSourceFields(t *testing.T, label string, gotEnergyKWh, gotCost float64, gotCount int, gotDist EndingBatteryDist, want chargingSourceWant) {
	t.Helper()
	if !almostEqual(gotEnergyKWh, want.energyKWh) {
		t.Errorf("%s EnergyKWh = %v, want %v", label, gotEnergyKWh, want.energyKWh)
	}
	if !almostEqual(gotCost, want.cost) {
		t.Errorf("%s Cost = %v, want %v", label, gotCost, want.cost)
	}
	if gotCount != want.count {
		t.Errorf("%s Count = %d, want %d", label, gotCount, want.count)
	}
	if gotDist != want.dist {
		t.Errorf("%s EndingBatteryDist = %+v, want %+v", label, gotDist, want.dist)
	}
}

// TestSyncMonth_ExternalCharges_SplitByTypeWithRealWriter proves SyncMonth's
// external-charge aggregation end to end: four entries seeded through the
// real charging.Writer, split across AC, DC, and no type at all. A charge
// with no ChargingType must not be counted in either bucket, and an
// IN_PROGRESS entry with no energy or ending percentage still counts toward
// its entry count and cost, contributing zero energy and no battery bucket.
func TestSyncMonth_ExternalCharges_SplitByTypeWithRealWriter(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	teslaID := int64(555005)
	cleanupMonthlySyncFixtures(t, pool, teslaID)

	accountID := uuid.New()
	const vin = "5YJ3E1EA0NF000001"
	lk := "HOME"
	ac := "AC"
	dc := "DC"
	writer := charging.NewWriter(pool)

	// AC, DONE: energy and ending percentage both present.
	doneEndedAt := time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC)
	doneEndPct := 55
	if _, err := writer.Create(ctx, charging.Entry{
		CreatedByAccountID: accountID,
		TeslaID:            teslaID,
		VIN:                vin,
		ChargedOn:          day(2026, 3, 5),
		Status:             charging.StatusDone,
		ChargingType:       &ac,
		EnergyAddedKWh:     fp(12.5),
		Price:              20000,
		Currency:           "COP",
		LocationKind:       &lk,
		EndedAt:            &doneEndedAt,
		EndBatteryPct:      &doneEndPct,
	}); err != nil {
		t.Fatalf("seeding AC done entry: %v", err)
	}

	// AC, IN_PROGRESS: no energy, no ending percentage -- still counts,
	// contributing zero energy and no battery bucket. An in-progress charge
	// has no honest end-of-session facts yet, but it still happened.
	if _, err := writer.Create(ctx, charging.Entry{
		CreatedByAccountID: accountID,
		TeslaID:            teslaID,
		VIN:                vin,
		ChargedOn:          day(2026, 3, 6),
		Status:             charging.StatusInProgress,
		ChargingType:       &ac,
		Price:              8000,
		Currency:           "COP",
		LocationKind:       &lk,
	}); err != nil {
		t.Fatalf("seeding AC in-progress entry: %v", err)
	}

	// DC, DONE.
	dcEndedAt := time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC)
	dcEndPct := 90
	if _, err := writer.Create(ctx, charging.Entry{
		CreatedByAccountID: accountID,
		TeslaID:            teslaID,
		VIN:                vin,
		ChargedOn:          day(2026, 3, 7),
		Status:             charging.StatusDone,
		ChargingType:       &dc,
		EnergyAddedKWh:     fp(30.0),
		Price:              45000,
		Currency:           "COP",
		LocationKind:       &lk,
		EndedAt:            &dcEndedAt,
		EndBatteryPct:      &dcEndPct,
	}); err != nil {
		t.Fatalf("seeding DC entry: %v", err)
	}

	// No ChargingType at all -- must land in neither AC nor DC.
	if _, err := writer.Create(ctx, charging.Entry{
		CreatedByAccountID: accountID,
		TeslaID:            teslaID,
		VIN:                vin,
		ChargedOn:          day(2026, 3, 8),
		Status:             charging.StatusInProgress,
		Price:              5000,
		Currency:           "COP",
		LocationKind:       &lk,
	}); err != nil {
		t.Fatalf("seeding entry with no charging type: %v", err)
	}

	syncer := newTestMonthlySyncer(pool)
	got, err := syncer.SyncMonth(ctx, teslaID, day(2026, 3, 15))
	if err != nil {
		t.Fatalf("SyncMonth: %v", err)
	}

	assertChargingSourceFields(t, "ExtAC", got.ExtACEnergyKWh, got.ExtACCost, got.ExtACEntryCount, got.ExtACEndingBatteryDist,
		chargingSourceWant{energyKWh: 12.5, cost: 28000, count: 2, dist: EndingBatteryDist{Bucket40To60: 1}})
	assertChargingSourceFields(t, "ExtDC", got.ExtDCEnergyKWh, got.ExtDCCost, got.ExtDCEntryCount, got.ExtDCEndingBatteryDist,
		chargingSourceWant{energyKWh: 30.0, cost: 45000, count: 1, dist: EndingBatteryDist{Bucket80To100: 1}})
}

// TestSyncMonth_SuperchargerSessionAcrossMonthBoundary_UsesPlatformZoneDay
// proves the widened Supercharger fetch and the platform-zone filter work
// together. Bogota is five hours behind UTC with no DST, so a session just
// after UTC midnight belongs to the PREVIOUS day in Bogota. Both sessions
// below sit on a UTC day inside one month but a Bogota day inside the
// other -- the exact boundary the widened window exists to catch.
func TestSyncMonth_SuperchargerSessionAcrossMonthBoundary_UsesPlatformZoneDay(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	teslaID := int64(555006)
	cleanupMonthlySyncFixtures(t, pool, teslaID)

	// UTC day: March 1. Bogota day: February 28 -- belongs to February.
	seedChargeSession(t, pool, charging.Session{
		TeslaID:             teslaID,
		ChargeStartDateTime: time.Date(2026, 3, 1, 1, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 3, 1, 2, 0, 0, 0, time.UTC),
	})
	// UTC day: April 1. Bogota day: March 31 -- belongs to March.
	seedChargeSession(t, pool, charging.Session{
		TeslaID:             teslaID,
		ChargeStartDateTime: time.Date(2026, 4, 1, 1, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 4, 1, 2, 0, 0, 0, time.UTC),
	})

	syncer := newTestMonthlySyncer(pool)

	march, err := syncer.SyncMonth(ctx, teslaID, day(2026, 3, 15))
	if err != nil {
		t.Fatalf("SyncMonth for March: %v", err)
	}
	if march.SCSessionCount != 1 {
		t.Errorf("March SCSessionCount = %d, want 1 (only the session whose Bogota day falls in March)", march.SCSessionCount)
	}

	february, err := syncer.SyncMonth(ctx, teslaID, day(2026, 2, 15))
	if err != nil {
		t.Fatalf("SyncMonth for February: %v", err)
	}
	if february.SCSessionCount != 1 {
		t.Errorf("February SCSessionCount = %d, want 1 (only the session whose Bogota day falls in February)", february.SCSessionCount)
	}
}

// TestSyncMonth_RerunSameMonthWithCharges_DoesNotDoubleCount mirrors
// TestSyncMonth_RerunSameMonth_IsIdempotentAndUpsertsInPlace for the charging
// figures: one external charge and one Supercharger session, SyncMonth
// called twice for the same month. Both calls must return the same figures
// -- UpsertVehicleMonthlyMetric overwrites the whole row, so a second run
// must never add its input on top of the first.
func TestSyncMonth_RerunSameMonthWithCharges_DoesNotDoubleCount(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	teslaID := int64(555007)
	cleanupMonthlySyncFixtures(t, pool, teslaID)

	accountID := uuid.New()
	lk := "HOME"
	ac := "AC"
	entryEndedAt := time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC)
	entryEndPct := 70
	if _, err := charging.NewWriter(pool).Create(ctx, charging.Entry{
		CreatedByAccountID: accountID,
		TeslaID:            teslaID,
		VIN:                "5YJ3E1EA0NF000001",
		ChargedOn:          day(2026, 3, 5),
		Status:             charging.StatusDone,
		ChargingType:       &ac,
		EnergyAddedKWh:     fp(15.0),
		Price:              25000,
		Currency:           "COP",
		LocationKind:       &lk,
		EndedAt:            &entryEndedAt,
		EndBatteryPct:      &entryEndPct,
	}); err != nil {
		t.Fatalf("seeding external charge: %v", err)
	}

	sessionEnergyKWh := 20.0
	sessionTotalCost := 35000.0
	sessionEndPct := 62
	seedChargeSession(t, pool, charging.Session{
		TeslaID:             teslaID,
		ChargeStartDateTime: time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC),
		EnergyKWh:           &sessionEnergyKWh,
		TotalCost:           &sessionTotalCost,
		EndBatteryPct:       &sessionEndPct,
	})

	syncer := newTestMonthlySyncer(pool)
	period := day(2026, 3, 1)

	first, err := syncer.SyncMonth(ctx, teslaID, period)
	if err != nil {
		t.Fatalf("first SyncMonth: %v", err)
	}
	second, err := syncer.SyncMonth(ctx, teslaID, period)
	if err != nil {
		t.Fatalf("second SyncMonth: %v", err)
	}

	wantAC := chargingSourceWant{energyKWh: 15.0, cost: 25000, count: 1, dist: EndingBatteryDist{Bucket60To80: 1}}
	wantSC := chargingSourceWant{energyKWh: 20.0, cost: 35000, count: 1, dist: EndingBatteryDist{Bucket60To80: 1}}
	for _, tc := range []struct {
		label string
		got   VehicleMonthlyMetrics
	}{
		{"first", first},
		{"second", second},
	} {
		assertChargingSourceFields(t, tc.label+" ExtAC", tc.got.ExtACEnergyKWh, tc.got.ExtACCost, tc.got.ExtACEntryCount, tc.got.ExtACEndingBatteryDist, wantAC)
		assertChargingSourceFields(t, tc.label+" SC", tc.got.SCEnergyKWh, tc.got.SCCost, tc.got.SCSessionCount, tc.got.SCEndingBatteryDist, wantSC)
	}
}
