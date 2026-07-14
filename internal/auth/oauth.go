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
	// it; offline_access mints the single-use refresh token. Changing this list
	// requires re-running the OAuth consent (cmd/setup) to mint a token that carries
	// the new scope; existing tokens keep whatever scopes they were granted.
	scopes = "openid vehicle_device_data vehicle_charging_cmds offline_access"
)

// BuildAuthURL constructs the Tesla OAuth authorization URL.
// Returns the full URL and the state value used to verify the callback.
func BuildAuthURL(clientID, redirectURI, state string) string {
	params := url.Values{}
	params.Set("client_id", clientID)
	params.Set("locale", "en-US")
	params.Set("prompt", "login")
	params.Set("redirect_uri", redirectURI)
	params.Set("response_type", "code")
	params.Set("scope", scopes)
	params.Set("state", state)

	return fmt.Sprintf("%s?%s", teslaAuthURL, params.Encode())
}

// OpenBrowser opens the given URL in the default macOS browser.
func OpenBrowser(authURL string) error {
	return exec.Command("open", authURL).Start()
}
