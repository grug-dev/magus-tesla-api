package account

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	accountdb "github.com/cristianpena/magus-tesla-api/internal/account/db"
	"github.com/cristianpena/magus-tesla-api/internal/auth"
)

// refreshMargin refreshes a Tesla access token this long before it actually
// expires, so a token handed out never dies mid-request.
const refreshMargin = 60 * time.Second

// refreshFunc matches auth.RefreshTokens. It is a field on the service so the
// refresh-on-expiry path can be exercised in tests without a real network call.
type refreshFunc func(clientID, clientSecret, refreshToken string) (*auth.TokenResponse, error)

// service is the concrete account.Service backed by Postgres (via accountdb) and
// the Tesla OAuth token endpoint (via auth.RefreshTokens).
type service struct {
	pool         *pgxpool.Pool
	q            *accountdb.Queries
	clientID     string
	clientSecret string
	refresh      refreshFunc
	now          func() time.Time
}

// NewService wires the account service over a pgx pool. clientID/clientSecret are
// the Tesla app credentials used to refresh expired access tokens. The caller
// (e.g. the future cmd/web) owns creating the pool via pgxpool.New(ctx, dsn).
func NewService(pool *pgxpool.Pool, clientID, clientSecret string) Service {
	return &service{
		pool:         pool,
		q:            accountdb.New(pool),
		clientID:     clientID,
		clientSecret: clientSecret,
		refresh:      auth.RefreshTokens,
		now:          time.Now,
	}
}

var _ Service = (*service)(nil)

func (s *service) UpsertFromOAuth(ctx context.Context, id OAuthIdentity) (Account, error) {
	row, err := s.q.UpsertAccountFromOAuth(ctx, accountdb.UpsertAccountFromOAuthParams{
		Email:       id.Email,
		Provider:    id.Provider,
		ProviderID:  id.ProviderID,
		DisplayName: textFromString(id.DisplayName),
	})
	if err != nil {
		return Account{}, fmt.Errorf("upserting account from oauth: %w", err)
	}
	return accountFromRow(row), nil
}

func (s *service) SaveTeslaTokens(ctx context.Context, accountID uuid.UUID, t TeslaTokens) error {
	_, err := s.q.UpsertTeslaToken(ctx, accountdb.UpsertTeslaTokenParams{
		AccountID:       accountID,
		TeslaEmail:      textFromString(t.TeslaEmail),
		AccessToken:     t.AccessToken,
		RefreshToken:    t.RefreshToken,
		AccessExpiresAt: timestamp(t.AccessExpiresAt),
	})
	if err != nil {
		return fmt.Errorf("saving tesla tokens: %w", err)
	}
	return nil
}

func (s *service) AccessTokenFor(ctx context.Context, accountID uuid.UUID) (string, error) {
	// The read and the rotating write share one transaction. The row is locked
	// FOR UPDATE so concurrent refreshes of the same connection serialize, and the
	// rotated (single-use) refresh token is persisted in the same tx as the read —
	// a crash can't leave a consumed token unsaved.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("beginning tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once committed

	qtx := s.q.WithTx(tx)

	tok, err := qtx.GetLatestTeslaTokenByAccountForUpdate(ctx, accountID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNoTeslaConnection
		}
		return "", fmt.Errorf("loading tesla token: %w", err)
	}

	// Still valid: return it as-is.
	if !needsRefresh(tok.AccessExpiresAt.Time, s.now(), refreshMargin) {
		if err := tx.Commit(ctx); err != nil {
			return "", fmt.Errorf("committing tx: %w", err)
		}
		return tok.AccessToken, nil
	}

	// Expired (or within the safety margin): rotate and persist.
	refreshed, err := s.refresh(s.clientID, s.clientSecret, tok.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("refreshing tesla token: %w", err)
	}

	updated, err := qtx.UpdateTeslaToken(ctx, accountdb.UpdateTeslaTokenParams{
		AccessToken:     refreshed.AccessToken,
		RefreshToken:    refreshed.RefreshToken,
		AccessExpiresAt: timestamp(accessExpiry(s.now(), refreshed.ExpiresIn)),
		ID:              tok.ID,
	})
	if err != nil {
		return "", fmt.Errorf("persisting rotated tesla token: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("committing tx: %w", err)
	}
	return updated.AccessToken, nil
}

func (s *service) RegisteredVehicles(ctx context.Context, accountID uuid.UUID) ([]Vehicle, error) {
	rows, err := s.q.ListVehiclesByAccount(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("listing registered vehicles: %w", err)
	}
	out := make([]Vehicle, 0, len(rows))
	for _, r := range rows {
		out = append(out, vehicleFromRow(r))
	}
	return out, nil
}

func (s *service) AllRegisteredVehicles(ctx context.Context) ([]OwnedVehicle, error) {
	rows, err := s.q.ListAllVehicles(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing all registered vehicles: %w", err)
	}
	// Non-nil empty slice when there are no rows: callers get a length-0 slice,
	// never nil, and never an error.
	out := make([]OwnedVehicle, 0, len(rows))
	for _, r := range rows {
		out = append(out, ownedVehicleFromRow(r))
	}
	return out, nil
}

func (s *service) SeedVehicles(ctx context.Context, accountID uuid.UUID, vehicles []SeedVehicle) ([]Vehicle, error) {
	for _, v := range vehicles {
		// ON CONFLICT DO NOTHING: existing vehicles are skipped, their stored
		// attributes are not overwritten ("the rest of the information should not
		// change"). InsertVehicleIfMissing is :exec (no RETURNING) — on a
		// conflict it simply does nothing; we re-read the full registered set
		// via RegisteredVehicles below so the returned slice reflects whatever
		// persisted state already had (e.g. an unchanged display_name).
		if err := s.q.InsertVehicleIfMissing(ctx, accountdb.InsertVehicleIfMissingParams{
			AccountID:   accountID,
			TeslaID:     v.TeslaID,
			Vin:         v.VIN,
			DisplayName: textFromString(v.DisplayName),
			AccessType:  textPtrToNullable(v.AccessType),
		}); err != nil {
			return nil, fmt.Errorf("registering vehicle tesla_id=%d: %w", v.TeslaID, err)
		}
	}
	return s.RegisteredVehicles(ctx, accountID)
}

func (s *service) SetVehicleConfigIfEmpty(ctx context.Context, accountID uuid.UUID, teslaID int64, exteriorColor, carType string) error {
	if err := s.q.UpdateVehicleConfigIfEmpty(ctx, accountdb.UpdateVehicleConfigIfEmptyParams{
		ExteriorColor: textFromString(exteriorColor),
		CarType:       textFromString(carType),
		AccountID:     accountID,
		TeslaID:       teslaID,
	}); err != nil {
		return fmt.Errorf("setting vehicle config for tesla_id=%d: %w", teslaID, err)
	}
	return nil
}

func (s *service) LanguageFor(ctx context.Context, accountID uuid.UUID) (string, error) {
	lang, err := s.q.GetAccountLanguage(ctx, accountID)
	if err != nil {
		return "", fmt.Errorf("loading account language: %w", err)
	}
	return normalizeLanguage(lang), nil
}

func (s *service) SetLanguage(ctx context.Context, accountID uuid.UUID, lang string) error {
	if !isSupportedLanguage(lang) {
		return ErrUnsupportedLanguage
	}
	if err := s.q.UpdateAccountLanguage(ctx, accountdb.UpdateAccountLanguageParams{
		ID:       accountID,
		Language: lang,
	}); err != nil {
		return fmt.Errorf("setting account language: %w", err)
	}
	return nil
}

// --- pure helpers (unit-tested without a database) ---

// needsRefresh reports whether a token expiring at expiresAt should be refreshed
// now, given a safety margin before actual expiry.
func needsRefresh(expiresAt, now time.Time, margin time.Duration) bool {
	return !now.Add(margin).Before(expiresAt)
}

// accessExpiry converts an OAuth expires_in (seconds from now) into an absolute
// expiry timestamp.
func accessExpiry(now time.Time, expiresIn int) time.Time {
	return now.Add(time.Duration(expiresIn) * time.Second)
}

// normalizeLanguage returns lang unchanged if it is a supported code, else the
// default LanguageES. This is the DB→domain normalization boundary (design.md D4).
func normalizeLanguage(lang string) string {
	if isSupportedLanguage(lang) {
		return lang
	}
	return LanguageES
}

func isSupportedLanguage(lang string) bool {
	return lang == LanguageES || lang == LanguageEN
}

// --- row → domain mapping ---

func vehicleFromRow(v accountdb.Vehicle) Vehicle {
	return Vehicle{
		TeslaID:       v.TeslaID,
		VIN:           v.Vin,
		DisplayName:   v.DisplayName.String, // "" when NULL
		AccessType:    nullableTextToPtr(v.AccessType),
		ExteriorColor: nullableTextToPtr(v.ExteriorColor),
		CarType:       nullableTextToPtr(v.CarType),
	}
}

// ownedVehicleFromRow maps an all-accounts ListAllVehicles row to the cross-account
// OwnedVehicle domain type, tagging it with its owning account id. account_id is
// already uuid.UUID via the sqlc override; pgtype.Text.String is "" when NULL —
// pgtype never leaves the module.
func ownedVehicleFromRow(v accountdb.ListAllVehiclesRow) OwnedVehicle {
	return OwnedVehicle{
		AccountID:     v.AccountID,
		TeslaID:       v.TeslaID,
		VIN:           v.Vin,
		DisplayName:   v.DisplayName.String, // "" when NULL
		AccessType:    nullableTextToPtr(v.AccessType),
		ExteriorColor: nullableTextToPtr(v.ExteriorColor),
		CarType:       nullableTextToPtr(v.CarType),
	}
}

func accountFromRow(a accountdb.Account) Account {
	return Account{
		ID:          a.ID,
		Email:       a.Email,
		Provider:    a.Provider,
		ProviderID:  a.ProviderID,
		DisplayName: a.DisplayName.String, // "" when NULL
		Status:      a.Status,
		CreatedAt:   a.CreatedAt.Time,
		UpdatedAt:   a.UpdatedAt.Time,
	}
}

// textFromString maps a Go string to a nullable pgtype.Text, treating "" as NULL.
func textFromString(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

// nullableTextToPtr maps a nullable pgtype.Text to *string; nil when not valid.
// Used at the DB→domain boundary so pgtype never leaks into public types (design.md D4).
func nullableTextToPtr(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	s := t.String
	return &s
}

// textPtrToNullable maps a *string to pgtype.Text; invalid (NULL) when nil.
// Used at the domain→DB boundary so pgtype never leaks into public types (design.md D4).
func textPtrToNullable(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

// timestamp maps a Go time.Time to a non-null pgtype.Timestamptz. sqlc's pgx/v5
// output uses pgtype.Timestamptz for our (NOT NULL) timestamptz columns; the domain
// types stay on plain time.Time, so we convert here at the DB boundary.
func timestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
