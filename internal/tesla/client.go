// Package tesla is an anti-corruption adapter over the external Tesla Fleet API.
//
// It is stateless about identity: every call receives the Credentials to use, so
// the same adapter serves ANY user (the multi-tenant model in ai/architecture.md
// §5). It never reads global config or another module's token store. Structs that
// mirror Tesla's JSON carry the ...Tesla suffix so vendor-shaped data is never
// mistaken for a domain model (ai/architecture.md §6).
package tesla

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const baseURL = "https://fleet-api.prd.na.vn.cloud.tesla.com"

// ErrUnauthorized is returned when the Fleet API responds with HTTP 401, meaning
// the supplied access token is expired or invalid. Detect it with errors.Is. In
// the target architecture the account module owns refresh and would obtain a
// fresh token and retry.
var ErrUnauthorized = errors.New("tesla: access token expired or invalid (HTTP 401)")

// ErrForbidden is returned when the Fleet API responds with HTTP 403, most
// commonly because the access token lacks a required scope ("missing scopes").
// Detect it with errors.Is; the wrapped error carries Tesla's response body
// with the specific reason.
var ErrForbidden = errors.New("tesla: forbidden — token missing required scopes (HTTP 403)")

// Credentials carries the per-user secret needed to call the Tesla Fleet API. The
// caller supplies it on every call — the adapter holds no identity of its own.
type Credentials struct {
	AccessToken string
}

// VehicleService is the public contract of this adapter (the module's "port").
// Callers depend on this interface, not on the concrete *Client.
type VehicleService interface {
	ListVehicles(ctx context.Context, creds Credentials) ([]VehicleTesla, error)
	// VehicleData returns both the typed snapshot and the lossless raw payload
	// (the inner vehicle_data object) from a single Fleet API request, so a caller
	// can store the raw bytes and read typed fields without a second paid call.
	VehicleData(ctx context.Context, creds Credentials, vehicleID int64) (*VehicleDataTesla, json.RawMessage, error)
	WakeUp(ctx context.Context, creds Credentials, vehicleID int64) (*VehicleTesla, error)
	// ChargingHistory returns the account's Tesla-billed charging session history
	// (Supercharger + DC fast-charging only). It is account-scoped — no vehicle id
	// is required — and server-side: the vehicle does not need to be awake.
	//
	// Pass ChargingHistoryParams to filter by date range or control pagination.
	// The zero value fetches all sessions from the beginning of account history.
	// The method iterates pages automatically and returns the merged result, so
	// callers receive the complete history in one Go call.
	//
	// Required scope: vehicle_charging_cmds.
	ChargingHistory(ctx context.Context, creds Credentials, params ChargingHistoryParams) (*ChargingHistoryTesla, error)
}

// Client is the HTTP implementation of VehicleService. Because credentials arrive
// per call, a single Client is safe to share across users.
type Client struct {
	http *http.Client
	// baseURL is the Fleet API root. It defaults to the production endpoint and is
	// unexported so only same-package tests can point the client at an httptest
	// server — the public API stays unchanged and no test ever hits Tesla.
	baseURL string
}

// NewClient returns a ready-to-use Tesla adapter.
func NewClient() *Client {
	return &Client{http: &http.Client{}, baseURL: baseURL}
}

// Compile-time check that *Client satisfies the public contract.
var _ VehicleService = (*Client)(nil)

func (c *Client) get(ctx context.Context, creds Credentials, path string, out any) error {
	return c.do(ctx, http.MethodGet, creds, path, out)
}

func (c *Client) post(ctx context.Context, creds Credentials, path string, out any) error {
	return c.do(ctx, http.MethodPost, creds, path, out)
}

// do performs an authenticated Fleet API request and decodes the JSON response.
func (c *Client) do(ctx context.Context, method string, creds Credentials, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%s: %w", path, ErrUnauthorized)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		hint := strings.TrimSpace(string(body))
		if resp.StatusCode == http.StatusForbidden {
			if hint != "" {
				return fmt.Errorf("%s: %w: %s", path, ErrForbidden, hint)
			}
			return fmt.Errorf("%s: %w", path, ErrForbidden)
		}
		if hint != "" {
			return fmt.Errorf("unexpected status %d from %s: %s", resp.StatusCode, path, hint)
		}
		return fmt.Errorf("unexpected status %d from %s", resp.StatusCode, path)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}
