package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The reader tests exercise the Reader port fully OFFLINE: a fakeReadStore implements
// the store seam without any database, network, or Tesla API call. Only the read method
// (latestSnapshotsByAccount) is exercised here; the write methods panic to catch any
// accidental call from the reader path.

// fakeReadStore is a read-only fake that implements the full store seam (required by
// the interface). The write methods panic — they must never be called by the reader.
type fakeReadStore struct {
	snapshots []Snapshot
	err       error
}

func (f *fakeReadStore) insertSnapshot(_ context.Context, _ Snapshot) error {
	panic("fakeReadStore: insertSnapshot must not be called from the reader path")
}

func (f *fakeReadStore) insertPollAttempt(_ context.Context, _ Attempt) error {
	panic("fakeReadStore: insertPollAttempt must not be called from the reader path")
}

func (f *fakeReadStore) latestSnapshotsByAccount(_ context.Context, _ uuid.UUID) ([]Snapshot, error) {
	if f.err != nil {
		return nil, f.err
	}
	// Return a copy so mutations in tests do not affect the fake's state.
	out := make([]Snapshot, len(f.snapshots))
	copy(out, f.snapshots)
	return out, nil
}

func (f *fakeReadStore) upsertSuperchargerSession(_ context.Context, _ SuperchargerSession) error {
	panic("fakeReadStore: upsertSuperchargerSession must not be called from the reader path")
}

// newFakeReader builds a *reader with the given fake store, bypassing the pool-backed
// NewReader constructor. This is the offline-test entry point (no Postgres needed).
func newFakeReader(s store) *reader {
	return &reader{store: s}
}

// TestReader_LatestSnapshotsByAccount_ReturnsFakeSnapshots asserts that the reader
// delegates directly to the store and returns whatever the store returns unmodified.
func TestReader_LatestSnapshotsByAccount_ReturnsFakeSnapshots(t *testing.T) {
	acctID := uuid.New()

	now := time.Now().UTC()
	older := now.Add(-time.Hour)
	sentryon := true

	want := []Snapshot{
		{
			AccountID:    acctID,
			TeslaID:      10,
			CapturedAt:   now,
			BatteryLevel: 80,
			BatteryRange: 250.0,
			Odometer:     12345.6,
			SentryMode:   &sentryon,
		},
		{
			AccountID:    acctID,
			TeslaID:      20,
			CapturedAt:   older,
			BatteryLevel: 55,
			BatteryRange: 150.0,
			Odometer:     8000.0,
			SentryMode:   nil,
		},
	}

	r := newFakeReader(&fakeReadStore{snapshots: want})
	got, err := r.LatestSnapshotsByAccount(context.Background(), acctID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 snapshots, got %d", len(got))
	}
	// Vehicle 10 carries the newer captured_at and sentry-on.
	found10, found20 := false, false
	for _, s := range got {
		switch s.TeslaID {
		case 10:
			found10 = true
			if !s.CapturedAt.Equal(now) {
				t.Errorf("vehicle 10: want CapturedAt=%v, got %v", now, s.CapturedAt)
			}
			if s.BatteryLevel != 80 {
				t.Errorf("vehicle 10: want BatteryLevel=80, got %d", s.BatteryLevel)
			}
			if s.SentryMode == nil || !*s.SentryMode {
				t.Errorf("vehicle 10: want SentryMode=*true, got %v", s.SentryMode)
			}
		case 20:
			found20 = true
			if !s.CapturedAt.Equal(older) {
				t.Errorf("vehicle 20: want CapturedAt=%v, got %v", older, s.CapturedAt)
			}
			if s.SentryMode != nil {
				t.Errorf("vehicle 20: want SentryMode=nil (not reported), got %v", s.SentryMode)
			}
		default:
			t.Errorf("unexpected vehicle TeslaID %d", s.TeslaID)
		}
	}
	if !found10 || !found20 {
		t.Errorf("not all vehicles returned: found10=%v found20=%v", found10, found20)
	}
}

// TestReader_EmptyAccount_ReturnsNonNilEmptySlice asserts that an account with no
// snapshots gets an empty (non-nil) slice and nil error (design D5 — avoids nil-slice
// footguns for the gateway).
func TestReader_EmptyAccount_ReturnsNonNilEmptySlice(t *testing.T) {
	r := newFakeReader(&fakeReadStore{snapshots: nil})

	got, err := r.LatestSnapshotsByAccount(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("empty account must return non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("empty account must return empty slice, got %d element(s)", len(got))
	}
}

// TestReader_StoreError_PropagatesError asserts that a store error is propagated to
// the caller as-is — the reader does not swallow errors.
func TestReader_StoreError_PropagatesError(t *testing.T) {
	wantErr := errors.New("db: connection lost")
	r := newFakeReader(&fakeReadStore{err: wantErr})

	_, err := r.LatestSnapshotsByAccount(context.Background(), uuid.New())
	if !errors.Is(err, wantErr) {
		t.Fatalf("want store error %v propagated, got %v", wantErr, err)
	}
}

// TestReader_SentryModeNilFidelity asserts that a snapshot whose SentryMode is nil
// (not reported by the vehicle) stays nil after passing through the reader — it must
// not be collapsed into *false (design D1).
func TestReader_SentryModeNilFidelity(t *testing.T) {
	snap := Snapshot{
		AccountID:  uuid.New(),
		TeslaID:    99,
		CapturedAt: time.Now().UTC(),
		SentryMode: nil,
	}
	r := newFakeReader(&fakeReadStore{snapshots: []Snapshot{snap}})

	got, err := r.LatestSnapshotsByAccount(context.Background(), snap.AccountID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 snapshot, got %d", len(got))
	}
	if got[0].SentryMode != nil {
		t.Errorf("SentryMode nil must stay nil (not-reported != off); got %v", got[0].SentryMode)
	}
}

// TestReader_KmCompanions asserts that the BatteryRangeKm() and OdometerKm() companion
// methods return the correct metric values (miles × 1.609344), exercising the milesToKm
// constant defined in telemetry.go.
func TestReader_KmCompanions(t *testing.T) {
	const miles = 200.0
	snap := Snapshot{
		AccountID:    uuid.New(),
		TeslaID:      1,
		CapturedAt:   time.Now().UTC(),
		BatteryRange: miles,
		Odometer:     miles,
	}
	r := newFakeReader(&fakeReadStore{snapshots: []Snapshot{snap}})

	got, err := r.LatestSnapshotsByAccount(context.Background(), snap.AccountID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 snapshot, got %d", len(got))
	}

	const wantKm = miles * milesToKm
	if diff := got[0].BatteryRangeKm() - wantKm; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("BatteryRangeKm: want %v, got %v", wantKm, got[0].BatteryRangeKm())
	}
	if diff := got[0].OdometerKm() - wantKm; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("OdometerKm: want %v, got %v", wantKm, got[0].OdometerKm())
	}
}
