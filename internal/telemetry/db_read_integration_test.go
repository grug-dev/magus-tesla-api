package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// These tests exercise the real dbStore.latestSnapshotsByAccount method against a live
// Postgres from DATABASE_URL and self-skip when it is unset, so `go test ./...` stays
// green without a database (ai/go-conventions.md §persistence). They mirror the pattern
// established in db_integration_test.go. Requires the goose migration applied
// (`make migrate-up`).

// TestReadStore_LatestSnapshotsByAccount_MultiVehicleLatestWins inserts two snapshots
// for vehicle A (different captured_at) and one for vehicle B, then asserts:
//   - exactly two rows returned (one per vehicle — the DISTINCT ON batch semantics)
//   - vehicle A returns the NEWER snapshot (latest-wins)
//   - vehicle B returns its only snapshot
//   - all typed fields round-trip faithfully
func TestReadStore_LatestSnapshotsByAccount_MultiVehicleLatestWins(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const vehicleA = int64(800001)
	const vehicleB = int64(800002)
	cleanupVehicle(t, pool, accountID, vehicleA)
	cleanupVehicle(t, pool, accountID, vehicleB)

	// Vehicle A — older snapshot (lower battery).
	olderTime := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond)
	snapAOlder := Snapshot{
		AccountID:      accountID,
		TeslaID:        vehicleA,
		CapturedAt:     olderTime,
		BatteryLevel:   40,
		BatteryRange:   120.0,
		ChargingState:  "Disconnected",
		ChargeLimitSoc: 80,
		Odometer:       10000.0,
		InsideTemp:     20.0,
		OutsideTemp:    15.0,
		Locked:         false,
		SentryMode:     nil,
		CarVersion:     "2026.10.1",
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
		AccountID:      accountID,
		TeslaID:        vehicleA,
		CapturedAt:     newerTime,
		BatteryLevel:   75,
		BatteryRange:   240.5,
		ChargingState:  "Charging",
		ChargeLimitSoc: 90,
		Odometer:       10050.5,
		InsideTemp:     22.5,
		OutsideTemp:    18.0,
		Locked:         true,
		SentryMode:     &sentryOn,
		CarVersion:     "2026.20.1",
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
		AccountID:      accountID,
		TeslaID:        vehicleB,
		CapturedAt:     snapBTime,
		BatteryLevel:   60,
		BatteryRange:   180.0,
		ChargingState:  "Disconnected",
		ChargeLimitSoc: 85,
		Odometer:       5000.0,
		InsideTemp:     19.0,
		OutsideTemp:    14.0,
		Locked:         true,
		SentryMode:     &sentryOff,
		CarVersion:     "2026.18.3",
		// latitude/longitude dropped in 20260801000001; lossless in raw_data JSONB.
		RawData: []byte(`{"vehicle":"B"}`),
	}
	if err := st.insertSnapshot(ctx, snapB); err != nil {
		t.Fatalf("insertSnapshot (B): %v", err)
	}

	got, err := st.latestSnapshotsByAccount(ctx, accountID)
	if err != nil {
		t.Fatalf("latestSnapshotsByAccount: %v", err)
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
	if a.BatteryLevel != 75 {
		t.Errorf("vehicle A: want BatteryLevel=75 (newer), got %d", a.BatteryLevel)
	}
	if a.BatteryRange != 240.5 {
		t.Errorf("vehicle A: want BatteryRange=240.5, got %v", a.BatteryRange)
	}
	if a.ChargingState != "Charging" {
		t.Errorf("vehicle A: want ChargingState=Charging, got %q", a.ChargingState)
	}
	if a.ChargeLimitSoc != 90 {
		t.Errorf("vehicle A: want ChargeLimitSoc=90, got %d", a.ChargeLimitSoc)
	}
	if a.Odometer != 10050.5 {
		t.Errorf("vehicle A: want Odometer=10050.5, got %v", a.Odometer)
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
	if a.AccountID != accountID {
		t.Errorf("vehicle A: AccountID wrong: %v", a.AccountID)
	}

	// Vehicle B must be its only snapshot.
	b, ok := byVehicle[vehicleB]
	if !ok {
		t.Fatal("vehicle B not in results")
	}
	if !b.CapturedAt.UTC().Equal(snapBTime) {
		t.Errorf("vehicle B: want CapturedAt=%v, got %v", snapBTime, b.CapturedAt.UTC())
	}
	if b.BatteryLevel != 60 {
		t.Errorf("vehicle B: want BatteryLevel=60, got %d", b.BatteryLevel)
	}
	if b.SentryMode == nil || *b.SentryMode {
		t.Errorf("vehicle B: want SentryMode=*false, got %v", b.SentryMode)
	}
}

// TestReadStore_LatestSnapshotsByAccount_SentryNilRoundTrip inserts a snapshot with
// sentry_mode = NULL (nil *bool) and asserts the returned Snapshot.SentryMode is nil —
// preserving the absent≠off distinction through the full pgtype→domain mapping path.
func TestReadStore_LatestSnapshotsByAccount_SentryNilRoundTrip(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(800010)
	cleanupVehicle(t, pool, accountID, teslaID)

	snap := Snapshot{
		AccountID:     accountID,
		TeslaID:       teslaID,
		CapturedAt:    time.Now().UTC().Truncate(time.Microsecond),
		ChargingState: "Disconnected",
		CarVersion:    "2026.1.0",
		SentryMode:    nil, // absent field — must round-trip as SQL NULL → nil *bool
		RawData:       []byte(`{}`),
	}
	if err := st.insertSnapshot(ctx, snap); err != nil {
		t.Fatalf("insertSnapshot: %v", err)
	}

	got, err := st.latestSnapshotsByAccount(ctx, accountID)
	if err != nil {
		t.Fatalf("latestSnapshotsByAccount: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 snapshot, got %d", len(got))
	}
	if got[0].SentryMode != nil {
		t.Errorf("nil SentryMode must round-trip through SQL NULL as nil *bool, got %v", got[0].SentryMode)
	}
}

// TestReadStore_LatestSnapshotsByAccount_EmptyAccount asserts that an account with no
// rows returns an empty non-nil slice and nil error (design D5).
func TestReadStore_LatestSnapshotsByAccount_EmptyAccount(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()

	// Use a brand-new UUID that has no rows in vehicle_snapshots.
	got, err := st.latestSnapshotsByAccount(ctx, uuid.New())
	if err != nil {
		t.Fatalf("latestSnapshotsByAccount: %v", err)
	}
	if got == nil {
		t.Fatal("empty account must return non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("empty account must return 0 results, got %d", len(got))
	}
}

// TestReadStore_LatestSnapshotsByAccount_DifferentAccountExcluded inserts rows for two
// accounts and asserts that each account's query returns only its own rows — cross-account
// data must never appear in the result (multi-tenant isolation).
func TestReadStore_LatestSnapshotsByAccount_DifferentAccountExcluded(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	acctA := uuid.New()
	acctB := uuid.New()
	const vehicleA = int64(800020)
	const vehicleB = int64(800021)
	cleanupVehicle(t, pool, acctA, vehicleA)
	cleanupVehicle(t, pool, acctB, vehicleB)

	snapA := Snapshot{
		AccountID:     acctA,
		TeslaID:       vehicleA,
		CapturedAt:    time.Now().UTC().Truncate(time.Microsecond),
		ChargingState: "Disconnected",
		CarVersion:    "v",
		RawData:       []byte(`{}`),
	}
	snapB := Snapshot{
		AccountID:     acctB,
		TeslaID:       vehicleB,
		CapturedAt:    time.Now().UTC().Truncate(time.Microsecond),
		ChargingState: "Disconnected",
		CarVersion:    "v",
		RawData:       []byte(`{}`),
	}
	if err := st.insertSnapshot(ctx, snapA); err != nil {
		t.Fatalf("insertSnapshot (acctA): %v", err)
	}
	if err := st.insertSnapshot(ctx, snapB); err != nil {
		t.Fatalf("insertSnapshot (acctB): %v", err)
	}

	// acctA query must return only acctA's row.
	gotA, err := st.latestSnapshotsByAccount(ctx, acctA)
	if err != nil {
		t.Fatalf("latestSnapshotsByAccount (acctA): %v", err)
	}
	if len(gotA) != 1 || gotA[0].TeslaID != vehicleA {
		t.Errorf("acctA: want 1 row for vehicle %d, got %+v", vehicleA, gotA)
	}
	for _, s := range gotA {
		if s.AccountID != acctA {
			t.Errorf("acctA query returned a row belonging to a different account: %v", s.AccountID)
		}
	}

	// acctB query must return only acctB's row.
	gotB, err := st.latestSnapshotsByAccount(ctx, acctB)
	if err != nil {
		t.Fatalf("latestSnapshotsByAccount (acctB): %v", err)
	}
	if len(gotB) != 1 || gotB[0].TeslaID != vehicleB {
		t.Errorf("acctB: want 1 row for vehicle %d, got %+v", vehicleB, gotB)
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
		AccountID:     accountID,
		TeslaID:       vehicleA,
		CapturedAt:    before,
		BatteryLevel:  50,
		BatteryRange:  150.0,
		ChargingState: "Disconnected",
		CarVersion:    "v",
		Odometer:      500.0,
		RawData:       []byte(`{"day":"before"}`),
	}
	if err := st.insertSnapshot(ctx, snapBefore); err != nil {
		t.Fatalf("insertSnapshot (before): %v", err)
	}

	// Snapshot AT since — inclusive boundary, must appear first.
	day0 := baseTime.Truncate(time.Microsecond)
	snap0 := Snapshot{
		AccountID:     accountID,
		TeslaID:       vehicleA,
		CapturedAt:    day0,
		BatteryLevel:  60,
		BatteryRange:  180.0,
		ChargingState: "Disconnected",
		CarVersion:    "v",
		Odometer:      600.0,
		RawData:       []byte(`{"day":"0"}`),
	}
	if err := st.insertSnapshot(ctx, snap0); err != nil {
		t.Fatalf("insertSnapshot (day0): %v", err)
	}

	// Snapshot +1 day after since.
	day1 := baseTime.Add(24 * time.Hour).Truncate(time.Microsecond)
	snap1 := Snapshot{
		AccountID:     accountID,
		TeslaID:       vehicleA,
		CapturedAt:    day1,
		BatteryLevel:  72,
		BatteryRange:  220.0,
		ChargingState: "Disconnected",
		CarVersion:    "v",
		Odometer:      650.0,
		RawData:       []byte(`{"day":"1"}`),
	}
	if err := st.insertSnapshot(ctx, snap1); err != nil {
		t.Fatalf("insertSnapshot (day1): %v", err)
	}

	// Snapshot +2 days after since.
	day2 := baseTime.Add(48 * time.Hour).Truncate(time.Microsecond)
	snap2 := Snapshot{
		AccountID:     accountID,
		TeslaID:       vehicleA,
		CapturedAt:    day2,
		BatteryLevel:  80,
		BatteryRange:  240.0,
		ChargingState: "Charging",
		CarVersion:    "v",
		Odometer:      700.0,
		RawData:       []byte(`{"day":"2"}`),
	}
	if err := st.insertSnapshot(ctx, snap2); err != nil {
		t.Fatalf("insertSnapshot (day2): %v", err)
	}

	// Query since = day0 (inclusive boundary).
	got, err := st.snapshotsByVehicleSince(ctx, accountID, vehicleA, day0)
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
	if got[0].BatteryLevel != 60 {
		t.Errorf("got[0]: want BatteryLevel=60, got %d", got[0].BatteryLevel)
	}
	if got[0].Odometer != 600.0 {
		t.Errorf("got[0]: want Odometer=600.0, got %v", got[0].Odometer)
	}

	if !got[1].CapturedAt.UTC().Equal(day1) {
		t.Errorf("got[1]: want CapturedAt=%v, got %v", day1, got[1].CapturedAt.UTC())
	}
	if got[1].BatteryLevel != 72 {
		t.Errorf("got[1]: want BatteryLevel=72, got %d", got[1].BatteryLevel)
	}

	if !got[2].CapturedAt.UTC().Equal(day2) {
		t.Errorf("got[2]: want CapturedAt=%v, got %v", day2, got[2].CapturedAt.UTC())
	}
	if got[2].BatteryLevel != 80 {
		t.Errorf("got[2]: want BatteryLevel=80, got %d", got[2].BatteryLevel)
	}

	// The snapshot before the since boundary must NOT appear.
	for _, s := range got {
		if s.CapturedAt.UTC().Equal(before) || s.CapturedAt.UTC().Before(day0) {
			t.Errorf("snapshot before since boundary appeared in results: captured_at=%v", s.CapturedAt)
		}
	}
}

// TestReadStore_SnapshotsByVehicleSince_CrossAccountExcluded seeds two accounts
// with snapshots for the same vehicle TeslaID and asserts that only the queried
// account's rows are returned (defense-in-depth multi-tenant isolation, D2).
func TestReadStore_SnapshotsByVehicleSince_CrossAccountExcluded(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	acctA := uuid.New()
	acctB := uuid.New()
	const vehicleID = int64(900002) // same TeslaID used in both accounts
	cleanupVehicle(t, pool, acctA, vehicleID)
	cleanupVehicle(t, pool, acctB, vehicleID)

	since := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Microsecond)

	snapA := Snapshot{
		AccountID:     acctA,
		TeslaID:       vehicleID,
		CapturedAt:    time.Now().UTC().Truncate(time.Microsecond),
		ChargingState: "Disconnected",
		CarVersion:    "v",
		BatteryLevel:  55,
		RawData:       []byte(`{}`),
	}
	snapB := Snapshot{
		AccountID:     acctB,
		TeslaID:       vehicleID,
		CapturedAt:    time.Now().UTC().Truncate(time.Microsecond),
		ChargingState: "Disconnected",
		CarVersion:    "v",
		BatteryLevel:  77,
		RawData:       []byte(`{}`),
	}
	if err := st.insertSnapshot(ctx, snapA); err != nil {
		t.Fatalf("insertSnapshot (acctA): %v", err)
	}
	if err := st.insertSnapshot(ctx, snapB); err != nil {
		t.Fatalf("insertSnapshot (acctB): %v", err)
	}

	// Query for acctA only — must not see acctB's row.
	gotA, err := st.snapshotsByVehicleSince(ctx, acctA, vehicleID, since)
	if err != nil {
		t.Fatalf("snapshotsByVehicleSince (acctA): %v", err)
	}
	if len(gotA) != 1 {
		t.Fatalf("acctA: want 1 row, got %d", len(gotA))
	}
	if gotA[0].AccountID != acctA {
		t.Errorf("acctA: returned row belongs to wrong account: %v", gotA[0].AccountID)
	}
	if gotA[0].BatteryLevel != 55 {
		t.Errorf("acctA: want BatteryLevel=55, got %d", gotA[0].BatteryLevel)
	}

	// Query for acctB only — must not see acctA's row.
	gotB, err := st.snapshotsByVehicleSince(ctx, acctB, vehicleID, since)
	if err != nil {
		t.Fatalf("snapshotsByVehicleSince (acctB): %v", err)
	}
	if len(gotB) != 1 {
		t.Fatalf("acctB: want 1 row, got %d", len(gotB))
	}
	if gotB[0].AccountID != acctB {
		t.Errorf("acctB: returned row belongs to wrong account: %v", gotB[0].AccountID)
	}
	if gotB[0].BatteryLevel != 77 {
		t.Errorf("acctB: want BatteryLevel=77, got %d", gotB[0].BatteryLevel)
	}
}

// TestReadStore_SnapshotsByVehicleSince_EmptyWindow asserts that no error and a
// non-nil empty slice are returned when no snapshots exist at or after since.
func TestReadStore_SnapshotsByVehicleSince_EmptyWindow(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()

	// Use a future since that no rows can satisfy.
	futureSince := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	got, err := st.snapshotsByVehicleSince(ctx, uuid.New(), 900099, futureSince)
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
