// Covers Step 6c of post-registration-setup.md — exchange the authorization code for tokens.
package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// -const teslaTokenURL = "https://auth.tesla.com/oauth2/v3/token"
const teslaTokenURL = "https://fleet-auth.prd.vn.cloud.tesla.com/oauth2/v3/token"

// fleetAudience is the regional Fleet API base URL Tesla requires as the
// audience of an authorization_code exchange. NA region — must match the
// region of the tesla adapter (internal/tesla/client.go baseURL).
const fleetAudience = "https://fleet-api.prd.na.vn.cloud.tesla.com"

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// ExchangeCode trades the authorization code from Step 6b for an access token and refresh token.
func ExchangeCode(clientID, clientSecret, code, redirectURI string) (*TokenResponse, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("client_id", clientID)
	data.Set("client_secret", clientSecret)
	data.Set("code", code)
	data.Set("audience", fleetAudience)
	data.Set("redirect_uri", redirectURI)

	resp, err := http.Post(teslaTokenURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("step 6c: token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("step 6c: token exchange returned status %d", resp.StatusCode)
	}

	var tokens TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
		return nil, fmt.Errorf("step 6c: failed to decode token response: %w", err)
	}

	return &tokens, nil
}

// RefreshTokens uses a saved refresh token to obtain a new access token.
// The refresh token is single-use — always save the new one returned in the response.
//
// clientSecret is unused: Tesla's refresh_token grant takes no client secret. The
// param is kept for signature parity with ExchangeCode and account.refreshFunc.
func RefreshTokens(clientID, clientSecret, refreshToken string) (*TokenResponse, error) {
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("client_id", clientID)
	data.Set("refresh_token", refreshToken)

	resp, err := http.Post(teslaTokenURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("token refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh returned status %d", resp.StatusCode)
	}

	var tokens TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
		return nil, fmt.Errorf("failed to decode refresh token response: %w", err)
	}

	return &tokens, nil
}
