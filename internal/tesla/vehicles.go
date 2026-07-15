package tesla

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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

// ChargingHistory returns the account's Tesla-billed charging session history
// across all pages. When params.PageNo == 0 (the normal path), the method
// auto-fetches pages starting at pageNo=1, merging their data slices until
// len(accumulated) >= totalResults or a page returns an empty data slice.
// When params.PageNo != 0, only that single page is fetched and returned.
func (c *Client) ChargingHistory(ctx context.Context, creds Credentials, params ChargingHistoryParams) (*ChargingHistoryTesla, error) {
	// buildPath returns the URL with query params appended for the given page.
	buildPath := func(pageNo int) string {
		path := "/api/1/dx/charging/history"
		sep := "?"
		add := func(key, val string) {
			path += sep + key + "=" + val
			sep = "&"
		}
		if params.StartTime != "" {
			add("startTime", params.StartTime)
		}
		if params.EndTime != "" {
			add("endTime", params.EndTime)
		}
		if pageNo != 0 {
			add("pageNo", strconv.Itoa(pageNo))
		}
		if params.Count != 0 {
			add("count", strconv.Itoa(params.Count))
		}
		return path
	}

	// Single-page path: caller supplied an explicit page number.
	if params.PageNo != 0 {
		var envelope chargingHistoryResponseTesla
		if err := c.get(ctx, creds, buildPath(params.PageNo), &envelope); err != nil {
			return nil, fmt.Errorf("fetching charging history page %d: %w", params.PageNo, err)
		}
		return &ChargingHistoryTesla{
			Data:         envelope.Data,
			TotalResults: envelope.TotalResults,
		}, nil
	}

	// Auto-fetch-all-pages path (Option A, DES6): iterate pages until
	// len(accumulated) >= totalResults or a page returns an empty data slice.
	var (
		accumulated  []ChargingSessionTesla
		totalResults int
		pageNo       = 1
	)
	for {
		var envelope chargingHistoryResponseTesla
		if err := c.get(ctx, creds, buildPath(pageNo), &envelope); err != nil {
			return nil, fmt.Errorf("fetching charging history page %d: %w", pageNo, err)
		}
		// Capture totalResults from the first page (consistent across pages).
		if pageNo == 1 {
			totalResults = envelope.TotalResults
		}
		if len(envelope.Data) == 0 {
			break
		}
		accumulated = append(accumulated, envelope.Data...)
		if len(accumulated) >= totalResults {
			break
		}
		pageNo++
	}

	return &ChargingHistoryTesla{
		Data:         accumulated,
		TotalResults: totalResults,
	}, nil
}
