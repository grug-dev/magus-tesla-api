package tesla

import (
	"context"
	"encoding/json"
	"fmt"
)

// ListVehicles returns every vehicle associated with the account behind creds.
func (c *Client) ListVehicles(ctx context.Context, creds Credentials) ([]VehicleTesla, error) {
	var out listResponseTesla
	if err := c.get(ctx, creds, "/api/1/vehicles", &out); err != nil {
		return nil, fmt.Errorf("listing vehicles: %w", err)
	}
	return out.Response, nil
}

// VehicleData fetches the full state snapshot for one vehicle, returning both the
// typed DTO and the raw inner vehicle_data payload from a single Fleet API request.
// The DTO is decoded from the very bytes returned as raw, so the two can never
// disagree; the raw payload is what a caller stores losslessly (e.g. telemetry
// JSONB). The vehicle must be awake — call WakeUp first and poll until its State is
// "online".
func (c *Client) VehicleData(ctx context.Context, creds Credentials, vehicleID int64) (*VehicleDataTesla, json.RawMessage, error) {
	var out dataResponseTesla
	path := fmt.Sprintf("/api/1/vehicles/%d/vehicle_data", vehicleID)
	if err := c.get(ctx, creds, path, &out); err != nil {
		return nil, nil, fmt.Errorf("fetching vehicle data: %w", err)
	}

	var data VehicleDataTesla
	if err := json.Unmarshal(out.Response, &data); err != nil {
		return nil, nil, fmt.Errorf("decoding vehicle data: %w", err)
	}
	return &data, out.Response, nil
}

// WakeUp asks a sleeping vehicle to come online. Poll ListVehicles until the
// vehicle's State == "online" before calling VehicleData.
func (c *Client) WakeUp(ctx context.Context, creds Credentials, vehicleID int64) (*VehicleTesla, error) {
	var out wakeResponseTesla
	path := fmt.Sprintf("/api/1/vehicles/%d/wake_up", vehicleID)
	if err := c.post(ctx, creds, path, &out); err != nil {
		return nil, fmt.Errorf("waking up vehicle: %w", err)
	}
	return &out.Response, nil
}
