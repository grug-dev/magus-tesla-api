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
	scopes       = "openid vehicle_device_data offline_access"
)

// BuildAuthURL constructs the Tesla OAuth authorization URL.
// Returns the full URL and the state value used to verify the callback.
func BuildAuthURL(clientID, redirectURI string) string {
	params := url.Values{}
	params.Set("client_id", clientID)
	params.Set("locale", "en-US")
	params.Set("prompt", "login")
	params.Set("redirect_uri", redirectURI)
	params.Set("response_type", "code")
	params.Set("scope", scopes)
	params.Set("state", "magus123")

	return fmt.Sprintf("%s?%s", teslaAuthURL, params.Encode())
}

// OpenBrowser opens the given URL in the default macOS browser.
func OpenBrowser(authURL string) error {
	return exec.Command("open", authURL).Start()
}
