package analytics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	analyticsdb "github.com/cristianpena/magus-tesla-api/internal/analytics/db"
	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// The reader tests exercise RecentEfficiency fully OFFLINE: one hand-written fake per
// consumed port (telemetry.Reader, charging.SuperchargerSessionAnalyticsReader, charging.Reader,
// vehicleLookup), constructed directly as &reader{...} rather than through NewReader —
// mirrors fakeReadStore/newFakeReader in internal/telemetry/reader_test.go one level up
// (fake ports instead of a fake store, since this module owns no store of its own).

// fakeTelemetryReader is a fake telemetry.Reader. SnapshotsByVehicleSince is exercised by
// RecentEfficiency; SnapshotsByVehicleBetween is exercised by ConsumedByDay (RM28 tier 3,
// design.md D-B13) — both share the snapshots/err fixture fields (safe: no existing
// RecentEfficiency test calls the Between path, so nothing observes the reuse).
// LatestSnapshotsByAccount is not called by anything in this module and still panics.
type fakeTelemetryReader struct {
	snapshots []telemetry.Snapshot
	err       error

	// preceding/precedingErr drive SnapshotPrecedingDay independently of the
	// snapshots/err pair above (RM29 tier 4, task 2.5): the zero value is the
	// no-gap case — (nil, nil), "this vehicle has no earlier snapshot" — while
	// a gap fixture (design.md Fixture D) sets preceding to the row that sits
	// before the fetched window. Kept separate from err so the existing
	// "SnapshotsByVehicleBetween error propagates" test keeps driving exactly
	// one port's failure.
	preceding    *telemetry.Snapshot
	precedingErr error

	gotAccountID uuid.UUID
	gotTeslaID   int64
	gotSince     time.Time

	gotBetweenStart time.Time
	gotBetweenEnd   time.Time

	gotPrecedingDay time.Time
}

func (f *fakeTelemetryReader) LatestSnapshotsByAccount(_ context.Context, _ uuid.UUID) ([]telemetry.Snapshot, error) {
	panic("fakeTelemetryReader: LatestSnapshotsByAccount must not be called from RecentEfficiency")
}

func (f *fakeTelemetryReader) SnapshotsByVehicleSince(_ context.Context, accountID uuid.UUID, teslaID int64, since time.Time) ([]telemetry.Snapshot, error) {
	f.gotAccountID = accountID
	f.gotTeslaID = teslaID
	f.gotSince = since
	if f.err != nil {
		return nil, f.err
	}
	return f.snapshots, nil
}

// SnapshotsByVehicleBetween implements the bounded-window fetch ConsumedByDay issues
// (design.md D-B13 — start-1/end+1). Un-panicked by RM28 tier 3 (task T5.1); previously a
// defensive stub since RecentEfficiency never called it.
func (f *fakeTelemetryReader) SnapshotsByVehicleBetween(_ context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]telemetry.Snapshot, error) {
	f.gotAccountID = accountID
	f.gotTeslaID = teslaID
	f.gotBetweenStart = start
	f.gotBetweenEnd = end
	if f.err != nil {
		return nil, f.err
	}
	return f.snapshots, nil
}

// SnapshotsByVehicleUpdatedSince satisfies the telemetry.Reader method added by
// RM29-analytics-add-vehicle-metrics task 1.2. It panics because only
// Recalculate/Reconcile call this path, and the tests in this file exercise
// ConsumedByDay/RecentEfficiency only -- a call here means a read path reached
// for the watermark port by mistake. Wave 4 replaces this with recording
// behaviour when the Reconcile tests need it.
func (f *fakeTelemetryReader) SnapshotsByVehicleUpdatedSince(_ context.Context, _ uuid.UUID, _ int64, _ time.Time) ([]telemetry.Snapshot, error) {
	panic("fakeTelemetryReader: SnapshotsByVehicleUpdatedSince must not be called from a Reader path")
}

// SnapshotPrecedingDay satisfies the telemetry.Reader method added by
// RM29-telemetry-drop-derived-columns task 1.2. Unlike
// SnapshotsByVehicleUpdatedSince above it does NOT panic: Recalculate calls it
// unconditionally on every run whose snapshot window returned rows (design.md
// D7), so panicking would break every Recalculate-driven test rather than
// catch a mistake. The zero-value fake returns (nil, nil) — the correct answer
// for a fixture with no capture gap.
func (f *fakeTelemetryReader) SnapshotPrecedingDay(_ context.Context, accountID uuid.UUID, teslaID int64, day time.Time) (*telemetry.Snapshot, error) {
	f.gotAccountID = accountID
	f.gotTeslaID = teslaID
	f.gotPrecedingDay = day
	if f.precedingErr != nil {
		return nil, f.precedingErr
	}
	return f.preceding, nil
}

// fakeSuperchargerReader is a fake charging.SuperchargerSessionAnalyticsReader
// (RM31-analytics-read-sessions-from-charging tier 3 retype — was a fake
// telemetry.SuperchargerReader before this tier). ListSessionsByVehicle is
// exercised by RecentEfficiency; ListSessionsByVehicleBetween/
// ListSessionsByVehicleUpdatedSince are not called from any Reader path today
// and remain defensive panics — both share the sessions/err fixture fields
// with ListSessionsByVehicle (safe: no existing RecentEfficiency test calls
// the Between/UpdatedSince paths). The old telemetry.SuperchargerReader
// SuperchargerSessionsByAccount stub is dropped entirely: it has no
// equivalent on charging.SuperchargerSessionAnalyticsReader, which this type
// now implements.
type fakeSuperchargerReader struct {
	sessions []charging.Session
	err      error

	gotAccountID uuid.UUID
	gotTeslaID   int64
	gotLimit     int

	gotBetweenStart time.Time
	gotBetweenEnd   time.Time
}

// ListSessionsByVehicleBetween implements the SessionReader half of
// charging.SuperchargerSessionAnalyticsReader. Not called by any Reader path
// today (ConsumedByDay reads precomputed vehicle_metrics rows instead, per
// AGENTS.md D-precompute) — recorded here defensively, matching the sibling
// panics below for the paths RecentEfficiency truly never calls.
func (f *fakeSuperchargerReader) ListSessionsByVehicleBetween(_ context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]charging.Session, error) {
	f.gotAccountID = accountID
	f.gotTeslaID = teslaID
	f.gotBetweenStart = start
	f.gotBetweenEnd = end
	if f.err != nil {
		return nil, f.err
	}
	return f.sessions, nil
}

// ListSessionsByVehicleUpdatedSince satisfies
// charging.SuperchargerSessionAnalyticsReader (Recalculator's Reconcile path
// calls this, not Reader). Panics for the same reason as
// fakeTelemetryReader's sibling above.
func (f *fakeSuperchargerReader) ListSessionsByVehicleUpdatedSince(_ context.Context, _ uuid.UUID, _ int64, _ time.Time) ([]charging.Session, error) {
	panic("fakeSuperchargerReader: ListSessionsByVehicleUpdatedSince must not be called from a Reader path")
}

func (f *fakeSuperchargerReader) ListSessionsByVehicle(_ context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]charging.Session, error) {
	f.gotAccountID = accountID
	f.gotTeslaID = teslaID
	f.gotLimit = limit
	if f.err != nil {
		return nil, f.err
	}
	return f.sessions, nil
}

// fakeManualReader is a fake charging.Reader. ListEntriesByVehicle is exercised by
// RecentEfficiency; ListEntriesByVehicleBetween is exercised by ConsumedByDay (RM28 tier
// 3, design.md D-B13) — both share the entries/err fixture fields (safe: no existing
// RecentEfficiency test calls the Between path).
type fakeManualReader struct {
	entries []charging.Entry
	err     error

	gotAccountID uuid.UUID
	gotTeslaID   int64
	gotLimit     int

	gotBetweenStart time.Time
	gotBetweenEnd   time.Time
}

func (f *fakeManualReader) ListEntriesByVehicle(_ context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]charging.Entry, error) {
	f.gotAccountID = accountID
	f.gotTeslaID = teslaID
	f.gotLimit = limit
	if f.err != nil {
		return nil, f.err
	}
	return f.entries, nil
}

func (f *fakeManualReader) ListEntriesByAccount(_ context.Context, _ uuid.UUID, _ int) ([]charging.Entry, error) {
	panic("fakeManualReader: ListEntriesByAccount must not be called from RecentEfficiency")
}

// ListEntriesByVehicleBetween implements the bounded-window fetch ConsumedByDay issues
// (design.md D-B13 — start-1/end, no tail). Un-panicked by RM28 tier 3 (task T5.3);
// previously a defensive stub since RecentEfficiency never called it.
func (f *fakeManualReader) ListEntriesByVehicleBetween(_ context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]charging.Entry, error) {
	f.gotAccountID = accountID
	f.gotTeslaID = teslaID
	f.gotBetweenStart = start
	f.gotBetweenEnd = end
	if f.err != nil {
		return nil, f.err
	}
	return f.entries, nil
}

// ListEntriesByVehicleUpdatedSince satisfies the charging.Reader method added by
// RM29-analytics-add-vehicle-metrics task 1.4. Panics for the same reason as the
// two telemetry siblings above.
func (f *fakeManualReader) ListEntriesByVehicleUpdatedSince(_ context.Context, _ uuid.UUID, _ int64, _ time.Time) ([]charging.Entry, error) {
	panic("fakeManualReader: ListEntriesByVehicleUpdatedSince must not be called from a Reader path")
}

// fakeVehicleLookup is a fake vehicleLookup (the narrow account.Service consumer
// interface) — only RegisteredVehicles exists on the interface, so there is nothing
// else to panic-guard.
type fakeVehicleLookup struct {
	vehicles []account.Vehicle
	err      error

	gotAccountID uuid.UUID
}

func (f *fakeVehicleLookup) RegisteredVehicles(_ context.Context, accountID uuid.UUID) ([]account.Vehicle, error) {
	f.gotAccountID = accountID
	if f.err != nil {
		return nil, f.err
	}
	return f.vehicles, nil
}

func fp(v float64) *float64 { return &v }
func sp(v string) *string   { return &v }

// TestRecentEfficiency_HappyPath_ComputesValue covers spec.md "A vehicle with two or
// more snapshots, known capacity, and net consumption gets a computed value" through
// the full port-wiring path (fetch, filter, resolve capacity, derive).
func TestRecentEfficiency_HappyPath_ComputesValue(t *testing.T) {
	accountID := uuid.New()
	const teslaID = int64(42)
	fixedNow := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	window := 30 * 24 * time.Hour
	since := fixedNow.Add(-window)

	start := snap(1000, 80, nil) // km
	end := snap(1100, 60, nil)   // km

	// One supercharger session and one manual entry, both inside the window.
	sessionEnergy := 3.0
	entryEnergy := 2.0
	telemetryFake := &fakeTelemetryReader{snapshots: []telemetry.Snapshot{start, end}}
	superchargerFake := &fakeSuperchargerReader{sessions: []charging.Session{
		{ChargeStartDateTime: since.Add(24 * time.Hour), EnergyKWh: fp(sessionEnergy)},
	}}
	manualFake := &fakeManualReader{entries: []charging.Entry{
		{ChargedOn: since.Add(48 * time.Hour), EnergyAddedKWh: fp(entryEnergy)},
	}}
	vehicleFake := &fakeVehicleLookup{vehicles: []account.Vehicle{
		{TeslaID: teslaID, CarType: sp("model3")},
	}}

	r := &reader{
		telemetry:    telemetryFake,
		supercharger: superchargerFake,
		manual:       manualFake,
		account:      vehicleFake,
		window:       window,
		now:          func() time.Time { return fixedNow },
	}

	// Hand-computed: kWhIn = 3 + 2 = 5; capacity known ("model3" = 75 kWh);
	// deltaSoC = 60-80 = -20; energy = 5 - 75*(-20)/100 = 20;
	// distance = 1100-1000 = 100 km; WhPerKm = 20*1000/100 = 200.
	wantDistance := 100.0
	wantWhPerKm := 20.0 * 1000 / wantDistance

	got, ok, err := r.RecentEfficiency(context.Background(), accountID, teslaID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("want ok=true")
	}
	if got.Approximate {
		t.Error("want Approximate=false: model3 is in the capacity table")
	}
	if !approxEqual(got.WhPerKm, wantWhPerKm) {
		t.Errorf("WhPerKm: want %v, got %v", wantWhPerKm, got.WhPerKm)
	}
}

// TestRecentEfficiency_WindowExcludesOldEntries covers design.md D6: sessions/entries
// dated before the window's since boundary must be excluded from the summed kWhIn.
func TestRecentEfficiency_WindowExcludesOldEntries(t *testing.T) {
	accountID := uuid.New()
	const teslaID = int64(42)
	fixedNow := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	window := 30 * 24 * time.Hour
	since := fixedNow.Add(-window)

	start := snap(1000, 80, nil) // km
	end := snap(1100, 60, nil)   // km

	// One in-window session/entry, one out-of-window (before since) each.
	telemetryFake := &fakeTelemetryReader{snapshots: []telemetry.Snapshot{start, end}}
	superchargerFake := &fakeSuperchargerReader{sessions: []charging.Session{
		{ChargeStartDateTime: since.Add(24 * time.Hour), EnergyKWh: fp(3.0)},    // in window
		{ChargeStartDateTime: since.Add(-24 * time.Hour), EnergyKWh: fp(100.0)}, // before window
	}}
	manualFake := &fakeManualReader{entries: []charging.Entry{
		{ChargedOn: since.Add(48 * time.Hour), EnergyAddedKWh: fp(2.0)},   // in window
		{ChargedOn: since.Add(-48 * time.Hour), EnergyAddedKWh: fp(50.0)}, // before window
	}}
	vehicleFake := &fakeVehicleLookup{vehicles: []account.Vehicle{
		{TeslaID: teslaID, CarType: sp("model3")},
	}}

	r := &reader{
		telemetry:    telemetryFake,
		supercharger: superchargerFake,
		manual:       manualFake,
		account:      vehicleFake,
		window:       window,
		now:          func() time.Time { return fixedNow },
	}

	// If the out-of-window rows had been included, kWhIn would be 3+100+2+50=155,
	// giving energy = 155-75*(-20)/100 = 170. With correct filtering kWhIn=5, energy=20.
	// distance = 1100-1000 = 100 km.
	wantDistance := 100.0
	wantWhPerKm := 20.0 * 1000 / wantDistance
	excludedWhPerKm := 170.0 * 1000 / wantDistance

	got, ok, err := r.RecentEfficiency(context.Background(), accountID, teslaID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("want ok=true")
	}
	if !approxEqual(got.WhPerKm, wantWhPerKm) {
		t.Errorf("WhPerKm: want %v (old entries excluded), got %v", wantWhPerKm, got.WhPerKm)
	}
	if approxEqual(got.WhPerKm, excludedWhPerKm) {
		t.Errorf("WhPerKm equals the value you'd get by including old entries (%v) — window filter did not run", excludedWhPerKm)
	}
}

// TestRecentEfficiency_UnknownVehicle_ApproximateTrue covers spec.md "Unknown car_type
// still returns a value, marked approximate", via the not-found-in-registry path.
func TestRecentEfficiency_UnknownVehicle_ApproximateTrue(t *testing.T) {
	accountID := uuid.New()
	const teslaID = int64(42)
	fixedNow := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	window := 30 * 24 * time.Hour
	since := fixedNow.Add(-window)

	start := snap(1000, 80, nil)
	end := snap(1100, 60, nil)

	telemetryFake := &fakeTelemetryReader{snapshots: []telemetry.Snapshot{start, end}}
	superchargerFake := &fakeSuperchargerReader{sessions: []charging.Session{
		{ChargeStartDateTime: since.Add(time.Hour), EnergyKWh: fp(5.0)},
	}}
	manualFake := &fakeManualReader{}
	// The registry has vehicles, but none matching teslaID — carTypeFor falls back to "".
	vehicleFake := &fakeVehicleLookup{vehicles: []account.Vehicle{
		{TeslaID: 999, CarType: sp("models")},
	}}

	r := &reader{
		telemetry:    telemetryFake,
		supercharger: superchargerFake,
		manual:       manualFake,
		account:      vehicleFake,
		window:       window,
		now:          func() time.Time { return fixedNow },
	}

	got, ok, err := r.RecentEfficiency(context.Background(), accountID, teslaID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("want ok=true")
	}
	if !got.Approximate {
		t.Error("want Approximate=true: vehicle not found in registry, capacity unknown")
	}
}

// TestRecentEfficiency_TelemetryError_Propagates, ...SuperchargerError_Propagates,
// ...ChargingError_Propagates, ...AccountError_Propagates cover error propagation:
// a port error is returned via errors.Is, unwrapped, matching
// TestReader_StoreError_PropagatesError in internal/telemetry/reader_test.go.

func TestRecentEfficiency_TelemetryError_Propagates(t *testing.T) {
	wantErr := errors.New("telemetry: connection lost")
	r := &reader{
		telemetry:    &fakeTelemetryReader{err: wantErr},
		supercharger: &fakeSuperchargerReader{},
		manual:       &fakeManualReader{},
		account:      &fakeVehicleLookup{},
		window:       30 * 24 * time.Hour,
		now:          time.Now,
	}

	_, _, err := r.RecentEfficiency(context.Background(), uuid.New(), 1)
	if !errors.Is(err, wantErr) {
		t.Fatalf("want error %v propagated unwrapped, got %v", wantErr, err)
	}
}

func TestRecentEfficiency_SuperchargerError_Propagates(t *testing.T) {
	wantErr := errors.New("supercharger: connection lost")
	r := &reader{
		telemetry:    &fakeTelemetryReader{},
		supercharger: &fakeSuperchargerReader{err: wantErr},
		manual:       &fakeManualReader{},
		account:      &fakeVehicleLookup{},
		window:       30 * 24 * time.Hour,
		now:          time.Now,
	}

	_, _, err := r.RecentEfficiency(context.Background(), uuid.New(), 1)
	if !errors.Is(err, wantErr) {
		t.Fatalf("want error %v propagated unwrapped, got %v", wantErr, err)
	}
}

func TestRecentEfficiency_ChargingError_Propagates(t *testing.T) {
	wantErr := errors.New("charging: connection lost")
	r := &reader{
		telemetry:    &fakeTelemetryReader{},
		supercharger: &fakeSuperchargerReader{},
		manual:       &fakeManualReader{err: wantErr},
		account:      &fakeVehicleLookup{},
		window:       30 * 24 * time.Hour,
		now:          time.Now,
	}

	_, _, err := r.RecentEfficiency(context.Background(), uuid.New(), 1)
	if !errors.Is(err, wantErr) {
		t.Fatalf("want error %v propagated unwrapped, got %v", wantErr, err)
	}
}

func TestRecentEfficiency_AccountError_Propagates(t *testing.T) {
	wantErr := errors.New("account: connection lost")
	r := &reader{
		telemetry:    &fakeTelemetryReader{},
		supercharger: &fakeSuperchargerReader{},
		manual:       &fakeManualReader{},
		account:      &fakeVehicleLookup{err: wantErr},
		window:       30 * 24 * time.Hour,
		now:          time.Now,
	}

	_, _, err := r.RecentEfficiency(context.Background(), uuid.New(), 1)
	if !errors.Is(err, wantErr) {
		t.Fatalf("want error %v propagated unwrapped, got %v", wantErr, err)
	}
}

// TestRecentEfficiency_AccountIDScoping_PassedToEveryPort covers design.md D4 / spec.md
// "Multi-Tenant Scoping": the same accountID argument must reach all four fakes'
// captured call arguments unchanged.
func TestRecentEfficiency_AccountIDScoping_PassedToEveryPort(t *testing.T) {
	accountID := uuid.New()
	const teslaID = int64(77)

	telemetryFake := &fakeTelemetryReader{snapshots: []telemetry.Snapshot{snap(1000, 80, nil), snap(1100, 60, nil)}}
	superchargerFake := &fakeSuperchargerReader{}
	manualFake := &fakeManualReader{}
	vehicleFake := &fakeVehicleLookup{}

	r := &reader{
		telemetry:    telemetryFake,
		supercharger: superchargerFake,
		manual:       manualFake,
		account:      vehicleFake,
		window:       30 * 24 * time.Hour,
		now:          time.Now,
	}

	// Result may be ok=false (no charging data), but every port must still have
	// been called with the same accountID before that outcome is reached.
	_, _, err := r.RecentEfficiency(context.Background(), accountID, teslaID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if telemetryFake.gotAccountID != accountID {
		t.Errorf("telemetry: accountID not passed through: want %v, got %v", accountID, telemetryFake.gotAccountID)
	}
	if superchargerFake.gotAccountID != accountID {
		t.Errorf("supercharger: accountID not passed through: want %v, got %v", accountID, superchargerFake.gotAccountID)
	}
	if manualFake.gotAccountID != accountID {
		t.Errorf("charging: accountID not passed through: want %v, got %v", accountID, manualFake.gotAccountID)
	}
	if vehicleFake.gotAccountID != accountID {
		t.Errorf("account: accountID not passed through: want %v, got %v", accountID, vehicleFake.gotAccountID)
	}

	if telemetryFake.gotTeslaID != teslaID {
		t.Errorf("telemetry: teslaID not passed through: want %d, got %d", teslaID, telemetryFake.gotTeslaID)
	}
	if superchargerFake.gotTeslaID != teslaID {
		t.Errorf("supercharger: teslaID not passed through: want %d, got %d", teslaID, superchargerFake.gotTeslaID)
	}
	if manualFake.gotTeslaID != teslaID {
		t.Errorf("charging: teslaID not passed through: want %d, got %d", teslaID, manualFake.gotTeslaID)
	}
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

	// latestRows/gotLatestAccountID back LatestVehicleMetricsByAccount
	// (RM38-analytics-add-vehicle-status-columns task 3.5) -- added here only
	// to keep this fake satisfying vehicleMetricsStore after the interface
	// gained the method; no assertions on it are added by this dispatch
	// (Wave 5, a later dispatch's own DB-integration test wave, owns those).
	latestRows         []analyticsdb.LatestVehicleMetricsByAccountRow
	gotLatestAccountID uuid.UUID

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

func (f *fakeVehicleMetricsStore) LatestVehicleMetricsByAccount(_ context.Context, accountID uuid.UUID) ([]analyticsdb.LatestVehicleMetricsByAccountRow, error) {
	f.gotLatestAccountID = accountID
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
	accountID := uuid.New()
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

	got, err := r.ConsumedByDay(context.Background(), accountID, teslaID, start, end)
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

	if metricsFake.gotConsumedParams.AccountID != accountID || metricsFake.gotConsumedParams.TeslaID != teslaID {
		t.Errorf("VehicleMetricsConsumedByVehicleBetween: accountID/teslaID not passed through: got %+v", metricsFake.gotConsumedParams)
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
	accountID := uuid.New()
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

	gotOdometer, err := r.OdometerDeltaByDay(context.Background(), accountID, teslaID, start, end)
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

	gotConsumed, err := r.ConsumedByDay(context.Background(), accountID, teslaID, start, end)
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
	accountID := uuid.New()
	const teslaID = int64(42)
	start := day(2026, 8, 4) // Fixture C's metric_date
	end := day(2026, 8, 4)

	metricsFake := &fakeVehicleMetricsStore{consumedRows: nil}
	r := &reader{metrics: metricsFake}

	got, err := r.ConsumedByDay(context.Background(), accountID, teslaID, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty result (Fixture C's predecessor-less row excluded by the IS NOT NULL filter), got %d entries: %+v", len(got), got)
	}
}

func TestReader_OdometerDeltaByDay_ExcludesPredecessorlessRow(t *testing.T) {
	accountID := uuid.New()
	const teslaID = int64(42)
	start := day(2026, 8, 4) // Fixture C's metric_date
	end := day(2026, 8, 4)

	metricsFake := &fakeVehicleMetricsStore{odometerRows: nil}
	r := &reader{metrics: metricsFake}

	got, err := r.OdometerDeltaByDay(context.Background(), accountID, teslaID, start, end)
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

	if _, err := r.ConsumedByDay(context.Background(), uuid.New(), 1, day(2026, 8, 10), day(2026, 8, 20)); !errors.Is(err, wantErr) {
		t.Fatalf("ConsumedByDay: want error %v propagated unwrapped, got %v", wantErr, err)
	}
	if _, err := r.OdometerDeltaByDay(context.Background(), uuid.New(), 1, day(2026, 8, 10), day(2026, 8, 20)); !errors.Is(err, wantErr) {
		t.Fatalf("OdometerDeltaByDay: want error %v propagated unwrapped, got %v", wantErr, err)
	}
}
