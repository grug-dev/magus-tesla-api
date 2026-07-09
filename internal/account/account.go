// Package account is the multi-tenant identity boundary (ai/architecture.md §5).
// It owns the app's users — provisioned from a social OAuth identity — and each
// user's Tesla connection tokens, which it persists and refreshes. Other modules
// obtain a user's current Tesla access token through the Service interface below;
// they never read this module's tables (the module-scoped accountdb package is
// unexported to them by convention).
//
// Domain types here carry NO vendor suffix — they are our own models, distinct
// from the ...Tesla DTOs the tesla adapter unmarshals (ai/architecture.md §6).
package account

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNoTeslaConnection is returned when an account has no stored Tesla connection
// to draw an access token from. Detect it with errors.Is.
var ErrNoTeslaConnection = errors.New("account: no tesla connection for account")

// Account is an app user. It is provisioned from a social OAuth identity and holds
// no password — authentication is delegated to the provider.
type Account struct {
	ID          uuid.UUID
	Email       string
	Provider    string // e.g. "google"
	ProviderID  string // the provider's subject id
	DisplayName string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// OAuthIdentity is the verified identity a social provider returns after login.
// (Provider, ProviderID) uniquely identifies an account.
type OAuthIdentity struct {
	Provider    string
	ProviderID  string
	Email       string
	DisplayName string
}

// TeslaTokens is one Tesla connection's credentials as stored for an account. An
// account may have more than one (a user connecting several Tesla accounts).
type TeslaTokens struct {
	TeslaEmail      string // which Tesla account this connection is for; optional
	AccessToken     string
	RefreshToken    string
	AccessExpiresAt time.Time
}

// Service is the account module's public port. The gateway and sibling modules
// depend on this interface, never on the concrete implementation or the DB.
type Service interface {
	// UpsertFromOAuth creates the account for a social identity, or resolves the
	// existing one. It is idempotent per (provider, provider_id).
	UpsertFromOAuth(ctx context.Context, id OAuthIdentity) (Account, error)

	// SaveTeslaTokens stores a new Tesla connection for the account.
	SaveTeslaTokens(ctx context.Context, accountID uuid.UUID, t TeslaTokens) error

	// AccessTokenFor returns a currently-valid Tesla access token for the account,
	// refreshing and persisting the rotated pair first if the stored token has
	// expired. Returns ErrNoTeslaConnection if the account has no connection.
	//
	// A plain token (not tesla.Credentials) is returned so this module need not
	// import tesla; the caller wraps it in tesla.Credentials before calling the
	// adapter.
	AccessTokenFor(ctx context.Context, accountID uuid.UUID) (string, error)
}
