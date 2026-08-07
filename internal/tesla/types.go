package tesla

import (
	"encoding/json"
	"fmt"
	"time"
)

// milesToKm is the exact miles→kilometers factor. Every miles/mph field on a
// ...Tesla DTO exposes a companion Km/Kmh method (see ai/go-conventions.md).
// The Fleet API only sends miles, so km values are always derived, never fields.
const milesToKm = 1.609344

// barToPSI is the exact bar→PSI (pounds per square inch) factor. Every
// tire-pressure field on a ...Tesla DTO exposes a companion PSI method (RM7
// tier 1, design D3). This factor must never be written inline at a call
// site — it is the same value used anywhere else in the platform that
// converts bar to PSI.
const barToPSI = 14.503773773

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
	// AccessType is the Fleet API owner/driver flag ("OWNER" or "DRIVER").
	// Plain string per D1 — no enum type; the adapter decodes-and-passes-through only.
	// Not a distance/speed field, so no Km()/Kmh() companion is applicable.
	AccessType string `json:"access_type"`
}

// VehicleDataTesla is the full snapshot from the vehicle_data endpoint.
type VehicleDataTesla struct {
	ID            int64              `json:"id"`
	VIN           string             `json:"vin"`
	DisplayName   string             `json:"display_name"`
	State         string             `json:"state"`
	ChargeState   ChargeStateTesla   `json:"charge_state"`
	ClimateState  ClimateStateTesla  `json:"climate_state"`
	DriveState    DriveStateTesla    `json:"drive_state"`
	VehicleState  VehicleStateTesla  `json:"vehicle_state"`
	VehicleConfig VehicleConfigTesla `json:"vehicle_config"`
}

type ChargeStateTesla struct {
	BatteryLevel       int     `json:"battery_level"`
	BatteryRange       float64 `json:"battery_range"`
	ChargingState      string  `json:"charging_state"`
	ChargeRate         float64 `json:"charge_rate"`
	ChargeLimitSoc     int     `json:"charge_limit_soc"`
	TimeToFullCharge   float64 `json:"time_to_full_charge"`
	ChargePortDoorOpen bool    `json:"charge_port_door_open"`
	// Charge-telemetry fields promoted for telemetry snapshot enrichment
	// (RM2-telemetry-add-charging-stats Source A). The Fleet API sends a concrete
	// value (0 / "" when the vehicle is idle), so these are plain — never miles/mph,
	// hence no Km()/Kmh() companions.
	ChargeEnergyAdded    float64 `json:"charge_energy_added"`    // kWh added this session
	ChargerPower         int     `json:"charger_power"`          // kW
	ChargerVoltage       int     `json:"charger_voltage"`        // V
	ChargerActualCurrent int     `json:"charger_actual_current"` // A
	UsableBatteryLevel   int     `json:"usable_battery_level"`   // %
	FastChargerType      string  `json:"fast_charger_type"`
	// Lifetime count of charges to the true 100% Maximum-Battery-Range limit — a
	// charging-habits health signal telemetry promotes to a typed snapshot column.
	MaxRangeChargeCounter int `json:"max_range_charge_counter"`
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
	// TPMS (tire-pressure monitoring system) pressures in bar — API-native. Plain
	// float64 (not pointer) in the DTO: the Fleet API includes these in vehicle_state
	// when the vehicle has TPMS sensors. snapshotFrom pointer-wraps them via ptr() so a
	// reported 0.0 bar is stored non-NULL and pre-migration rows stay NULL (D12/DSA3).
	TpmsPressureFL float64 `json:"tpms_pressure_fl"`
	TpmsPressureFR float64 `json:"tpms_pressure_fr"`
	TpmsPressureRL float64 `json:"tpms_pressure_rl"`
	TpmsPressureRR float64 `json:"tpms_pressure_rr"`
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

// TpmsPressureFLPSI returns the front-left tire pressure converted from bar to PSI.
func (v VehicleStateTesla) TpmsPressureFLPSI() float64 {
	return v.TpmsPressureFL * barToPSI
}

// TpmsPressureFRPSI returns the front-right tire pressure converted from bar to PSI.
func (v VehicleStateTesla) TpmsPressureFRPSI() float64 {
	return v.TpmsPressureFR * barToPSI
}

// TpmsPressureRLPSI returns the rear-left tire pressure converted from bar to PSI.
func (v VehicleStateTesla) TpmsPressureRLPSI() float64 {
	return v.TpmsPressureRL * barToPSI
}

// TpmsPressureRRPSI returns the rear-right tire pressure converted from bar to PSI.
func (v VehicleStateTesla) TpmsPressureRRPSI() float64 {
	return v.TpmsPressureRR * barToPSI
}

// VehicleConfigTesla carries the subset of the vehicle_config sub-object this platform
// extracts today (RD3 of RM6-static-vehicle-config-fields — exactly these two fields; the
// live payload has ~47 keys). Both values are static: they never change for a given
// vehicle after manufacture.
type VehicleConfigTesla struct {
	// ExteriorColor is the paint colour code (e.g. "PearlWhite").
	ExteriorColor string `json:"exterior_color"`
	// CarType is the model code (e.g. "modely").
	CarType string `json:"car_type"`
}

// --- Charging history types ---

// chargingHistoryResponseTesla is the unexported HTTP envelope returned by
// GET /api/1/dx/charging/history. Only Data and TotalResults are exposed to
// callers via ChargingHistoryTesla.
type chargingHistoryResponseTesla struct {
	Data         []ChargingSessionTesla `json:"data"`
	TotalResults int                    `json:"totalResults"`
}

// ChargingHistoryTesla is the result returned by VehicleService.ChargingHistory.
// It is the decoded, merged view of potentially multiple pages. No JSON tags —
// this is the caller-facing struct, not an envelope.
type ChargingHistoryTesla struct {
	Data         []ChargingSessionTesla
	TotalResults int
}

// ChargingSessionTesla represents one Tesla-billed charging session from the
// dx/charging/history endpoint (Supercharger / DC fast-charging only).
//
// No Km() or Kmh() companion methods are needed: the payload contains no distance
// or speed fields. Numeric values are energy (kWh), currency amounts, and rates —
// none expressed in miles or mph. The mandatory milesToKm companion rule (DES3,
// ai/go-conventions.md) is inapplicable to this struct.
type ChargingSessionTesla struct {
	SessionID           int64                  `json:"sessionId"`
	VIN                 string                 `json:"vin"`
	SiteLocationName    string                 `json:"siteLocationName"`
	ChargeStartDateTime time.Time              // parsed from RFC3339 by UnmarshalJSON below
	ChargeStopDateTime  time.Time              // parsed from RFC3339 by UnmarshalJSON below
	UnlatchDateTime     time.Time              // parsed from RFC3339 by UnmarshalJSON below
	CountryCode         string                 `json:"countryCode"`
	Fees                []ChargingFeeTesla     `json:"fees"`
	BillingType         string                 `json:"billingType"`
	Invoices            []ChargingInvoiceTesla `json:"invoices"`
	VehicleMakeType     string                 `json:"vehicleMakeType"`
	// Raw is the verbatim JSON of this session object, captured in UnmarshalJSON so
	// callers can persist a lossless copy (mirroring how VehicleData exposes raw
	// bytes). Excluded from (un)marshalling — it is populated manually, not from a
	// JSON field. Used by telemetry's supercharger_sessions.raw_data column.
	Raw json.RawMessage `json:"-"`
}

// rfcTimeString is a helper that wraps a string for RFC3339 parsing in the
// custom UnmarshalJSON below. Tesla sends offset RFC3339 timestamps without
// nanoseconds (e.g. "2026-06-28T10:24:41-05:00"); encoding/json only auto-parses
// time.RFC3339Nano by default, so we use time.Parse(time.RFC3339, ...) explicitly.
type rfcTimeString string

func (s rfcTimeString) parse() (time.Time, error) {
	if string(s) == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, string(s))
	if err != nil {
		return time.Time{}, fmt.Errorf("tesla: parsing RFC3339 timestamp %q: %w", string(s), err)
	}
	return t, nil
}

// UnmarshalJSON implements json.Unmarshaler for ChargingSessionTesla to parse
// the three datetime fields from RFC3339 strings with timezone offsets.
func (s *ChargingSessionTesla) UnmarshalJSON(data []byte) error {
	// Use an alias to avoid recursion in UnmarshalJSON.
	type alias struct {
		SessionID           int64                  `json:"sessionId"`
		VIN                 string                 `json:"vin"`
		SiteLocationName    string                 `json:"siteLocationName"`
		ChargeStartDateTime rfcTimeString          `json:"chargeStartDateTime"`
		ChargeStopDateTime  rfcTimeString          `json:"chargeStopDateTime"`
		UnlatchDateTime     rfcTimeString          `json:"unlatchDateTime"`
		CountryCode         string                 `json:"countryCode"`
		Fees                []ChargingFeeTesla     `json:"fees"`
		BillingType         string                 `json:"billingType"`
		Invoices            []ChargingInvoiceTesla `json:"invoices"`
		VehicleMakeType     string                 `json:"vehicleMakeType"`
	}
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}

	start, err := a.ChargeStartDateTime.parse()
	if err != nil {
		return err
	}
	stop, err := a.ChargeStopDateTime.parse()
	if err != nil {
		return err
	}
	unlatch, err := a.UnlatchDateTime.parse()
	if err != nil {
		return err
	}

	s.SessionID = a.SessionID
	s.VIN = a.VIN
	s.SiteLocationName = a.SiteLocationName
	s.ChargeStartDateTime = start
	s.ChargeStopDateTime = stop
	s.UnlatchDateTime = unlatch
	s.CountryCode = a.CountryCode
	s.Fees = a.Fees
	s.BillingType = a.BillingType
	s.Invoices = a.Invoices
	s.VehicleMakeType = a.VehicleMakeType
	// Capture the verbatim session bytes so callers can persist a lossless copy
	// (defensive copy — the decoder may reuse the backing array).
	s.Raw = append(json.RawMessage(nil), data...)
	return nil
}

// ChargingFeeTesla represents one fee entry within a ChargingSessionTesla.
// Tier 3 and tier 4 rate/usage fields are *float64 (nullable) because the live
// Tesla payload sends JSON null for unused tiers — not 0, not omitted. Using
// *float64 preserves the null vs. zero distinction (DES2, design.md).
// Total tier fields are float64 (non-pointer) because the live payload sends 0,
// not null, for unused total tiers.
type ChargingFeeTesla struct {
	SessionFeeID int64    `json:"sessionFeeId"`
	FeeType      string   `json:"feeType"`
	CurrencyCode string   `json:"currencyCode"`
	PricingType  string   `json:"pricingType"`
	RateBase     float64  `json:"rateBase"`
	RateTier1    float64  `json:"rateTier1"`
	RateTier2    float64  `json:"rateTier2"`
	RateTier3    *float64 `json:"rateTier3"` // nullable in live payload
	RateTier4    *float64 `json:"rateTier4"` // nullable in live payload
	UsageBase    float64  `json:"usageBase"`
	UsageTier1   float64  `json:"usageTier1"`
	UsageTier2   float64  `json:"usageTier2"`
	UsageTier3   *float64 `json:"usageTier3"` // nullable in live payload
	UsageTier4   *float64 `json:"usageTier4"` // nullable in live payload
	TotalBase    float64  `json:"totalBase"`
	TotalTier1   float64  `json:"totalTier1"`
	TotalTier2   float64  `json:"totalTier2"`
	TotalTier3   float64  `json:"totalTier3"` // 0 in captured payload — non-nullable
	TotalTier4   float64  `json:"totalTier4"` // 0 in captured payload — non-nullable
	TotalDue     float64  `json:"totalDue"`
	NetDue       float64  `json:"netDue"`
	UOM          string   `json:"uom"`
	IsPaid       bool     `json:"isPaid"`
	Status       string   `json:"status"`
}

// ChargingInvoiceTesla represents a PDF invoice reference for a charging session.
type ChargingInvoiceTesla struct {
	FileName    string `json:"fileName"`
	ContentID   string `json:"contentId"`
	InvoiceType string `json:"invoiceType"`
}

// ChargingHistoryParams holds the optional query parameters for the
// dx/charging/history endpoint. Zero value = no filters, fetch all pages.
type ChargingHistoryParams struct {
	// StartTime and EndTime filter sessions by charge-start date (ISO-8601 or
	// RFC-3339). Both are optional; omit to fetch the full account history.
	StartTime string
	EndTime   string
	// PageNo is the 1-based page number for a single-page fetch. Zero means
	// "start from page 1 and auto-fetch all pages" (the normal path).
	PageNo int
	// Count is the number of results per page. Zero uses the server default (20).
	Count int
}
