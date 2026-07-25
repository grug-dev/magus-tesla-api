// Package auth handles the Tesla OAuth 2.0 authorization flow.
// Covers Step 6a of post-registration-setup.md — build the auth URL and open the browser.
package auth

import (
	"fmt"
	"net/url"
	"os/exec"
)

const (
	teslaAuthURL = "https://auth.tesla.com/oauth2/v3/authorize"
	// scopes requested at consent (space-delimited set; order is irrelevant).
	// vehicle_device_data backs the nightly vehicle_data snapshots;
	// vehicle_charging_cmds unlocks the charging billing/history endpoint
	// (GET /api/1/dx/charging/history) — Tesla returns 403 "missing scopes" without
	// it; energy_device_data unlocks the account's energy products in GET /api/1/products
	// and the energy-site endpoints (e.g. GET /api/1/energy_sites/{id}/telemetry_history
	// ?kind=charge — Tesla Wall Connector charging history), also 403 without it;
	// user_data unlocks the user-account endpoints under /api/1/users/* — notably
	// GET /api/1/users/me (authenticated user's account summary), 403 without it;
	// offline_access mints the single-use refresh token. Changing this list
	// requires re-running the OAuth consent (cmd/setup) to mint a token that carries
	// the new scope; existing tokens keep whatever scopes they were granted. NOTE:
	// each requested scope must also be enabled for the app in the Tesla developer
	// portal, or consent rejects it.
	//
	// prompt_missing_scopes=true forces Tesla to prompt the user for any requested
	// scope they have not already granted, instead of silently dropping it and
	// minting a scope-deficient token. Without it, adding a scope to this list has
	// no effect on returning users — Tesla re-issues a token with only the
	// previously-granted scopes.
	scopes = "openid user_data vehicle_device_data vehicle_cmds vehicle_specs vehicle_pricing_info vehicle_charging_cmds energy_device_data offline_access"
)

// BuildAuthURL constructs the Tesla OAuth authorization URL.
// Returns the full URL and the state value used to verify the callback.
func BuildAuthURL(clientID, redirectURI, state string) string {
	params := url.Values{}
	params.Set("client_id", clientID)
	params.Set("locale", "en-US")
	params.Set("prompt", "login")
	params.Set("prompt_missing_scopes", "true")
	params.Set("redirect_uri", redirectURI)
	params.Set("response_type", "code")
	params.Set("scope", scopes)
	params.Set("state", state)

	fmt.Printf("Authorization URL: %s?%s\n", teslaAuthURL, params.Encode())

	return fmt.Sprintf("%s?%s", teslaAuthURL, params.Encode())
}

// OpenBrowser opens the given URL in the default macOS browser.
func OpenBrowser(authURL string) error {
	return exec.Command("open", authURL).Start()
}
