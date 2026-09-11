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

// pgNullableInt16AsInt converts a nullable pgtype.Int2 (SMALLINT) to *int:
// {Valid: false} -> nil, {Valid: true} -> &v. First SMALLINT column in this module;
// mirrors internal/charging's identical intPtrToPgInt2/pgInt2ToIntPtr shape for
// its own start_battery_pct/end_battery_pct (module boundaries mean the four-line
// helper is duplicated here, not imported). Reused across both SMALLINT columns
// on supercharger_history (RM27-telemetry-add-supercharger-battery-pct, design D5).
func pgNullableInt16AsInt(v pgtype.Int2) *int {
	if !v.Valid {
		return nil
	}
	r := int(v.Int16)
	return &r
}

// rowToSnapshot converts a generated telemetrydb.VehicleSnapshot row into the
// domain Snapshot type. This is the DB→domain mapping boundary for the read path:
// all pgtype conversions are confined here so pgtype never escapes the module
// (ai/go-conventions.md §persistence). Mapping rules:
//
//   - CapturedAt: pgtype.Timestamptz.Time → time.Time (UTC via the stored value)
//   - CapturedDate: pgtype.Date.Time → time.Time (calendar date, UTC-midnight
//     normalized; design D2 of telemetry-dedupe-daily-snapshots).
//   - EffectiveDate: derived here (no DB column) as CapturedAt.AddDate(0, 0, -1) —
//     calendar-day arithmetic, not a 24h duration, so DST does not shift it
//     (telemetry-add-effective-date design D3/D4). This is the ONLY place
//     EffectiveDate is set; the write path (insertSnapshot) never goes through
//     this mapper, so a write-built Snapshot leaves EffectiveDate at its zero
//     value.
//   - UpdatedAt: pgtype.Timestamptz.Time → time.Time, the same conversion as
//     CapturedAt. Was previously selected in every query for struct-sharing but
//     intentionally left unmapped onto Snapshot; now surfaced on the domain type
//     (RM29-analytics-add-vehicle-metrics task 1.1) — no new DB column, no new
//     write-path behavior.
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
		TeslaID:           r.TeslaID,
		CapturedAt:        r.CapturedAt.Time,
		CapturedDate:      r.CapturedDate.Time,
		EffectiveDate:     r.CapturedAt.Time.AddDate(0, 0, -1),
		UpdatedAt:         r.UpdatedAt.Time,
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

// rowToSuperchargerHistory converts a generated telemetrydb.SuperchargerHistory row
// into the domain SuperchargerHistory type. This is the DB→domain mapping boundary
// for the SuperchargerHistoryReader read path: all pgtype conversions are confined here so
// pgtype never appears in the domain type or any caller (ai/go-conventions.md
// §persistence, design B6.1). Mapping rules:
//
//   - pgtype.Int8 → *int64: {Valid: false} → nil, {Valid: true} → &v
//   - pgtype.Timestamptz → *time.Time for nullable unlatch_date_time; .Time for non-null
//   - pgtype.Float8 → *float64: {Valid: false} → nil
//   - pgtype.Text → *string: {Valid: false} → nil
//   - pgtype.Bool → *bool: {Valid: false} → nil
//   - Battery-% verification columns (RM27-telemetry-add-supercharger-battery-pct,
//     design D5): pgtype.Int2 → *int via pgNullableInt16AsInt (both remaining
//     SMALLINT columns); pgtype.Text → *string via the existing pgNullableText for
//     BatteryPctSource. NULL means no override exists.
func rowToSuperchargerHistory(r telemetrydb.SuperchargerHistory) SuperchargerHistory {
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

	return SuperchargerHistory{
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

		// Battery-% verification/override trio (RM27 tier 1, MAG-14, design D5).
		// Placed last, mirroring the migration's physical column-append order.
		StartBatteryPct:  pgNullableInt16AsInt(r.StartBatteryPct),
		EndBatteryPct:    pgNullableInt16AsInt(r.EndBatteryPct),
		BatteryPctSource: pgNullableText(r.BatteryPctSource),
	}
}
