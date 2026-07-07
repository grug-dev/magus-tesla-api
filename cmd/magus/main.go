// Command magus lists every vehicle assigned to a Tesla account.
//
// Tokens may be supplied as flags (--access-token / --refresh-token) so the
// command works for ANY user — the multi-tenant direction where the account
// module will later supply each user's tokens from the database. If no
// --access-token is given, tokens fall back to .env (the single-user smoke-test
// path, kept until the account module lands).
//
// On HTTP 401 the access token is refreshed once (using the refresh token plus
// the app credentials from .env) and the call is retried. NOTE: this refresh
// orchestration is temporary and will move into internal/account/ — see
// ai/architecture.md §5.
//
// Usage:
//
//	go run ./cmd/magus                                      # tokens from .env
//	go run ./cmd/magus --access-token=AT --refresh-token=RT # tokens from flags
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"

	"github.com/cristianpena/magus-tesla-api/internal/auth"
	"github.com/cristianpena/magus-tesla-api/internal/config"
	"github.com/cristianpena/magus-tesla-api/internal/tesla"
)

func main() {
	accessTokenFlag := flag.String("access-token", "", "Tesla access token; if empty, falls back to .env (TESLA_ACCESS_TOKEN)")
	refreshTokenFlag := flag.String("refresh-token", "", "Tesla refresh token for auto-refresh on 401; if empty, falls back to .env (TESLA_REFRESH_TOKEN)")
	flag.Parse()

	// config supplies the app credentials (client id/secret) needed to refresh,
	// and the .env token fallback used when no token flags are given. Best-effort:
	// running purely with token flags doesn't strictly need it, but refresh does.
	cfg, cfgErr := config.Load()

	accessToken := *accessTokenFlag
	refreshToken := *refreshTokenFlag
	fromEnv := false
	if accessToken == "" {
		// No token flags — fall back to .env (temporary single-user path until the
		// account module supplies per-user tokens from the database).
		if cfgErr != nil {
			log.Fatalf("no --access-token given and could not read .env: %v", cfgErr)
		}
		accessToken = cfg.AccessToken
		refreshToken = cfg.RefreshToken
		fromEnv = true
	}
	if accessToken == "" {
		log.Fatal("no access token: pass --access-token or set TESLA_ACCESS_TOKEN in .env")
	}

	ctx := context.Background()
	client := tesla.NewClient()
	creds := tesla.Credentials{AccessToken: accessToken}

	fmt.Println("Fetching vehicles for the account...")
	vehicles, err := client.ListVehicles(ctx, creds)
	if errors.Is(err, tesla.ErrUnauthorized) {
		// Access token expired — refresh once and retry.
		creds, err = refreshAndRetry(cfg, refreshToken, fromEnv)
		if err != nil {
			log.Fatalf("%v", err)
		}
		vehicles, err = client.ListVehicles(ctx, creds)
	}
	if err != nil {
		log.Fatalf("could not list vehicles: %v", err)
	}

	printVehicles(vehicles)
}

// refreshAndRetry exchanges the refresh token for a new access token using the
// app credentials from .env, persists (or surfaces) the rotated pair, and returns
// fresh Credentials to retry with.
//
// TEMPORARY: this belongs in internal/account/, which will own per-user tokens in
// the database (see ai/architecture.md §5).
func refreshAndRetry(cfg *config.Config, refreshToken string, fromEnv bool) (tesla.Credentials, error) {
	if cfg == nil {
		return tesla.Credentials{}, errors.New("cannot refresh: app credentials unavailable (need .env with TESLA_CLIENT_ID and TESLA_CLIENT_SECRET)")
	}
	if refreshToken == "" {
		return tesla.Credentials{}, errors.New("access token expired and no refresh token available (pass --refresh-token or set TESLA_REFRESH_TOKEN in .env)")
	}

	fmt.Println("[token] access token expired — refreshing...")
	tokens, err := auth.RefreshTokens(cfg.ClientID, cfg.ClientSecret, refreshToken)
	if err != nil {
		return tesla.Credentials{}, fmt.Errorf("refreshing tokens: %w", err)
	}

	if fromEnv {
		// Tokens came from .env — persist the rotated pair back to .env.
		if err := config.SaveTokens(tokens.AccessToken, tokens.RefreshToken); err != nil {
			return tesla.Credentials{}, fmt.Errorf("saving refreshed tokens to .env: %w", err)
		}
		fmt.Println("[token] new tokens saved to .env.")
	} else {
		// Per-user tokens from flags: the refresh token is single-use, so surface
		// the rotated pair for the caller to store (the account module will persist
		// these to the database later).
		fmt.Println("[token] refreshed — save these; the refresh token is single-use:")
		fmt.Printf("  --access-token=%s\n", tokens.AccessToken)
		fmt.Printf("  --refresh-token=%s\n", tokens.RefreshToken)
	}

	return tesla.Credentials{AccessToken: tokens.AccessToken}, nil
}

// printVehicles writes every vehicle on the account to stdout.
func printVehicles(vehicles []tesla.VehicleTesla) {
	if len(vehicles) == 0 {
		fmt.Println("No vehicles found on this account.")
		return
	}

	fmt.Printf("\n=== %d vehicle(s) on this account ===\n\n", len(vehicles))
	for i, v := range vehicles {
		fmt.Printf("%d. %s\n", i+1, v.DisplayName)
		fmt.Printf("   VIN   : %s\n", v.VIN)
		fmt.Printf("   State : %s\n", v.State)
		fmt.Printf("   ID    : %d\n\n", v.ID)
	}
}
