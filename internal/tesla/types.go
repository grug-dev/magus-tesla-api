package tesla

import "encoding/json"

// milesToKm is the exact miles→kilometers factor. Every miles/mph field on a
// ...Tesla DTO exposes a companion Km/Kmh method (see ai/go-conventions.md).
// The Fleet API only sends miles, so km values are always derived, never fields.
const milesToKm = 1.609344

// --- Response envelopes (mirror the Tesla JSON wrapper shape) ---

type listResponseTesla struct {
	Response []VehicleTesla `json:"response"`
	Count    int            `json:"count"`
}

// dataResponseTesla captures the vehicle_data envelope. Its Response is held as
// raw bytes (not a decoded VehicleDataTesla) so a single fetch yields both the
// lossless payload for JSONB storage and the typed DTO, decoded from those same
// bytes — see (*Client).VehicleData.
type dataResponseTesla struct {
	Response json.RawMessage `json:"response"`
}

type wakeResponseTesla struct {
	Response VehicleTesla `json:"response"`
}

// --- Vendor-shaped DTOs (the ...Tesla suffix marks them as external, per
// ai/architecture.md §6 — never build domain logic directly on these) ---

// VehicleTesla is the identity + state of a vehicle from the Fleet API.
type VehicleTesla struct {
	ID          int64  `json:"id"`
	VehicleID   int64  `json:"vehicle_id"`
	VIN         string `json:"vin"`
	DisplayName string `json:"display_name"`
	State       string `json:"state"`
}

// VehicleDataTesla is the full snapshot from the vehicle_data endpoint.
type VehicleDataTesla struct {
	ID           int64             `json:"id"`
	VIN          string            `json:"vin"`
	DisplayName  string            `json:"display_name"`
	State        string            `json:"state"`
	ChargeState  ChargeStateTesla  `json:"charge_state"`
	ClimateState ClimateStateTesla `json:"climate_state"`
	DriveState   DriveStateTesla   `json:"drive_state"`
	VehicleState VehicleStateTesla `json:"vehicle_state"`
}

type ChargeStateTesla struct {
	BatteryLevel       int     `json:"battery_level"`
	BatteryRange       float64 `json:"battery_range"`
	ChargingState      string  `json:"charging_state"`
	ChargeRate         float64 `json:"charge_rate"`
	ChargeLimitSoc     int     `json:"charge_limit_soc"`
	TimeToFullCharge   float64 `json:"time_to_full_charge"`
	ChargePortDoorOpen bool    `json:"charge_port_door_open"`
}

// BatteryRangeKm returns the estimated range converted from miles to kilometers.
func (c ChargeStateTesla) BatteryRangeKm() float64 {
	return c.BatteryRange * milesToKm
}

// ChargeRateKmh returns the charge rate (range added per hour) converted from
// mph to km/h.
func (c ChargeStateTesla) ChargeRateKmh() float64 {
	return c.ChargeRate * milesToKm
}

type ClimateStateTesla struct {
	InsideTemp           float64 `json:"inside_temp"`
	OutsideTemp          float64 `json:"outside_temp"`
	IsClimateOn          bool    `json:"is_climate_on"`
	DriverTempSetting    float64 `json:"driver_temp_setting"`
	PassengerTempSetting float64 `json:"passenger_temp_setting"`
}

type DriveStateTesla struct {
	Speed     *float64 `json:"speed"`
	Latitude  float64  `json:"latitude"`
	Longitude float64  `json:"longitude"`
	Heading   int      `json:"heading"`
}

// SpeedKmh returns the speed converted from mph to km/h, or nil when the vehicle
// reports no speed (e.g. parked). Nil-safe per the pointer-field convention.
func (d DriveStateTesla) SpeedKmh() *float64 {
	if d.Speed == nil {
		return nil
	}
	kmh := *d.Speed * milesToKm
	return &kmh
}

type VehicleStateTesla struct {
	Locked     bool    `json:"locked"`
	Odometer   float64 `json:"odometer"`
	CarVersion string  `json:"car_version"`
	// SentryMode is a pointer so an absent field (a vehicle that does not report
	// sentry) stays distinguishable from a reported-off sentry: nil = not reported,
	// *false = off, *true = on. Collapsing absent into false would lose that
	// distinction at the column layer (ai/architecture.md §6; AGENTS.md — never lose
	// historical information).
	SentryMode *bool `json:"sentry_mode"`
}

// OdometerKm returns the odometer reading converted from miles to kilometers.
func (v VehicleStateTesla) OdometerKm() float64 {
	return v.Odometer * milesToKm
}
