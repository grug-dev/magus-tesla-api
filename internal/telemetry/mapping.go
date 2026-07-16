package telemetry

import (
	"time"

	telemetrydb "github.com/cristianpena/magus-tesla-api/internal/telemetry/db"
)

// rowToSnapshot converts a generated telemetrydb.VehicleSnapshot row into the
// domain Snapshot type. This is the DB→domain mapping boundary for the read path:
// all pgtype conversions are confined here so pgtype never escapes the module
// (ai/go-conventions.md §persistence). Mapping rules:
//
//   - CapturedAt: pgtype.Timestamptz.Time → time.Time (UTC via the stored value)
//   - SentryMode: pgtype.Bool → *bool: {Valid: false} → nil, {Valid: true, Bool: v} → &v
//   - BatteryLevel, ChargeLimitSoc: int32 → int (sqlc generates int32; domain uses int)
//   - All other fields are value-compatible (float64, string, bool, uuid.UUID, []byte)
func rowToSnapshot(r telemetrydb.VehicleSnapshot) Snapshot {
	var sentryMode *bool
	if r.SentryMode.Valid {
		v := r.SentryMode.Bool
		sentryMode = &v
	}

	return Snapshot{
		AccountID:      r.AccountID,
		TeslaID:        r.TeslaID,
		CapturedAt:     r.CapturedAt.Time,
		RawData:        r.RawData,
		BatteryLevel:   int(r.BatteryLevel),
		BatteryRange:   r.BatteryRange,
		ChargingState:  r.ChargingState,
		ChargeLimitSoc: int(r.ChargeLimitSoc),
		Odometer:       r.Odometer,
		InsideTemp:     r.InsideTemp,
		OutsideTemp:    r.OutsideTemp,
		Locked:         r.Locked,
		SentryMode:     sentryMode,
		CarVersion:     r.CarVersion,
		Latitude:       r.Latitude,
		Longitude:      r.Longitude,
	}
}

// rowToSuperchargerSession converts a generated telemetrydb.SuperchargerSession row
// into the domain SuperchargerSession type. This is the DB→domain mapping boundary
// for the SuperchargerReader read path: all pgtype conversions are confined here so
// pgtype never appears in the domain type or any caller (ai/go-conventions.md
// §persistence, design B6.1). Mapping rules:
//
//   - pgtype.Int8 → *int64: {Valid: false} → nil, {Valid: true} → &v
//   - pgtype.Timestamptz → *time.Time for nullable unlatch_date_time; .Time for non-null
//   - pgtype.Float8 → *float64: {Valid: false} → nil
//   - pgtype.Text → *string: {Valid: false} → nil
//   - pgtype.Bool → *bool: {Valid: false} → nil
func rowToSuperchargerSession(r telemetrydb.SuperchargerSession) SuperchargerSession {
	// nullable tesla_id
	var teslaID *int64
	if r.TeslaID.Valid {
		v := r.TeslaID.Int64
		teslaID = &v
	}

	// nullable unlatch_date_time
	var unlatchDT *time.Time
	if r.UnlatchDateTime.Valid {
		t := r.UnlatchDateTime.Time
		unlatchDT = &t
	}

	// nullable derived fields
	var energyKWh *float64
	if r.EnergyKwh.Valid {
		v := r.EnergyKwh.Float64
		energyKWh = &v
	}
	var totalCost *float64
	if r.TotalCost.Valid {
		v := r.TotalCost.Float64
		totalCost = &v
	}
	var currency *string
	if r.Currency.Valid {
		v := r.Currency.String
		currency = &v
	}
	var isPaid *bool
	if r.IsPaid.Valid {
		v := r.IsPaid.Bool
		isPaid = &v
	}

	return SuperchargerSession{
		ID:                  r.ID,
		SessionID:           r.SessionID,
		AccountID:           r.AccountID,
		VIN:                 r.Vin,
		TeslaID:             teslaID,
		SiteLocationName:    r.SiteLocationName,
		CountryCode:         r.CountryCode,
		ChargeStartDateTime: r.ChargeStartDateTime.Time,
		ChargeStopDateTime:  r.ChargeStopDateTime.Time,
		UnlatchDateTime:     unlatchDT,
		BillingType:         r.BillingType,
		VehicleMakeType:     r.VehicleMakeType,
		EnergyKWh:           energyKWh,
		TotalCost:           totalCost,
		Currency:            currency,
		IsPaid:              isPaid,
		RawData:             r.RawData,
		CreatedAt:           r.CreatedAt.Time,
		UpdatedAt:           r.UpdatedAt.Time,
	}
}
