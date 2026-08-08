package telemetry

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	telemetrydb "github.com/cristianpena/magus-tesla-api/internal/telemetry/db"
)

// pgNullableFloat64 converts a nullable pgtype.Float8 to *float64.
// Returns nil when !v.Valid (SQL NULL → nil pointer, meaning "row predates enrichment");
// returns a pointer to the concrete value otherwise. Design DSA4/D12: nil means
// "pre-migration row", never "reported zero" — the write path stores a truthful 0.
func pgNullableFloat64(v pgtype.Float8) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}

// pgNullableFloat4AsFloat64 converts a nullable pgtype.Float4 (REAL / float32) to
// *float64. Returns nil when !v.Valid (SQL NULL → nil pointer); widens the stored
// float32 to float64 otherwise. Used for TPMS pressure columns, which are PostgreSQL
// REAL. Design DSA4/D12: same nil-means-pre-migration semantics as pgNullableFloat64.
func pgNullableFloat4AsFloat64(v pgtype.Float4) *float64 {
	if !v.Valid {
		return nil
	}
	f := float64(v.Float32)
	return &f
}

// pgNullableInt32AsInt converts a nullable pgtype.Int4 to *int.
// Returns nil when !v.Valid; returns a pointer to int(v.Int32) otherwise.
// Design DSA4/D12: same nil-means-pre-migration semantics as pgNullableFloat64.
func pgNullableInt32AsInt(v pgtype.Int4) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int32)
	return &n
}

// pgNullableText converts a nullable pgtype.Text to *string.
// Returns nil when !v.Valid; returns a pointer to v.String otherwise.
// Design DSA4/D12: same nil-means-pre-migration semantics as pgNullableFloat64.
func pgNullableText(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

// rowToSnapshot converts a generated telemetrydb.VehicleSnapshot row into the
// domain Snapshot type. This is the DB→domain mapping boundary for the read path:
// all pgtype conversions are confined here so pgtype never escapes the module
// (ai/go-conventions.md §persistence). Mapping rules:
//
//   - CapturedAt: pgtype.Timestamptz.Time → time.Time (UTC via the stored value)
//   - CapturedDate: pgtype.Date.Time → time.Time (calendar date, UTC-midnight
//     normalized; design D2 of telemetry-dedupe-daily-snapshots). updated_at is
//     selected in every query for struct-sharing but intentionally NOT mapped
//     onto Snapshot, mirroring the existing id-selected-but-unsurfaced precedent.
//   - EffectiveDate: derived here (no DB column) as CapturedAt.AddDate(0, 0, -1) —
//     calendar-day arithmetic, not a 24h duration, so DST does not shift it
//     (telemetry-add-effective-date design D3/D4). This is the ONLY place
//     EffectiveDate is set; the write path (insertSnapshot) never goes through
//     this mapper, so a write-built Snapshot leaves EffectiveDate at its zero
//     value.
//   - SentryMode: pgtype.Bool → *bool: {Valid: false} → nil, {Valid: true, Bool: v} → &v
//   - BatteryLevelPct, ChargeLimitSocPct: int32 → int (sqlc generates int32; domain uses int)
//   - All other fields are value-compatible (float64, string, bool, uuid.UUID, []byte)
//   - Source A charge fields: pgtype nullable → *float64/*int via pgNullable* helpers.
//     nil means "pre-migration row" (never backfilled); a stored 0 round-trips as a non-nil *0.
//   - MaxRangeChargeCounter: pgtype.Int4 → *int via pgNullableInt32AsInt (same DSA4/D12
//     convention). nil = column is SQL NULL (pre-20260801000001 row not backfilled from
//     raw_data, or vehicle did not report the field). *0 = truthful zero (new vehicle).
//   - latitude/longitude and fast_charger_type were dropped in migration 20260801000001;
//     they are not present on VehicleSnapshot and not mapped here. Values are lossless
//     in raw_data JSONB.
func rowToSnapshot(r telemetrydb.VehicleSnapshot) Snapshot {
	var sentryMode *bool
	if r.SentryMode.Valid {
		v := r.SentryMode.Bool
		sentryMode = &v
	}

	return Snapshot{
		AccountID:         r.AccountID,
		TeslaID:           r.TeslaID,
		CapturedAt:        r.CapturedAt.Time,
		CapturedDate:      r.CapturedDate.Time,
		EffectiveDate:     r.CapturedAt.Time.AddDate(0, 0, -1),
		RawData:           r.RawData,
		BatteryLevelPct:   int(r.BatteryLevelPct),
		BatteryRangeKm:    r.BatteryRangeKm,
		ChargingState:     r.ChargingState,
		ChargeLimitSocPct: int(r.ChargeLimitSocPct),
		OdometerKm:        r.OdometerKm,
		InsideTempC:       r.InsideTempC,
		OutsideTempC:      r.OutsideTempC,
		Locked:            r.Locked,
		SentryMode:        sentryMode,
		CarVersion:        r.CarVersion,
		// Source A charge enrichment (RM2-telemetry-add-charging-stats/DSA4):
		// nullable columns → domain pointer fields. nil iff the column is SQL NULL
		// (pre-migration row). A stored 0 comes back as a non-nil pointer to 0.
		ChargeEnergyAddedKWh:  pgNullableFloat64(r.ChargeEnergyAddedKwh),
		ChargerPowerKW:        pgNullableInt32AsInt(r.ChargerPowerKw),
		ChargerVoltageV:       pgNullableInt32AsInt(r.ChargerVoltageV),
		ChargerActualCurrentA: pgNullableInt32AsInt(r.ChargerActualCurrentA),
		UsableBatteryLevelPct: pgNullableInt32AsInt(r.UsableBatteryLevelPct),
		// MaxRangeChargeCounter: pgtype.Int4 → *int. Same DSA4/D12 semantics:
		// SQL NULL → nil (pre-extraction row); non-NULL 0 → non-nil *0 (truthful).
		MaxRangeChargeCounter: pgNullableInt32AsInt(r.MaxRangeChargeCounter),
		// TPMS pressure fields: pgtype.Float4 → *float64, now PSI (converted at
		// capture time, telemetry-store-display-units design D1/D3). SQL NULL → nil
		// (pre-migration row or vehicle did not report TPMS). Non-NULL 0.0 → non-nil
		// *0.0 (truthful). pgNullableFloat4AsFloat64 widens float32 → float64 at the
		// mapping boundary.
		TpmsPressureFLPSI: pgNullableFloat4AsFloat64(r.TpmsPressureFlPsi),
		TpmsPressureFRPSI: pgNullableFloat4AsFloat64(r.TpmsPressureFrPsi),
		TpmsPressureRLPSI: pgNullableFloat4AsFloat64(r.TpmsPressureRlPsi),
		TpmsPressureRRPSI: pgNullableFloat4AsFloat64(r.TpmsPressureRrPsi),
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
