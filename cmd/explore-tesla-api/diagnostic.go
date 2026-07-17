// diagnostic.go re-issues the two Tesla calls that fail in listVehiclesWithAutoRefresh
// (GET /api/1/vehicles → 401, POST /oauth2/v3/token → 400) via raw net/http so the
// actual Tesla response body is captured and logged.
//
// The shared helpers (tesla.Client.do, auth.RefreshTokens) discard the response body
// on non-200, which hides Tesla's {"error":"..."} reason. These helpers exist ONLY to
// surface that body for the explore flow — they touch nothing the web server uses and
// are not on any hot path. Each is a single read-only HTTP call.
package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	diagTokenURL  = "https://fleet-auth.prd.vn.cloud.tesla.com/oauth2/v3/token"
	diagFleetBase = "https://fleet-api.prd.na.vn.cloud.tesla.com"
)

// dumpRefreshBody re-issues the refresh_token grant that auth.RefreshTokens just failed
// and returns Tesla's status + body. auth.RefreshTokens discards the body, so this
// duplicate call is the only way to see whether Tesla said invalid_grant (consumed/
// expired) vs invalid_client vs something else. Safe to re-issue: a 400 means Tesla
// already rejected the refresh, so re-issuing cannot consume it further.
//
// The request body mirrors auth.RefreshTokens exactly: grant_type, client_id,
// refresh_token — no client_secret (Tesla's refresh_token grant takes none).
func dumpRefreshBody(clientID, refreshToken string) string {
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("client_id", clientID)
	data.Set("refresh_token", refreshToken)

	resp, err := http.Post(diagTokenURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Sprintf("(refresh diagnostic request failed: %v)", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Sprintf("status %d, body: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

// dumpListBody re-issues GET /api/1/vehicles with the access token that just 401'd and
// returns Tesla's status + body. The shared tesla.Client.do returns ErrUnauthorized
// without the body, so this call is the only way to see why a freshly-minted access
// token is rejected. Read-only and idempotent — safe to re-issue.
func dumpListBody(accessToken string) string {
	req, err := http.NewRequest(http.MethodGet, diagFleetBase+"/api/1/vehicles", nil)
	if err != nil {
		return fmt.Sprintf("(list diagnostic request build failed: %v)", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("(list diagnostic request failed: %v)", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Sprintf("status %d, body: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}
