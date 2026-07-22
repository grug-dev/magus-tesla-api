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

// Vehicle is one registered Tesla vehicle for an account, as exposed through the
// account module's public interface. It carries only the durable identity +
// display fields — the volatile `state` returned by the Tesla API is
// intentionally NOT stored or surfaced (see openspec/changes/persist-tesla-vehicles).
type Vehicle struct {
	TeslaID     int64
	VIN         string
	DisplayName string
	// AccessType is Tesla's per-vehicle access type for the account, either "OWNER"
	// or "DRIVER". nil means the value was not captured at seed time (predates this
	// change or the caller supplied nil).
	AccessType *string
}

// OwnedVehicle is one registered Tesla vehicle in the CROSS-ACCOUNT view: unlike
// Vehicle (the gateway's per-account view, which omits the account id because the
// caller already knows it), OwnedVehicle carries its owning AccountID so a
// background collection job can enumerate every vehicle across ALL accounts and,
// per vehicle, resolve a token via AccessTokenFor(AccountID). It is our own domain
// model — no vendor suffix — and, like Vehicle, carries only durable identity +
// display fields, never the volatile Tesla `state`.
type OwnedVehicle struct {
	AccountID   uuid.UUID
	TeslaID     int64
	VIN         string
	DisplayName string
	// AccessType is Tesla's per-vehicle access type for the account, either "OWNER"
	// or "DRIVER". nil means the value was not captured at seed time (predates this
	// change or the caller supplied nil).
	AccessType *string
}

// SeedVehicle is the mapped slice the gateway hands the account module when
// registering vehicles from a tesla.ListVehicles result. It is a small,
// tesla-free DTO so this module does not import the adapter.
type SeedVehicle struct {
	TeslaID     int64
	VIN         string
	DisplayName string
	// AccessType is Tesla's per-vehicle access type. The gateway (tier 4) sets this
	// from VehicleTesla.AccessType before calling SeedVehicles. Callers that do not
	// supply it leave it nil, which is stored as NULL.
	AccessType *string
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

	// RegisteredVehicles returns the vehicles currently registered to the account.
	// The result is empty (not an error) when the account has no vehicles
	// registered yet. The gateway uses this to decide whether a one-time Tesla
	// ListVehicles call is needed to seed the registry.
	RegisteredVehicles(ctx context.Context, accountID uuid.UUID) ([]Vehicle, error)

	// AllRegisteredVehicles returns every registered vehicle across ALL accounts,
	// each tagged with its owning account id (see OwnedVehicle). The result is
	// empty (not an error) when no vehicle is registered under any account. It is
	// for server-side background collection jobs (e.g. the nightly telemetry
	// collector) that must list every vehicle across every account without reading
	// this module's tables.
	//
	// It does NOT check Tesla-connection liveness: vehicles are returned regardless
	// of whether their owning account currently has a usable token. Deciding
	// whether a token is available — and handling expired or revoked ones — is the
	// caller's job, done per account via AccessTokenFor (which returns
	// ErrNoTeslaConnection when there is none).
	AllRegisteredVehicles(ctx context.Context) ([]OwnedVehicle, error)

	// SeedVehicles registers the given vehicles for the account, idempotently per
	// (account_id, tesla_id): a vehicle already registered is left untouched (its
	// stored display_name is NOT overwritten), and only truly new vehicles are
	// inserted. It returns the account's full registered set after the seed.
	SeedVehicles(ctx context.Context, accountID uuid.UUID, vehicles []SeedVehicle) ([]Vehicle, error)
}
