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
