package account

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	accountdb "github.com/cristianpena/magus-tesla-api/internal/account/db"
	"github.com/cristianpena/magus-tesla-api/internal/auth"
)

// newTestService builds a white-box service against a real Postgres from
// DATABASE_URL, with a fake refresh so no network call is made. The test is
// skipped when DATABASE_URL is unset. Requires the goose migration to be applied
// (`goose ... up`).
func newTestService(t *testing.T) (*service, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping account integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
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
		_, _ = pool.Exec(context.Background(), "DELETE FROM accounts WHERE id = $1", id)
	})
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
		"SELECT count(*) FROM tesla_tokens WHERE account_id = $1", acct.ID).Scan(&count); err != nil {
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

	acctB, err := s.UpsertFromOAuth(ctx, OAuthIdentity{
		Provider:   "google",
		ProviderID: uuid.NewString(),
		Email:      "all-multi-b@example.com",
	})
	if err != nil {
		t.Fatalf("provisioning account B: %v", err)
	}
	deleteAccount(t, pool, acctB.ID)

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

	seed := []SeedVehicle{
		{TeslaID: 11, VIN: "VIN11", DisplayName: "Original"},
		{TeslaID: 22, VIN: "VIN22", DisplayName: "Original Two"},
	}
	if _, err := s.SeedVehicles(ctx, acct.ID, seed); err != nil {
		t.Fatalf("first seed: %v", err)
	}

	// Re-seed with a changed display name for an existing vehicle and a new one.
	reseed := []SeedVehicle{
		{TeslaID: 11, VIN: "VIN11", DisplayName: "CHANGED"},            // existing: must NOT overwrite
		{TeslaID: 33, VIN: "VIN33", DisplayName: "New Three"},          // new: inserted
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
		"SELECT count(*) FROM vehicles WHERE account_id = $1 AND tesla_id = $2", acct.ID, int64(11)).Scan(&count); err != nil {
		t.Fatalf("counting vehicles: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one row for tesla_id=11, got %d", count)
	}
}
