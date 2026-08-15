package battery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/manualcharge"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// The reader tests exercise RecentEfficiency fully OFFLINE: one hand-written fake per
// consumed port (telemetry.Reader, telemetry.SuperchargerReader, manualcharge.Reader,
// vehicleLookup), constructed directly as &reader{...} rather than through NewReader —
// mirrors fakeReadStore/newFakeReader in internal/telemetry/reader_test.go one level up
// (fake ports instead of a fake store, since this module owns no store of its own).

// fakeTelemetryReader is a fake telemetry.Reader — only SnapshotsByVehicleSince is
// exercised by battery; LatestSnapshotsByAccount panics to catch an accidental call.
type fakeTelemetryReader struct {
	snapshots []telemetry.Snapshot
	err       error

	gotAccountID uuid.UUID
	gotTeslaID   int64
	gotSince     time.Time
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

func (f *fakeTelemetryReader) SnapshotsByVehicleBetween(_ context.Context, _ uuid.UUID, _ int64, _ time.Time, _ time.Time) ([]telemetry.Snapshot, error) {
	panic("fakeTelemetryReader: SnapshotsByVehicleBetween must not be called from RecentEfficiency")
}

// fakeSuperchargerReader is a fake telemetry.SuperchargerReader — only
// SuperchargerSessionsByVehicle is exercised by battery.
type fakeSuperchargerReader struct {
	sessions []telemetry.SuperchargerSession
	err      error

	gotAccountID uuid.UUID
	gotTeslaID   int64
	gotLimit     int
}

func (f *fakeSuperchargerReader) SuperchargerSessionsByAccount(_ context.Context, _ uuid.UUID, _ int) ([]telemetry.SuperchargerSession, error) {
	panic("fakeSuperchargerReader: SuperchargerSessionsByAccount must not be called from RecentEfficiency")
}

// SuperchargerSessionsByVehicleBetween satisfies the port's third method
// (RM28-telemetry-add-charge-gap-storage). RecentEfficiency does not use the
// bounded-window read — it windows in Go from a limit-based fetch — so a call
// here means the production path changed without this fake being revisited.
// The battery-consumed derivation (RM28 tier 3) is the intended first caller
// and will need its own fake behavior when it lands.
func (f *fakeSuperchargerReader) SuperchargerSessionsByVehicleBetween(_ context.Context, _ uuid.UUID, _ int64, _, _ time.Time) ([]telemetry.SuperchargerSession, error) {
	panic("fakeSuperchargerReader: SuperchargerSessionsByVehicleBetween must not be called from RecentEfficiency")
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

// fakeManualReader is a fake manualcharge.Reader — only ListEntriesByVehicle is
// exercised by battery.
type fakeManualReader struct {
	entries []manualcharge.Entry
	err     error

	gotAccountID uuid.UUID
	gotTeslaID   int64
	gotLimit     int
}

func (f *fakeManualReader) ListEntriesByVehicle(_ context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]manualcharge.Entry, error) {
	f.gotAccountID = accountID
	f.gotTeslaID = teslaID
	f.gotLimit = limit
	if f.err != nil {
		return nil, f.err
	}
	return f.entries, nil
}

func (f *fakeManualReader) ListEntriesByAccount(_ context.Context, _ uuid.UUID, _ int) ([]manualcharge.Entry, error) {
	panic("fakeManualReader: ListEntriesByAccount must not be called from RecentEfficiency")
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
	manualFake := &fakeManualReader{entries: []manualcharge.Entry{
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
	manualFake := &fakeManualReader{entries: []manualcharge.Entry{
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
// ...ManualChargeError_Propagates, ...AccountError_Propagates cover error propagation:
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

func TestRecentEfficiency_ManualChargeError_Propagates(t *testing.T) {
	wantErr := errors.New("manualcharge: connection lost")
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
		t.Errorf("manualcharge: accountID not passed through: want %v, got %v", accountID, manualFake.gotAccountID)
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
		t.Errorf("manualcharge: teslaID not passed through: want %d, got %d", teslaID, manualFake.gotTeslaID)
	}
}
