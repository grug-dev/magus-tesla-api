// Step 8 of post-registration-setup.md — vehicle data types and Fleet API calls.
package vehicle

import "fmt"

// milesToKm is the exact conversion factor from miles to kilometers.
// Every miles-based field in this package exposes a companion km method
// (see CLAUDE.md → Coding Rules) that multiplies by this constant.
const milesToKm = 1.609344

// --- Types ---

type listResponse struct {
	Response []Vehicle `json:"response"`
	Count    int       `json:"count"`
}

type dataResponse struct {
	Response VehicleData `json:"response"`
}

type wakeResponse struct {
	Response Vehicle `json:"response"`
}

// Vehicle holds the basic identity and state of a Tesla vehicle.
type Vehicle struct {
	ID          int64  `json:"id"`
	VehicleID   int64  `json:"vehicle_id"`
	VIN         string `json:"vin"`
	DisplayName string `json:"display_name"`
	State       string `json:"state"`
}

// VehicleData holds the full snapshot returned by the vehicle_data endpoint.
type VehicleData struct {
	ID          int64        `json:"id"`
	VIN         string       `json:"vin"`
	DisplayName string       `json:"display_name"`
	State       string       `json:"state"`
	ChargeState ChargeState  `json:"charge_state"`
	ClimateState ClimateState `json:"climate_state"`
	DriveState  DriveState   `json:"drive_state"`
	VehicleState VehicleState `json:"vehicle_state"`
}

type ChargeState struct {
	BatteryLevel       int     `json:"battery_level"`
	BatteryRange       float64 `json:"battery_range"`
	ChargingState      string  `json:"charging_state"`
	ChargeRate         float64 `json:"charge_rate"`
	TimeToFullCharge   float64 `json:"time_to_full_charge"`
	ChargePortDoorOpen bool    `json:"charge_port_door_open"`
}

// BatteryRangeKm returns the estimated range converted from miles to kilometers.
func (c ChargeState) BatteryRangeKm() float64 {
	return c.BatteryRange * milesToKm
}

// ChargeRateKmh returns the charge rate (range added per hour) converted from
// mph to km/h.
func (c ChargeState) ChargeRateKmh() float64 {
	return c.ChargeRate * milesToKm
}

type ClimateState struct {
	InsideTemp            float64 `json:"inside_temp"`
	OutsideTemp           float64 `json:"outside_temp"`
	IsClimateOn           bool    `json:"is_climate_on"`
	DriverTempSetting     float64 `json:"driver_temp_setting"`
	PassengerTempSetting  float64 `json:"passenger_temp_setting"`
}

type DriveState struct {
	Speed     *float64 `json:"speed"`
	Latitude  float64  `json:"latitude"`
	Longitude float64  `json:"longitude"`
	Heading   int      `json:"heading"`
}

// SpeedKmh returns the speed converted from mph to km/h, or nil when the
// vehicle is not reporting a speed (e.g. parked).
func (d DriveState) SpeedKmh() *float64 {
	if d.Speed == nil {
		return nil
	}
	kmh := *d.Speed * milesToKm
	return &kmh
}

type VehicleState struct {
	Locked      bool    `json:"locked"`
	Odometer    float64 `json:"odometer"`
	CarVersion  string  `json:"car_version"`
}

// OdometerKm returns the odometer reading converted from miles to kilometers.
// The Fleet API reports distance in miles only, so this value is derived.
func (v VehicleState) OdometerKm() float64 {
	return v.Odometer * milesToKm
}

// --- API calls ---

// List returns all vehicles associated with the account.
func (c *Client) List() ([]Vehicle, error) {
	var out listResponse
	if err := c.get("/api/1/vehicles", &out); err != nil {
		return nil, fmt.Errorf("listing vehicles: %w", err)
	}
	return out.Response, nil
}

// Data fetches the full state snapshot for a vehicle.
// The vehicle must be awake — call WakeUp first if State is "asleep".
func (c *Client) Data(vehicleID int64) (*VehicleData, error) {
	var out dataResponse
	path := fmt.Sprintf("/api/1/vehicles/%d/vehicle_data", vehicleID)
	if err := c.get(path, &out); err != nil {
		return nil, fmt.Errorf("fetching vehicle data: %w", err)
	}
	return &out.Response, nil
}

// WakeUp sends a wake-up signal to a sleeping vehicle.
// Poll List() until State == "online" before calling Data().
func (c *Client) WakeUp(vehicleID int64) (*Vehicle, error) {
	var out wakeResponse
	path := fmt.Sprintf("/api/1/vehicles/%d/wake_up", vehicleID)
	if err := c.post(path, &out); err != nil {
		return nil, fmt.Errorf("waking up vehicle: %w", err)
	}
	return &out.Response, nil
}
