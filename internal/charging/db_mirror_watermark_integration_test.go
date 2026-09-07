// Package charging_test — database-backed integration tests for
// MirrorWatermarkStore (mirror_watermark.go) and its GetMirrorWatermark /
// UpsertMirrorWatermark queries (db/query.sql), covering
// RM44-platform-add-mirror-watermark design.md's Test Contract T-cw-1
// through T-cw-6 (tasks.md task 1b.6).
//
// Assertions are against charging.MirrorWatermarkStore's returned time.Time
// values, and — for T-cw-6, which needs the created_at bookkeeping column
// the port does not expose — direct SQL reads. pgtype NEVER appears in this
// file (internal/charging/AGENTS.md §Testing Notes).
//
// Test → Test Contract case mapping:
//
//	T-cw-1  TestMirrorWatermark_NoRowReturnsEpoch
//	T-cw-2  TestMirrorWatermark_StoredCursorReturnedExactly
//	T-cw-3  TestMirrorWatermark_AdvanceCreatesFirstRow
//	T-cw-4  TestMirrorWatermark_AdvanceOverwritesExistingRow
//	T-cw-5  TestMirrorWatermark_CursorsIsolatedPerAccount
//	T-cw-6  TestMirrorWatermark_CreatedAtSetOnceNotOnEveryAdvance
package charging_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// --- mirror-watermark-specific test helpers ---

// cleanupMirrorWatermarks registers a cleanup that deletes mirror_watermarks
// rows for the given accounts so a shared DB stays tidy across test runs.
func cleanupMirrorWatermarks(t *testing.T, pool *pgxpool.Pool, accountIDs ...uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, id := range accountIDs {
			_, _ = pool.Exec(ctx, "DELETE FROM charging.mirror_watermarks WHERE account_id = $1", id)
		}
	})
}

// fetchMirrorWatermarkBookkeeping reads created_at/updated_at for one
// account's mirror_watermarks row directly — MirrorWatermarkStore has no
// method surfacing these bookkeeping columns. ok is false when no row
// exists (not an error).
func fetchMirrorWatermarkBookkeeping(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID) (createdAt, updatedAt time.Time, ok bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		"SELECT created_at, updated_at FROM charging.mirror_watermarks WHERE account_id = $1",
		accountID,
	).Scan(&createdAt, &updatedAt)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	return createdAt, updatedAt, true
}

// T-cw-1: an account with no mirror_watermarks row reports the epoch, not an
// error.
func TestMirrorWatermark_NoRowReturnsEpoch(t *testing.T) {
	pool := testPool
	accountID := uuid.New()
	cleanupMirrorWatermarks(t, pool, accountID)

	store := charging.NewMirrorWatermarkStore(pool)
	got, err := store.MirrorWatermark(context.Background(), accountID)
	if err != nil {
		t.Fatalf("MirrorWatermark: unexpected error: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("MirrorWatermark: want zero time.Time (epoch), got %v", got)
	}
}

// T-cw-2: a stored cursor is returned exactly.
func TestMirrorWatermark_StoredCursorReturnedExactly(t *testing.T) {
	pool := testPool
	accountID := uuid.New()
	cleanupMirrorWatermarks(t, pool, accountID)

	store := charging.NewMirrorWatermarkStore(pool)
	want := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	if err := store.AdvanceMirrorWatermark(context.Background(), accountID, want); err != nil {
		t.Fatalf("AdvanceMirrorWatermark: %v", err)
	}

	got, err := store.MirrorWatermark(context.Background(), accountID)
	if err != nil {
		t.Fatalf("MirrorWatermark: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("MirrorWatermark: want %v, got %v", want, got)
	}
}

// T-cw-3: AdvanceMirrorWatermark creates a first row when none exists yet.
func TestMirrorWatermark_AdvanceCreatesFirstRow(t *testing.T) {
	pool := testPool
	accountID := uuid.New()
	cleanupMirrorWatermarks(t, pool, accountID)

	store := charging.NewMirrorWatermarkStore(pool)

	// Confirm there is genuinely no row yet before advancing.
	before, err := store.MirrorWatermark(context.Background(), accountID)
	if err != nil {
		t.Fatalf("MirrorWatermark (before): %v", err)
	}
	if !before.IsZero() {
		t.Fatalf("MirrorWatermark (before): want zero time.Time, got %v", before)
	}

	x := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if err := store.AdvanceMirrorWatermark(context.Background(), accountID, x); err != nil {
		t.Fatalf("AdvanceMirrorWatermark: %v", err)
	}

	got, err := store.MirrorWatermark(context.Background(), accountID)
	if err != nil {
		t.Fatalf("MirrorWatermark (after): %v", err)
	}
	if !got.Equal(x) {
		t.Errorf("MirrorWatermark (after): want %v, got %v", x, got)
	}
}

// T-cw-4: AdvanceMirrorWatermark overwrites an existing row — a later
// advance replaces an earlier one.
func TestMirrorWatermark_AdvanceOverwritesExistingRow(t *testing.T) {
	pool := testPool
	accountID := uuid.New()
	cleanupMirrorWatermarks(t, pool, accountID)

	store := charging.NewMirrorWatermarkStore(pool)

	x := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	y := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC) // later than x

	if err := store.AdvanceMirrorWatermark(context.Background(), accountID, x); err != nil {
		t.Fatalf("AdvanceMirrorWatermark(x): %v", err)
	}
	if err := store.AdvanceMirrorWatermark(context.Background(), accountID, y); err != nil {
		t.Fatalf("AdvanceMirrorWatermark(y): %v", err)
	}

	got, err := store.MirrorWatermark(context.Background(), accountID)
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

// T-cw-5: cursors are isolated per account.
func TestMirrorWatermark_CursorsIsolatedPerAccount(t *testing.T) {
	pool := testPool
	accountA := uuid.New()
	accountB := uuid.New()
	cleanupMirrorWatermarks(t, pool, accountA, accountB)

	store := charging.NewMirrorWatermarkStore(pool)

	wantA := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	wantB := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)

	if err := store.AdvanceMirrorWatermark(context.Background(), accountA, wantA); err != nil {
		t.Fatalf("AdvanceMirrorWatermark(A): %v", err)
	}
	if err := store.AdvanceMirrorWatermark(context.Background(), accountB, wantB); err != nil {
		t.Fatalf("AdvanceMirrorWatermark(B): %v", err)
	}

	gotA, err := store.MirrorWatermark(context.Background(), accountA)
	if err != nil {
		t.Fatalf("MirrorWatermark(A): %v", err)
	}
	if !gotA.Equal(wantA) {
		t.Errorf("MirrorWatermark(A): want %v, got %v", wantA, gotA)
	}

	gotB, err := store.MirrorWatermark(context.Background(), accountB)
	if err != nil {
		t.Fatalf("MirrorWatermark(B): %v", err)
	}
	if !gotB.Equal(wantB) {
		t.Errorf("MirrorWatermark(B): want %v, got %v", wantB, gotB)
	}
}

// T-cw-6: created_at is set once, on the first advance, and stays unchanged
// across a later advance; updated_at (bookkeeping) changes.
func TestMirrorWatermark_CreatedAtSetOnceNotOnEveryAdvance(t *testing.T) {
	pool := testPool
	accountID := uuid.New()
	cleanupMirrorWatermarks(t, pool, accountID)

	store := charging.NewMirrorWatermarkStore(pool)

	t1 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if err := store.AdvanceMirrorWatermark(context.Background(), accountID, t1); err != nil {
		t.Fatalf("AdvanceMirrorWatermark(t1): %v", err)
	}

	createdAtFirst, updatedAtFirst, ok := fetchMirrorWatermarkBookkeeping(t, pool, accountID)
	if !ok {
		t.Fatalf("fetchMirrorWatermarkBookkeeping: no row found after first advance")
	}

	// A short sleep so a real clock advance is observable in updated_at,
	// mirroring db_session_integration_test.go's mirrorGap precedent for the
	// identical purpose.
	time.Sleep(15 * time.Millisecond)

	t2 := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	if err := store.AdvanceMirrorWatermark(context.Background(), accountID, t2); err != nil {
		t.Fatalf("AdvanceMirrorWatermark(t2): %v", err)
	}

	createdAtSecond, updatedAtSecond, ok := fetchMirrorWatermarkBookkeeping(t, pool, accountID)
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
