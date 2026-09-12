package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/clock"
)

// These tests exercise the real dbStore.latestSnapshotsByVehicles method against a live
// Postgres from TEST_DATABASE_URL and self-skip when it is unset, so `go test ./...` stays
// green without a database (ai/go-conventions.md §persistence). They mirror the pattern
// established in db_integration_test.go. Requires the goose migration applied
// (`make migrate-up`).

// TestReadStore_LatestSnapshotsByVehicles_MultiVehicleLatestWins inserts two snapshots
// for vehicle A (different captured_at, on DIFFERENT calendar days) and one for
// vehicle B, then asserts:
//   - exactly two rows returned (one per vehicle — the DISTINCT ON batch semantics)
//   - vehicle A returns the NEWER snapshot (latest-wins)
//   - vehicle B returns its only snapshot
//   - all typed fields round-trip faithfully
//
// Vehicle A's two snapshots MUST land on different calendar days: two same-day
// captures for one vehicle collapse into a single row via the upsert, which would
// leave DISTINCT ON with only one row to choose from and silently stop exercising
// latest-wins at all. The 26h/1h offsets below are >24h apart, so they are always on
// different dates regardless of what time of day the suite runs.
func TestReadStore_LatestSnapshotsByVehicles_MultiVehicleLatestWins(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const vehicleA = int64(800001)
	const vehicleB = int64(800002)
	cleanupVehicle(t, pool, accountID, vehicleA)
	cleanupVehicle(t, pool, accountID, vehicleB)

	// Vehicle A — older snapshot (lower battery), on the PREVIOUS calendar day so it
	// coexists with the newer one instead of being replaced by it (see doc comment).
	olderTime := time.Now().UTC().Add(-26 * time.Hour).Truncate(time.Microsecond)
	snapAOlder := Snapshot{
		TeslaID:           vehicleA,
		CapturedAt:        olderTime,
		CapturedDate:      clock.CalendarDay(olderTime, time.UTC),
		BatteryLevelPct:   40,
		BatteryRangeKm:    193.12128, // 120.0 mi * 1.609344
		ChargingState:     "Disconnected",
		ChargeLimitSocPct: 80,
		OdometerKm:        16093.44, // 10000.0 mi * 1.609344
		InsideTempC:       20.0,
		OutsideTempC:      15.0,
		Locked:            false,
		SentryMode:        nil,
		CarVersion:        "2026.10.1",
		// latitude/longitude dropped in 20260801000001; lossless in raw_data JSONB.
		RawData: []byte(`{"vehicle":"A-old"}`),
	}
	if err := st.insertSnapshot(ctx, snapAOlder); err != nil {
		t.Fatalf("insertSnapshot (A older): %v", err)
	}

	// Vehicle A — newer snapshot (higher battery). This is the one DISTINCT ON should return.
	newerTime := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	sentryOn := true
	snapANewer := Snapshot{
		TeslaID:           vehicleA,
		CapturedAt:        newerTime,
		CapturedDate:      clock.CalendarDay(newerTime, time.UTC),
		BatteryLevelPct:   75,
		BatteryRangeKm:    387.047232, // 240.5 mi * 1.609344
		ChargingState:     "Charging",
		ChargeLimitSocPct: 90,
		OdometerKm:        16174.711872, // 10050.5 mi * 1.609344
		InsideTempC:       22.5,
		OutsideTempC:      18.0,
		Locked:            true,
		SentryMode:        &sentryOn,
		CarVersion:        "2026.20.1",
		// latitude/longitude dropped in 20260801000001; lossless in raw_data JSONB.
		RawData: []byte(`{"vehicle":"A-new"}`),
	}
	if err := st.insertSnapshot(ctx, snapANewer); err != nil {
		t.Fatalf("insertSnapshot (A newer): %v", err)
	}

	// Vehicle B — one snapshot only.
	snapBTime := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Microsecond)
	sentryOff := false
	snapB := Snapshot{
		TeslaID:           vehicleB,
		CapturedAt:        snapBTime,
		CapturedDate:      clock.CalendarDay(snapBTime, time.UTC),
		BatteryLevelPct:   60,
		BatteryRangeKm:    289.68192, // 180.0 mi * 1.609344
		ChargingState:     "Disconnected",
		ChargeLimitSocPct: 85,
		OdometerKm:        8046.72, // 5000.0 mi * 1.609344
		InsideTempC:       19.0,
		OutsideTempC:      14.0,
		Locked:            true,
		SentryMode:        &sentryOff,
		CarVersion:        "2026.18.3",
		// latitude/longitude dropped in 20260801000001; lossless in raw_data JSONB.
		RawData: []byte(`{"vehicle":"B"}`),
	}
	if err := st.insertSnapshot(ctx, snapB); err != nil {
		t.Fatalf("insertSnapshot (B): %v", err)
	}

	got, err := st.latestSnapshotsByVehicles(ctx, []int64{vehicleA, vehicleB})
	if err != nil {
		t.Fatalf("latestSnapshotsByVehicles: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want exactly 2 results (one per vehicle), got %d", len(got))
	}

	byVehicle := map[int64]Snapshot{}
	for _, s := range got {
		byVehicle[s.TeslaID] = s
	}

	// Vehicle A must be the NEWER snapshot.
	a, ok := byVehicle[vehicleA]
	if !ok {
		t.Fatal("vehicle A not in results")
	}
	if !a.CapturedAt.UTC().Equal(newerTime) {
		t.Errorf("vehicle A: want newer CapturedAt=%v, got %v", newerTime, a.CapturedAt.UTC())
	}
	if a.BatteryLevelPct != 75 {
		t.Errorf("vehicle A: want BatteryLevelPct=75 (newer), got %d", a.BatteryLevelPct)
	}
	if a.BatteryRangeKm != 387.047232 {
		t.Errorf("vehicle A: want BatteryRangeKm=387.047232, got %v", a.BatteryRangeKm)
	}
	if a.ChargingState != "Charging" {
		t.Errorf("vehicle A: want ChargingState=Charging, got %q", a.ChargingState)
	}
	if a.ChargeLimitSocPct != 90 {
		t.Errorf("vehicle A: want ChargeLimitSocPct=90, got %d", a.ChargeLimitSocPct)
	}
	if a.OdometerKm != 16174.711872 {
		t.Errorf("vehicle A: want OdometerKm=16174.711872, got %v", a.OdometerKm)
	}
	if !a.Locked {
		t.Error("vehicle A: want Locked=true")
	}
	if a.SentryMode == nil || !*a.SentryMode {
		t.Errorf("vehicle A: want SentryMode=*true, got %v", a.SentryMode)
	}
	if a.CarVersion != "2026.20.1" {
		t.Errorf("vehicle A: want CarVersion=2026.20.1, got %q", a.CarVersion)
	}
	// latitude/longitude dropped in 20260801000001 — not asserted here;
	// values remain recoverable from raw_data->'drive_state'.

	// Vehicle B must be its only snapshot.
	b, ok := byVehicle[vehicleB]
	if !ok {
		t.Fatal("vehicle B not in results")
	}
	if !b.CapturedAt.UTC().Equal(snapBTime) {
		t.Errorf("vehicle B: want CapturedAt=%v, got %v", snapBTime, b.CapturedAt.UTC())
	}
	if b.BatteryLevelPct != 60 {
		t.Errorf("vehicle B: want BatteryLevelPct=60, got %d", b.BatteryLevelPct)
	}
	if b.SentryMode == nil || *b.SentryMode {
		t.Errorf("vehicle B: want SentryMode=*false, got %v", b.SentryMode)
	}
}

// TestReadStore_LatestSnapshotsByVehicles_SentryNilRoundTrip inserts a snapshot with
// sentry_mode = NULL (nil *bool) and asserts the returned Snapshot.SentryMode is nil —
// preserving the absent≠off distinction through the full pgtype→domain mapping path.
func TestReadStore_LatestSnapshotsByVehicles_SentryNilRoundTrip(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(800010)
	cleanupVehicle(t, pool, accountID, teslaID)

	captured := time.Now().UTC().Truncate(time.Microsecond)
	snap := Snapshot{
		TeslaID:       teslaID,
		CapturedAt:    captured,
		CapturedDate:  clock.CalendarDay(captured, time.UTC),
		ChargingState: "Disconnected",
		CarVersion:    "2026.1.0",
		SentryMode:    nil, // absent field — must round-trip as SQL NULL → nil *bool
		RawData:       []byte(`{}`),
	}
	if err := st.insertSnapshot(ctx, snap); err != nil {
		t.Fatalf("insertSnapshot: %v", err)
	}

	got, err := st.latestSnapshotsByVehicles(ctx, []int64{teslaID})
	if err != nil {
		t.Fatalf("latestSnapshotsByVehicles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 snapshot, got %d", len(got))
	}
	if got[0].SentryMode != nil {
		t.Errorf("nil SentryMode must round-trip through SQL NULL as nil *bool, got %v", got[0].SentryMode)
	}
}

// TestReadStore_LatestSnapshotsByVehicles_EmptyResult asserts that a batch of
// tesla_ids with no rows returns an empty non-nil slice and nil error.
func TestReadStore_LatestSnapshotsByVehicles_EmptyResult(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()

	// Use a tesla_id that has no rows in vehicle_snapshots.
	got, err := st.latestSnapshotsByVehicles(ctx, []int64{999999999})
	if err != nil {
		t.Fatalf("latestSnapshotsByVehicles: %v", err)
	}
	if got == nil {
		t.Fatal("no matching rows must return non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("no matching rows must return 0 results, got %d", len(got))
	}
}

// --- SnapshotsByVehicleSince store tests ---

// TestReadStore_SnapshotsByVehicleSince_OldestFirstAndSinceBoundary seeds three
// snapshots for vehicleA across three days, plus one snapshot for vehicleA before
// the since boundary. It asserts:
//   - only snapshots at or after since are returned (boundary inclusive)
//   - snapshots are oldest-first by captured_at
//   - all typed fields (battery_level, odometer, captured_at) round-trip faithfully
func TestReadStore_SnapshotsByVehicleSince_OldestFirstAndSinceBoundary(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const vehicleA = int64(900001)
	cleanupVehicle(t, pool, accountID, vehicleA)

	baseTime := time.Date(2026, 7, 1, 3, 30, 0, 0, time.UTC)

	// Snapshot BEFORE since — must NOT appear in results.
	before := baseTime.Add(-24 * time.Hour).Truncate(time.Microsecond)
	snapBefore := Snapshot{
		TeslaID:         vehicleA,
		CapturedAt:      before,
		CapturedDate:    clock.CalendarDay(before, time.UTC),
		BatteryLevelPct: 50,
		BatteryRangeKm:  241.4016, // 150.0 mi * 1.609344
		ChargingState:   "Disconnected",
		CarVersion:      "v",
		OdometerKm:      804.672, // 500.0 mi * 1.609344
		RawData:         []byte(`{"day":"before"}`),
	}
	if err := st.insertSnapshot(ctx, snapBefore); err != nil {
		t.Fatalf("insertSnapshot (before): %v", err)
	}

	// Snapshot AT since — inclusive boundary, must appear first.
	day0 := baseTime.Truncate(time.Microsecond)
	snap0 := Snapshot{
		TeslaID:         vehicleA,
		CapturedAt:      day0,
		CapturedDate:    clock.CalendarDay(day0, time.UTC),
		BatteryLevelPct: 60,
		BatteryRangeKm:  289.68192, // 180.0 mi * 1.609344
		ChargingState:   "Disconnected",
		CarVersion:      "v",
		OdometerKm:      965.6064, // 600.0 mi * 1.609344
		RawData:         []byte(`{"day":"0"}`),
	}
	if err := st.insertSnapshot(ctx, snap0); err != nil {
		t.Fatalf("insertSnapshot (day0): %v", err)
	}

	// Snapshot +1 day after since.
	day1 := baseTime.Add(24 * time.Hour).Truncate(time.Microsecond)
	snap1 := Snapshot{
		TeslaID:         vehicleA,
		CapturedAt:      day1,
		CapturedDate:    clock.CalendarDay(day1, time.UTC),
		BatteryLevelPct: 72,
		BatteryRangeKm:  354.05568, // 220.0 mi * 1.609344
		ChargingState:   "Disconnected",
		CarVersion:      "v",
		OdometerKm:      1046.0736, // 650.0 mi * 1.609344
		RawData:         []byte(`{"day":"1"}`),
	}
	if err := st.insertSnapshot(ctx, snap1); err != nil {
		t.Fatalf("insertSnapshot (day1): %v", err)
	}

	// Snapshot +2 days after since.
	day2 := baseTime.Add(48 * time.Hour).Truncate(time.Microsecond)
	snap2 := Snapshot{
		TeslaID:         vehicleA,
		CapturedAt:      day2,
		CapturedDate:    clock.CalendarDay(day2, time.UTC),
		BatteryLevelPct: 80,
		BatteryRangeKm:  386.24256, // 240.0 mi * 1.609344
		ChargingState:   "Charging",
		CarVersion:      "v",
		OdometerKm:      1126.5408, // 700.0 mi * 1.609344
		RawData:         []byte(`{"day":"2"}`),
	}
	if err := st.insertSnapshot(ctx, snap2); err != nil {
		t.Fatalf("insertSnapshot (day2): %v", err)
	}

	// Query since = day0 (inclusive boundary).
	got, err := st.snapshotsByVehicleSince(ctx, vehicleA, day0)
	if err != nil {
		t.Fatalf("snapshotsByVehicleSince: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 snapshots (day0, day1, day2), got %d: %+v", len(got), got)
	}

	// Oldest-first: got[0] = day0 < got[1] = day1 < got[2] = day2.
	if !got[0].CapturedAt.UTC().Equal(day0) {
		t.Errorf("got[0]: want CapturedAt=%v, got %v", day0, got[0].CapturedAt.UTC())
	}
	if got[0].BatteryLevelPct != 60 {
		t.Errorf("got[0]: want BatteryLevelPct=60, got %d", got[0].BatteryLevelPct)
	}
	if got[0].OdometerKm != 965.6064 {
		t.Errorf("got[0]: want OdometerKm=965.6064, got %v", got[0].OdometerKm)
	}

	if !got[1].CapturedAt.UTC().Equal(day1) {
		t.Errorf("got[1]: want CapturedAt=%v, got %v", day1, got[1].CapturedAt.UTC())
	}
	if got[1].BatteryLevelPct != 72 {
		t.Errorf("got[1]: want BatteryLevelPct=72, got %d", got[1].BatteryLevelPct)
	}

	if !got[2].CapturedAt.UTC().Equal(day2) {
		t.Errorf("got[2]: want CapturedAt=%v, got %v", day2, got[2].CapturedAt.UTC())
	}
	if got[2].BatteryLevelPct != 80 {
		t.Errorf("got[2]: want BatteryLevelPct=80, got %d", got[2].BatteryLevelPct)
	}

	// The snapshot before the since boundary must NOT appear.
	for _, s := range got {
		if s.CapturedAt.UTC().Equal(before) || s.CapturedAt.UTC().Before(day0) {
			t.Errorf("snapshot before since boundary appeared in results: captured_at=%v", s.CapturedAt)
		}
	}
}

// --- EffectiveDate (telemetry-add-effective-date, task 2.3) ---

// TestReadStore_LatestSnapshotsByVehicles_EffectiveDate inserts a real row via
// insertSnapshot and asserts that latestSnapshotsByVehicles — which maps every
// row through rowToSnapshot (service.go) — returns a Snapshot whose
// EffectiveDate is non-zero and equals CapturedAt minus one calendar day, end
// to end against a live Postgres row (not just the pure-function unit test in
// reader_test.go).
func TestReadStore_LatestSnapshotsByVehicles_EffectiveDate(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(800030)
	cleanupVehicle(t, pool, accountID, teslaID)

	captured := time.Now().UTC().Truncate(time.Microsecond)
	snap := Snapshot{
		TeslaID:       teslaID,
		CapturedAt:    captured,
		CapturedDate:  clock.CalendarDay(captured, time.UTC),
		ChargingState: "Disconnected",
		CarVersion:    "2026.20.1",
		RawData:       []byte(`{}`),
	}
	if err := st.insertSnapshot(ctx, snap); err != nil {
		t.Fatalf("insertSnapshot: %v", err)
	}

	got, err := st.latestSnapshotsByVehicles(ctx, []int64{teslaID})
	if err != nil {
		t.Fatalf("latestSnapshotsByVehicles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 snapshot, got %d", len(got))
	}

	if got[0].EffectiveDate.IsZero() {
		t.Fatal("EffectiveDate must be non-zero for a real DB row")
	}
	wantEffective := captured.AddDate(0, 0, -1)
	if !got[0].EffectiveDate.UTC().Equal(wantEffective) {
		t.Errorf("EffectiveDate: want %v, got %v", wantEffective, got[0].EffectiveDate.UTC())
	}
	if !got[0].CapturedAt.UTC().Equal(captured) {
		t.Errorf("CapturedAt must round-trip unchanged: want %v, got %v", captured, got[0].CapturedAt.UTC())
	}
}

// TestReadStore_SnapshotsByVehicleSince_EffectiveDate mirrors the assertion
// above for the history read path, which shares the same rowToSnapshot mapper
// (design D4) — proving both Reader methods carry EffectiveDate on real rows.
func TestReadStore_SnapshotsByVehicleSince_EffectiveDate(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(800031)
	cleanupVehicle(t, pool, accountID, teslaID)

	captured := time.Now().UTC().Truncate(time.Microsecond)
	snap := Snapshot{
		TeslaID:       teslaID,
		CapturedAt:    captured,
		CapturedDate:  clock.CalendarDay(captured, time.UTC),
		ChargingState: "Disconnected",
		CarVersion:    "2026.20.1",
		RawData:       []byte(`{}`),
	}
	if err := st.insertSnapshot(ctx, snap); err != nil {
		t.Fatalf("insertSnapshot: %v", err)
	}

	since := captured.Add(-time.Hour)
	got, err := st.snapshotsByVehicleSince(ctx, teslaID, since)
	if err != nil {
		t.Fatalf("snapshotsByVehicleSince: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 snapshot, got %d", len(got))
	}

	if got[0].EffectiveDate.IsZero() {
		t.Fatal("EffectiveDate must be non-zero for a real DB row")
	}
	wantEffective := captured.AddDate(0, 0, -1)
	if !got[0].EffectiveDate.UTC().Equal(wantEffective) {
		t.Errorf("EffectiveDate: want %v, got %v", wantEffective, got[0].EffectiveDate.UTC())
	}
}

// TestReadStore_SnapshotsByVehicleSince_EmptyWindow asserts that no error and a
// non-nil empty slice are returned when no snapshots exist at or after since.
func TestReadStore_SnapshotsByVehicleSince_EmptyWindow(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()

	// Use a future since that no rows can satisfy.
	futureSince := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	got, err := st.snapshotsByVehicleSince(ctx, 900099, futureSince)
	if err != nil {
		t.Fatalf("snapshotsByVehicleSince: %v", err)
	}
	if got == nil {
		t.Fatal("empty window must return non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("empty window must return 0 elements, got %d", len(got))
	}
}

// --- SnapshotsByVehicleBetween store tests (RM8 tier 1) ---
//
// These exercise the real dbStore.snapshotsByVehicleBetween implementation against a
// live Postgres (testcontainers-provisioned). The dbStore translates the caller's
// (start, end) window into captured_at bounds (start+1day, end+2day) and runs the
// forward-range-scan query; rowToSnapshot then derives EffectiveDate = CapturedAt-1day.
// Together with the offline reader pass-through tests (reader_test.go), these prove
// the full EffectiveDate ∈ [start, end] inclusive semantics end-to-end.

// seedConsecutiveNights inserts count nightly snapshots for teslaID, starting at
// baseEffectiveDay (the EffectiveDate of the first snapshot), one per calendar day.
// Each capture is at 08:30 UTC (mirrors the nightly 03:30-local / America/Bogota
// UTC-5 cadence) so EffectiveDate = captureDay - 1 calendar day lands exactly on
// baseEffectiveDay for the first row. BatteryLevelPct and OdometerKm are uniquely
// tagged per night so a test can confirm it got the rows it expected. accountID is
// kept as a parameter only so callers that seed the same vehicle under different
// (now meaningless) accounts do not need to change their call shape; it is not used
// to build the row — vehicle_snapshots is keyed on tesla_id alone.
func seedConsecutiveNights(t *testing.T, st *dbStore, ctx context.Context, accountID uuid.UUID, teslaID int64, baseEffectiveDay time.Time, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		effDay := baseEffectiveDay.AddDate(0, 0, i)
		captured := effDay.AddDate(0, 0, 1).Add(8*time.Hour + 30*time.Minute)
		snap := Snapshot{
			TeslaID:         teslaID,
			CapturedAt:      captured.Truncate(time.Microsecond),
			CapturedDate:    clock.CalendarDay(captured, time.UTC),
			ChargingState:   "Disconnected",
			CarVersion:      "v",
			BatteryLevelPct: 50 + i, // tag per-night so the test can assert ordering/identity
			OdometerKm:      float64(10000 + i),
			RawData:         []byte(`{}`),
		}
		if err := st.insertSnapshot(ctx, snap); err != nil {
			t.Fatalf("seedConsecutiveNights insert (%d, effDay=%v): %v", i, effDay, err)
		}
	}
}

// TestReadStore_SnapshotsByVehicleBetween_EffectiveDateInRange seeds 14 consecutive
// nightly snapshots (EffectiveDate span Aug 1..Aug 14, 2026) for one vehicle in one
// account, then requests a 7-day sub-window [Aug 3, Aug 9] inclusive and asserts:
//   - exactly the 7 snapshots whose EffectiveDate calendar day ∈ [start, end] are returned
//   - every returned EffectiveDate calendar day is within [start, end] inclusive
//   - the result is ordered ascending by EffectiveDate (oldest-first)
//   - each returned captured_at matches the stored row (EffectiveDate + 1 calendar day at 08:30 UTC)
//
// This is the core EffectiveDate-in-range + ascending-order scenario (task 5.1).
func TestReadStore_SnapshotsByVehicleBetween_EffectiveDateInRange(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const vehicleA = int64(910001)
	cleanupVehicle(t, pool, accountID, vehicleA)

	// 14 nights: EffectiveDate Aug 1..Aug 14, 2026 (UTC).
	baseEffectiveDay := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	seedConsecutiveNights(t, st, ctx, accountID, vehicleA, baseEffectiveDay, 14)

	// 7-day sub-window, end inclusive: EffectiveDate ∈ [Aug 3, Aug 9].
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)

	got, err := st.snapshotsByVehicleBetween(ctx, vehicleA, start, end)
	if err != nil {
		t.Fatalf("snapshotsByVehicleBetween: %v", err)
	}

	// Expected EffectiveDate calendar days: Aug 3, 4, 5, 6, 7, 8, 9 → 7 snapshots.
	if len(got) != 7 {
		t.Fatalf("want 7 snapshots (EffectiveDate Aug 3..Aug 9), got %d: %+v", len(got), got)
	}

	// Each returned EffectiveDate calendar day must be within [start, end] inclusive,
	// the slice must be ascending by EffectiveDate calendar day, and captured_at must
	// match the stored row (effDay + 1 calendar day at 08:30 UTC). dateNormalizes a
	// full time.Time (EffectiveDate preserves time-of-day) to its UTC-midnight
	// calendar day for comparison against the UTC-midnight start/end bounds.
	for i, s := range got {
		effCalDay := clock.CalendarDay(s.EffectiveDate, time.UTC)
		if effCalDay.Before(start) || effCalDay.After(end) {
			t.Errorf("got[%d]: EffectiveDate calendar day %v outside [%v, %v]", i, effCalDay, start, end)
		}
		wantDay := start.AddDate(0, 0, i)
		if !effCalDay.Equal(wantDay) {
			t.Errorf("got[%d]: want EffectiveDate calendar day %v (ascending), got %v", i, wantDay, effCalDay)
		}
		// captured_at for this row == wantDay + 1 calendar day at 08:30 UTC.
		wantCaptured := wantDay.AddDate(0, 0, 1).Add(8*time.Hour + 30*time.Minute)
		if !s.CapturedAt.UTC().Equal(wantCaptured) {
			t.Errorf("got[%d]: want CapturedAt %v, got %v", i, wantCaptured, s.CapturedAt.UTC())
		}
		// BatteryLevelPct tag increments with the night (50 + i offset into the 14-day
		// seed: Aug 3 is index 2 → BatteryLevelPct 52), confirming the right row.
		if s.BatteryLevelPct != 50+2+i {
			t.Errorf("got[%d]: want BatteryLevelPct=%d (seed tag), got %d", i, 50+2+i, s.BatteryLevelPct)
		}
	}
}

// TestReadStore_SnapshotsByVehicleBetween_BoundariesInclusive asserts the start and
// end boundaries are both INCLUSIVE, and the just-outside snapshots are excluded
// (task 5.2). Seed four snapshots with EffectiveDate calendar days spanning a 2-day
// window [Aug 5, Aug 6]:
//   - EffectiveDate Aug 4 (start-1) → EXCLUDED
//   - EffectiveDate Aug 5 (==start) → INCLUDED
//   - EffectiveDate Aug 6 (==end)   → INCLUDED
//   - EffectiveDate Aug 7 (end+1)   → EXCLUDED
//
// Expect exactly 2 snapshots (Aug 5, Aug 6), proving start-inclusive, end-inclusive,
// start-1 excluded, and end+1 excluded in one shot.
func TestReadStore_SnapshotsByVehicleBetween_BoundariesInclusive(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const vehicleA = int64(910002)
	cleanupVehicle(t, pool, accountID, vehicleA)

	// Seed 4 nights: EffectiveDate Aug 4, 5, 6, 7 (UTC).
	baseEffectiveDay := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	seedConsecutiveNights(t, st, ctx, accountID, vehicleA, baseEffectiveDay, 4)

	// 2-day window: EffectiveDate ∈ [Aug 5, Aug 6].
	start := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)

	got, err := st.snapshotsByVehicleBetween(ctx, vehicleA, start, end)
	if err != nil {
		t.Fatalf("snapshotsByVehicleBetween: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want exactly 2 snapshots (EffectiveDate Aug 5, Aug 6), got %d: %+v", len(got), got)
	}

	// got[0] = Aug 5 (== start, inclusive), got[1] = Aug 6 (== end, inclusive).
	wantDays := []time.Time{start, end}
	for i, s := range got {
		effCalDay := clock.CalendarDay(s.EffectiveDate, time.UTC)
		if !effCalDay.Equal(wantDays[i]) {
			t.Errorf("got[%d]: want EffectiveDate calendar day %v, got %v", i, wantDays[i], effCalDay)
		}
	}
	// Defense: no just-outside snapshot (Aug 4 or Aug 7) may appear.
	for _, s := range got {
		effCalDay := clock.CalendarDay(s.EffectiveDate, time.UTC)
		if effCalDay.Equal(start.AddDate(0, 0, -1)) {
			t.Errorf("start-1 (Aug 4) snapshot must NOT be included: %v", effCalDay)
		}
		if effCalDay.Equal(end.AddDate(0, 0, 1)) {
			t.Errorf("end+1 (Aug 7) snapshot must NOT be included: %v", effCalDay)
		}
	}
}

// TestReadStore_SnapshotsByVehicleBetween_EmptyNonNil asserts that a window with no
// snapshots returns an empty (non-nil) slice and nil error (task 5.3 / design D5
// parity — no nil-slice footgun for the gateway).
func TestReadStore_SnapshotsByVehicleBetween_EmptyNonNil(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()

	// A far-future window no rows can satisfy (the vehicle has no snapshots at all).
	start := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2099, 1, 7, 0, 0, 0, 0, time.UTC)
	got, err := st.snapshotsByVehicleBetween(ctx, 910099, start, end)
	if err != nil {
		t.Fatalf("snapshotsByVehicleBetween: %v", err)
	}
	if got == nil {
		t.Fatal("empty window must return non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("empty window must return 0 elements, got %d", len(got))
	}
}

// TestReadStore_SnapshotsByVehicleBetween_SiblingVehicleExcluded seeds two
// vehicles over the same nights and asserts a query for one never returns the
// other's rows. The window predicate is tesla_id plus a date range, so a sibling
// vehicle is the only way a wrong row can reach the result.
func TestReadStore_SnapshotsByVehicleBetween_SiblingVehicleExcluded(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	acct := uuid.New()
	const vehicleV = int64(910010)
	const vehicleW = int64(910011)
	cleanupVehicle(t, pool, acct, vehicleV)
	cleanupVehicle(t, pool, acct, vehicleW)

	// Shared window so both seeds are in-range.
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	baseEffV := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)

	// Two nights for the requested vehicle, two for its sibling.
	seedConsecutiveNights(t, st, ctx, acct, vehicleV, baseEffV, 2)
	seedConsecutiveNights(t, st, ctx, acct, vehicleW, baseEffV, 2)

	got, err := st.snapshotsByVehicleBetween(ctx, vehicleV, start, end)
	if err != nil {
		t.Fatalf("snapshotsByVehicleBetween (vehicleV): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want exactly 2 snapshots for vehicleV, got %d: %+v", len(got), got)
	}
	for i, s := range got {
		if s.TeslaID != vehicleV {
			t.Errorf("got[%d]: returned row belongs to wrong vehicle: %d (want %d)", i, s.TeslaID, vehicleV)
		}
		// BatteryLevelPct tag for vehicleV seed: 50, 51 — confirms it is the right
		// vehicle's rows, not vehicleW's.
		if s.BatteryLevelPct != 50+i {
			t.Errorf("got[%d]: want BatteryLevelPct=%d (vehicleV seed tag), got %d", i, 50+i, s.BatteryLevelPct)
		}
	}
}
