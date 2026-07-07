package tesla

import (
	"context"
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

// VehicleData fetches the full state snapshot for one vehicle. The vehicle must
// be awake — call WakeUp first and poll until its State is "online".
func (c *Client) VehicleData(ctx context.Context, creds Credentials, vehicleID int64) (*VehicleDataTesla, error) {
	var out dataResponseTesla
	path := fmt.Sprintf("/api/1/vehicles/%d/vehicle_data", vehicleID)
	if err := c.get(ctx, creds, path, &out); err != nil {
		return nil, fmt.Errorf("fetching vehicle data: %w", err)
	}
	return &out.Response, nil
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
