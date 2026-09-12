package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// The reader tests exercise the Reader port fully OFFLINE: a fakeReadStore implements
// the store seam without any database, network, or Tesla API call. Only the read method
// (latestSnapshotsByVehicles) is exercised here; the write methods panic to catch any
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

func (f *fakeReadStore) latestSnapshotsByVehicles(_ context.Context, _ []int64) ([]Snapshot, error) {
	if f.err != nil {
		return nil, f.err
	}
	// Return a copy so mutations in tests do not affect the fake's state.
	out := make([]Snapshot, len(f.snapshots))
	copy(out, f.snapshots)
	return out, nil
}

// snapshotsByVehicleSince satisfies the store seam for reader tests that exercise
// SnapshotsByVehicleSince. Base fakeReadStore returns nil, nil; extend with
// fakeHistoryStore for history-specific tests.
func (f *fakeReadStore) snapshotsByVehicleSince(_ context.Context, _ int64, _ time.Time) ([]Snapshot, error) {
	return nil, nil
}

// snapshotsByVehicleBetween satisfies the store seam (added by RM8 tier 1). Base
// fakeReadStore returns nil, nil; extend with fakeBetweenStore for Between-specific
// tests. Kept as a no-op return (not a panic) so existing LatestSnapshotsByVehicles
// tests that build a fakeReadStore keep compiling without touching the Between path.
func (f *fakeReadStore) snapshotsByVehicleBetween(_ context.Context, _ int64, _, _ time.Time) ([]Snapshot, error) {
	return nil, nil
}

// snapshotsByVehicleUpdatedSince satisfies the store seam added by
// RM29-analytics-add-vehicle-metrics task 1.2. Base fakeReadStore returns nil, nil
// (no-op), mirroring snapshotsByVehicleSince/Between's own precedent above — this
// method is not exercised by the LatestSnapshotsByVehicles-focused tests in this
// file.
func (f *fakeReadStore) snapshotsByVehicleUpdatedSince(_ context.Context, _ int64, _ time.Time) ([]Snapshot, error) {
	return nil, nil
}

func (f *fakeReadStore) upsertSuperchargerHistory(_ context.Context, _ SuperchargerHistory) error {
	panic("fakeReadStore: upsertSuperchargerHistory must not be called from the reader path")
}

// snapshotPrecedingDay satisfies the store seam added by
// RM29-telemetry-drop-derived-columns (design D2, wave 1). The reader tests in
// this file exercise it via dedicated fakes where needed; this no-op stub
// (nil, nil — "no predecessor") keeps fakeReadStore implementing the full store
// interface, mirroring snapshotsByVehicleSince/Between's own no-op-return
// precedent above.
func (f *fakeReadStore) snapshotPrecedingDay(_ context.Context, _ int64, _ time.Time) (*Snapshot, error) {
	return nil, nil
}

// newFakeReader builds a *reader with the given fake store, bypassing the pool-backed
// NewReader constructor. This is the offline-test entry point (no Postgres needed).
func newFakeReader(s store) *reader {
	return &reader{store: s}
}

// TestReader_LatestSnapshotsByVehicles_ReturnsFakeSnapshots asserts that the reader
// delegates directly to the store and returns whatever the store returns unmodified.
func TestReader_LatestSnapshotsByVehicles_ReturnsFakeSnapshots(t *testing.T) {
	now := time.Now().UTC()
	older := now.Add(-time.Hour)
	sentryon := true

	want := []Snapshot{
		{
			TeslaID:         10,
			CapturedAt:      now,
			BatteryLevelPct: 80,
			BatteryRangeKm:  402.336,       // 250.0 mi * 1.609344 — already display-unit, no companion
			OdometerKm:      19868.3172864, // 12345.6 mi * 1.609344
			SentryMode:      &sentryon,
		},
		{
			TeslaID:         20,
			CapturedAt:      older,
			BatteryLevelPct: 55,
			BatteryRangeKm:  241.4016,
			OdometerKm:      12874.752,
			SentryMode:      nil,
		},
	}

	r := newFakeReader(&fakeReadStore{snapshots: want})
	got, err := r.LatestSnapshotsByVehicles(context.Background(), []int64{10, 20})
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
			if s.BatteryLevelPct != 80 {
				t.Errorf("vehicle 10: want BatteryLevelPct=80, got %d", s.BatteryLevelPct)
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

// TestReader_EmptyVehicleList_ReturnsNonNilEmptySlice asserts that a batch with no
// snapshots gets an empty (non-nil) slice and nil error (design D5 — avoids nil-slice
// footguns for the gateway).
func TestReader_EmptyVehicleList_ReturnsNonNilEmptySlice(t *testing.T) {
	r := newFakeReader(&fakeReadStore{snapshots: nil})

	got, err := r.LatestSnapshotsByVehicles(context.Background(), []int64{999999})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("no matching snapshots must return non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("no matching snapshots must return empty slice, got %d element(s)", len(got))
	}
}

// TestReader_StoreError_PropagatesError asserts that a store error is propagated to
// the caller as-is — the reader does not swallow errors.
func TestReader_StoreError_PropagatesError(t *testing.T) {
	wantErr := errors.New("db: connection lost")
	r := newFakeReader(&fakeReadStore{err: wantErr})

	_, err := r.LatestSnapshotsByVehicles(context.Background(), []int64{1})
	if !errors.Is(err, wantErr) {
		t.Fatalf("want store error %v propagated, got %v", wantErr, err)
	}
}

// TestReader_SentryModeNilFidelity asserts that a snapshot whose SentryMode is nil
// (not reported by the vehicle) stays nil after passing through the reader — it must
// not be collapsed into *false (design D1).
func TestReader_SentryModeNilFidelity(t *testing.T) {
	snap := Snapshot{
		TeslaID:    99,
		CapturedAt: time.Now().UTC(),
		SentryMode: nil,
	}
	r := newFakeReader(&fakeReadStore{snapshots: []Snapshot{snap}})

	got, err := r.LatestSnapshotsByVehicles(context.Background(), []int64{snap.TeslaID})
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

// TestReader_NoConversionOnRead asserts that the reader returns BatteryRangeKm and
// OdometerKm exactly as the store supplied them — no arithmetic, no companion method
// call. This replaces the old TestReader_KmCompanions, which exercised
// BatteryRangeKm()/OdometerKm() companion methods; design D4 deliberately removed
// those (the renamed fields collide with the method names), and the spec's "Latest
// Snapshot Read Port" requirement now states the read port "SHALL NOT perform, or
// require its callers to perform, any unit conversion on read" and "SHALL NOT expose
// companion conversion methods". The values below are already-converted km (as
// snapshotFrom would have stored them at capture time) — the reader's only job is to
// pass them through unmodified.
func TestReader_NoConversionOnRead(t *testing.T) {
	const wantKm = 321.8688 // an arbitrary already-converted km value, not derived
	// from a miles literal here — proving the reader does no further arithmetic on it.
	snap := Snapshot{
		TeslaID:        1,
		CapturedAt:     time.Now().UTC(),
		BatteryRangeKm: wantKm,
		OdometerKm:     wantKm,
	}
	r := newFakeReader(&fakeReadStore{snapshots: []Snapshot{snap}})

	got, err := r.LatestSnapshotsByVehicles(context.Background(), []int64{snap.TeslaID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 snapshot, got %d", len(got))
	}

	if got[0].BatteryRangeKm != wantKm {
		t.Errorf("BatteryRangeKm: want %v unmodified, got %v", wantKm, got[0].BatteryRangeKm)
	}
	if got[0].OdometerKm != wantKm {
		t.Errorf("OdometerKm: want %v unmodified, got %v", wantKm, got[0].OdometerKm)
	}
}

// --- SnapshotsByVehicleSince unit tests (offline, fake store) ---

// fakeHistoryStore extends the read-only fake store seam to return configurable
// snapshots for SnapshotsByVehicleSince, capturing the params passed by the reader
// for assertion. Write methods and latestSnapshotsByVehicles panic to catch accidental
// calls — history tests exercise only the history method.
type fakeHistoryStore struct {
	snapshots  []Snapshot
	err        error
	gotTeslaID int64
	gotSince   time.Time
}

func (f *fakeHistoryStore) insertSnapshot(_ context.Context, _ Snapshot) error {
	panic("fakeHistoryStore: insertSnapshot must not be called")
}

func (f *fakeHistoryStore) insertPollAttempt(_ context.Context, _ Attempt) error {
	panic("fakeHistoryStore: insertPollAttempt must not be called")
}

func (f *fakeHistoryStore) latestSnapshotsByVehicles(_ context.Context, _ []int64) ([]Snapshot, error) {
	panic("fakeHistoryStore: latestSnapshotsByVehicles must not be called from history path")
}

func (f *fakeHistoryStore) snapshotsByVehicleSince(_ context.Context, teslaID int64, since time.Time) ([]Snapshot, error) {
	f.gotTeslaID = teslaID
	f.gotSince = since
	if f.err != nil {
		return nil, f.err
	}
	out := make([]Snapshot, len(f.snapshots))
	copy(out, f.snapshots)
	return out, nil
}

// snapshotsByVehicleBetween satisfies the store seam (added by RM8 tier 1).
// fakeHistoryStore exercises only the Since path, so the Between path panics to
// catch any accidental cross-path call.
func (f *fakeHistoryStore) snapshotsByVehicleBetween(_ context.Context, _ int64, _, _ time.Time) ([]Snapshot, error) {
	panic("fakeHistoryStore: snapshotsByVehicleBetween must not be called from the Since path")
}

// snapshotsByVehicleUpdatedSince satisfies the store seam added by
// RM29-analytics-add-vehicle-metrics task 1.2. fakeHistoryStore exercises only the
// Since path, so this panics to catch any accidental cross-path call, mirroring
// snapshotsByVehicleBetween's own precedent immediately above.
func (f *fakeHistoryStore) snapshotsByVehicleUpdatedSince(_ context.Context, _ int64, _ time.Time) ([]Snapshot, error) {
	panic("fakeHistoryStore: snapshotsByVehicleUpdatedSince must not be called from the Since path")
}

func (f *fakeHistoryStore) upsertSuperchargerHistory(_ context.Context, _ SuperchargerHistory) error {
	panic("fakeHistoryStore: upsertSuperchargerHistory must not be called")
}

// snapshotPrecedingDay satisfies the store seam added by
// RM29-telemetry-drop-derived-columns (design D2, wave 1). fakeHistoryStore
// exercises only the Since path, so this panics to catch any accidental
// cross-path call, mirroring this fake's existing pattern for the other
// unrelated seam methods above.
func (f *fakeHistoryStore) snapshotPrecedingDay(_ context.Context, _ int64, _ time.Time) (*Snapshot, error) {
	panic("fakeHistoryStore: snapshotPrecedingDay must not be called from the Since path")
}

// TestReader_SnapshotsByVehicleSince_OldestFirst asserts that the reader returns
// snapshots in the order the store delivers them (oldest-first is enforced at the
// query level — the reader passes through the order without re-sorting).
func TestReader_SnapshotsByVehicleSince_OldestFirst(t *testing.T) {
	now := time.Now().UTC()
	day1 := now.Add(-48 * time.Hour)
	day2 := now.Add(-24 * time.Hour)
	day3 := now

	const teslaID = int64(111)
	since := day1

	// Store returns snapshots already oldest-first (as the DB query guarantees).
	want := []Snapshot{
		{TeslaID: teslaID, CapturedAt: day1, BatteryLevelPct: 70, OdometerKm: 1609.344},  // 1000 mi * 1.609344
		{TeslaID: teslaID, CapturedAt: day2, BatteryLevelPct: 68, OdometerKm: 1689.8112}, // 1050 mi * 1.609344
		{TeslaID: teslaID, CapturedAt: day3, BatteryLevelPct: 65, OdometerKm: 1770.2784}, // 1100 mi * 1.609344
	}

	fake := &fakeHistoryStore{snapshots: want}
	r := &reader{store: fake}

	got, err := r.SnapshotsByVehicleSince(context.Background(), teslaID, since)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 snapshots, got %d", len(got))
	}
	// Oldest-first: day1 < day2 < day3
	if !got[0].CapturedAt.Equal(day1) {
		t.Errorf("got[0].CapturedAt: want %v, got %v", day1, got[0].CapturedAt)
	}
	if !got[1].CapturedAt.Equal(day2) {
		t.Errorf("got[1].CapturedAt: want %v, got %v", day2, got[1].CapturedAt)
	}
	if !got[2].CapturedAt.Equal(day3) {
		t.Errorf("got[2].CapturedAt: want %v, got %v", day3, got[2].CapturedAt)
	}
}

// TestReader_SnapshotsByVehicleSince_EmptyNonNil asserts that an empty window
// returns a non-nil empty slice and nil error (D5 parity: no nil-slice footgun).
func TestReader_SnapshotsByVehicleSince_EmptyNonNil(t *testing.T) {
	fake := &fakeHistoryStore{snapshots: nil}
	r := &reader{store: fake}

	got, err := r.SnapshotsByVehicleSince(context.Background(), 42, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("empty window must return non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("empty window must return 0 elements, got %d", len(got))
	}
}

// TestReader_SnapshotsByVehicleSince_ParamsPassedThrough asserts that teslaID and
// since are forwarded to the store unchanged (no silent mutation).
func TestReader_SnapshotsByVehicleSince_ParamsPassedThrough(t *testing.T) {
	const teslaID = int64(9999)
	since := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	fake := &fakeHistoryStore{}
	r := &reader{store: fake}

	_, err := r.SnapshotsByVehicleSince(context.Background(), teslaID, since)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.gotTeslaID != teslaID {
		t.Errorf("teslaID not passed through: want %d, got %d", teslaID, fake.gotTeslaID)
	}
	if !fake.gotSince.Equal(since) {
		t.Errorf("since not passed through: want %v, got %v", since, fake.gotSince)
	}
}

// TestReader_SnapshotsByVehicleSince_StoreError asserts that a store error is
// propagated to the caller without wrapping (same contract as LatestSnapshotsByVehicles).
func TestReader_SnapshotsByVehicleSince_StoreError(t *testing.T) {
	wantErr := errors.New("store: connection reset")
	fake := &fakeHistoryStore{err: wantErr}
	r := &reader{store: fake}

	_, err := r.SnapshotsByVehicleSince(context.Background(), 1, time.Now())
	if !errors.Is(err, wantErr) {
		t.Fatalf("want store error %v propagated, got %v", wantErr, err)
	}
}

// --- EffectiveDate (telemetry-add-effective-date) ---
//
// rowToSnapshot (mapping.go) is the SINGLE DB→domain mapper both
// dbStore.latestSnapshotsByVehicles and dbStore.snapshotsByVehicleSince call per
// row (service.go — design D4: computed once in the mapper, not per read
// method). Testing rowToSnapshot directly therefore proves EffectiveDate for
// both Reader methods without a database. TestReadStore_*_EffectiveDate in
// db_read_integration_test.go additionally proves it end-to-end against a real
// Postgres row (task 2.3).

// TestRowToSnapshot_EffectiveDate_OneCalendarDayBeforeCapturedAt asserts that
// for a row with a known captured_at, rowToSnapshot derives EffectiveDate as
// exactly CapturedAt.AddDate(0,0,-1) and leaves CapturedAt itself unchanged —
// the property both LatestSnapshotsByVehicles and SnapshotsByVehicleSince
// inherit for free since they share this one mapper.
func TestRowToSnapshot_EffectiveDate_OneCalendarDayBeforeCapturedAt(t *testing.T) {
	capturedAt := time.Date(2026, 8, 8, 3, 30, 0, 0, time.UTC)
	row := snapshotRow{
		TeslaID:    10,
		CapturedAt: pgtype.Timestamptz{Time: capturedAt, Valid: true},
	}

	got := rowToSnapshot(row)

	wantEffective := capturedAt.AddDate(0, 0, -1)
	if !got.EffectiveDate.Equal(wantEffective) {
		t.Errorf("EffectiveDate: want %v, got %v", wantEffective, got.EffectiveDate)
	}
	if !got.CapturedAt.Equal(capturedAt) {
		t.Errorf("CapturedAt must stay unchanged: want %v, got %v", capturedAt, got.CapturedAt)
	}
}

// TestRowToSnapshot_EffectiveDate_DSTBoundary_CalendarDayNotDuration asserts
// that EffectiveDate steps one CALENDAR day, not a fixed 24-hour duration, on a
// day adjacent to a US spring-forward transition (design D3/risk in
// openspec/changes/telemetry-add-effective-date/design.md: AddDate(0,0,-1) vs.
// Add(-24*time.Hour)).
//
// 2026-03-08 is the US spring-forward day in America/New_York: clocks jump
// 02:00 EST -> 03:00 EDT, so the day has only 23 real hours. CapturedAt is set
// to 2026-03-09 01:30 (a wall-clock time that exists unambiguously the day
// AFTER the transition, fully in EDT). AddDate(0,0,-1) must land on
// 2026-03-08 01:30 (same wall clock, offset auto-adjusted to EST) — the
// calendar day before. Subtracting a fixed 24-hour duration instead would land
// on 2026-03-08 00:30 (drifted back an extra hour by the missing 02:00-02:59
// hour), which is what this test guards against.
func TestRowToSnapshot_EffectiveDate_DSTBoundary_CalendarDayNotDuration(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}

	capturedAt := time.Date(2026, 3, 9, 1, 30, 0, 0, loc)
	row := snapshotRow{
		TeslaID:    20,
		CapturedAt: pgtype.Timestamptz{Time: capturedAt, Valid: true},
	}

	got := rowToSnapshot(row)

	wantCalendarDay := time.Date(2026, 3, 8, 1, 30, 0, 0, loc) // AddDate: correct
	wrongDurationDay := capturedAt.Add(-24 * time.Hour)        // Add(-24h): drifted

	if !got.EffectiveDate.Equal(wantCalendarDay) {
		t.Errorf("EffectiveDate must step one calendar day: want %v, got %v", wantCalendarDay, got.EffectiveDate)
	}
	if got.EffectiveDate.Equal(wrongDurationDay) {
		t.Errorf("EffectiveDate must NOT equal the fixed-24h-duration result %v (DST drift)", wrongDurationDay)
	}
	if h, m := got.EffectiveDate.Hour(), got.EffectiveDate.Minute(); h != 1 || m != 30 {
		t.Errorf("EffectiveDate wall-clock time must stay 01:30, got %02d:%02d", h, m)
	}
}

// --- SnapshotsByVehicleBetween unit tests (offline, fake store — RM8 tier 1) ---

// fakeBetweenStore extends the read-only fake store seam to return configurable
// snapshots for SnapshotsByVehicleBetween, capturing the params passed by the reader
// for assertion. Write methods, latestSnapshotsByVehicles, and snapshotsByVehicleSince
// panic to catch accidental cross-path calls — Between tests exercise only the
// Between method (mirrors fakeHistoryStore's pattern for the Since path).
type fakeBetweenStore struct {
	snapshots  []Snapshot
	err        error
	gotTeslaID int64
	gotStart   time.Time
	gotEnd     time.Time
}

func (f *fakeBetweenStore) insertSnapshot(_ context.Context, _ Snapshot) error {
	panic("fakeBetweenStore: insertSnapshot must not be called")
}

func (f *fakeBetweenStore) insertPollAttempt(_ context.Context, _ Attempt) error {
	panic("fakeBetweenStore: insertPollAttempt must not be called")
}

func (f *fakeBetweenStore) latestSnapshotsByVehicles(_ context.Context, _ []int64) ([]Snapshot, error) {
	panic("fakeBetweenStore: latestSnapshotsByVehicles must not be called from Between path")
}

func (f *fakeBetweenStore) snapshotsByVehicleSince(_ context.Context, _ int64, _ time.Time) ([]Snapshot, error) {
	panic("fakeBetweenStore: snapshotsByVehicleSince must not be called from Between path")
}

// snapshotsByVehicleBetween is the seam the Between tests exercise: it captures the
// reader's forwarded params and returns the configurable slice (or the injected error).
func (f *fakeBetweenStore) snapshotsByVehicleBetween(_ context.Context, teslaID int64, start, end time.Time) ([]Snapshot, error) {
	f.gotTeslaID = teslaID
	f.gotStart = start
	f.gotEnd = end
	if f.err != nil {
		return nil, f.err
	}
	out := make([]Snapshot, len(f.snapshots))
	copy(out, f.snapshots)
	return out, nil
}

// snapshotsByVehicleUpdatedSince satisfies the store seam added by
// RM29-analytics-add-vehicle-metrics task 1.2. fakeBetweenStore exercises only the
// Between path, so this panics to catch any accidental cross-path call, mirroring
// snapshotsByVehicleSince's own precedent above.
func (f *fakeBetweenStore) snapshotsByVehicleUpdatedSince(_ context.Context, _ int64, _ time.Time) ([]Snapshot, error) {
	panic("fakeBetweenStore: snapshotsByVehicleUpdatedSince must not be called from Between path")
}

func (f *fakeBetweenStore) upsertSuperchargerHistory(_ context.Context, _ SuperchargerHistory) error {
	panic("fakeBetweenStore: upsertSuperchargerHistory must not be called")
}

// snapshotPrecedingDay satisfies the store seam added by
// RM29-telemetry-drop-derived-columns (design D2, wave 1). fakeBetweenStore
// exercises only the Between path, so this panics to catch any accidental
// cross-path call, mirroring this fake's existing pattern for the other
// unrelated seam methods above.
func (f *fakeBetweenStore) snapshotPrecedingDay(_ context.Context, _ int64, _ time.Time) (*Snapshot, error) {
	panic("fakeBetweenStore: snapshotPrecedingDay must not be called from the Between path")
}

// TestReader_SnapshotsByVehicleBetween_ParamsPassedThrough asserts that the reader
// forwards (teslaID, start, end) to the store seam UNCHANGED — the reader
// does NOT translate start/end into captured_at bounds. The +1day/+2day translation is
// the dbStore implementation's job (design D5), not the reader's; the public port stays
// a clean window. This mirrors TestReader_SnapshotsByVehicleSince_ParamsPassedThrough
// for the bounded-window path (task 4.2).
func TestReader_SnapshotsByVehicleBetween_ParamsPassedThrough(t *testing.T) {
	const teslaID = int64(7007)
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)

	fake := &fakeBetweenStore{}
	r := &reader{store: fake}

	_, err := r.SnapshotsByVehicleBetween(context.Background(), teslaID, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.gotTeslaID != teslaID {
		t.Errorf("teslaID not passed through: want %d, got %d", teslaID, fake.gotTeslaID)
	}
	if !fake.gotStart.Equal(start) {
		t.Errorf("start not passed through unchanged: want %v, got %v", start, fake.gotStart)
	}
	if !fake.gotEnd.Equal(end) {
		t.Errorf("end not passed through unchanged: want %v, got %v", end, fake.gotEnd)
	}
}

// TestReader_SnapshotsByVehicleBetween_ReturnsStoreSliceVerbatim asserts that the
// reader returns whatever the store returned, unmodified (parity with the
// LatestSnapshotsByVehicles / Since pass-through contract). The store's ordering
// (oldest-first) is preserved by the reader; the reader does not re-sort.
func TestReader_SnapshotsByVehicleBetween_ReturnsStoreSliceVerbatim(t *testing.T) {
	const teslaID = int64(7008)
	day1 := time.Date(2026, 8, 2, 8, 30, 0, 0, time.UTC)
	day2 := time.Date(2026, 8, 5, 8, 30, 0, 0, time.UTC)

	want := []Snapshot{
		{TeslaID: teslaID, CapturedAt: day1, BatteryLevelPct: 70, OdometerKm: 1609.344},
		{TeslaID: teslaID, CapturedAt: day2, BatteryLevelPct: 68, OdometerKm: 1689.8112},
	}

	fake := &fakeBetweenStore{snapshots: want}
	r := &reader{store: fake}

	got, err := r.SnapshotsByVehicleBetween(context.Background(), teslaID,
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 snapshots, got %d", len(got))
	}
	if !got[0].CapturedAt.Equal(day1) {
		t.Errorf("got[0].CapturedAt: want %v, got %v", day1, got[0].CapturedAt)
	}
	if !got[1].CapturedAt.Equal(day2) {
		t.Errorf("got[1].CapturedAt: want %v, got %v", day2, got[1].CapturedAt)
	}
	if got[0].BatteryLevelPct != 70 || got[1].BatteryLevelPct != 68 {
		t.Errorf("battery levels not passed through verbatim: got %d, %d", got[0].BatteryLevelPct, got[1].BatteryLevelPct)
	}
}

// TestReader_SnapshotsByVehicleBetween_EmptyNonNil asserts that an empty window
// returns a non-nil empty slice and nil error (D5 parity — no nil-slice footgun).
func TestReader_SnapshotsByVehicleBetween_EmptyNonNil(t *testing.T) {
	fake := &fakeBetweenStore{snapshots: nil}
	r := &reader{store: fake}

	got, err := r.SnapshotsByVehicleBetween(context.Background(), 42,
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("empty window must return non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("empty window must return 0 elements, got %d", len(got))
	}
}

// TestReader_SnapshotsByVehicleBetween_StoreError asserts that a store error is
// propagated to the caller without wrapping (parity with Since /
// LatestSnapshotsByVehicles).
func TestReader_SnapshotsByVehicleBetween_StoreError(t *testing.T) {
	wantErr := errors.New("store: between query failed")
	fake := &fakeBetweenStore{err: wantErr}
	r := &reader{store: fake}

	_, err := r.SnapshotsByVehicleBetween(context.Background(), 1,
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC),
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("want store error %v propagated, got %v", wantErr, err)
	}
}
