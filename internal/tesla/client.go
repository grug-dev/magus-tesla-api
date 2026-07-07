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
	"net/http"
)

const baseURL = "https://fleet-api.prd.na.vn.cloud.tesla.com"

// ErrUnauthorized is returned when the Fleet API responds with HTTP 401, meaning
// the supplied access token is expired or invalid. Detect it with errors.Is. In
// the target architecture the account module owns refresh and would obtain a
// fresh token and retry.
var ErrUnauthorized = errors.New("tesla: access token expired or invalid (HTTP 401)")

// Credentials carries the per-user secret needed to call the Tesla Fleet API. The
// caller supplies it on every call — the adapter holds no identity of its own.
type Credentials struct {
	AccessToken string
}

// VehicleService is the public contract of this adapter (the module's "port").
// Callers depend on this interface, not on the concrete *Client.
type VehicleService interface {
	ListVehicles(ctx context.Context, creds Credentials) ([]VehicleTesla, error)
	VehicleData(ctx context.Context, creds Credentials, vehicleID int64) (*VehicleDataTesla, error)
	WakeUp(ctx context.Context, creds Credentials, vehicleID int64) (*VehicleTesla, error)
}

// Client is the HTTP implementation of VehicleService. Because credentials arrive
// per call, a single Client is safe to share across users.
type Client struct {
	http *http.Client
}

// NewClient returns a ready-to-use Tesla adapter.
func NewClient() *Client {
	return &Client{http: &http.Client{}}
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
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, nil)
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
		return fmt.Errorf("unexpected status %d from %s", resp.StatusCode, path)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}
