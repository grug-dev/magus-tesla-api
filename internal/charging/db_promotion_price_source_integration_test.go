// Package charging_test — database-backed integration tests for
// RM51-charging-derive-status-and-price-source tier 1 (MAG-58): the
// promoteIfComplete auto-promotion rule and the price_source column/write
// path. Covers design.md's Test Contract Group B (B1-B3, direct SQL against
// the schema itself) and Group C (C1-C11, through Writer/Reader). Mirrors
// db_entry_status_integration_test.go's style and reuses its shared Group B
// helpers (insertEntryColumns, minColumns, minArgs) and its
// assertErrorNamesField helper -- same package, so no duplication.
//
// Assertions are ONLY against charging.Entry domain fields, raw SQL column
// values, and *pgconn.PgError's SQLSTATE (via the shared assertPgErrorCode
// helper) -- never pgtype, in this file or any other
// (internal/charging/AGENTS.md §Testing Notes).
//
// Test -> Test Contract case mapping:
//
//	B1  TestPriceSource_B1_PreMigrationInsertDefaultsToUnconfirmed
//	B2  TestPriceSource_B2_CheckRejectsUnknownValue
//	B3  TestPriceSource_B3_DefaultIsPriceBlind
//	C1  TestPriceSource_C1_PositivePriceIsUser
//	C2  TestPriceSource_C2_ConfirmedZeroIsUser
//	C3  TestPriceSource_C3_UnconfirmedZeroIsUnconfirmed
//	C4  TestPriceSource_C4_PrecedenceThroughWritePath
//	C5  TestPriceSource_C5_CallerCannotSetProvenanceDirectly
//	C6  TestPriceSource_C6_RecomputedNeverStickyOnUpdate
//	C7  TestPromotion_C7_CompleteInProgressPromotedOnCreate
//	C8  TestPromotion_C8_PromotedOnUpdate
//	C9  TestPromotion_C9_EmptyStatusNormalizesThenPromotes
//	C10 TestPromotion_C10_IncompleteExplicitDoneStillRejected
//	C11 TestPromotion_C11_ReopenWithoutClearingFieldsRePromotes
package charging_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// --- Group B: schema, defaults and constraints (direct SQL) ---

// TestPriceSource_B1_PreMigrationInsertDefaultsToUnconfirmed implements
// design.md Test Contract B1: an INSERT naming only pre-migration columns (no
// price_source) always lands on the column DEFAULT -- this is the DEFAULT
// MECHANISM, not the real historical backfill, which the owner verifies
// after `make migrate-up` (design.md "Owner verification").
func TestPriceSource_B1_PreMigrationInsertDefaultsToUnconfirmed(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()

	args := minArgs(accountID, 330001)
	args[5] = 8000.00 // price
	id, err := insertEntryColumns(ctx, pool, minColumns, args...)
	if err != nil {
		t.Fatalf("B1: insert: %v", err)
	}

	var priceSource string
	if err := pool.QueryRow(ctx,
		"SELECT price_source FROM charging.manual_charge_entries WHERE id = $1", id,
	).Scan(&priceSource); err != nil {
		t.Fatalf("B1: read back: %v", err)
	}
	if priceSource != "UNCONFIRMED" {
		t.Errorf("B1: price_source = %q, want UNCONFIRMED", priceSource)
	}
}

// TestPriceSource_B2_CheckRejectsUnknownValue implements design.md Test
// Contract B2: the price_source CHECK (D2). Asserts SQLSTATE 23514, not
// message text.
func TestPriceSource_B2_CheckRejectsUnknownValue(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()

	columns := minColumns + ", price_source"
	args := append(minArgs(accountID, 330002), "FREE")
	_, err := insertEntryColumns(ctx, pool, columns, args...)
	assertPgErrorCode(t, err, "23514")
}

// TestPriceSource_B3_DefaultIsPriceBlind implements design.md Test Contract
// B3: the same insert shape as B1, restated as its own case for clarity
// (design.md). The DEFAULT does not implement the price-based rule -- only
// the migration's one-time backfill UPDATE and the Go write path
// (resolvePriceSource, Group C) apply it. A raw insert with a positive price
// and no explicit price_source still defaults to UNCONFIRMED, never USER.
func TestPriceSource_B3_DefaultIsPriceBlind(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()

	args := minArgs(accountID, 330003)
	args[5] = 8000.00 // price -- positive, but the DEFAULT cannot see it
	id, err := insertEntryColumns(ctx, pool, minColumns, args...)
	if err != nil {
		t.Fatalf("B3: insert: %v", err)
	}

	var priceSource string
	if err := pool.QueryRow(ctx,
		"SELECT price_source FROM charging.manual_charge_entries WHERE id = $1", id,
	).Scan(&priceSource); err != nil {
		t.Fatalf("B3: read back: %v", err)
	}
	if priceSource != "UNCONFIRMED" {
		t.Errorf("B3: price_source = %q, want UNCONFIRMED (not USER -- the DEFAULT is price-blind)", priceSource)
	}
}

// --- Group C: price_source write-path behaviour through Writer/Reader ---

// TestPriceSource_C1_PositivePriceIsUser implements design.md Test Contract
// C1: a positive price is USER through the real write path (RD3 row 1).
func TestPriceSource_C1_PositivePriceIsUser(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	e := minEntry(accountID, 340001)
	e.Price = 8000.00
	e.PriceConfirmed = false

	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C1: Create: unexpected error: %v", err)
	}
	if created.PriceSource != charging.PriceSourceUser {
		t.Errorf("C1: PriceSource = %v, want USER", created.PriceSource)
	}
}

// TestPriceSource_C2_ConfirmedZeroIsUser implements design.md Test Contract
// C2: a confirmed zero price is USER (RD3 row 2).
func TestPriceSource_C2_ConfirmedZeroIsUser(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	e := minEntry(accountID, 340002)
	e.Price = 0
	e.PriceConfirmed = true

	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C2: Create: unexpected error: %v", err)
	}
	if created.PriceSource != charging.PriceSourceUser {
		t.Errorf("C2: PriceSource = %v, want USER", created.PriceSource)
	}
}

// TestPriceSource_C3_UnconfirmedZeroIsUnconfirmed implements design.md Test
// Contract C3: an unconfirmed zero price is UNCONFIRMED (RD3 row 3) -- also
// the behaviour of every caller that has not adopted PriceConfirmed yet
// (design.md Context fact 6).
func TestPriceSource_C3_UnconfirmedZeroIsUnconfirmed(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	e := minEntry(accountID, 340003)
	e.Price = 0
	e.PriceConfirmed = false

	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C3: Create: unexpected error: %v", err)
	}
	if created.PriceSource != charging.PriceSourceUnconfirmed {
		t.Errorf("C3: PriceSource = %v, want UNCONFIRMED", created.PriceSource)
	}
}

// TestPriceSource_C4_PrecedenceThroughWritePath implements design.md Test
// Contract C4: precedence (D5) through the write path, matching A10 -- a
// positive price wins regardless of the confirmation flag.
func TestPriceSource_C4_PrecedenceThroughWritePath(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	e := minEntry(accountID, 340004)
	e.Price = 8000.00
	e.PriceConfirmed = true

	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C4: Create: unexpected error: %v", err)
	}
	if created.PriceSource != charging.PriceSourceUser {
		t.Errorf("C4: PriceSource = %v, want USER", created.PriceSource)
	}
}

// TestPriceSource_C5_CallerCannotSetProvenanceDirectly implements design.md
// Test Contract C5: a caller cannot set the provenance directly -- it is
// always computed (D3), matching EnergySource's precedent (RM33 C4).
func TestPriceSource_C5_CallerCannotSetProvenanceDirectly(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	e := minEntry(accountID, 340005)
	e.Price = 0
	e.PriceConfirmed = false
	e.PriceSource = charging.PriceSourceUser // must be ignored

	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C5: Create: unexpected error: %v", err)
	}
	if created.PriceSource != charging.PriceSourceUnconfirmed {
		t.Errorf("C5: PriceSource = %v, want UNCONFIRMED (caller's USER must be ignored)", created.PriceSource)
	}
}

// TestPriceSource_C6_RecomputedNeverStickyOnUpdate implements design.md Test
// Contract C6: provenance is recomputed on every write, never sticky --
// mirrors EnergySource's C14 precedent from RM33.
func TestPriceSource_C6_RecomputedNeverStickyOnUpdate(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	e := minEntry(accountID, 340006)
	e.Price = 0
	e.PriceConfirmed = false
	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C6: setup Create: %v", err)
	}
	if created.PriceSource != charging.PriceSourceUnconfirmed {
		t.Fatalf("C6: setup: PriceSource = %v, want UNCONFIRMED", created.PriceSource)
	}

	updated := created
	updated.PriceConfirmed = true

	result, err := w.Update(ctx, updated)
	if err != nil {
		t.Fatalf("C6: Update: unexpected error: %v", err)
	}
	if result.PriceSource != charging.PriceSourceUser {
		t.Errorf("C6: PriceSource = %v, want USER", result.PriceSource)
	}
}

// --- Group C: promotion (RD1/RD2) through Writer/Reader ---

// TestPromotion_C7_CompleteInProgressPromotedOnCreate implements design.md
// Test Contract C7: the headline promotion feature, through Writer.Create
// (RD1).
func TestPromotion_C7_CompleteInProgressPromotedOnCreate(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	end := time.Date(2026, 9, 9, 18, 0, 0, 0, time.UTC)
	e := minEntry(accountID, 340007)
	e.Status = charging.StatusInProgress
	e.EndedAt = ptrTime(end)
	e.EndBatteryPct = ptrInt(90)

	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C7: Create: unexpected error: %v", err)
	}
	if created.Status != charging.StatusDone {
		t.Errorf("C7: Status = %v, want DONE", created.Status)
	}
}

// TestPromotion_C8_PromotedOnUpdate implements design.md Test Contract C8:
// promotion also fires on Update (RD2) -- the second write path, proven
// independently of C7.
func TestPromotion_C8_PromotedOnUpdate(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	e := minEntry(accountID, 340008)
	e.Status = charging.StatusInProgress
	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C8: setup Create: %v", err)
	}
	if created.Status != charging.StatusInProgress {
		t.Fatalf("C8: setup: Status = %v, want IN_PROGRESS", created.Status)
	}

	end := time.Date(2026, 9, 9, 18, 0, 0, 0, time.UTC)
	updated := created
	updated.Status = charging.StatusInProgress
	updated.EndedAt = ptrTime(end)
	updated.EndBatteryPct = ptrInt(90)

	result, err := w.Update(ctx, updated)
	if err != nil {
		t.Fatalf("C8: Update: unexpected error: %v", err)
	}
	if result.Status != charging.StatusDone {
		t.Errorf("C8: Status = %v, want DONE", result.Status)
	}
}

// TestPromotion_C9_EmptyStatusNormalizesThenPromotes implements design.md
// Test Contract C9 (D6): an omitted status is not a special case -- it
// normalizes to IN_PROGRESS first, then promotes exactly as an explicit
// IN_PROGRESS submission would.
func TestPromotion_C9_EmptyStatusNormalizesThenPromotes(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	end := time.Date(2026, 9, 9, 18, 0, 0, 0, time.UTC)
	e := minEntry(accountID, 340009)
	e.Status = "" // Go's zero value
	e.EndedAt = ptrTime(end)
	e.EndBatteryPct = ptrInt(90)

	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C9: Create: unexpected error: %v", err)
	}
	if created.Status != charging.StatusDone {
		t.Errorf("C9: Status = %v, want DONE", created.Status)
	}
}

// TestPromotion_C10_IncompleteExplicitDoneStillRejected implements
// design.md Test Contract C10: promotion changes nothing for an explicit
// DONE submission -- an incomplete DONE entry is still rejected exactly as
// RM33 specified (D1).
func TestPromotion_C10_IncompleteExplicitDoneStillRejected(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)
	r := charging.NewReader(pool)

	end := time.Date(2026, 9, 9, 18, 0, 0, 0, time.UTC)
	e := minEntry(accountID, 340010)
	e.Status = charging.StatusDone
	e.EndedAt = ptrTime(end)
	e.EndBatteryPct = nil

	_, err := w.Create(ctx, e)
	assertErrorNamesField(t, err, string(charging.FieldEndBatteryPct))

	entries, listErr := r.ListEntriesByVehicle(ctx, accountID, 340010, 10)
	if listErr != nil {
		t.Fatalf("C10: ListEntriesByVehicle: %v", listErr)
	}
	if len(entries) != 0 {
		t.Errorf("C10: expected 0 rows after rejection, got %d", len(entries))
	}
}

// TestPromotion_C11_ReopenWithoutClearingFieldsRePromotes implements
// design.md Test Contract C11 (D1's known-and-accepted cost, proven
// concretely): "reopening" a DONE entry without clearing at least one of its
// two DONE-only fields is immediately re-promoted back to DONE -- the row
// never visibly reopens.
func TestPromotion_C11_ReopenWithoutClearingFieldsRePromotes(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	end := time.Date(2026, 9, 9, 18, 0, 0, 0, time.UTC)
	e := minEntry(accountID, 340011)
	e.Status = charging.StatusInProgress
	e.EndedAt = ptrTime(end)
	e.EndBatteryPct = ptrInt(90)
	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C11: setup Create: %v", err)
	}
	if created.Status != charging.StatusDone {
		t.Fatalf("C11: setup: Status = %v, want DONE", created.Status)
	}

	updated := created
	updated.Status = charging.StatusInProgress
	// EndedAt and EndBatteryPct are left unchanged (still non-nil) -- the
	// caller did not clear either field, so the entry is re-promoted rather
	// than reopened.

	result, err := w.Update(ctx, updated)
	if err != nil {
		t.Fatalf("C11: Update: unexpected error: %v", err)
	}
	if result.Status != charging.StatusDone {
		t.Errorf("C11: Status = %v, want DONE (re-promoted, not reopened)", result.Status)
	}
}
