package analytics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	analyticsdb "github.com/cristianpena/magus-tesla-api/internal/analytics/db"
	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// fakeTelemetryReader, fakeSuperchargerReader and fakeManualReader are hand-written
// fakes for telemetry.Reader, charging.SuperchargerSessionAnalyticsReader and
// charging.Reader. They back Recalculate's own offline tests (recalculate_test.go)
// and Wave 6's DB-integration tests (db_integration_test.go), neither of which needs
// a live upstream module -- only this module's own vehicle_metrics writes do.

// fakeTelemetryReader is a fake telemetry.Reader.
type fakeTelemetryReader struct {
	snapshots []telemetry.Snapshot
	err       error

	// preceding/precedingErr drive SnapshotPrecedingDay independently of the
	// snapshots/err pair above: the zero value is the no-gap case —
	// (nil, nil), "this vehicle has no earlier snapshot" — while a gap
	// fixture sets preceding to the row that sits before the fetched window.
	preceding    *telemetry.Snapshot
	precedingErr error

	gotTeslaID int64
	gotSince   time.Time

	gotBetweenStart time.Time
	gotBetweenEnd   time.Time

	gotPrecedingDay time.Time
}

func (f *fakeTelemetryReader) LatestSnapshotsByVehicles(_ context.Context, _ []int64) ([]telemetry.Snapshot, error) {
	panic("fakeTelemetryReader: LatestSnapshotsByVehicles must not be called from a Recalculate path")
}

func (f *fakeTelemetryReader) SnapshotsByVehicleSince(_ context.Context, teslaID int64, since time.Time) ([]telemetry.Snapshot, error) {
	f.gotTeslaID = teslaID
	f.gotSince = since
	if f.err != nil {
		return nil, f.err
	}
	return f.snapshots, nil
}

// SnapshotsByVehicleBetween implements the bounded-window fetch Recalculate issues.
func (f *fakeTelemetryReader) SnapshotsByVehicleBetween(_ context.Context, teslaID int64, start, end time.Time) ([]telemetry.Snapshot, error) {
	f.gotTeslaID = teslaID
	f.gotBetweenStart = start
	f.gotBetweenEnd = end
	if f.err != nil {
		return nil, f.err
	}
	return f.snapshots, nil
}

// SnapshotsByVehicleUpdatedSince satisfies telemetry.Reader. It panics because
// this fake backs Recalculate's offline tests, which never call it -- only
// Reconcile does, and Reconcile is exercised against a real database instead
// (db_integration_test.go).
func (f *fakeTelemetryReader) SnapshotsByVehicleUpdatedSince(_ context.Context, _ int64, _ time.Time) ([]telemetry.Snapshot, error) {
	panic("fakeTelemetryReader: SnapshotsByVehicleUpdatedSince must not be called from a Recalculate path")
}

// SnapshotPrecedingDay satisfies telemetry.Reader. Unlike
// SnapshotsByVehicleUpdatedSince above it does NOT panic: Recalculate calls it
// unconditionally on every run whose snapshot window returned rows, so
// panicking would break every Recalculate-driven test rather than catch a
// mistake. The zero-value fake returns (nil, nil) — the correct answer for a
// fixture with no capture gap.
func (f *fakeTelemetryReader) SnapshotPrecedingDay(_ context.Context, teslaID int64, day time.Time) (*telemetry.Snapshot, error) {
	f.gotTeslaID = teslaID
	f.gotPrecedingDay = day
	if f.precedingErr != nil {
		return nil, f.precedingErr
	}
	return f.preceding, nil
}

// fakeSuperchargerReader is a fake charging.SuperchargerSessionAnalyticsReader.
// These three ports are keyed on tesla_id alone, so the fake records the
// vehicle and no account.
type fakeSuperchargerReader struct {
	sessions []charging.Session
	err      error

	gotTeslaID int64
	gotLimit   int

	gotBetweenStart time.Time
	gotBetweenEnd   time.Time
}

// ListSessionsByVehicleBetween implements the bounded-window fetch Recalculate issues.
func (f *fakeSuperchargerReader) ListSessionsByVehicleBetween(_ context.Context, teslaID int64, start, end time.Time) ([]charging.Session, error) {
	f.gotTeslaID = teslaID
	f.gotBetweenStart = start
	f.gotBetweenEnd = end
	if f.err != nil {
		return nil, f.err
	}
	return f.sessions, nil
}

// ListSessionsByVehicleUpdatedSince satisfies charging.SuperchargerSessionAnalyticsReader
// (Reconcile's path, not Recalculate's -- panics for the same reason as
// fakeTelemetryReader's sibling above).
func (f *fakeSuperchargerReader) ListSessionsByVehicleUpdatedSince(_ context.Context, _ int64, _ time.Time) ([]charging.Session, error) {
	panic("fakeSuperchargerReader: ListSessionsByVehicleUpdatedSince must not be called from a Recalculate path")
}

func (f *fakeSuperchargerReader) ListSessionsByVehicle(_ context.Context, teslaID int64, limit int) ([]charging.Session, error) {
	f.gotTeslaID = teslaID
	f.gotLimit = limit
	if f.err != nil {
		return nil, f.err
	}
	return f.sessions, nil
}

// fakeManualReader is a fake charging.Reader. The manual-charge reads are
// keyed on the vehicle only, so this fake records the tesla id and never an
// account id.
type fakeManualReader struct {
	entries []charging.Entry
	err     error

	gotTeslaID int64
	gotLimit   int

	gotBetweenStart time.Time
	gotBetweenEnd   time.Time
}

func (f *fakeManualReader) ListEntriesByVehicle(_ context.Context, teslaID int64, limit int) ([]charging.Entry, error) {
	f.gotTeslaID = teslaID
	f.gotLimit = limit
	if f.err != nil {
		return nil, f.err
	}
	return f.entries, nil
}

func (f *fakeManualReader) ListEntriesByVehicles(_ context.Context, _ []int64, _ int) ([]charging.Entry, error) {
	panic("fakeManualReader: ListEntriesByVehicles must not be called from a Recalculate path")
}

// ListEntriesByVehicleBetween implements the bounded-window fetch Recalculate
// issues: start-1 to end, with no tail.
func (f *fakeManualReader) ListEntriesByVehicleBetween(_ context.Context, teslaID int64, start, end time.Time) ([]charging.Entry, error) {
	f.gotTeslaID = teslaID
	f.gotBetweenStart = start
	f.gotBetweenEnd = end
	if f.err != nil {
		return nil, f.err
	}
	return f.entries, nil
}

// ListEntriesByVehicleUpdatedSince completes the charging.Reader interface. It
// panics for the same reason as the two telemetry siblings above: this fake
// backs Recalculate's offline tests, and only Reconcile calls this path.
func (f *fakeManualReader) ListEntriesByVehicleUpdatedSince(_ context.Context, _ int64, _ time.Time) ([]charging.Entry, error) {
	panic("fakeManualReader: ListEntriesByVehicleUpdatedSince must not be called from a Recalculate path")
}

// fp returns a pointer to v -- a small literal-to-pointer helper shared by
// every test in this package that builds a nullable float64 field.
func fp(v float64) *float64 { return &v }

// intPtr returns a pointer to v -- the int counterpart of fp, shared by every
// test in this package that builds a nullable int field.
func intPtr(v int) *int { return &v }

// epsilon is the tolerance approxEqual below uses for floating-point
// comparisons, so a test asserts "close enough" rather than bit-exact
// equality on a value computed through division.
const epsilon = 1e-9

// approxEqual reports whether a and b differ by less than epsilon -- shared
// by every test in this package that compares a computed float64 result.
func approxEqual(a, b float64) bool {
	diff := a - b
	return diff < epsilon && diff > -epsilon
}

// --- ConsumedByDay/OdometerDeltaByDay precomputed-read tests (RM29 tier 3, design.md
// D13/D-precompute, Wave 4 task 4.2) ---
//
// OBSOLETE SECTION REMOVED HERE (worker finding, not silent): this file used to carry
// "ConsumedByDay port-wiring tests" (TestConsumedByDay_FetchWindows,
// ...AccountIDScoping_PassedToEveryPort, ...TelemetryError_Propagates,
// ...SuperchargerError_Propagates, ...ChargingError_Propagates) that proved
// ConsumedByDay fetched the right telemetry/supercharger/manual windows and propagated
// their errors. Wave 3's D-precompute change moved that entire fetch onto
// Recalculate (recalculate.go) -- ConsumedByDay and OdometerDeltaByDay no longer call
// r.telemetry/r.supercharger/r.manual AT ALL; they SELECT from r.metrics
// (vehicleMetricsStore) exclusively (design.md D13, reader.go). Those five tests
// therefore exercised a code path ConsumedByDay no longer has: run for real they would
// nil-pointer-panic on r.metrics (never set in their &reader{...} literals), since
// vet compiles but does not execute tests. Recalculate's OWN fetch-window/
// error-propagation behavior is covered by Wave 6's DB-integration tests
// (db_integration_test.go) instead, per recalculate.go's own doc comment: "recalculator
// is ... NOT part of the fake-testable seam reader.go's vehicleMetricsStore interface
// provides ... its own DB-integration tests (Wave 6) exercise it directly". Removing
// this section is therefore required by Wave 3's own D13 change, not an
// assertion-weakening: the behavior it tested was relocated, not deleted, and its new
// home is a different port entirely.

// fakeVehicleMetricsStore is a fake vehicleMetricsStore (reader.go) -- the two D13
// filtered SELECTs ConsumedByDay/OdometerDeltaByDay depend on now that both read
// exclusively from vehicle_metrics (design.md D-precompute). It does NOT simulate SQL
// WHERE itself (task 4.2's own instruction): each field below IS the exact,
// already-filtered result set the real query would return for a given call -- a test
// proving the IS NOT NULL filter's OUTPUT effect (a predecessor-less row's exclusion,
// design.md Fixture C) simply omits that row from the canned rows, mirroring what the
// real filtered SELECT already did before the row ever reached Go.
type fakeVehicleMetricsStore struct {
	consumedRows []analyticsdb.VehicleMetricsConsumedByVehicleBetweenRow
	odometerRows []analyticsdb.VehicleMetricsOdometerByVehicleBetweenRow
	err          error

	gotConsumedParams analyticsdb.VehicleMetricsConsumedByVehicleBetweenParams
	gotOdometerParams analyticsdb.VehicleMetricsOdometerByVehicleBetweenParams

	// latestRows/gotLatestTeslaIDs back LatestVehicleMetricsByVehicles. They
	// exist so this fake still satisfies vehicleMetricsStore; the assertions on
	// that method live in the DB-backed integration tests, not here.
	latestRows        []analyticsdb.LatestVehicleMetricsByVehiclesRow
	gotLatestTeslaIDs []int64

	// batteryRows/gotBatteryParams back VehicleMetricsBatteryByVehicleBetween
	// (RM40-analytics-add-battery-level-read task 3.1) -- added here only to
	// keep this fake satisfying vehicleMetricsStore after the interface
	// gained the method; no assertions on it are added by this dispatch
	// (unit tests excluded per this change's design.md D7).
	batteryRows      []analyticsdb.VehicleMetricsBatteryByVehicleBetweenRow
	gotBatteryParams analyticsdb.VehicleMetricsBatteryByVehicleBetweenParams
}

func (f *fakeVehicleMetricsStore) VehicleMetricsConsumedByVehicleBetween(_ context.Context, arg analyticsdb.VehicleMetricsConsumedByVehicleBetweenParams) ([]analyticsdb.VehicleMetricsConsumedByVehicleBetweenRow, error) {
	f.gotConsumedParams = arg
	if f.err != nil {
		return nil, f.err
	}
	return f.consumedRows, nil
}

func (f *fakeVehicleMetricsStore) VehicleMetricsOdometerByVehicleBetween(_ context.Context, arg analyticsdb.VehicleMetricsOdometerByVehicleBetweenParams) ([]analyticsdb.VehicleMetricsOdometerByVehicleBetweenRow, error) {
	f.gotOdometerParams = arg
	if f.err != nil {
		return nil, f.err
	}
	return f.odometerRows, nil
}

func (f *fakeVehicleMetricsStore) LatestVehicleMetricsByVehicles(_ context.Context, teslaIDs []int64) ([]analyticsdb.LatestVehicleMetricsByVehiclesRow, error) {
	f.gotLatestTeslaIDs = teslaIDs
	if f.err != nil {
		return nil, f.err
	}
	return f.latestRows, nil
}

func (f *fakeVehicleMetricsStore) VehicleMetricsBatteryByVehicleBetween(_ context.Context, arg analyticsdb.VehicleMetricsBatteryByVehicleBetweenParams) ([]analyticsdb.VehicleMetricsBatteryByVehicleBetweenRow, error) {
	f.gotBatteryParams = arg
	if f.err != nil {
		return nil, f.err
	}
	return f.batteryRows, nil
}

// TestReader_ConsumedByDay_ReadsPrecomputedRows covers design.md D13/D-precompute: a
// canned analyticsdb row (Fixture A's own expected values, consumed_test.go's
// TestDeriveVehicleMetrics_FixtureA) maps to the identical DayConsumption the
// pre-precompute live derivation produced for the same fixture -- no derivation logic
// runs in ConsumedByDay any more, only SELECT + map.
func TestReader_ConsumedByDay_ReadsPrecomputedRows(t *testing.T) {
	const teslaID = int64(42)
	start := day(2026, 8, 10)
	end := day(2026, 8, 10)

	metricsFake := &fakeVehicleMetricsStore{
		consumedRows: []analyticsdb.VehicleMetricsConsumedByVehicleBetweenRow{
			{
				MetricDate:             dateFrom(start),
				ConsumedPct:            pgtype.Float8{Float64: 15.0, Valid: true},
				DistanceTraveledKmCalc: pgtype.Float8{Float64: 50.0, Valid: true},
				Flagged:                false,
				MissingChargingType:    pgtype.Text{Valid: false},
				DaysSpannedCalc:        pgtype.Int4{Int32: 1, Valid: true},
			},
		},
	}
	r := &reader{metrics: metricsFake}

	got, err := r.ConsumedByDay(context.Background(), teslaID, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(got), got)
	}
	want := DayConsumption{Date: start, ConsumedPct: 15.0, DistanceKm: 50.0, Flagged: false, MissingChargingType: "", DaysSpanned: 1}
	if got[0] != want {
		t.Errorf("ConsumedByDay: want %+v, got %+v", want, got[0])
	}

	if metricsFake.gotConsumedParams.TeslaID != teslaID {
		t.Errorf("VehicleMetricsConsumedByVehicleBetween: teslaID not passed through: got %+v", metricsFake.gotConsumedParams)
	}
	if !metricsFake.gotConsumedParams.StartDate.Time.Equal(start) || !metricsFake.gotConsumedParams.EndDate.Time.Equal(end) {
		t.Errorf("VehicleMetricsConsumedByVehicleBetween: start/end not passed through unwidened: got %+v", metricsFake.gotConsumedParams)
	}
}

// TestReader_OdometerDeltaByDay_ClampsOnRead covers design.md Test Contract Fixture
// B's Reader-level assertion: a stored raw distance of -2.0 (a clock-skew/odometer-read
// anomaly) reads back as KmDriven: 0.0 from OdometerDeltaByDay (D13's clamp, roadmap
// D5), while ConsumedByDay's DistanceKm for the identical stored value stays -2.0
// (unclamped) -- DayConsumption.DistanceKm was never clamped by the gateway even
// today; only the odometer chart's displayed delta is. This is the exact divergence
// design.md's Test Contract calls out.
func TestReader_OdometerDeltaByDay_ClampsOnRead(t *testing.T) {
	const teslaID = int64(42)
	start := day(2026, 8, 12)
	end := day(2026, 8, 12)

	metricsFake := &fakeVehicleMetricsStore{
		consumedRows: []analyticsdb.VehicleMetricsConsumedByVehicleBetweenRow{
			{
				MetricDate:             dateFrom(start),
				ConsumedPct:            pgtype.Float8{Float64: -45.0, Valid: true},
				DistanceTraveledKmCalc: pgtype.Float8{Float64: -2.0, Valid: true}, // Fixture B's raw, unclamped value
				Flagged:                true,
				MissingChargingType:    pgtype.Text{String: "MANUAL", Valid: true},
				DaysSpannedCalc:        pgtype.Int4{Int32: 1, Valid: true},
			},
		},
		odometerRows: []analyticsdb.VehicleMetricsOdometerByVehicleBetweenRow{
			{
				MetricDate:             dateFrom(start),
				OdometerKm:             1998.0,
				DistanceTraveledKmCalc: pgtype.Float8{Float64: -2.0, Valid: true}, // same raw stored value
			},
		},
	}
	r := &reader{metrics: metricsFake}

	gotOdometer, err := r.OdometerDeltaByDay(context.Background(), teslaID, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gotOdometer) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(gotOdometer), gotOdometer)
	}
	if gotOdometer[0].KmDriven != 0.0 {
		t.Errorf("KmDriven: want 0.0 (clamped, math.Max(0, -2.0)), got %v", gotOdometer[0].KmDriven)
	}
	if gotOdometer[0].OdometerKm != 1998.0 {
		t.Errorf("OdometerKm: want 1998.0 (unclamped absolute reading), got %v", gotOdometer[0].OdometerKm)
	}

	gotConsumed, err := r.ConsumedByDay(context.Background(), teslaID, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gotConsumed) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(gotConsumed), gotConsumed)
	}
	if gotConsumed[0].DistanceKm != -2.0 {
		t.Errorf("DistanceKm: want -2.0 (DayConsumption.DistanceKm is never clamped -- only OdometerDeltaByDay's KmDriven is), got %v", gotConsumed[0].DistanceKm)
	}
}

// TestReader_ConsumedByDay_ExcludesPredecessorlessRow and
// TestReader_OdometerDeltaByDay_ExcludesPredecessorlessRow cover design.md Test
// Contract Fixture C's Reader-level guarantee (D13): a vehicle_metrics row exists for
// a predecessor-less day (Fixture C, consumed_test.go's
// TestDeriveVehicleMetrics_FixtureC), but the real SQL's "IS NOT NULL" filter never
// returns it to either Reader method. The fake does not simulate SQL WHERE itself
// (task 4.2) -- Fixture C's row is "fed to the fake" by simply NOT including it among
// the canned rows, exactly mirroring what the real filtered SELECT already did before
// the row ever reached Go. This is the test that would catch an implementation that
// forgot the filter entirely: that bug would show up as a non-empty analyticsdb query
// result reaching this fake in production, not as a build failure -- this test instead
// proves the Reader's OWN mapping correctly produces an empty slice (not a panic, not
// a garbage zero-value entry) when the store returns zero rows for the window.
func TestReader_ConsumedByDay_ExcludesPredecessorlessRow(t *testing.T) {
	const teslaID = int64(42)
	start := day(2026, 8, 4) // Fixture C's metric_date
	end := day(2026, 8, 4)

	metricsFake := &fakeVehicleMetricsStore{consumedRows: nil}
	r := &reader{metrics: metricsFake}

	got, err := r.ConsumedByDay(context.Background(), teslaID, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty result (Fixture C's predecessor-less row excluded by the IS NOT NULL filter), got %d entries: %+v", len(got), got)
	}
}

func TestReader_OdometerDeltaByDay_ExcludesPredecessorlessRow(t *testing.T) {
	const teslaID = int64(42)
	start := day(2026, 8, 4) // Fixture C's metric_date
	end := day(2026, 8, 4)

	metricsFake := &fakeVehicleMetricsStore{odometerRows: nil}
	r := &reader{metrics: metricsFake}

	got, err := r.OdometerDeltaByDay(context.Background(), teslaID, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty result (Fixture C's predecessor-less row excluded by the IS NOT NULL filter), got %d entries: %+v", len(got), got)
	}
}

// TestReader_VehicleMetricsStoreError_Propagates covers ConsumedByDay/OdometerDeltaByDay's
// error path, the D13 successor to the removed ...ConsumedByDay_*Error_Propagates
// tests above: a store error is returned via errors.Is, unwrapped, from both methods.
func TestReader_VehicleMetricsStoreError_Propagates(t *testing.T) {
	wantErr := errors.New("analyticsdb: connection lost")
	r := &reader{metrics: &fakeVehicleMetricsStore{err: wantErr}}

	if _, err := r.ConsumedByDay(context.Background(), 1, day(2026, 8, 10), day(2026, 8, 20)); !errors.Is(err, wantErr) {
		t.Fatalf("ConsumedByDay: want error %v propagated unwrapped, got %v", wantErr, err)
	}
	if _, err := r.OdometerDeltaByDay(context.Background(), 1, day(2026, 8, 10), day(2026, 8, 20)); !errors.Is(err, wantErr) {
		t.Fatalf("OdometerDeltaByDay: want error %v propagated unwrapped, got %v", wantErr, err)
	}
}
