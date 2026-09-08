package account

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	accountdb "github.com/cristianpena/magus-tesla-api/internal/account/db"
	"github.com/cristianpena/magus-tesla-api/internal/auth"
)

// newTestService builds a white-box service against the test Postgres
// provisioned by TestMain (see testdb_test.go), with a fake refresh so no
// network call is made.
func newTestService(t *testing.T) (*service, *pgxpool.Pool) {
	t.Helper()
	if testDSN == "" {
		t.Fatalf("account test DSN not initialized; TestMain failure?")
	}
	pool, err := pgxpool.New(context.Background(), testDSN)
	if err != nil {
		t.Fatalf("connecting to Postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	s := &service{
		pool:         pool,
		q:            accountdb.New(pool),
		clientID:     "test-client",
		clientSecret: "test-secret",
		refresh: func(_, _, _ string) (*auth.TokenResponse, error) {
			return &auth.TokenResponse{
				AccessToken:  "new-access",
				RefreshToken: "new-refresh",
				ExpiresIn:    28800,
			}, nil
		},
		now: time.Now,
	}
	return s, pool
}

func deleteAccount(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) {
	t.Cleanup(func() {
		// ON DELETE CASCADE removes the account's tesla_tokens too.
		_, _ = pool.Exec(context.Background(), "DELETE FROM account.accounts WHERE id = $1", id)
	})
}

// activateAccount flips a test account to StatusActive via direct SQL — a
// test-only concession (ai/go-conventions.md §Persistence "Seeding another
// module's tables"). Every UpsertFromOAuth-provisioned account defaults to
// StatusInactive (RM34 D2), and design.md D14/D15 (RM34-account-add-record-status)
// gate ListVehiclesByAccount, ListAllVehicles, GetLatestTeslaTokenByAccount, and
// GetLatestTeslaTokenByAccountForUpdate on the owning account's status via an
// EXISTS check — so any test that seeds a vehicle or token and reads it back
// through one of those paths must activate its account first, or the read
// returns nothing despite the row existing.
func activateAccount(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		"UPDATE account.accounts SET status = 'Active' WHERE id = $1", id,
	); err != nil {
		t.Fatalf("activating test account: %v", err)
	}
}

func TestUpsertFromOAuth_Idempotent(t *testing.T) {
	s, pool := newTestService(t)
	ctx := context.Background()

	id := OAuthIdentity{
		Provider:    "google",
		ProviderID:  uuid.NewString(), // unique per run
		Email:       "a@example.com",
		DisplayName: "A",
	}

	first, err := s.UpsertFromOAuth(ctx, id)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	deleteAccount(t, pool, first.ID)
	if first.Status != StatusInactive {
		t.Errorf("expected first upsert Status == StatusInactive (RM34 D2 default), got %q", first.Status)
	}

	// Same identity, changed profile fields → same account, updated fields.
	id.Email = "changed@example.com"
	second, err := s.UpsertFromOAuth(ctx, id)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("expected same account id for repeated identity, got %s then %s", first.ID, second.ID)
	}
	if second.Email != "changed@example.com" {
		t.Errorf("expected email updated to changed@example.com, got %q", second.Email)
	}
	if second.Status != StatusInactive {
		t.Errorf("expected second upsert Status == StatusInactive (RM34 D2 default, unaffected by re-upsert), got %q", second.Status)
	}
}

func TestSaveTeslaTokens_ReplacesExistingConnection(t *testing.T) {
	s, pool := newTestService(t)
	ctx := context.Background()

	acct, err := s.UpsertFromOAuth(ctx, OAuthIdentity{
		Provider:   "google",
		ProviderID: uuid.NewString(),
		Email:      "replace@example.com",
	})
	if err != nil {
		t.Fatalf("provisioning account: %v", err)
	}
	deleteAccount(t, pool, acct.ID)
	activateAccount(t, pool, acct.ID) // RM34 D14/D15: token reads are gated on account status

	if err := s.SaveTeslaTokens(ctx, acct.ID, TeslaTokens{
		AccessToken:     "first-access",
		RefreshToken:    "first-refresh",
		AccessExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// Reconnecting the same account must replace the tokens in place, not add a row.
	if err := s.SaveTeslaTokens(ctx, acct.ID, TeslaTokens{
		AccessToken:     "second-access",
		RefreshToken:    "second-refresh",
		AccessExpiresAt: time.Now().Add(2 * time.Hour),
	}); err != nil {
		t.Fatalf("second save: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM account.tesla_tokens WHERE account_id = $1", acct.ID).Scan(&count); err != nil {
		t.Fatalf("counting tokens: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one tesla_tokens row after reconnect, got %d", count)
	}

	tok, err := s.q.GetLatestTeslaTokenByAccount(ctx, acct.ID)
	if err != nil {
		t.Fatalf("reloading token: %v", err)
	}
	if tok.AccessToken != "second-access" || tok.RefreshToken != "second-refresh" {
		t.Errorf("expected the second pair to be stored, got access=%q refresh=%q", tok.AccessToken, tok.RefreshToken)
	}
}

func TestAccessTokenFor_RefreshesAndPersistsOnExpiry(t *testing.T) {
	s, pool := newTestService(t)
	ctx := context.Background()

	acct, err := s.UpsertFromOAuth(ctx, OAuthIdentity{
		Provider:   "google",
		ProviderID: uuid.NewString(),
		Email:      "b@example.com",
	})
	if err != nil {
		t.Fatalf("provisioning account: %v", err)
	}
	deleteAccount(t, pool, acct.ID)
	activateAccount(t, pool, acct.ID) // RM34 D14/D15: token reads are gated on account status

	// Store an already-expired connection.
	if err := s.SaveTeslaTokens(ctx, acct.ID, TeslaTokens{
		AccessToken:     "old-access",
		RefreshToken:    "old-refresh",
		AccessExpiresAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("saving tesla tokens: %v", err)
	}

	got, err := s.AccessTokenFor(ctx, acct.ID)
	if err != nil {
		t.Fatalf("AccessTokenFor: %v", err)
	}
	if got != "new-access" {
		t.Fatalf("expected refreshed access token %q, got %q", "new-access", got)
	}

	// The rotated pair must be persisted, with the expiry advanced into the future.
	tok, err := s.q.GetLatestTeslaTokenByAccount(ctx, acct.ID)
	if err != nil {
		t.Fatalf("reloading token: %v", err)
	}
	if tok.AccessToken != "new-access" || tok.RefreshToken != "new-refresh" {
		t.Errorf("rotated pair not persisted: access=%q refresh=%q", tok.AccessToken, tok.RefreshToken)
	}
	if !tok.AccessExpiresAt.Time.After(time.Now()) {
		t.Errorf("expected expiry advanced into the future, got %v", tok.AccessExpiresAt.Time)
	}
}

func TestAccessTokenFor_ReturnsSentinelWhenNoConnection(t *testing.T) {
	s, pool := newTestService(t)
	ctx := context.Background()

	acct, err := s.UpsertFromOAuth(ctx, OAuthIdentity{
		Provider:   "google",
		ProviderID: uuid.NewString(),
		Email:      "c@example.com",
	})
	if err != nil {
		t.Fatalf("provisioning account: %v", err)
	}
	deleteAccount(t, pool, acct.ID)

	if _, err := s.AccessTokenFor(ctx, acct.ID); !errors.Is(err, ErrNoTeslaConnection) {
		t.Fatalf("expected ErrNoTeslaConnection, got %v", err)
	}
}

func TestRegisteredVehicles_EmptyForNewAccount(t *testing.T) {
	s, pool := newTestService(t)
	ctx := context.Background()

	acct, err := s.UpsertFromOAuth(ctx, OAuthIdentity{
		Provider:   "google",
		ProviderID: uuid.NewString(),
		Email:      "vehicles-empty@example.com",
	})
	if err != nil {
		t.Fatalf("provisioning account: %v", err)
	}
	deleteAccount(t, pool, acct.ID)

	got, err := s.RegisteredVehicles(ctx, acct.ID)
	if err != nil {
		t.Fatalf("RegisteredVehicles: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty vehicle set for new account, got %d", len(got))
	}
}

// vehiclesForAccount filters an all-accounts enumeration down to a single account
// and keys the result by tesla_id. The integration tests share a real Postgres, so
// AllRegisteredVehicles can legitimately see vehicles other tests created; asserting
// against a specific account id keeps these tests robust regardless of what else the
// registry holds.
func vehiclesForAccount(all []OwnedVehicle, accountID uuid.UUID) map[int64]OwnedVehicle {
	byID := map[int64]OwnedVehicle{}
	for _, v := range all {
		if v.AccountID == accountID {
			byID[v.TeslaID] = v
		}
	}
	return byID
}

func TestAllRegisteredVehicles_EmptyWhenNoneRegistered(t *testing.T) {
	s, pool := newTestService(t)
	ctx := context.Background()

	// A brand-new account with no vehicles: the all-accounts enumeration must not
	// error, must return a non-nil slice, and must contain none of this account's
	// vehicles (it may contain other tests' data in a shared DB).
	acct, err := s.UpsertFromOAuth(ctx, OAuthIdentity{
		Provider:   "google",
		ProviderID: uuid.NewString(),
		Email:      "all-empty@example.com",
	})
	if err != nil {
		t.Fatalf("provisioning account: %v", err)
	}
	deleteAccount(t, pool, acct.ID)

	got, err := s.AllRegisteredVehicles(ctx)
	if err != nil {
		t.Fatalf("AllRegisteredVehicles: %v", err)
	}
	if got == nil {
		t.Fatalf("expected a non-nil slice from AllRegisteredVehicles, got nil")
	}
	if mine := vehiclesForAccount(got, acct.ID); len(mine) != 0 {
		t.Fatalf("expected no vehicles for a fresh account, got %d (%+v)", len(mine), mine)
	}
}

func TestAllRegisteredVehicles_SingleAccountTaggedWithItsID(t *testing.T) {
	s, pool := newTestService(t)
	ctx := context.Background()

	acct, err := s.UpsertFromOAuth(ctx, OAuthIdentity{
		Provider:   "google",
		ProviderID: uuid.NewString(),
		Email:      "all-single@example.com",
	})
	if err != nil {
		t.Fatalf("provisioning account: %v", err)
	}
	deleteAccount(t, pool, acct.ID)
	activateAccount(t, pool, acct.ID) // RM34 D14/D15: vehicle reads are gated on account status

	// One vehicle with a display name, one with a NULL display name (empty string
	// seeds as NULL via textFromString), to assert the NULL→"" mapping.
	if _, err := s.SeedVehicles(ctx, acct.ID, []SeedVehicle{
		{TeslaID: 5001, VIN: "VIN5001", DisplayName: "Named Car"},
		{TeslaID: 5002, VIN: "VIN5002", DisplayName: ""},
	}); err != nil {
		t.Fatalf("seeding vehicles: %v", err)
	}

	all, err := s.AllRegisteredVehicles(ctx)
	if err != nil {
		t.Fatalf("AllRegisteredVehicles: %v", err)
	}

	mine := vehiclesForAccount(all, acct.ID)
	if len(mine) != 2 {
		t.Fatalf("expected 2 vehicles for the account, got %d (%+v)", len(mine), mine)
	}
	if v := mine[5001]; v.AccountID != acct.ID || v.VIN != "VIN5001" || v.DisplayName != "Named Car" {
		t.Errorf("vehicle 5001 round-trip wrong: %+v (want AccountID=%s VIN=VIN5001 DisplayName=Named Car)", v, acct.ID)
	}
	if v := mine[5002]; v.AccountID != acct.ID || v.VIN != "VIN5002" || v.DisplayName != "" {
		t.Errorf("vehicle 5002 round-trip wrong: %+v (want AccountID=%s VIN=VIN5002 DisplayName=\"\")", v, acct.ID)
	}
}

func TestAllRegisteredVehicles_MultipleAccountsEachTaggedCorrectly(t *testing.T) {
	s, pool := newTestService(t)
	ctx := context.Background()

	acctA, err := s.UpsertFromOAuth(ctx, OAuthIdentity{
		Provider:   "google",
		ProviderID: uuid.NewString(),
		Email:      "all-multi-a@example.com",
	})
	if err != nil {
		t.Fatalf("provisioning account A: %v", err)
	}
	deleteAccount(t, pool, acctA.ID)
	activateAccount(t, pool, acctA.ID) // RM34 D14/D15: vehicle reads are gated on account status

	acctB, err := s.UpsertFromOAuth(ctx, OAuthIdentity{
		Provider:   "google",
		ProviderID: uuid.NewString(),
		Email:      "all-multi-b@example.com",
	})
	if err != nil {
		t.Fatalf("provisioning account B: %v", err)
	}
	deleteAccount(t, pool, acctB.ID)
	activateAccount(t, pool, acctB.ID) // RM34 D14/D15: vehicle reads are gated on account status

	// A has one vehicle; B has two.
	if _, err := s.SeedVehicles(ctx, acctA.ID, []SeedVehicle{
		{TeslaID: 6001, VIN: "VIN6001", DisplayName: "A-Car"},
	}); err != nil {
		t.Fatalf("seeding account A: %v", err)
	}
	if _, err := s.SeedVehicles(ctx, acctB.ID, []SeedVehicle{
		{TeslaID: 6101, VIN: "VIN6101", DisplayName: "B-Car-One"},
		{TeslaID: 6102, VIN: "VIN6102", DisplayName: "B-Car-Two"},
	}); err != nil {
		t.Fatalf("seeding account B: %v", err)
	}

	all, err := s.AllRegisteredVehicles(ctx)
	if err != nil {
		t.Fatalf("AllRegisteredVehicles: %v", err)
	}

	mineA := vehiclesForAccount(all, acctA.ID)
	if len(mineA) != 1 {
		t.Fatalf("expected 1 vehicle for account A, got %d (%+v)", len(mineA), mineA)
	}
	if v := mineA[6001]; v.AccountID != acctA.ID || v.VIN != "VIN6001" || v.DisplayName != "A-Car" {
		t.Errorf("account A vehicle wrong: %+v (want AccountID=%s)", v, acctA.ID)
	}

	mineB := vehiclesForAccount(all, acctB.ID)
	if len(mineB) != 2 {
		t.Fatalf("expected 2 vehicles for account B, got %d (%+v)", len(mineB), mineB)
	}
	if v := mineB[6101]; v.AccountID != acctB.ID || v.VIN != "VIN6101" || v.DisplayName != "B-Car-One" {
		t.Errorf("account B vehicle 6101 wrong: %+v (want AccountID=%s)", v, acctB.ID)
	}
	if v := mineB[6102]; v.AccountID != acctB.ID || v.VIN != "VIN6102" || v.DisplayName != "B-Car-Two" {
		t.Errorf("account B vehicle 6102 wrong: %+v (want AccountID=%s)", v, acctB.ID)
	}

	// Cross-check: A's vehicle is never tagged with B's id and vice versa.
	if _, ok := mineA[6101]; ok {
		t.Errorf("account A's result leaked account B's vehicle 6101")
	}
	if _, ok := mineB[6001]; ok {
		t.Errorf("account B's result leaked account A's vehicle 6001")
	}
}

func TestSeedVehicles_InsertsWhenMissing(t *testing.T) {
	s, pool := newTestService(t)
	ctx := context.Background()

	acct, err := s.UpsertFromOAuth(ctx, OAuthIdentity{
		Provider:   "google",
		ProviderID: uuid.NewString(),
		Email:      "seed-insert@example.com",
	})
	if err != nil {
		t.Fatalf("provisioning account: %v", err)
	}
	deleteAccount(t, pool, acct.ID)
	activateAccount(t, pool, acct.ID) // RM34 D14/D15: vehicle reads are gated on account status

	got, err := s.SeedVehicles(ctx, acct.ID, []SeedVehicle{
		{TeslaID: 100, VIN: "VIN100", DisplayName: "Car One"},
		{TeslaID: 200, VIN: "VIN200", DisplayName: "Car Two"},
	})
	if err != nil {
		t.Fatalf("SeedVehicles: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 registered vehicles, got %d (%+v)", len(got), got)
	}
	want := map[int64]Vehicle{
		100: {TeslaID: 100, VIN: "VIN100", DisplayName: "Car One"},
		200: {TeslaID: 200, VIN: "VIN200", DisplayName: "Car Two"},
	}
	for _, v := range got {
		w, ok := want[v.TeslaID]
		if !ok {
			t.Errorf("unexpected vehicle tesla_id=%d", v.TeslaID)
			continue
		}
		if v.VIN != w.VIN || v.DisplayName != w.DisplayName {
			t.Errorf("vehicle %d: want %+v, got %+v", v.TeslaID, w, v)
		}
	}
}

// ptr returns a pointer to the given string. Helper for tests that need *string literals.
func ptr(s string) *string { return &s }

// TestAccessType_RoundTrip verifies that access_type is persisted and read back
// correctly through the full seed→read path (DATABASE_URL-gated, design.md D4).
func TestAccessType_RoundTrip(t *testing.T) {
	s, pool := newTestService(t)
	ctx := context.Background()

	acct, err := s.UpsertFromOAuth(ctx, OAuthIdentity{
		Provider:   "google",
		ProviderID: uuid.NewString(),
		Email:      "access-type-roundtrip@example.com",
	})
	if err != nil {
		t.Fatalf("provisioning account: %v", err)
	}
	deleteAccount(t, pool, acct.ID)
	activateAccount(t, pool, acct.ID) // RM34 D14/D15: vehicle reads are gated on account status

	vehicles, err := s.SeedVehicles(ctx, acct.ID, []SeedVehicle{
		{TeslaID: 7001, VIN: "VIN7001", DisplayName: "Owner Car", AccessType: ptr("OWNER")},
		{TeslaID: 7002, VIN: "VIN7002", DisplayName: "Driver Car", AccessType: ptr("DRIVER")},
		{TeslaID: 7003, VIN: "VIN7003", DisplayName: "Unknown Car", AccessType: nil},
	})
	if err != nil {
		t.Fatalf("SeedVehicles: %v", err)
	}
	if len(vehicles) != 3 {
		t.Fatalf("expected 3 vehicles, got %d", len(vehicles))
	}

	// Index by TeslaID for assertion.
	byID := map[int64]Vehicle{}
	for _, v := range vehicles {
		byID[v.TeslaID] = v
	}

	// OWNER round-trip via RegisteredVehicles.
	if v := byID[7001]; v.AccessType == nil || *v.AccessType != "OWNER" {
		t.Errorf("vehicle 7001: want AccessType=OWNER, got %v", v.AccessType)
	}
	// DRIVER round-trip.
	if v := byID[7002]; v.AccessType == nil || *v.AccessType != "DRIVER" {
		t.Errorf("vehicle 7002: want AccessType=DRIVER, got %v", v.AccessType)
	}
	// nil round-trip.
	if v := byID[7003]; v.AccessType != nil {
		t.Errorf("vehicle 7003: want AccessType=nil, got %v", v.AccessType)
	}

	// AllRegisteredVehicles also surfaces access_type correctly.
	all, err := s.AllRegisteredVehicles(ctx)
	if err != nil {
		t.Fatalf("AllRegisteredVehicles: %v", err)
	}
	mine := vehiclesForAccount(all, acct.ID)
	if len(mine) != 3 {
		t.Fatalf("expected 3 owned vehicles, got %d", len(mine))
	}
	if v := mine[7001]; v.AccessType == nil || *v.AccessType != "OWNER" {
		t.Errorf("AllRegisteredVehicles vehicle 7001: want AccessType=OWNER, got %v", v.AccessType)
	}
	if v := mine[7002]; v.AccessType == nil || *v.AccessType != "DRIVER" {
		t.Errorf("AllRegisteredVehicles vehicle 7002: want AccessType=DRIVER, got %v", v.AccessType)
	}
	if v := mine[7003]; v.AccessType != nil {
		t.Errorf("AllRegisteredVehicles vehicle 7003: want AccessType=nil, got %v", v.AccessType)
	}
}

// TestAccessType_IdempotencyPreservesStoredValue verifies that re-seeding an existing
// vehicle with a different access_type leaves the stored value unchanged (ON CONFLICT
// DO NOTHING — design.md D3).
func TestAccessType_IdempotencyPreservesStoredValue(t *testing.T) {
	s, pool := newTestService(t)
	ctx := context.Background()

	acct, err := s.UpsertFromOAuth(ctx, OAuthIdentity{
		Provider:   "google",
		ProviderID: uuid.NewString(),
		Email:      "access-type-idempotent@example.com",
	})
	if err != nil {
		t.Fatalf("provisioning account: %v", err)
	}
	deleteAccount(t, pool, acct.ID)
	activateAccount(t, pool, acct.ID) // RM34 D14/D15: vehicle reads are gated on account status

	// First seed: OWNER.
	if _, err := s.SeedVehicles(ctx, acct.ID, []SeedVehicle{
		{TeslaID: 8001, VIN: "VIN8001", DisplayName: "My Car", AccessType: ptr("OWNER")},
	}); err != nil {
		t.Fatalf("first seed: %v", err)
	}

	// Re-seed same vehicle with DRIVER — ON CONFLICT DO NOTHING must leave it as OWNER.
	got, err := s.SeedVehicles(ctx, acct.ID, []SeedVehicle{
		{TeslaID: 8001, VIN: "VIN8001", DisplayName: "My Car", AccessType: ptr("DRIVER")},
	})
	if err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 vehicle after re-seed, got %d", len(got))
	}
	if got[0].AccessType == nil || *got[0].AccessType != "OWNER" {
		t.Errorf("re-seed overwrote access_type: want OWNER (unchanged), got %v", got[0].AccessType)
	}
}

// TestAccessType_InvalidValueRejectedByCheckConstraint verifies that seeding a vehicle
// with an invalid access_type (not 'OWNER' or 'DRIVER') produces a DB CHECK constraint
// error (design.md D1).
func TestAccessType_InvalidValueRejectedByCheckConstraint(t *testing.T) {
	s, pool := newTestService(t)
	ctx := context.Background()

	acct, err := s.UpsertFromOAuth(ctx, OAuthIdentity{
		Provider:   "google",
		ProviderID: uuid.NewString(),
		Email:      "access-type-check@example.com",
	})
	if err != nil {
		t.Fatalf("provisioning account: %v", err)
	}
	deleteAccount(t, pool, acct.ID)

	_, err = s.SeedVehicles(ctx, acct.ID, []SeedVehicle{
		{TeslaID: 9001, VIN: "VIN9001", DisplayName: "Bad Car", AccessType: ptr("ADMIN")},
	})
	if err == nil {
		t.Fatalf("expected CHECK constraint error for invalid access_type 'ADMIN', got nil")
	}
	// Verify the error message mentions the check constraint (pgx surfaces this).
	if errStr := err.Error(); errStr == "" {
		t.Errorf("expected a non-empty error string for CHECK violation")
	}
}

// TestSetVehicleConfigIfEmpty_RoundTrip verifies the conditional-update port method
// (design.md D4, DATABASE_URL-gated) through the full seed -> capture -> read path.
// Mirrors TestAccessType_RoundTrip's setup. Covers: fresh capture, no-op once fully
// captured, self-heal of a partially-captured row (the OR-semantics case that
// distinguishes design.md D4 from the rejected AND), both read paths, and the nil
// (never-captured) case.
func TestSetVehicleConfigIfEmpty_RoundTrip(t *testing.T) {
	s, pool := newTestService(t)
	ctx := context.Background()

	acct, err := s.UpsertFromOAuth(ctx, OAuthIdentity{
		Provider:   "google",
		ProviderID: uuid.NewString(),
		Email:      "vehicle-config-roundtrip@example.com",
	})
	if err != nil {
		t.Fatalf("provisioning account: %v", err)
	}
	deleteAccount(t, pool, acct.ID)
	activateAccount(t, pool, acct.ID) // RM34 D14/D15: vehicle reads are gated on account status

	const (
		freshCaptureID  int64 = 10001 // fresh capture, then a no-op re-call
		selfHealID      int64 = 10002 // partially-captured row, self-heals via OR
		neverCapturedID int64 = 10003 // nil round-trip: capture never called
	)

	if _, err := s.SeedVehicles(ctx, acct.ID, []SeedVehicle{
		{TeslaID: freshCaptureID, VIN: "VIN10001", DisplayName: "Fresh Capture Car"},
		{TeslaID: selfHealID, VIN: "VIN10002", DisplayName: "Self Heal Car"},
		{TeslaID: neverCapturedID, VIN: "VIN10003", DisplayName: "Never Captured Car"},
	}); err != nil {
		t.Fatalf("seeding vehicles: %v", err)
	}
	// SeedVehicles never sets exterior_color/car_type: all three start NULL.

	// --- Fresh capture: both NULL -> both set ---
	if err := s.SetVehicleConfigIfEmpty(ctx, acct.ID, freshCaptureID, "PearlWhite", "modely"); err != nil {
		t.Fatalf("SetVehicleConfigIfEmpty (fresh capture): %v", err)
	}

	byID := func() map[int64]Vehicle {
		vehicles, err := s.RegisteredVehicles(ctx, acct.ID)
		if err != nil {
			t.Fatalf("RegisteredVehicles: %v", err)
		}
		m := map[int64]Vehicle{}
		for _, v := range vehicles {
			m[v.TeslaID] = v
		}
		return m
	}

	got := byID()
	if v := got[freshCaptureID]; v.ExteriorColor == nil || *v.ExteriorColor != "PearlWhite" || v.CarType == nil || *v.CarType != "modely" {
		t.Fatalf("fresh capture: want ExteriorColor=PearlWhite CarType=modely, got %+v", v)
	}

	// --- No-op on a fully-captured row: a second call with different values must not overwrite ---
	if err := s.SetVehicleConfigIfEmpty(ctx, acct.ID, freshCaptureID, "SolidBlack", "modelx"); err != nil {
		t.Fatalf("SetVehicleConfigIfEmpty (no-op attempt): %v", err)
	}
	got = byID()
	if v := got[freshCaptureID]; v.ExteriorColor == nil || *v.ExteriorColor != "PearlWhite" || v.CarType == nil || *v.CarType != "modely" {
		t.Fatalf("expected no-op to leave PearlWhite/modely unchanged, got %+v", v)
	}

	// --- Self-heal on a partially-captured row ---
	// A partial state is unreachable through the port (SetVehicleConfigIfEmpty always
	// sets both columns together), so construct it directly with a raw exec: leave
	// car_type NULL while exterior_color is already set.
	if _, err := pool.Exec(ctx,
		"UPDATE account.vehicles SET exterior_color = $1 WHERE account_id = $2 AND tesla_id = $3",
		"PearlWhite", acct.ID, selfHealID,
	); err != nil {
		t.Fatalf("constructing partially-captured row: %v", err)
	}
	// Confirm the partial state landed before exercising the method under test.
	partial := byID()
	if v := partial[selfHealID]; v.ExteriorColor == nil || *v.ExteriorColor != "PearlWhite" || v.CarType != nil {
		t.Fatalf("expected partial row exterior_color=PearlWhite car_type=nil before self-heal, got %+v", v)
	}

	if err := s.SetVehicleConfigIfEmpty(ctx, acct.ID, selfHealID, "PearlWhite", "modely"); err != nil {
		t.Fatalf("SetVehicleConfigIfEmpty (self-heal): %v", err)
	}
	got = byID()
	if v := got[selfHealID]; v.ExteriorColor == nil || *v.ExteriorColor != "PearlWhite" || v.CarType == nil || *v.CarType != "modely" {
		t.Fatalf("self-heal: want BOTH columns populated (PearlWhite/modely) proving the OR condition matched, got %+v", v)
	}

	// --- nil round-trip: capture never called for this vehicle ---
	if v := got[neverCapturedID]; v.ExteriorColor != nil || v.CarType != nil {
		t.Fatalf("never-captured vehicle: want ExteriorColor=nil CarType=nil, got %+v", v)
	}

	// --- AllRegisteredVehicles surfaces the same two fields correctly ---
	all, err := s.AllRegisteredVehicles(ctx)
	if err != nil {
		t.Fatalf("AllRegisteredVehicles: %v", err)
	}
	mine := vehiclesForAccount(all, acct.ID)
	if len(mine) != 3 {
		t.Fatalf("expected 3 owned vehicles, got %d (%+v)", len(mine), mine)
	}
	if v := mine[freshCaptureID]; v.ExteriorColor == nil || *v.ExteriorColor != "PearlWhite" || v.CarType == nil || *v.CarType != "modely" {
		t.Errorf("AllRegisteredVehicles fresh-capture vehicle: want PearlWhite/modely, got %+v", v)
	}
	if v := mine[selfHealID]; v.ExteriorColor == nil || *v.ExteriorColor != "PearlWhite" || v.CarType == nil || *v.CarType != "modely" {
		t.Errorf("AllRegisteredVehicles self-heal vehicle: want PearlWhite/modely, got %+v", v)
	}
	if v := mine[neverCapturedID]; v.ExteriorColor != nil || v.CarType != nil {
		t.Errorf("AllRegisteredVehicles never-captured vehicle: want nil/nil, got %+v", v)
	}
}

// TestLanguagePreference_RoundTrip verifies the full language preference
// read/write path (design.md D3/D4, DATABASE_URL-gated): a fresh account's
// default, a successful switch and switch-back, rejection of an unsupported
// code (no write occurs), and normalization of a value written outside this
// module's write path. Mirrors TestAccessType_RoundTrip's setup.
func TestLanguagePreference_RoundTrip(t *testing.T) {
	s, pool := newTestService(t)
	ctx := context.Background()

	acct, err := s.UpsertFromOAuth(ctx, OAuthIdentity{
		Provider:   "google",
		ProviderID: uuid.NewString(),
		Email:      "language-roundtrip@example.com",
	})
	if err != nil {
		t.Fatalf("provisioning account: %v", err)
	}
	deleteAccount(t, pool, acct.ID)

	// RM34: a freshly-provisioned account defaults to StatusInactive, and every
	// language read/write below is now filtered by status = 'Active' (design.md
	// D4). Activate it before exercising any language read/write.
	activateAccount(t, pool, acct.ID)

	// --- Default on a fresh account: no explicit SetLanguage call yet ---
	got, err := s.LanguageFor(ctx, acct.ID)
	if err != nil {
		t.Fatalf("LanguageFor (fresh account): %v", err)
	}
	if got != LanguageES {
		t.Fatalf("fresh account language: want %q, got %q", LanguageES, got)
	}

	// --- Successful switch to en ---
	if err := s.SetLanguage(ctx, acct.ID, LanguageEN); err != nil {
		t.Fatalf("SetLanguage(en): %v", err)
	}
	got, err = s.LanguageFor(ctx, acct.ID)
	if err != nil {
		t.Fatalf("LanguageFor (after switch to en): %v", err)
	}
	if got != LanguageEN {
		t.Fatalf("after switching to en: want %q, got %q", LanguageEN, got)
	}

	// --- Switching back to es ---
	if err := s.SetLanguage(ctx, acct.ID, LanguageES); err != nil {
		t.Fatalf("SetLanguage(es): %v", err)
	}
	got, err = s.LanguageFor(ctx, acct.ID)
	if err != nil {
		t.Fatalf("LanguageFor (after switch back to es): %v", err)
	}
	if got != LanguageES {
		t.Fatalf("after switching back to es: want %q, got %q", LanguageES, got)
	}

	// --- Rejecting an unsupported code: no write occurs ---
	if err := s.SetLanguage(ctx, acct.ID, "fr"); !errors.Is(err, ErrUnsupportedLanguage) {
		t.Fatalf("SetLanguage(fr): want ErrUnsupportedLanguage, got %v", err)
	}
	got, err = s.LanguageFor(ctx, acct.ID)
	if err != nil {
		t.Fatalf("LanguageFor (after rejected write): %v", err)
	}
	if got != LanguageES {
		t.Fatalf("after rejected SetLanguage: want unchanged %q, got %q", LanguageES, got)
	}

	// --- Normalizing a legacy/out-of-band value written outside this module's
	// write path (raw pool.Exec, mirroring the vehicle_config self-heal test
	// technique). RM42 tier 1 moved language from account.accounts into
	// account.settings (design.md D1/D2) — the out-of-band write targets its new
	// home. ---
	if _, err := pool.Exec(ctx,
		"UPDATE account.settings SET language = $1 WHERE account_id = $2", "fr", acct.ID,
	); err != nil {
		t.Fatalf("simulating out-of-band language write: %v", err)
	}
	got, err = s.LanguageFor(ctx, acct.ID)
	if err != nil {
		t.Fatalf("LanguageFor (after out-of-band write): %v", err)
	}
	if got != LanguageES {
		t.Fatalf("out-of-band unsupported value: want normalized %q, got %q", LanguageES, got)
	}
}

// TestAccountSettings_RoundTrip covers Test Contract items 2, 3, 4, and 6 from
// design.md (RM42-account-add-settings-table): fresh-signup defaults, the
// SetTheme round-trip and its rejection of an unsupported code, normalizing
// out-of-band stored values for both preferences at once via PreferencesFor,
// and Inactive-account gating (design.md D10, mirroring RM34's existing
// language/vehicle/token gating).
func TestAccountSettings_RoundTrip(t *testing.T) {
	s, pool := newTestService(t)
	ctx := context.Background()

	acct, err := s.UpsertFromOAuth(ctx, OAuthIdentity{
		Provider:   "google",
		ProviderID: uuid.NewString(),
		Email:      "settings-roundtrip@example.com",
	})
	if err != nil {
		t.Fatalf("provisioning account: %v", err)
	}
	deleteAccount(t, pool, acct.ID)

	// RM34: a freshly-provisioned account defaults to StatusInactive, and every
	// settings read/write below is filtered by status = 'Active' (design.md D10).
	activateAccount(t, pool, acct.ID)

	// --- Fresh-signup defaults (Test Contract item 2): no explicit write yet,
	// the settings row was created atomically by UpsertFromOAuth (design.md D3) ---
	prefs, err := s.PreferencesFor(ctx, acct.ID)
	if err != nil {
		t.Fatalf("PreferencesFor (fresh account): %v", err)
	}

	// RM49 Test Contract items 4-5: a fresh signup's analysis start date is
	// today's calendar day in America/Bogota, stored as UTC midnight of that
	// day. The zone and the truncation are recomputed here instead of calling
	// internal/clock, so a regression in the production helper cannot hide
	// behind the same call. make tz-guard skips _test.go files, which is what
	// makes pinning the zone name here legal.
	bogota, err := time.LoadLocation("America/Bogota")
	if err != nil {
		t.Fatalf("loading America/Bogota: %v", err)
	}
	local := time.Now().In(bogota)
	wantStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)

	want := Settings{Language: LanguageES, Theme: ThemeGraphite, AnalysisStartDate: wantStart}
	if prefs != want {
		t.Fatalf("fresh account settings: want %+v, got %+v", want, prefs)
	}

	// --- SetTheme round-trip ---
	if err := s.SetTheme(ctx, acct.ID, ThemeHalloween); err != nil {
		t.Fatalf("SetTheme(halloween): %v", err)
	}
	gotTheme, err := s.ThemeFor(ctx, acct.ID)
	if err != nil {
		t.Fatalf("ThemeFor (after switch to halloween): %v", err)
	}
	if gotTheme != ThemeHalloween {
		t.Fatalf("after switching to halloween: want %q, got %q", ThemeHalloween, gotTheme)
	}

	// --- SetTheme rejects a value outside the closed set (Test Contract item
	// 3): no write occurs ---
	if err := s.SetTheme(ctx, acct.ID, "cyberpunk"); !errors.Is(err, ErrUnsupportedTheme) {
		t.Fatalf("SetTheme(cyberpunk): want ErrUnsupportedTheme, got %v", err)
	}
	gotTheme, err = s.ThemeFor(ctx, acct.ID)
	if err != nil {
		t.Fatalf("ThemeFor (after rejected write): %v", err)
	}
	if gotTheme != ThemeHalloween {
		t.Fatalf("after rejected SetTheme: want unchanged %q, got %q", ThemeHalloween, gotTheme)
	}

	// --- Normalizing out-of-band stored values for both columns at once
	// (Test Contract item 4), verified via ThemeFor, LanguageFor, AND the
	// combined PreferencesFor in one call ---
	if _, err := pool.Exec(ctx,
		"UPDATE account.settings SET theme = $1, language = $2 WHERE account_id = $3",
		"neon", "fr", acct.ID,
	); err != nil {
		t.Fatalf("simulating out-of-band settings write: %v", err)
	}
	if gotTheme, err = s.ThemeFor(ctx, acct.ID); err != nil {
		t.Fatalf("ThemeFor (after out-of-band write): %v", err)
	} else if gotTheme != ThemeGraphite {
		t.Fatalf("out-of-band unsupported theme: want normalized %q, got %q", ThemeGraphite, gotTheme)
	}
	if gotLang, err := s.LanguageFor(ctx, acct.ID); err != nil {
		t.Fatalf("LanguageFor (after out-of-band write): %v", err)
	} else if gotLang != LanguageES {
		t.Fatalf("out-of-band unsupported language: want normalized %q, got %q", LanguageES, gotLang)
	}
	prefs, err = s.PreferencesFor(ctx, acct.ID)
	if err != nil {
		t.Fatalf("PreferencesFor (after out-of-band write): %v", err)
	}
	want = Settings{Language: LanguageES, Theme: ThemeGraphite, AnalysisStartDate: wantStart}
	if prefs != want {
		t.Fatalf("PreferencesFor after out-of-band write: want %+v, got %+v", want, prefs)
	}

	// --- Inactive-account gating (Test Contract item 6, design.md D10) ---
	if _, err := pool.Exec(ctx,
		"UPDATE account.accounts SET status = 'Inactive' WHERE id = $1", acct.ID,
	); err != nil {
		t.Fatalf("deactivating test account: %v", err)
	}

	if _, err := s.PreferencesFor(ctx, acct.ID); err == nil {
		t.Fatalf("PreferencesFor (inactive account): want error, got nil")
	}
	if _, err := s.LanguageFor(ctx, acct.ID); err == nil {
		t.Fatalf("LanguageFor (inactive account): want error, got nil")
	}
	if _, err := s.ThemeFor(ctx, acct.ID); err == nil {
		t.Fatalf("ThemeFor (inactive account): want error, got nil")
	}

	// SetLanguage/SetTheme against an Inactive account are silent no-ops: no
	// error, but the write matches zero rows (the EXISTS gate), so the stored
	// value is unchanged. Verify by reactivating and reading back.
	if err := s.SetLanguage(ctx, acct.ID, LanguageEN); err != nil {
		t.Fatalf("SetLanguage (inactive account): want no error (silent no-op), got %v", err)
	}
	if err := s.SetTheme(ctx, acct.ID, ThemeApex); err != nil {
		t.Fatalf("SetTheme (inactive account): want no error (silent no-op), got %v", err)
	}

	activateAccount(t, pool, acct.ID)
	prefs, err = s.PreferencesFor(ctx, acct.ID)
	if err != nil {
		t.Fatalf("PreferencesFor (after reactivating): %v", err)
	}
	want = Settings{Language: LanguageES, Theme: ThemeGraphite, AnalysisStartDate: wantStart}
	if prefs != want {
		t.Fatalf("no-op writes against inactive account changed stored values: want unchanged %+v, got %+v", want, prefs)
	}
}

func TestSeedVehicles_IdempotentAndDoesNotOverwrite(t *testing.T) {
	s, pool := newTestService(t)
	ctx := context.Background()

	acct, err := s.UpsertFromOAuth(ctx, OAuthIdentity{
		Provider:   "google",
		ProviderID: uuid.NewString(),
		Email:      "seed-idempotent@example.com",
	})
	if err != nil {
		t.Fatalf("provisioning account: %v", err)
	}
	deleteAccount(t, pool, acct.ID)
	activateAccount(t, pool, acct.ID) // RM34 D14/D15: vehicle reads are gated on account status

	seed := []SeedVehicle{
		{TeslaID: 11, VIN: "VIN11", DisplayName: "Original"},
		{TeslaID: 22, VIN: "VIN22", DisplayName: "Original Two"},
	}
	if _, err := s.SeedVehicles(ctx, acct.ID, seed); err != nil {
		t.Fatalf("first seed: %v", err)
	}

	// Re-seed with a changed display name for an existing vehicle and a new one.
	reseed := []SeedVehicle{
		{TeslaID: 11, VIN: "VIN11", DisplayName: "CHANGED"},   // existing: must NOT overwrite
		{TeslaID: 33, VIN: "VIN33", DisplayName: "New Three"}, // new: inserted
	}
	got, err := s.SeedVehicles(ctx, acct.ID, reseed)
	if err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 registered vehicles after re-seed, got %d (%+v)", len(got), got)
	}

	byID := map[int64]Vehicle{}
	for _, v := range got {
		byID[v.TeslaID] = v
	}
	if byID[11].DisplayName != "Original" {
		t.Errorf("existing vehicle 11 display_name overwritten: want %q, got %q", "Original", byID[11].DisplayName)
	}
	if byID[22].DisplayName != "Original Two" {
		t.Errorf("existing vehicle 22 display_name changed: want %q, got %q", "Original Two", byID[22].DisplayName)
	}
	if byID[33].VIN != "VIN33" || byID[33].DisplayName != "New Three" {
		t.Errorf("new vehicle 33 not inserted correctly: %+v", byID[33])
	}

	// No duplicate rows for the re-seeded tesla_id.
	var count int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM account.vehicles WHERE account_id = $1 AND tesla_id = $2", acct.ID, int64(11)).Scan(&count); err != nil {
		t.Fatalf("counting vehicles: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one row for tesla_id=11, got %d", count)
	}
}
