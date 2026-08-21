package analytics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// The reader tests exercise RecentEfficiency fully OFFLINE: one hand-written fake per
// consumed port (telemetry.Reader, telemetry.SuperchargerReader, charging.Reader,
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

	gotAccountID uuid.UUID
	gotTeslaID   int64
	gotSince     time.Time

	gotBetweenStart time.Time
	gotBetweenEnd   time.Time
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

// fakeSuperchargerReader is a fake telemetry.SuperchargerReader. SuperchargerSessionsByVehicle
// is exercised by RecentEfficiency; SuperchargerSessionsByVehicleBetween is exercised by
// ConsumedByDay (RM28 tier 3, design.md D-B13) — both share the sessions/err fixture
// fields (safe: no existing RecentEfficiency test calls the Between path).
type fakeSuperchargerReader struct {
	sessions []telemetry.SuperchargerSession
	err      error

	gotAccountID uuid.UUID
	gotTeslaID   int64
	gotLimit     int

	gotBetweenStart time.Time
	gotBetweenEnd   time.Time
}

func (f *fakeSuperchargerReader) SuperchargerSessionsByAccount(_ context.Context, _ uuid.UUID, _ int) ([]telemetry.SuperchargerSession, error) {
	panic("fakeSuperchargerReader: SuperchargerSessionsByAccount must not be called from RecentEfficiency")
}

// SuperchargerSessionsByVehicleBetween implements the bounded-window fetch ConsumedByDay
// issues (design.md D-B13 — start-1/end+2, the widest tail of the three ports). Un-panicked
// by RM28 tier 3 (task T5.2); previously a defensive stub since RecentEfficiency never
// called it.
func (f *fakeSuperchargerReader) SuperchargerSessionsByVehicleBetween(_ context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]telemetry.SuperchargerSession, error) {
	f.gotAccountID = accountID
	f.gotTeslaID = teslaID
	f.gotBetweenStart = start
	f.gotBetweenEnd = end
	if f.err != nil {
		return nil, f.err
	}
	return f.sessions, nil
}

// SuperchargerSessionsByVehicleUpdatedSince satisfies the
// telemetry.SuperchargerReader method added by
// RM29-analytics-add-vehicle-metrics task 1.3. Panics for the same reason as
// fakeTelemetryReader's sibling above.
func (f *fakeSuperchargerReader) SuperchargerSessionsByVehicleUpdatedSince(_ context.Context, _ uuid.UUID, _ int64, _ time.Time) ([]telemetry.SuperchargerSession, error) {
	panic("fakeSuperchargerReader: SuperchargerSessionsByVehicleUpdatedSince must not be called from a Reader path")
}

func (f *fakeSuperchargerReader) SuperchargerSessionsByVehicle(_ context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]telemetry.SuperchargerSession, error) {
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
	superchargerFake := &fakeSuperchargerReader{sessions: []telemetry.SuperchargerSession{
		{ChargeStartDateTime: since.Add(24 * time.Hour), EnergyKWh: fp(sessionEnergy)},
	}}
	manualFake := &fakeManualReader{entries: []charging.Entry{
		{ChargedOn: since.Add(48 * time.Hour), EnergyAddedKWh: entryEnergy},
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
	superchargerFake := &fakeSuperchargerReader{sessions: []telemetry.SuperchargerSession{
		{ChargeStartDateTime: since.Add(24 * time.Hour), EnergyKWh: fp(3.0)},    // in window
		{ChargeStartDateTime: since.Add(-24 * time.Hour), EnergyKWh: fp(100.0)}, // before window
	}}
	manualFake := &fakeManualReader{entries: []charging.Entry{
		{ChargedOn: since.Add(48 * time.Hour), EnergyAddedKWh: 2.0},   // in window
		{ChargedOn: since.Add(-48 * time.Hour), EnergyAddedKWh: 50.0}, // before window
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
	superchargerFake := &fakeSuperchargerReader{sessions: []telemetry.SuperchargerSession{
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

// --- ConsumedByDay port-wiring tests (RM28 tier 3, design.md Test Contract (j)-(l)) ---
//
// These prove ConsumedByDay fetches the right windows and propagates arguments/errors
// correctly; the arithmetic itself is already proven by consumed_test.go's (a)-(n), so
// these use trivial fixtures (mirrors the design.md note directly above the Test Contract
// section "(j)").

// TestConsumedByDay_FetchWindows covers design.md Test Contract (j) (expected values
// CHANGED by roadmap D18 / design D-B13): every port must be called with a window WIDER
// than [start, end] to cover both the D9a predecessor lookback and the zone shift.
func TestConsumedByDay_FetchWindows(t *testing.T) {
	accountID := uuid.New()
	const teslaID = int64(42)
	start := day(2026, 8, 10)
	end := day(2026, 8, 20)

	telemetryFake := &fakeTelemetryReader{}
	superchargerFake := &fakeSuperchargerReader{}
	manualFake := &fakeManualReader{}

	r := &reader{
		telemetry:    telemetryFake,
		supercharger: superchargerFake,
		manual:       manualFake,
		account:      &fakeVehicleLookup{},
		window:       30 * 24 * time.Hour,
		now:          time.Now,
	}

	if _, err := r.ConsumedByDay(context.Background(), accountID, teslaID, start, end); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantSnapshotsStart, wantSnapshotsEnd := day(2026, 8, 9), day(2026, 8, 21) // start-1, end+1 (D9a lookback, zone tail)
	if !telemetryFake.gotBetweenStart.Equal(wantSnapshotsStart) || !telemetryFake.gotBetweenEnd.Equal(wantSnapshotsEnd) {
		t.Errorf("SnapshotsByVehicleBetween: want (%v, %v), got (%v, %v)", wantSnapshotsStart, wantSnapshotsEnd, telemetryFake.gotBetweenStart, telemetryFake.gotBetweenEnd)
	}

	wantSessionsStart, wantSessionsEnd := day(2026, 8, 9), day(2026, 8, 22) // start-1, end+2 (two-day tail, design D-B5/D-B13)
	if !superchargerFake.gotBetweenStart.Equal(wantSessionsStart) || !superchargerFake.gotBetweenEnd.Equal(wantSessionsEnd) {
		t.Errorf("SuperchargerSessionsByVehicleBetween: want (%v, %v), got (%v, %v)", wantSessionsStart, wantSessionsEnd, superchargerFake.gotBetweenStart, superchargerFake.gotBetweenEnd)
	}

	wantEntriesStart, wantEntriesEnd := day(2026, 8, 9), day(2026, 8, 20) // start-1, end (no tail)
	if !manualFake.gotBetweenStart.Equal(wantEntriesStart) || !manualFake.gotBetweenEnd.Equal(wantEntriesEnd) {
		t.Errorf("ListEntriesByVehicleBetween: want (%v, %v), got (%v, %v)", wantEntriesStart, wantEntriesEnd, manualFake.gotBetweenStart, manualFake.gotBetweenEnd)
	}
}

// TestConsumedByDay_AccountIDScoping_PassedToEveryPort covers design.md Test Contract
// (k), mirroring TestRecentEfficiency_AccountIDScoping_PassedToEveryPort's existing
// pattern for the three ports ConsumedByDay actually calls (never account/vehicleLookup —
// design.md D-B1).
func TestConsumedByDay_AccountIDScoping_PassedToEveryPort(t *testing.T) {
	accountID := uuid.New()
	const teslaID = int64(77)

	telemetryFake := &fakeTelemetryReader{}
	superchargerFake := &fakeSuperchargerReader{}
	manualFake := &fakeManualReader{}

	r := &reader{
		telemetry:    telemetryFake,
		supercharger: superchargerFake,
		manual:       manualFake,
		account:      &fakeVehicleLookup{},
		window:       30 * 24 * time.Hour,
		now:          time.Now,
	}

	if _, err := r.ConsumedByDay(context.Background(), accountID, teslaID, day(2026, 8, 10), day(2026, 8, 20)); err != nil {
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

// TestConsumedByDay_TelemetryError_Propagates, ...SuperchargerError_Propagates, and
// ...ChargingError_Propagates cover design.md Test Contract (l): a port error is
// returned via errors.Is, unwrapped, mirroring the existing
// TestRecentEfficiency_*Error_Propagates pattern. ConsumedByDay never calls the account
// port, so there is no ...AccountError_Propagates counterpart here.

func TestConsumedByDay_TelemetryError_Propagates(t *testing.T) {
	wantErr := errors.New("telemetry: connection lost")
	r := &reader{
		telemetry:    &fakeTelemetryReader{err: wantErr},
		supercharger: &fakeSuperchargerReader{},
		manual:       &fakeManualReader{},
		account:      &fakeVehicleLookup{},
		window:       30 * 24 * time.Hour,
		now:          time.Now,
	}

	_, err := r.ConsumedByDay(context.Background(), uuid.New(), 1, day(2026, 8, 10), day(2026, 8, 20))
	if !errors.Is(err, wantErr) {
		t.Fatalf("want error %v propagated unwrapped, got %v", wantErr, err)
	}
}

func TestConsumedByDay_SuperchargerError_Propagates(t *testing.T) {
	wantErr := errors.New("supercharger: connection lost")
	r := &reader{
		telemetry:    &fakeTelemetryReader{},
		supercharger: &fakeSuperchargerReader{err: wantErr},
		manual:       &fakeManualReader{},
		account:      &fakeVehicleLookup{},
		window:       30 * 24 * time.Hour,
		now:          time.Now,
	}

	_, err := r.ConsumedByDay(context.Background(), uuid.New(), 1, day(2026, 8, 10), day(2026, 8, 20))
	if !errors.Is(err, wantErr) {
		t.Fatalf("want error %v propagated unwrapped, got %v", wantErr, err)
	}
}

func TestConsumedByDay_ChargingError_Propagates(t *testing.T) {
	wantErr := errors.New("charging: connection lost")
	r := &reader{
		telemetry:    &fakeTelemetryReader{},
		supercharger: &fakeSuperchargerReader{},
		manual:       &fakeManualReader{err: wantErr},
		account:      &fakeVehicleLookup{},
		window:       30 * 24 * time.Hour,
		now:          time.Now,
	}

	_, err := r.ConsumedByDay(context.Background(), uuid.New(), 1, day(2026, 8, 10), day(2026, 8, 20))
	if !errors.Is(err, wantErr) {
		t.Fatalf("want error %v propagated unwrapped, got %v", wantErr, err)
	}
}
