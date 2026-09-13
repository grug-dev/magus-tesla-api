// Package charging_test — database-backed integration tests for
// MirrorWatermarkStore (mirror_watermark.go) and its GetMirrorWatermark /
// UpsertMirrorWatermark queries (db/query.sql). Re-keyed on tesla_id, not
// account_id (RM57-charging-rekey-supercharger-sessions-on-tesla-id, MAG-67):
// the cursor is now one per vehicle, not one per account.
//
// Assertions are against charging.MirrorWatermarkStore's returned time.Time
// values, and — for the created_at bookkeeping column the port does not
// expose — direct SQL reads. pgtype NEVER appears in this file
// (internal/charging/AGENTS.md §Testing Notes).
//
// Test → Test Contract case mapping (design.md §"Test contract"):
//
//	T-12  TestMirrorWatermark_T12_PerVehicleCursor
//	      TestMirrorWatermark_NoRowReturnsEpoch
//	      TestMirrorWatermark_StoredCursorReturnedExactly
//	      TestMirrorWatermark_AdvanceCreatesFirstRow
//	      TestMirrorWatermark_AdvanceOverwritesExistingRow
//	      TestMirrorWatermark_CursorsIsolatedPerVehicle
//	      TestMirrorWatermark_CreatedAtSetOnceNotOnEveryAdvance
package charging_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// --- mirror-watermark-specific test helpers ---

// cleanupMirrorWatermarks registers a cleanup that deletes mirror_watermarks
// rows for the given vehicles so a shared DB stays tidy across test runs.
func cleanupMirrorWatermarks(t *testing.T, pool *pgxpool.Pool, teslaIDs ...int64) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, "DELETE FROM charging.mirror_watermarks WHERE tesla_id = ANY($1)", teslaIDs)
	})
}

// fetchMirrorWatermarkBookkeeping reads created_at/updated_at for one vehicle's
// mirror_watermarks row directly — MirrorWatermarkStore has no method surfacing
// these bookkeeping columns. ok is false when no row exists (not an error).
func fetchMirrorWatermarkBookkeeping(t *testing.T, pool *pgxpool.Pool, teslaID int64) (createdAt, updatedAt time.Time, ok bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		"SELECT created_at, updated_at FROM charging.mirror_watermarks WHERE tesla_id = $1",
		teslaID,
	).Scan(&createdAt, &updatedAt)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	return createdAt, updatedAt, true
}

// T-12: the watermark is per vehicle, and two vehicles no longer share one.
// Given an empty table: MirrorWatermark(ctx, 111) returns the zero time.Time.
// AdvanceMirrorWatermark(ctx, 111, t1), then MirrorWatermark(ctx, 111) returns
// exactly t1. AdvanceMirrorWatermark(ctx, 111, t2), then the read returns t2.
// Throughout, MirrorWatermark(ctx, 222) returns the zero time.Time — a second
// vehicle of the same account has its own cursor.
func TestMirrorWatermark_T12_PerVehicleCursor(t *testing.T) {
	pool := testPool
	cleanupMirrorWatermarks(t, pool, 111, 222)
	ctx := context.Background()
	store := charging.NewMirrorWatermarkStore(pool)

	got, err := store.MirrorWatermark(ctx, 111)
	if err != nil {
		t.Fatalf("MirrorWatermark(111, before): %v", err)
	}
	if !got.IsZero() {
		t.Fatalf("MirrorWatermark(111, before): want zero time.Time, got %v", got)
	}

	t1 := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	if err := store.AdvanceMirrorWatermark(ctx, 111, t1); err != nil {
		t.Fatalf("AdvanceMirrorWatermark(111, t1): %v", err)
	}
	got, err = store.MirrorWatermark(ctx, 111)
	if err != nil {
		t.Fatalf("MirrorWatermark(111, after t1): %v", err)
	}
	if !got.Equal(t1) {
		t.Errorf("MirrorWatermark(111, after t1): want %v, got %v", t1, got)
	}

	t2 := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	if err := store.AdvanceMirrorWatermark(ctx, 111, t2); err != nil {
		t.Fatalf("AdvanceMirrorWatermark(111, t2): %v", err)
	}
	got, err = store.MirrorWatermark(ctx, 111)
	if err != nil {
		t.Fatalf("MirrorWatermark(111, after t2): %v", err)
	}
	if !got.Equal(t2) {
		t.Errorf("MirrorWatermark(111, after t2): want %v (the later advance), got %v", t2, got)
	}

	got222, err := store.MirrorWatermark(ctx, 222)
	if err != nil {
		t.Fatalf("MirrorWatermark(222): %v", err)
	}
	if !got222.IsZero() {
		t.Errorf("MirrorWatermark(222): want zero time.Time (its own cursor, untouched), got %v", got222)
	}
}

// TestMirrorWatermark_NoRowReturnsEpoch: a vehicle with no mirror_watermarks row
// reports the epoch, not an error.
func TestMirrorWatermark_NoRowReturnsEpoch(t *testing.T) {
	pool := testPool
	const teslaID = int64(980001)
	cleanupMirrorWatermarks(t, pool, teslaID)

	store := charging.NewMirrorWatermarkStore(pool)
	got, err := store.MirrorWatermark(context.Background(), teslaID)
	if err != nil {
		t.Fatalf("MirrorWatermark: unexpected error: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("MirrorWatermark: want zero time.Time (epoch), got %v", got)
	}
}

// TestMirrorWatermark_StoredCursorReturnedExactly: a stored cursor is returned
// exactly.
func TestMirrorWatermark_StoredCursorReturnedExactly(t *testing.T) {
	pool := testPool
	const teslaID = int64(980002)
	cleanupMirrorWatermarks(t, pool, teslaID)

	store := charging.NewMirrorWatermarkStore(pool)
	want := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	if err := store.AdvanceMirrorWatermark(context.Background(), teslaID, want); err != nil {
		t.Fatalf("AdvanceMirrorWatermark: %v", err)
	}

	got, err := store.MirrorWatermark(context.Background(), teslaID)
	if err != nil {
		t.Fatalf("MirrorWatermark: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("MirrorWatermark: want %v, got %v", want, got)
	}
}

// TestMirrorWatermark_AdvanceCreatesFirstRow: AdvanceMirrorWatermark creates a
// first row when none exists yet.
func TestMirrorWatermark_AdvanceCreatesFirstRow(t *testing.T) {
	pool := testPool
	const teslaID = int64(980003)
	cleanupMirrorWatermarks(t, pool, teslaID)

	store := charging.NewMirrorWatermarkStore(pool)

	// Confirm there is genuinely no row yet before advancing.
	before, err := store.MirrorWatermark(context.Background(), teslaID)
	if err != nil {
		t.Fatalf("MirrorWatermark (before): %v", err)
	}
	if !before.IsZero() {
		t.Fatalf("MirrorWatermark (before): want zero time.Time, got %v", before)
	}

	x := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if err := store.AdvanceMirrorWatermark(context.Background(), teslaID, x); err != nil {
		t.Fatalf("AdvanceMirrorWatermark: %v", err)
	}

	got, err := store.MirrorWatermark(context.Background(), teslaID)
	if err != nil {
		t.Fatalf("MirrorWatermark (after): %v", err)
	}
	if !got.Equal(x) {
		t.Errorf("MirrorWatermark (after): want %v, got %v", x, got)
	}
}

// TestMirrorWatermark_AdvanceOverwritesExistingRow: AdvanceMirrorWatermark
// overwrites an existing row — a later advance replaces an earlier one.
func TestMirrorWatermark_AdvanceOverwritesExistingRow(t *testing.T) {
	pool := testPool
	const teslaID = int64(980004)
	cleanupMirrorWatermarks(t, pool, teslaID)

	store := charging.NewMirrorWatermarkStore(pool)

	x := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	y := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC) // later than x

	if err := store.AdvanceMirrorWatermark(context.Background(), teslaID, x); err != nil {
		t.Fatalf("AdvanceMirrorWatermark(x): %v", err)
	}
	if err := store.AdvanceMirrorWatermark(context.Background(), teslaID, y); err != nil {
		t.Fatalf("AdvanceMirrorWatermark(y): %v", err)
	}

	got, err := store.MirrorWatermark(context.Background(), teslaID)
	if err != nil {
		t.Fatalf("MirrorWatermark: %v", err)
	}
	if !got.Equal(y) {
		t.Errorf("MirrorWatermark: want %v (y, the later advance), got %v", y, got)
	}
	if got.Equal(x) {
		t.Errorf("MirrorWatermark: got the earlier value %v, want it replaced by %v", x, y)
	}
}

// TestMirrorWatermark_CursorsIsolatedPerVehicle: cursors are isolated per
// vehicle.
func TestMirrorWatermark_CursorsIsolatedPerVehicle(t *testing.T) {
	pool := testPool
	const teslaA = int64(980005)
	const teslaB = int64(980006)
	cleanupMirrorWatermarks(t, pool, teslaA, teslaB)

	store := charging.NewMirrorWatermarkStore(pool)

	wantA := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	wantB := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)

	if err := store.AdvanceMirrorWatermark(context.Background(), teslaA, wantA); err != nil {
		t.Fatalf("AdvanceMirrorWatermark(A): %v", err)
	}
	if err := store.AdvanceMirrorWatermark(context.Background(), teslaB, wantB); err != nil {
		t.Fatalf("AdvanceMirrorWatermark(B): %v", err)
	}

	gotA, err := store.MirrorWatermark(context.Background(), teslaA)
	if err != nil {
		t.Fatalf("MirrorWatermark(A): %v", err)
	}
	if !gotA.Equal(wantA) {
		t.Errorf("MirrorWatermark(A): want %v, got %v", wantA, gotA)
	}

	gotB, err := store.MirrorWatermark(context.Background(), teslaB)
	if err != nil {
		t.Fatalf("MirrorWatermark(B): %v", err)
	}
	if !gotB.Equal(wantB) {
		t.Errorf("MirrorWatermark(B): want %v, got %v", wantB, gotB)
	}
}

// TestMirrorWatermark_CreatedAtSetOnceNotOnEveryAdvance: created_at is set once,
// on the first advance, and stays unchanged across a later advance; updated_at
// (bookkeeping) changes.
func TestMirrorWatermark_CreatedAtSetOnceNotOnEveryAdvance(t *testing.T) {
	pool := testPool
	const teslaID = int64(980007)
	cleanupMirrorWatermarks(t, pool, teslaID)

	store := charging.NewMirrorWatermarkStore(pool)

	t1 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if err := store.AdvanceMirrorWatermark(context.Background(), teslaID, t1); err != nil {
		t.Fatalf("AdvanceMirrorWatermark(t1): %v", err)
	}

	createdAtFirst, updatedAtFirst, ok := fetchMirrorWatermarkBookkeeping(t, pool, teslaID)
	if !ok {
		t.Fatalf("fetchMirrorWatermarkBookkeeping: no row found after first advance")
	}

	// A short sleep so a real clock advance is observable in updated_at,
	// mirroring db_session_integration_test.go's mirrorGap precedent for the
	// identical purpose.
	time.Sleep(15 * time.Millisecond)

	t2 := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	if err := store.AdvanceMirrorWatermark(context.Background(), teslaID, t2); err != nil {
		t.Fatalf("AdvanceMirrorWatermark(t2): %v", err)
	}

	createdAtSecond, updatedAtSecond, ok := fetchMirrorWatermarkBookkeeping(t, pool, teslaID)
	if !ok {
		t.Fatalf("fetchMirrorWatermarkBookkeeping: no row found after second advance")
	}

	if !createdAtSecond.Equal(createdAtFirst) {
		t.Errorf("created_at: want unchanged %v across the second advance, got %v", createdAtFirst, createdAtSecond)
	}
	if !updatedAtSecond.After(updatedAtFirst) {
		t.Errorf("updated_at: want it to advance past %v on the second call, got %v", updatedAtFirst, updatedAtSecond)
	}
}
