// File mapping.go holds this module's pgtype<->domain conversion helpers,
// used by both recalculate.go (domain -> pgtype, on write, D11) and
// reader.go (pgtype -> domain, on read, D13). Mirrors
// internal/telemetry/mapping.go's and internal/charging/service.go's
// identical per-module helper convention -- pgtype never leaks past this
// module's own db-facing files (ai/go-conventions.md §Persistence). analytics
// is the module: this file is analyticsdb's one mapping boundary, exactly as
// telemetry/mapping.go and charging/service.go's own conversion helpers are
// each their module's.
package analytics

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// dateFrom converts a plain calendar-date time.Time to a valid pgtype.Date
// for a DATE column (mirrors internal/telemetry/service.go's dateFrom and
// internal/charging/service.go's dateFromTime, same shape).
func dateFrom(t time.Time) pgtype.Date {
	return pgtype.Date{Time: t, Valid: true}
}

// dateFromPg converts a non-nullable pgtype.Date to a plain time.Time
// (mirrors internal/telemetry/mapping.go's identical helper).
func dateFromPg(d pgtype.Date) time.Time {
	return d.Time
}

// pgFloat8FromPtr maps a *float64 to a nullable pgtype.Float8 -- nil (no
// predecessor for this row, design.md D9) becomes SQL NULL.
func pgFloat8FromPtr(v *float64) pgtype.Float8 {
	if v == nil {
		return pgtype.Float8{Valid: false}
	}
	return pgtype.Float8{Float64: *v, Valid: true}
}

// pgInt4FromPtr maps a *int to a nullable pgtype.Int4 -- nil becomes SQL
// NULL.
func pgInt4FromPtr(v *int) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{Valid: false}
	}
	return pgtype.Int4{Int32: int32(*v), Valid: true}
}

// pgTextFromMissingType maps a MissingChargingType to a nullable
// pgtype.Text -- the zero value "" (never flagged, including a
// predecessor-less row, design.md D9) becomes SQL NULL, matching
// vehicle_metrics_missing_type_iff_flagged's CHECK constraint.
func pgTextFromMissingType(v MissingChargingType) pgtype.Text {
	if v == "" {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: string(v), Valid: true}
}

// missingChargingTypeFromPg maps a nullable pgtype.Text back to
// MissingChargingType -- SQL NULL becomes the zero value "".
func missingChargingTypeFromPg(v pgtype.Text) MissingChargingType {
	if !v.Valid {
		return ""
	}
	return MissingChargingType(v.String)
}

// --- RM38-analytics-add-vehicle-status-columns pg-conversion helpers (design
// D3) -- write-side (domain *T -> pgtype) and read-side (pgtype -> domain
// *T) for the eight new nullable vehicle_metrics columns. Generic by pg
// type, not by column, mirroring pgFloat8FromPtr/pgInt4FromPtr above --
// pgTextFromPtr is intentionally separate from pgTextFromMissingType, which
// encodes a different zero-value rule (empty string -> NULL) that does not
// apply here (nil pointer -> NULL; a non-nil pointer to "" is a real,
// distinct value).

// pgBoolFromPtr maps a *bool to a nullable pgtype.Bool -- nil becomes SQL
// NULL.
func pgBoolFromPtr(v *bool) pgtype.Bool {
	if v == nil {
		return pgtype.Bool{Valid: false}
	}
	return pgtype.Bool{Bool: *v, Valid: true}
}

// pgTextFromPtr maps a *string to a nullable pgtype.Text -- nil becomes SQL
// NULL. Unlike pgTextFromMissingType, a non-nil pointer to "" is stored as a
// real empty string, not NULL.
func pgTextFromPtr(v *string) pgtype.Text {
	if v == nil {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: *v, Valid: true}
}

// pgTimestamptzFromPtr maps a *time.Time to a nullable pgtype.Timestamptz --
// nil becomes SQL NULL.
func pgTimestamptzFromPtr(v *time.Time) pgtype.Timestamptz {
	if v == nil {
		return pgtype.Timestamptz{Valid: false}
	}
	return pgtype.Timestamptz{Time: *v, Valid: true}
}

// ptrBoolFromPg maps a nullable pgtype.Bool back to *bool -- SQL NULL
// becomes nil, never a fabricated false.
func ptrBoolFromPg(v pgtype.Bool) *bool {
	if !v.Valid {
		return nil
	}
	b := v.Bool
	return &b
}

// ptrStringFromPg maps a nullable pgtype.Text back to *string -- SQL NULL
// becomes nil, never a fabricated "".
func ptrStringFromPg(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

// ptrFloat64FromPg maps a nullable pgtype.Float8 back to *float64 -- SQL
// NULL becomes nil. Mirrors pgFloat8FromPtr's nil-check idiom for the
// reverse direction.
func ptrFloat64FromPg(v pgtype.Float8) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}

// ptrIntFromPg maps a nullable pgtype.Int4 back to *int -- SQL NULL becomes
// nil.
func ptrIntFromPg(v pgtype.Int4) *int {
	if !v.Valid {
		return nil
	}
	i := int(v.Int32)
	return &i
}

// ptrTimeFromPg maps a nullable pgtype.Timestamptz back to *time.Time -- SQL
// NULL becomes nil.
func ptrTimeFromPg(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time
	return &t
}

// --- vehicle_monthly_metrics pg-conversion helpers ---

// jsonFromEndingBatteryDist marshals an EndingBatteryDist for one of the
// three *_ending_battery_dist JSONB columns. The struct's own json tags
// already match the stored bucket keys, so this never fails on a value
// this module produces.
func jsonFromEndingBatteryDist(d EndingBatteryDist) []byte {
	b, _ := json.Marshal(d)
	return b
}

// endingBatteryDistFromJSON unmarshals one of the three
// *_ending_battery_dist JSONB columns back into EndingBatteryDist. Returns
// an error on malformed JSON rather than silently returning the zero
// value, which would look identical to a genuine all-zero distribution.
func endingBatteryDistFromJSON(b []byte) (EndingBatteryDist, error) {
	var d EndingBatteryDist
	if err := json.Unmarshal(b, &d); err != nil {
		return EndingBatteryDist{}, err
	}
	return d, nil
}

// pgNumericFromFloat64 converts a float64 to a valid pgtype.Numeric, for
// binding one of the three *_cost parameters on UpsertVehicleMonthlyMetric.
// Scans the float's decimal string representation -- the only pgx/v5 path
// for writing a plain float64 into a NUMERIC column. The three cost fields
// this module ever writes are sums of finite float64 values, so the
// decimal string is always well-formed; a Scan error here would mean an
// upstream bug, not a real NaN/Infinity cost, so it is absorbed into a
// zero-value Numeric rather than widening this function's signature for a
// case this module's own writer cannot reach.
func pgNumericFromFloat64(f float64) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(strconv.FormatFloat(f, 'f', -1, 64))
	return n
}

// float64FromPgNumeric converts a pgtype.Numeric back to float64, for
// reading UpsertVehicleMonthlyMetric's RETURNING row. Returns an error
// instead of silently truncating: pgtype.Numeric can represent values
// float64 cannot hold exactly (e.g. NaN/Infinity encodings) -- a case this
// table's own writer never produces, but this function does not assume
// that of every future caller.
func float64FromPgNumeric(v pgtype.Numeric) (float64, error) {
	f8, err := v.Float64Value()
	if err != nil {
		return 0, err
	}
	return f8.Float64, nil
}
