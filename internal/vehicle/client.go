// Package vehicle provides a client for the Tesla Fleet API vehicle endpoints.
// Step 8 of post-registration-setup.md — make API calls to fetch Magus's data.
package vehicle

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

const baseURL = "https://fleet-api.prd.na.vn.cloud.tesla.com"

// ErrUnauthorized is returned when the Fleet API responds with HTTP 401.
// It means the access token is expired. The caller should refresh tokens
// using auth.RefreshTokens() and retry with a new Client.
var ErrUnauthorized = errors.New("access token expired (HTTP 401) — refresh tokens and retry")

// Client is an authenticated HTTP client for the Tesla Fleet API.
type Client struct {
	accessToken string
	http        *http.Client
}

// NewClient creates a vehicle Client using the access token from .env.
func NewClient(accessToken string) *Client {
	return &Client{
		accessToken: accessToken,
		http:        &http.Client{},
	}
}

func (c *Client) get(path string, out any) error {
	req, err := http.NewRequest(http.MethodGet, baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.accessToken)
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

func (c *Client) post(path string, out any) error {
	req, err := http.NewRequest(http.MethodPost, baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.accessToken)
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
