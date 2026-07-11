package telemetry

import (
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
