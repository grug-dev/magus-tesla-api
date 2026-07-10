package tesla

import (
	"context"
	"encoding/json"
	"fmt"
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
