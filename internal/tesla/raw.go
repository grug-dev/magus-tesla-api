package tesla

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// This file backs the `tesla-exploration` capability: raw, undecoded access to the
// Fleet API endpoints for on-demand inspection tooling (cmd/explore-tesla-api).
//
// These Raw* methods exist ONLY so exploration tooling can see the complete Tesla
// payload — including fields the typed ...Tesla DTOs omit. Domain code MUST use the
// typed methods in vehicles.go instead. They are deliberately NOT part of the
// VehicleService interface, so adding them changes no existing caller (contract
// isolation). Because they route through do, a 401 still yields ErrUnauthorized.
//
// When a new typed Fleet API call is added to vehicles.go, add its raw sibling here
// and surface it in cmd/explore-tesla-api (see that package's README / CLAUDE.md).

// ListVehiclesRaw returns the unmodified JSON body of the vehicle inventory endpoint.
func (c *Client) ListVehiclesRaw(ctx context.Context, creds Credentials) (json.RawMessage, error) {
	var out json.RawMessage
	if err := c.get(ctx, creds, "/api/1/vehicles", &out); err != nil {
		return nil, fmt.Errorf("listing vehicles (raw): %w", err)
	}
	return out, nil
}

// VehicleDataRaw returns the unmodified JSON body of one vehicle's data snapshot.
// The vehicle must be online — wake it and poll ListVehiclesRaw first.
func (c *Client) VehicleDataRaw(ctx context.Context, creds Credentials, vehicleID int64) (json.RawMessage, error) {
	var out json.RawMessage
	path := fmt.Sprintf("/api/1/vehicles/%d/vehicle_data", vehicleID)
	if err := c.get(ctx, creds, path, &out); err != nil {
		return nil, fmt.Errorf("fetching vehicle data (raw): %w", err)
	}
	return out, nil
}

// WakeUpRaw returns the unmodified JSON body of the wake endpoint.
func (c *Client) WakeUpRaw(ctx context.Context, creds Credentials, vehicleID int64) (json.RawMessage, error) {
	var out json.RawMessage
	path := fmt.Sprintf("/api/1/vehicles/%d/wake_up", vehicleID)
	if err := c.post(ctx, creds, path, &out); err != nil {
		return nil, fmt.Errorf("waking up vehicle (raw): %w", err)
	}
	return out, nil
}

// ChargingHistoryRaw returns the unmodified JSON body of the account-level
// charging-history endpoint. It is account-scoped and server-side — it needs
// no vehicle id and does NOT require the vehicle to be awake.
func (c *Client) ChargingHistoryRaw(ctx context.Context, creds Credentials) (json.RawMessage, error) {
	var out json.RawMessage
	if err := c.get(ctx, creds, "/api/1/dx/charging/history", &out); err != nil {
		return nil, fmt.Errorf("fetching charging history (raw): %w", err)
	}
	return out, nil
}

// ProductsRaw returns the unmodified JSON body of the account's product list —
// vehicles AND energy products (Powerwall / solar sites and Tesla Wall Connectors).
// Account-scoped, server-side, no vehicle id, no wake. Energy products only appear
// when the token carries the `energy_device_data` scope; without it Tesla returns
// 403 (ErrForbidden) or an energy-free list. It is the way to discover an
// `energy_site_id` for the energy endpoints (e.g. EnergyChargeHistoryRaw).
func (c *Client) ProductsRaw(ctx context.Context, creds Credentials) (json.RawMessage, error) {
	var out json.RawMessage
	if err := c.get(ctx, creds, "/api/1/products", &out); err != nil {
		return nil, fmt.Errorf("listing products (raw): %w", err)
	}
	return out, nil
}

// MeRaw returns the unmodified JSON body of the account-summary endpoint
// (GET /api/1/users/me) — a summary of the authenticated user's account. Account-
// scoped, server-side, no vehicle id, no wake. The logged output of the explorer
// is the way to see exactly which fields Tesla returns for this account.
func (c *Client) MeRaw(ctx context.Context, creds Credentials) (json.RawMessage, error) {
	var out json.RawMessage
	if err := c.get(ctx, creds, "/api/1/users/me", &out); err != nil {
		return nil, fmt.Errorf("fetching user (raw): %w", err)
	}
	return out, nil
}

// EnergyChargeHistoryRaw returns the unmodified JSON body of a Tesla Wall Connector's
// charging history for ONE energy site — the energy delivered over time, in watt-hours
// (`GET /api/1/energy_sites/{energy_site_id}/telemetry_history?kind=charge`). Account-
// scoped, server-side, no wake. It reports connector-level energy for a WALL CONNECTOR
// owned by THIS account; it is NOT per-vehicle (no VIN) and never covers a connector on
// another account. Requires the `energy_device_data` scope and an energy_site_id from
// ProductsRaw. start/end are YYYY-MM-DD; timeZone is an IANA name (e.g. America/Bogota).
func (c *Client) EnergyChargeHistoryRaw(ctx context.Context, creds Credentials, energySiteID, startDate, endDate, timeZone string) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("kind", "charge")
	q.Set("start_date", startDate)
	q.Set("end_date", endDate)
	if timeZone != "" {
		q.Set("time_zone", timeZone)
	}
	path := fmt.Sprintf("/api/1/energy_sites/%s/telemetry_history?%s", url.PathEscape(energySiteID), q.Encode())
	var out json.RawMessage
	if err := c.get(ctx, creds, path, &out); err != nil {
		return nil, fmt.Errorf("fetching energy charge history (raw): %w", err)
	}
	return out, nil
}
