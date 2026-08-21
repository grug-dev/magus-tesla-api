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
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
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

// pgTextFromMissingType maps a telemetry.MissingChargingType to a nullable
// pgtype.Text -- the zero value "" (never flagged, including a
// predecessor-less row, design.md D9) becomes SQL NULL, matching
// vehicle_metrics_missing_type_iff_flagged's CHECK constraint.
func pgTextFromMissingType(v telemetry.MissingChargingType) pgtype.Text {
	if v == "" {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: string(v), Valid: true}
}

// missingChargingTypeFromPg maps a nullable pgtype.Text back to
// telemetry.MissingChargingType -- SQL NULL becomes the zero value "".
func missingChargingTypeFromPg(v pgtype.Text) telemetry.MissingChargingType {
	if !v.Valid {
		return ""
	}
	return telemetry.MissingChargingType(v.String)
}
