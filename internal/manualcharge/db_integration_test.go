// Package manualcharge_test contains database-backed integration tests for the
// manualcharge module. Postgres is auto-provisioned by testdb_test.go's TestMain:
//   - When DATABASE_URL is set, that managed Postgres is used (unchanged behavior).
//   - Otherwise a disposable `postgres:16-alpine` container is started for the run.
//
// goose migrations are embedded and applied before any test runs, so the schema is
// always at the latest version — no `make migrate-up` step required.
//
// Coverage:
//   - Writer.Create: required-only, all-optional, CHECK constraint violations.
//   - Writer.Update: mutation of mutable fields, created_at unchanged, cross-account guard.
//   - Writer.Delete: own entry, cross-account guard.
//   - Reader.ListEntriesByVehicle: vehicle isolation, newest-first ordering, limit, empty slice.
//   - Reader.ListEntriesByAccount: account isolation, ordering, limit, empty slice.
//   - Multi-tenant spot-check: no read ever returns another account's rows.
//
// Assertions: ONLY against manualcharge.Entry domain fields — never pgtype.
// No Tesla API call fires anywhere in this file (no import of internal/tesla).
package manualcharge_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/manualcharge"
)

// --- test helpers ---

// newTestPool returns the package-wide pool created by TestMain. The pool is
// shared across the whole package (one container, fully migrated) so per-test
// overhead is just the cleanupAccount row deletion registered below.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testPool == nil {
		t.Fatalf("manualcharge test pool not initialized; TestMain failure?")
	}
	return testPool
}

// cleanupAccount removes all manual_charge_entries rows created by this test so
// a shared DB stays tidy. Uses t.Cleanup so cleanup runs even when the test fails.
func cleanupAccount(t *testing.T, pool *pgxpool.Pool, accountIDs ...uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, id := range accountIDs {
			_, _ = pool.Exec(ctx, "DELETE FROM manual_charge_entries WHERE account_id = $1", id)
		}
	})
}

// minEntry returns a minimal valid Entry for accountID + teslaID with all optional
// fields as nil. LocationKind is set to "HOME" because it became a required field
// in RM4-manualcharge-require-location-kind (Writer.Create/Update reject nil or empty).
// The caller may override any field before passing to Writer.Create.
func minEntry(accountID uuid.UUID, teslaID int64) manualcharge.Entry {
	lk := "HOME"
	return manualcharge.Entry{
		AccountID:      accountID,
		TeslaID:        teslaID,
		VIN:            "5YJ3E1EA0NF000001",
		ChargedOn:      time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC),
		EnergyAddedKWh: 20.5,
		Price:          45000.00,
		Currency:       "COP",
		LocationKind:   &lk,
	}
}

func ptrString(s string) *string { return &s }
func ptrInt(i int) *int          { return &i }
func ptrTime(t time.Time) *time.Time {
	tc := t
	return &tc
}

// --- T6.2 Writer.Create ---

// TestCreate_RequiredOnly creates an entry with only the required fields and reads it
// back, asserting all required fields round-trip faithfully including currency default.
func TestCreate_RequiredOnly(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)
	r := manualcharge.NewReader(pool)

	e := minEntry(accountID, 1234567890)
	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	// ID and timestamps are server-assigned.
	if created.ID == (uuid.UUID{}) {
		t.Errorf("Create: expected non-zero ID")
	}
	if created.CreatedAt.IsZero() {
		t.Errorf("Create: expected non-zero CreatedAt")
	}
	if created.UpdatedAt.IsZero() {
		t.Errorf("Create: expected non-zero UpdatedAt")
	}

	// Required fields round-trip.
	if got, want := created.AccountID, accountID; got != want {
		t.Errorf("AccountID: got %v, want %v", got, want)
	}
	if got, want := created.TeslaID, e.TeslaID; got != want {
		t.Errorf("TeslaID: got %v, want %v", got, want)
	}
	if got, want := created.VIN, e.VIN; got != want {
		t.Errorf("VIN: got %q, want %q", got, want)
	}
	if got, want := created.EnergyAddedKWh, e.EnergyAddedKWh; got != want {
		t.Errorf("EnergyAddedKWh: got %v, want %v", got, want)
	}
	if got, want := created.Price, e.Price; got != want {
		t.Errorf("Price: got %v, want %v", got, want)
	}
	if got, want := created.Currency, "COP"; got != want {
		t.Errorf("Currency: got %q, want %q (DB default)", got, want)
	}

	// LocationKind is required and set in minEntry (HOME); it must round-trip.
	if created.LocationKind == nil || *created.LocationKind != "HOME" {
		t.Errorf("LocationKind: got %v, want \"HOME\"", created.LocationKind)
	}

	// Other optional fields are nil.
	if created.StartedAt != nil {
		t.Errorf("StartedAt: expected nil, got %v", *created.StartedAt)
	}
	if created.Notes != nil {
		t.Errorf("Notes: expected nil, got %q", *created.Notes)
	}

	// Read back via Reader confirms persistence.
	entries, err := r.ListEntriesByAccount(ctx, accountID, 10)
	if err != nil {
		t.Fatalf("ListEntriesByAccount: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].ID != created.ID {
		t.Errorf("round-trip ID mismatch: got %v, want %v", entries[0].ID, created.ID)
	}
}

// TestCreate_AllOptionals creates an entry with every optional field populated and
// asserts all fields round-trip without any going silently NULL.
func TestCreate_AllOptionals(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)
	r := manualcharge.NewReader(pool)

	start := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 18, 12, 30, 0, 0, time.UTC)

	e := minEntry(accountID, 9876543210)
	e.StartedAt = ptrTime(start)
	e.EndedAt = ptrTime(end)
	e.StartBatteryPct = ptrInt(20)
	e.EndBatteryPct = ptrInt(80)
	e.ChargingType = ptrString("AC")
	e.LocationKind = ptrString("HOME")
	e.LocationLabel = ptrString("Casa principal")
	e.Notes = ptrString("Primera carga en casa")

	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("Create with all optionals: %v", err)
	}

	// Read back.
	entries, err := r.ListEntriesByAccount(ctx, accountID, 10)
	if err != nil {
		t.Fatalf("ListEntriesByAccount: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	got := entries[0]

	if got.ID != created.ID {
		t.Errorf("ID mismatch")
	}
	if got.StartedAt == nil || !got.StartedAt.Equal(start) {
		t.Errorf("StartedAt: got %v, want %v", got.StartedAt, start)
	}
	if got.EndedAt == nil || !got.EndedAt.Equal(end) {
		t.Errorf("EndedAt: got %v, want %v", got.EndedAt, end)
	}
	if got.StartBatteryPct == nil || *got.StartBatteryPct != 20 {
		t.Errorf("StartBatteryPct: got %v, want 20", got.StartBatteryPct)
	}
	if got.EndBatteryPct == nil || *got.EndBatteryPct != 80 {
		t.Errorf("EndBatteryPct: got %v, want 80", got.EndBatteryPct)
	}
	if got.ChargingType == nil || *got.ChargingType != "AC" {
		t.Errorf("ChargingType: got %v, want AC", got.ChargingType)
	}
	if got.LocationKind == nil || *got.LocationKind != "HOME" {
		t.Errorf("LocationKind: got %v, want HOME", got.LocationKind)
	}
	if got.LocationLabel == nil || *got.LocationLabel != "Casa principal" {
		t.Errorf("LocationLabel: got %v, want 'Casa principal'", got.LocationLabel)
	}
	if got.Notes == nil || *got.Notes != "Primera carga en casa" {
		t.Errorf("Notes: got %v, want 'Primera carga en casa'", got.Notes)
	}
}

// TestCreate_CheckConstraint_EnergyZero asserts that creating an entry with
// energy_added_kwh = 0 returns an error (DB CHECK energy_added_kwh > 0).
func TestCreate_CheckConstraint_EnergyZero(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)

	e := minEntry(accountID, 111)
	e.EnergyAddedKWh = 0 // violates CHECK (energy_added_kwh > 0)

	_, err := w.Create(ctx, e)
	if err == nil {
		t.Fatal("Create with energy=0: expected error (CHECK violation), got nil")
	}
}

// TestCreate_CheckConstraint_EnergyNegative asserts that negative energy is rejected.
func TestCreate_CheckConstraint_EnergyNegative(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)

	e := minEntry(accountID, 112)
	e.EnergyAddedKWh = -5.0 // violates CHECK (energy_added_kwh > 0)

	_, err := w.Create(ctx, e)
	if err == nil {
		t.Fatal("Create with energy=-5: expected error (CHECK violation), got nil")
	}
}

// TestCreate_CheckConstraint_PriceNegative asserts that negative price is rejected.
func TestCreate_CheckConstraint_PriceNegative(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)

	e := minEntry(accountID, 113)
	e.Price = -100.0 // violates CHECK (price >= 0)

	_, err := w.Create(ctx, e)
	if err == nil {
		t.Fatal("Create with price=-100: expected error (CHECK violation), got nil")
	}
}

// TestCreate_CheckConstraint_BatteryOutOfRange asserts that start_battery_pct = 101 is rejected.
func TestCreate_CheckConstraint_BatteryOutOfRange(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)

	e := minEntry(accountID, 114)
	e.StartBatteryPct = ptrInt(101) // violates CHECK (BETWEEN 0 AND 100)

	_, err := w.Create(ctx, e)
	if err == nil {
		t.Fatal("Create with start_battery_pct=101: expected error (CHECK violation), got nil")
	}
}

// TestCreate_CheckConstraint_EndedBeforeStarted asserts that ended_at < started_at is rejected.
func TestCreate_CheckConstraint_EndedBeforeStarted(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)

	e := minEntry(accountID, 115)
	start := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC) // end before start
	e.StartedAt = ptrTime(start)
	e.EndedAt = ptrTime(end)

	_, err := w.Create(ctx, e)
	if err == nil {
		t.Fatal("Create with ended_at < started_at: expected error (CHECK violation), got nil")
	}
}

// --- T6.3 Writer.Update ---

// TestUpdate_MutatesFieldsAndAdvancesUpdatedAt creates an entry, updates price and notes,
// then reads back to assert the mutation is visible, created_at is unchanged, and
// updated_at has advanced or stayed equal (server clock resolution may be microseconds).
func TestUpdate_MutatesFieldsAndAdvancesUpdatedAt(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)
	r := manualcharge.NewReader(pool)

	e := minEntry(accountID, 222)
	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Pause briefly so the server clock advances between create and update.
	time.Sleep(5 * time.Millisecond)

	// Mutate price and add a note.
	updated := created
	updated.Price = 99999.99
	updated.Notes = ptrString("corrected price")

	result, err := w.Update(ctx, updated)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if got, want := result.Price, 99999.99; got != want {
		t.Errorf("Price after update: got %v, want %v", got, want)
	}
	if result.Notes == nil || *result.Notes != "corrected price" {
		t.Errorf("Notes after update: got %v, want 'corrected price'", result.Notes)
	}
	// created_at must be unchanged.
	if !result.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("CreatedAt changed: was %v, now %v", created.CreatedAt, result.CreatedAt)
	}
	// updated_at must be >= created_at (may be equal on fast hardware, never before).
	if result.UpdatedAt.Before(created.UpdatedAt) {
		t.Errorf("UpdatedAt regressed: was %v, now %v", created.UpdatedAt, result.UpdatedAt)
	}

	// Read back via Reader to confirm persistence.
	entries, err := r.ListEntriesByAccount(ctx, accountID, 10)
	if err != nil {
		t.Fatalf("ListEntriesByAccount: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if got, want := entries[0].Price, 99999.99; got != want {
		t.Errorf("Price after read-back: got %v, want %v", got, want)
	}
}

// TestUpdate_CrossAccountIsNoOp asserts that updating an entry with a different
// account_id is a no-op — the WHERE id=X AND account_id=Y finds zero rows and
// returns a not-found / zero-rows error.
func TestUpdate_CrossAccountIsNoOp(t *testing.T) {
	pool := newTestPool(t)
	ownerID := uuid.New()
	attackerID := uuid.New()
	cleanupAccount(t, pool, ownerID, attackerID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)

	// Create entry under ownerID.
	e := minEntry(ownerID, 333)
	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Attempt to update it using attackerID as the account scope.
	tampered := created
	tampered.AccountID = attackerID // wrong account
	tampered.Price = 1.0

	_, err = w.Update(ctx, tampered)
	// Expected: error (pgx returns pgx.ErrNoRows when RETURNING * finds 0 rows).
	if err == nil {
		t.Fatal("Update with wrong account_id: expected error (no rows), got nil")
	}

	// Owner's row must be unchanged.
	r := manualcharge.NewReader(pool)
	entries, err := r.ListEntriesByAccount(ctx, ownerID, 10)
	if err != nil {
		t.Fatalf("ListEntriesByAccount: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry for owner, got %d", len(entries))
	}
	if entries[0].Price != created.Price {
		t.Errorf("owner's Price was mutated: got %v, want %v", entries[0].Price, created.Price)
	}
}

// --- T6.4 Writer.Delete ---

// TestDelete_OwnEntry creates an entry, deletes it, and confirms it no longer appears.
func TestDelete_OwnEntry(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)
	r := manualcharge.NewReader(pool)

	e := minEntry(accountID, 444)
	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := w.Delete(ctx, accountID, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	entries, err := r.ListEntriesByAccount(ctx, accountID, 10)
	if err != nil {
		t.Fatalf("ListEntriesByAccount after delete: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after delete, got %d", len(entries))
	}
}

// TestDelete_CrossAccountGuard attempts to delete an entry with the wrong account_id
// and asserts the row survives.
func TestDelete_CrossAccountGuard(t *testing.T) {
	pool := newTestPool(t)
	ownerID := uuid.New()
	attackerID := uuid.New()
	cleanupAccount(t, pool, ownerID, attackerID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)
	r := manualcharge.NewReader(pool)

	// Create entry under ownerID.
	e := minEntry(ownerID, 555)
	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Delete with wrong account — :exec does not return an error for zero rows deleted;
	// the row simply survives.
	if err := w.Delete(ctx, attackerID, created.ID); err != nil {
		t.Fatalf("Delete with wrong account returned unexpected error: %v", err)
	}

	// Owner's row must still exist.
	entries, err := r.ListEntriesByAccount(ctx, ownerID, 10)
	if err != nil {
		t.Fatalf("ListEntriesByAccount: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry (row survived), got %d", len(entries))
	}
}

// --- T6.5 Reader.ListEntriesByVehicle ---

// TestListByVehicle_VehicleIsolation creates entries for two vehicles in the same
// account and asserts ListEntriesByVehicle(V1) returns only V1 entries.
func TestListByVehicle_VehicleIsolation(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)
	r := manualcharge.NewReader(pool)

	const v1 = int64(601)
	const v2 = int64(602)

	e1 := minEntry(accountID, v1)
	if _, err := w.Create(ctx, e1); err != nil {
		t.Fatalf("Create v1: %v", err)
	}
	e2 := minEntry(accountID, v2)
	if _, err := w.Create(ctx, e2); err != nil {
		t.Fatalf("Create v2: %v", err)
	}

	got, err := r.ListEntriesByVehicle(ctx, accountID, v1, 10)
	if err != nil {
		t.Fatalf("ListEntriesByVehicle(v1): %v", err)
	}
	if len(got) != 1 {
		t.Errorf("ListEntriesByVehicle(v1): expected 1 entry, got %d", len(got))
	}
	for _, g := range got {
		if g.TeslaID != v1 {
			t.Errorf("ListEntriesByVehicle(v1): got entry for teslaID %d, want %d", g.TeslaID, v1)
		}
	}
}

// TestListByVehicle_NewestFirst creates 3 entries for the same vehicle with different
// charged_on dates and asserts they are returned newest-first (charged_on DESC).
func TestListByVehicle_NewestFirst(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)
	r := manualcharge.NewReader(pool)

	const teslaID = int64(700)

	dates := []time.Time{
		time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), // oldest
		time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC), // middle
		time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC), // newest
	}
	for _, d := range dates {
		e := minEntry(accountID, teslaID)
		e.ChargedOn = d
		if _, err := w.Create(ctx, e); err != nil {
			t.Fatalf("Create entry for %v: %v", d, err)
		}
	}

	got, err := r.ListEntriesByVehicle(ctx, accountID, teslaID, 10)
	if err != nil {
		t.Fatalf("ListEntriesByVehicle: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	// Newest first: got[0].ChargedOn >= got[1].ChargedOn >= got[2].ChargedOn
	for i := 1; i < len(got); i++ {
		if got[i-1].ChargedOn.Before(got[i].ChargedOn) {
			t.Errorf("ordering: entry[%d].ChargedOn=%v is before entry[%d].ChargedOn=%v — expected DESC",
				i-1, got[i-1].ChargedOn, i, got[i].ChargedOn)
		}
	}
}

// TestListByVehicle_Limit asserts that a limit of 1 returns exactly 1 entry.
func TestListByVehicle_Limit(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)
	r := manualcharge.NewReader(pool)

	const teslaID = int64(800)
	for i := range 3 {
		e := minEntry(accountID, teslaID)
		e.ChargedOn = time.Date(2026, 7, i+15, 0, 0, 0, 0, time.UTC)
		if _, err := w.Create(ctx, e); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	got, err := r.ListEntriesByVehicle(ctx, accountID, teslaID, 1)
	if err != nil {
		t.Fatalf("ListEntriesByVehicle(limit=1): %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 entry with limit=1, got %d", len(got))
	}
}

// TestListByVehicle_EmptyNonNil asserts that a vehicle with no entries returns an empty
// non-nil slice (not nil), so callers can range over it safely.
func TestListByVehicle_EmptyNonNil(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	r := manualcharge.NewReader(pool)

	got, err := r.ListEntriesByVehicle(ctx, accountID, 999999, 10)
	if err != nil {
		t.Fatalf("ListEntriesByVehicle for empty vehicle: %v", err)
	}
	if got == nil {
		t.Errorf("expected non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("expected 0 entries, got %d", len(got))
	}
}

// --- T6.6 Reader.ListEntriesByAccount ---

// TestListByAccount_AccountIsolation creates entries for two accounts and asserts
// ListEntriesByAccount(A) returns only account A's entries.
func TestListByAccount_AccountIsolation(t *testing.T) {
	pool := newTestPool(t)
	accountA := uuid.New()
	accountB := uuid.New()
	cleanupAccount(t, pool, accountA, accountB)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)
	r := manualcharge.NewReader(pool)

	eA := minEntry(accountA, 901)
	if _, err := w.Create(ctx, eA); err != nil {
		t.Fatalf("Create A: %v", err)
	}
	eB := minEntry(accountB, 902)
	if _, err := w.Create(ctx, eB); err != nil {
		t.Fatalf("Create B: %v", err)
	}

	gotA, err := r.ListEntriesByAccount(ctx, accountA, 10)
	if err != nil {
		t.Fatalf("ListEntriesByAccount(A): %v", err)
	}
	if len(gotA) != 1 {
		t.Errorf("expected 1 entry for accountA, got %d", len(gotA))
	}
	for _, g := range gotA {
		if g.AccountID != accountA {
			t.Errorf("ListEntriesByAccount(A): got entry for accountID %v, want %v", g.AccountID, accountA)
		}
	}
}

// TestListByAccount_NewestFirst creates entries with different charged_on dates and
// asserts newest-first ordering.
func TestListByAccount_NewestFirst(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)
	r := manualcharge.NewReader(pool)

	dates := []time.Time{
		time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC),
	}
	for _, d := range dates {
		e := minEntry(accountID, 950)
		e.ChargedOn = d
		if _, err := w.Create(ctx, e); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	got, err := r.ListEntriesByAccount(ctx, accountID, 10)
	if err != nil {
		t.Fatalf("ListEntriesByAccount: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].ChargedOn.Before(got[i].ChargedOn) {
			t.Errorf("ordering: entry[%d].ChargedOn=%v before entry[%d].ChargedOn=%v — expected DESC",
				i-1, got[i-1].ChargedOn, i, got[i].ChargedOn)
		}
	}
}

// TestListByAccount_Limit asserts that a limit of 1 returns at most 1 entry.
func TestListByAccount_Limit(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)
	r := manualcharge.NewReader(pool)

	for i := range 3 {
		e := minEntry(accountID, 960)
		e.ChargedOn = time.Date(2026, 7, i+14, 0, 0, 0, 0, time.UTC)
		if _, err := w.Create(ctx, e); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	got, err := r.ListEntriesByAccount(ctx, accountID, 1)
	if err != nil {
		t.Fatalf("ListEntriesByAccount(limit=1): %v", err)
	}
	if len(got) > 1 {
		t.Errorf("expected at most 1 entry, got %d", len(got))
	}
}

// TestListByAccount_EmptyNonNil asserts that an account with no entries returns an empty
// non-nil slice (not nil).
func TestListByAccount_EmptyNonNil(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	r := manualcharge.NewReader(pool)

	got, err := r.ListEntriesByAccount(ctx, accountID, 10)
	if err != nil {
		t.Fatalf("ListEntriesByAccount for empty account: %v", err)
	}
	if got == nil {
		t.Errorf("expected non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("expected 0 entries, got %d", len(got))
	}
}

// --- T6.7 Multi-tenant isolation spot-check ---

// TestMultiTenantIsolation_NeverLeaks creates entries for two accounts with the same
// tesla_id and asserts that no read method ever returns another account's rows.
// This is the definitive guard that the account_id scope in every WHERE clause is real.
func TestMultiTenantIsolation_NeverLeaks(t *testing.T) {
	pool := newTestPool(t)
	alice := uuid.New()
	bob := uuid.New()
	cleanupAccount(t, pool, alice, bob)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)
	r := manualcharge.NewReader(pool)

	// Same tesla_id to make the multi-tenant boundary as stressful as possible.
	const sharedTeslaID = int64(100001)

	eA := minEntry(alice, sharedTeslaID)
	eA.Notes = ptrString("alice's entry")
	if _, err := w.Create(ctx, eA); err != nil {
		t.Fatalf("Create alice: %v", err)
	}

	eB := minEntry(bob, sharedTeslaID)
	eB.Notes = ptrString("bob's entry")
	if _, err := w.Create(ctx, eB); err != nil {
		t.Fatalf("Create bob: %v", err)
	}

	// Alice's account view: must see only alice's rows.
	aliceByVehicle, err := r.ListEntriesByVehicle(ctx, alice, sharedTeslaID, 100)
	if err != nil {
		t.Fatalf("alice ListByVehicle: %v", err)
	}
	for _, e := range aliceByVehicle {
		if e.AccountID != alice {
			t.Errorf("alice ListByVehicle: got entry for accountID %v (want alice %v)", e.AccountID, alice)
		}
	}

	aliceByAccount, err := r.ListEntriesByAccount(ctx, alice, 100)
	if err != nil {
		t.Fatalf("alice ListByAccount: %v", err)
	}
	for _, e := range aliceByAccount {
		if e.AccountID != alice {
			t.Errorf("alice ListByAccount: got entry for accountID %v (want alice %v)", e.AccountID, alice)
		}
	}

	// Bob's account view: must see only bob's rows.
	bobByVehicle, err := r.ListEntriesByVehicle(ctx, bob, sharedTeslaID, 100)
	if err != nil {
		t.Fatalf("bob ListByVehicle: %v", err)
	}
	for _, e := range bobByVehicle {
		if e.AccountID != bob {
			t.Errorf("bob ListByVehicle: got entry for accountID %v (want bob %v)", e.AccountID, bob)
		}
	}

	bobByAccount, err := r.ListEntriesByAccount(ctx, bob, 100)
	if err != nil {
		t.Fatalf("bob ListByAccount: %v", err)
	}
	for _, e := range bobByAccount {
		if e.AccountID != bob {
			t.Errorf("bob ListByAccount: got entry for accountID %v (want bob %v)", e.AccountID, bob)
		}
	}
}

// --- T3: location_kind required-field scenarios (RM4-manualcharge-require-location-kind) ---

// TestCreate_RejectsNilLocationKind asserts that Writer.Create returns a non-nil error
// when LocationKind is nil, and that no row is inserted.
func TestCreate_RejectsNilLocationKind(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)
	r := manualcharge.NewReader(pool)

	e := minEntry(accountID, 1001)
	e.LocationKind = nil // required field absent

	_, err := w.Create(ctx, e)
	if err == nil {
		t.Fatal("Create with nil LocationKind: expected non-nil error, got nil")
	}
	if !strings.Contains(err.Error(), "location_kind is required") {
		t.Errorf("expected error to mention 'location_kind is required', got: %v", err)
	}

	// Assert no row was inserted.
	entries, listErr := r.ListEntriesByAccount(ctx, accountID, 10)
	if listErr != nil {
		t.Fatalf("ListEntriesByAccount: %v", listErr)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 rows after rejection, got %d", len(entries))
	}
}

// TestCreate_RejectsEmptyLocationKind asserts that Writer.Create returns a non-nil error
// when LocationKind points to an empty string, and that no row is inserted.
func TestCreate_RejectsEmptyLocationKind(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)
	r := manualcharge.NewReader(pool)

	e := minEntry(accountID, 1002)
	e.LocationKind = ptrString("") // empty string — also invalid

	_, err := w.Create(ctx, e)
	if err == nil {
		t.Fatal("Create with empty LocationKind: expected non-nil error, got nil")
	}
	if !strings.Contains(err.Error(), "location_kind is required") {
		t.Errorf("expected error to mention 'location_kind is required', got: %v", err)
	}

	// Assert no row was inserted.
	entries, listErr := r.ListEntriesByAccount(ctx, accountID, 10)
	if listErr != nil {
		t.Fatalf("ListEntriesByAccount: %v", listErr)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 rows after rejection, got %d", len(entries))
	}
}

// TestCreate_AcceptsHOME asserts that Writer.Create succeeds when LocationKind is "HOME"
// and the returned entry carries the correct value.
func TestCreate_AcceptsHOME(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)

	e := minEntry(accountID, 1003)
	e.LocationKind = ptrString("HOME")

	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("Create with LocationKind=HOME: unexpected error: %v", err)
	}
	if created.LocationKind == nil || *created.LocationKind != "HOME" {
		t.Errorf("LocationKind: got %v, want \"HOME\"", created.LocationKind)
	}
}

// TestCreate_AcceptsWORK asserts that Writer.Create succeeds when LocationKind is "WORK".
func TestCreate_AcceptsWORK(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)

	e := minEntry(accountID, 1004)
	e.LocationKind = ptrString("WORK")

	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("Create with LocationKind=WORK: unexpected error: %v", err)
	}
	if created.LocationKind == nil || *created.LocationKind != "WORK" {
		t.Errorf("LocationKind: got %v, want \"WORK\"", created.LocationKind)
	}
}

// TestCreate_AcceptsOTHER asserts that Writer.Create succeeds when LocationKind is "OTHER".
func TestCreate_AcceptsOTHER(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)

	e := minEntry(accountID, 1005)
	e.LocationKind = ptrString("OTHER")

	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("Create with LocationKind=OTHER: unexpected error: %v", err)
	}
	if created.LocationKind == nil || *created.LocationKind != "OTHER" {
		t.Errorf("LocationKind: got %v, want \"OTHER\"", created.LocationKind)
	}
}

// TestUpdate_RejectsNilLocationKind creates a valid entry with LocationKind="HOME", then
// attempts an Update with LocationKind=nil. Asserts: error is non-nil, and the original
// row is unchanged (LocationKind still "HOME").
func TestUpdate_RejectsNilLocationKind(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)
	r := manualcharge.NewReader(pool)

	// Create a valid entry first.
	e := minEntry(accountID, 1006)
	e.LocationKind = ptrString("HOME")
	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Attempt Update with nil LocationKind.
	tampered := created
	tampered.LocationKind = nil

	_, err = w.Update(ctx, tampered)
	if err == nil {
		t.Fatal("Update with nil LocationKind: expected non-nil error, got nil")
	}
	if !strings.Contains(err.Error(), "location_kind is required") {
		t.Errorf("expected error to mention 'location_kind is required', got: %v", err)
	}

	// Assert the original row is unchanged.
	entries, listErr := r.ListEntriesByAccount(ctx, accountID, 10)
	if listErr != nil {
		t.Fatalf("ListEntriesByAccount: %v", listErr)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].LocationKind == nil || *entries[0].LocationKind != "HOME" {
		t.Errorf("original row's LocationKind was mutated: got %v, want \"HOME\"", entries[0].LocationKind)
	}
}

// --- T4: Reader.ListEntriesByVehicleBetween (design.md Test Contract, RM28-manualcharge-add-date-range-reader) ---

// TestListByVehicleBetween_InclusiveBounds — design.md Test Contract (a): entries at
// charged_on = from, a mid-window date, and charged_on = to must all be returned; both
// bounds are inclusive.
func TestListByVehicleBetween_InclusiveBounds(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)
	r := manualcharge.NewReader(pool)

	const teslaID = int64(2001)
	from := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	mid := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)

	for _, d := range []time.Time{from, mid, to} {
		e := minEntry(accountID, teslaID)
		e.ChargedOn = d
		if _, err := w.Create(ctx, e); err != nil {
			t.Fatalf("Create entry for %v: %v", d, err)
		}
	}

	got, err := r.ListEntriesByVehicleBetween(ctx, accountID, teslaID, from, to)
	if err != nil {
		t.Fatalf("ListEntriesByVehicleBetween: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}

	var sawFrom, sawTo bool
	for _, e := range got {
		if e.ChargedOn.Equal(from) {
			sawFrom = true
		}
		if e.ChargedOn.Equal(to) {
			sawTo = true
		}
	}
	if !sawFrom {
		t.Errorf("expected entry dated exactly from (%v) to be present", from)
	}
	if !sawTo {
		t.Errorf("expected entry dated exactly to (%v) to be present", to)
	}
}

// TestListByVehicleBetween_ExcludesOutsideBounds — design.md Test Contract (b): entries
// one day before `from` and one day after `to` must never appear in the result.
func TestListByVehicleBetween_ExcludesOutsideBounds(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)
	r := manualcharge.NewReader(pool)

	const teslaID = int64(2002)
	from := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	mid := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	beforeFrom := time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC)
	afterTo := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)

	for _, d := range []time.Time{from, mid, to, beforeFrom, afterTo} {
		e := minEntry(accountID, teslaID)
		e.ChargedOn = d
		if _, err := w.Create(ctx, e); err != nil {
			t.Fatalf("Create entry for %v: %v", d, err)
		}
	}

	got, err := r.ListEntriesByVehicleBetween(ctx, accountID, teslaID, from, to)
	if err != nil {
		t.Fatalf("ListEntriesByVehicleBetween: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 entries (window only), got %d", len(got))
	}
	for _, e := range got {
		if e.ChargedOn.Equal(beforeFrom) {
			t.Errorf("entry dated %v (one day before from) must not appear", beforeFrom)
		}
		if e.ChargedOn.Equal(afterTo) {
			t.Errorf("entry dated %v (one day after to) must not appear", afterTo)
		}
	}
}

// TestListByVehicleBetween_NewestFirst — design.md Test Contract (c): results are
// ordered charged_on DESC regardless of insertion order (same assertion style as
// TestListByVehicle_NewestFirst).
func TestListByVehicleBetween_NewestFirst(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)
	r := manualcharge.NewReader(pool)

	const teslaID = int64(2003)
	from := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	mid := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)

	// Insert out of date order: mid, to, from.
	for _, d := range []time.Time{mid, to, from} {
		e := minEntry(accountID, teslaID)
		e.ChargedOn = d
		if _, err := w.Create(ctx, e); err != nil {
			t.Fatalf("Create entry for %v: %v", d, err)
		}
	}

	got, err := r.ListEntriesByVehicleBetween(ctx, accountID, teslaID, from, to)
	if err != nil {
		t.Fatalf("ListEntriesByVehicleBetween: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	if !got[0].ChargedOn.Equal(to) {
		t.Errorf("result[0].ChargedOn: got %v, want %v", got[0].ChargedOn, to)
	}
	if !got[1].ChargedOn.Equal(mid) {
		t.Errorf("result[1].ChargedOn: got %v, want %v", got[1].ChargedOn, mid)
	}
	if !got[2].ChargedOn.Equal(from) {
		t.Errorf("result[2].ChargedOn: got %v, want %v", got[2].ChargedOn, from)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].ChargedOn.Before(got[i].ChargedOn) {
			t.Errorf("ordering: entry[%d].ChargedOn=%v is before entry[%d].ChargedOn=%v — expected DESC",
				i-1, got[i-1].ChargedOn, i, got[i].ChargedOn)
		}
	}
}

// TestListByVehicleBetween_EmptyNonNil — design.md Test Contract (d): a window with no
// matching rows returns a non-nil, zero-length slice and a nil error.
func TestListByVehicleBetween_EmptyNonNil(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	r := manualcharge.NewReader(pool)

	const teslaID = int64(2004)
	from := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)

	got, err := r.ListEntriesByVehicleBetween(ctx, accountID, teslaID, from, to)
	if err != nil {
		t.Fatalf("ListEntriesByVehicleBetween for empty window: %v", err)
	}
	if got == nil {
		t.Errorf("expected non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("expected 0 entries, got %d", len(got))
	}
}

// TestListByVehicleBetween_AccountIsolation — design.md Test Contract (e): two accounts
// share the same teslaID and the same charged_on inside the window; the scoped call must
// return only the requesting account's entry.
func TestListByVehicleBetween_AccountIsolation(t *testing.T) {
	pool := newTestPool(t)
	accountA := uuid.New()
	accountB := uuid.New()
	cleanupAccount(t, pool, accountA, accountB)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)
	r := manualcharge.NewReader(pool)

	const sharedTeslaID = int64(2005)
	chargedOn := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)

	eA := minEntry(accountA, sharedTeslaID)
	eA.ChargedOn = chargedOn
	if _, err := w.Create(ctx, eA); err != nil {
		t.Fatalf("Create A: %v", err)
	}
	eB := minEntry(accountB, sharedTeslaID)
	eB.ChargedOn = chargedOn
	if _, err := w.Create(ctx, eB); err != nil {
		t.Fatalf("Create B: %v", err)
	}

	got, err := r.ListEntriesByVehicleBetween(ctx, accountA, sharedTeslaID, from, to)
	if err != nil {
		t.Fatalf("ListEntriesByVehicleBetween: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 entry, got %d", len(got))
	}
	if got[0].AccountID != accountA {
		t.Errorf("AccountID: got %v, want accountA %v", got[0].AccountID, accountA)
	}
}

// TestListByVehicleBetween_VehicleIsolation — design.md Test Contract (f): one account
// with two vehicles, each with an entry at the same charged_on inside the window; the
// scoped call must return only the requested vehicle's entry.
func TestListByVehicleBetween_VehicleIsolation(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)
	r := manualcharge.NewReader(pool)

	const v1 = int64(2006)
	const v2 = int64(2007)
	chargedOn := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)

	e1 := minEntry(accountID, v1)
	e1.ChargedOn = chargedOn
	if _, err := w.Create(ctx, e1); err != nil {
		t.Fatalf("Create v1: %v", err)
	}
	e2 := minEntry(accountID, v2)
	e2.ChargedOn = chargedOn
	if _, err := w.Create(ctx, e2); err != nil {
		t.Fatalf("Create v2: %v", err)
	}

	got, err := r.ListEntriesByVehicleBetween(ctx, accountID, v1, from, to)
	if err != nil {
		t.Fatalf("ListEntriesByVehicleBetween: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 entry, got %d", len(got))
	}
	if got[0].TeslaID != v1 {
		t.Errorf("TeslaID: got %v, want %v", got[0].TeslaID, v1)
	}
}

// TestUpdate_AcceptsLocationKindChange creates an entry with LocationKind="HOME", then
// updates it to LocationKind="WORK". Asserts: no error, returned entry has "WORK",
// and updated_at is greater than or equal to created_at (i.e. it has advanced or the
// same-microsecond floor applies — it must NOT have regressed).
func TestUpdate_AcceptsLocationKindChange(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)

	ctx := context.Background()
	w := manualcharge.NewWriter(pool)

	// Create with HOME.
	e := minEntry(accountID, 1007)
	e.LocationKind = ptrString("HOME")
	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Pause so the server clock can advance between create and update.
	time.Sleep(5 * time.Millisecond)

	// Update to WORK.
	updated := created
	updated.LocationKind = ptrString("WORK")

	result, err := w.Update(ctx, updated)
	if err != nil {
		t.Fatalf("Update LocationKind HOME→WORK: unexpected error: %v", err)
	}
	if result.LocationKind == nil || *result.LocationKind != "WORK" {
		t.Errorf("LocationKind after update: got %v, want \"WORK\"", result.LocationKind)
	}
	// updated_at must not have regressed (must be >= created_at).
	if result.UpdatedAt.Before(created.UpdatedAt) {
		t.Errorf("UpdatedAt regressed: was %v, now %v", created.UpdatedAt, result.UpdatedAt)
	}
}
