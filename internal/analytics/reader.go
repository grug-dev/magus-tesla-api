package analytics

import (
	"context"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	analyticsdb "github.com/cristianpena/magus-tesla-api/internal/analytics/db"
	"github.com/cristianpena/magus-tesla-api/internal/vehicleref"
)

// vehicleMetricsStore is a narrow consumer interface over the two D13
// filtered SELECTs analyticsdb.Queries exposes (ai/go-conventions.md "accept
// interfaces") — ConsumedByDay/OdometerDeltaByDay's only two dependencies
// now that both read exclusively from vehicle_metrics (design.md
// D-precompute). Any *analyticsdb.Queries satisfies this automatically
// (structural typing, no adapter needed at NewReader's call site); a test
// fake supplies canned rows without a live database.
type vehicleMetricsStore interface {
	VehicleMetricsConsumedByVehicleBetween(ctx context.Context, arg analyticsdb.VehicleMetricsConsumedByVehicleBetweenParams) ([]analyticsdb.VehicleMetricsConsumedByVehicleBetweenRow, error)
	VehicleMetricsOdometerByVehicleBetween(ctx context.Context, arg analyticsdb.VehicleMetricsOdometerByVehicleBetweenParams) ([]analyticsdb.VehicleMetricsOdometerByVehicleBetweenRow, error)
	LatestVehicleMetricsByVehicles(ctx context.Context, teslaIDs []int64) ([]analyticsdb.LatestVehicleMetricsByVehiclesRow, error)
	// VehicleMetricsBatteryByVehicleBetween backs BatteryLevelByDay
	// (RM40-analytics-add-battery-level-read design.md D2). *analyticsdb.
	// Queries satisfies it automatically, no adapter needed, mirroring this
	// interface's other methods.
	VehicleMetricsBatteryByVehicleBetween(ctx context.Context, arg analyticsdb.VehicleMetricsBatteryByVehicleBetweenParams) ([]analyticsdb.VehicleMetricsBatteryByVehicleBetweenRow, error)
}

type reader struct {
	metrics vehicleMetricsStore
}

// Compile-time assertion: *reader must satisfy the public Reader interface.
var _ Reader = (*reader)(nil)

// NewReader constructs a Reader over the module's own database pool. Every
// method on the returned Reader reads exclusively from vehicle_metrics,
// precomputed ahead of time by Recalculator — see recalculate.go. The
// returned value is wrapped so the nightly poller's read is logged.
func NewReader(pool *pgxpool.Pool) Reader {
	return newLoggingReader(&reader{metrics: analyticsdb.New(pool)})
}

// ConsumedByDay implements Reader (design.md D13, D-precompute). It SELECTs
// from vehicle_metrics via VehicleMetricsConsumedByVehicleBetween (the
// battery_used_pct_calc IS NOT NULL-filtered query, analyticsdb, 2.3/3.3) and
// maps row-by-row into DayConsumption — no derivation logic here, no
// recomputation on read (that moved to Recalculate, recalculate.go). Every
// row that passes the filter is guaranteed non-NULL
// distance_traveled_km_calc/days_spanned_calc too (D9's "co-occur"
// guarantee), so the mapping needs no nil-check and no fallback-to-1 branch.
func (r *reader) ConsumedByDay(ctx context.Context, teslaID int64, start, end time.Time) ([]DayConsumption, error) {
	rows, err := r.metrics.VehicleMetricsConsumedByVehicleBetween(ctx, analyticsdb.VehicleMetricsConsumedByVehicleBetweenParams{
		TeslaID:   teslaID,
		StartDate: dateFrom(start),
		EndDate:   dateFrom(end),
	})
	if err != nil {
		return nil, err
	}

	out := make([]DayConsumption, 0, len(rows))
	for _, row := range rows {
		out = append(out, DayConsumption{
			Date:                dateFromPg(row.MetricDate),
			ConsumedPct:         row.ConsumedPct.Float64,
			DistanceKm:          row.DistanceTraveledKmCalc.Float64,
			Flagged:             row.Flagged,
			MissingChargingType: missingChargingTypeFromPg(row.MissingChargingType),
			DaysSpanned:         int(row.DaysSpannedCalc.Int32),
		})
	}
	return out, nil
}

// OdometerDeltaByDay implements Reader (design.md D13, roadmap D5). It
// SELECTs from vehicle_metrics via VehicleMetricsOdometerByVehicleBetween
// (the distance_traveled_km_calc IS NOT NULL-filtered query, analyticsdb,
// 2.3/3.3) and maps row-by-row into DayDistance, applying the roadmap-D5
// clamp (math.Max(0, ...)) on this already-guaranteed-non-NULL value — the
// ONLY place a negative distance is ever clamped; the stored column itself
// stays the raw, unclamped value (the clamp is never applied to a NULL, the
// filter guarantees that).
func (r *reader) OdometerDeltaByDay(ctx context.Context, teslaID int64, start, end time.Time) ([]DayDistance, error) {
	rows, err := r.metrics.VehicleMetricsOdometerByVehicleBetween(ctx, analyticsdb.VehicleMetricsOdometerByVehicleBetweenParams{
		TeslaID:   teslaID,
		StartDate: dateFrom(start),
		EndDate:   dateFrom(end),
	})
	if err != nil {
		return nil, err
	}

	out := make([]DayDistance, 0, len(rows))
	for _, row := range rows {
		out = append(out, DayDistance{
			Date:       dateFromPg(row.MetricDate),
			KmDriven:   math.Max(0, row.DistanceTraveledKmCalc.Float64),
			OdometerKm: row.OdometerKm,
		})
	}
	return out, nil
}

// BatteryLevelByDay implements Reader
// (RM40-analytics-add-battery-level-read design.md D2). It SELECTs from
// vehicle_metrics via VehicleMetricsBatteryByVehicleBetween (the UNFILTERED
// query -- design.md D3, no IS NOT NULL predicate, since
// battery_level_pct/battery_range_km are NOT NULL and carry no predecessor
// requirement) and maps row-by-row into DayBattery -- no derivation logic
// here, pure row-to-domain mapping, mirroring ConsumedByDay/
// OdometerDeltaByDay's own "no derivation logic here" convention.
func (r *reader) BatteryLevelByDay(ctx context.Context, teslaID int64, start, end time.Time) ([]DayBattery, error) {
	rows, err := r.metrics.VehicleMetricsBatteryByVehicleBetween(ctx, analyticsdb.VehicleMetricsBatteryByVehicleBetweenParams{
		TeslaID:   teslaID,
		StartDate: dateFrom(start),
		EndDate:   dateFrom(end),
	})
	if err != nil {
		return nil, err
	}

	out := make([]DayBattery, 0, len(rows))
	for _, row := range rows {
		out = append(out, DayBattery{
			Date:            dateFromPg(row.MetricDate),
			BatteryLevelPct: int(row.BatteryLevelPct),
			BatteryRangeKm:  row.BatteryRangeKm,
		})
	}
	return out, nil
}

// LatestMetricsForVehicles implements Reader. It SELECTs the latest
// vehicle_metrics row for each vehicle in refs via
// LatestVehicleMetricsByVehicles and maps row-by-row into VehicleStatus — no
// derivation logic here, pure row-to-domain mapping, mirroring ConsumedByDay's
// own "no derivation logic here" convention. refs is unwrapped to a plain
// []int64 via vehicleref.TeslaIDs before reaching the store, which — like
// every other analyticsdb method — deals only in plain vehicle ids; the
// proof that these ids belong to the requesting account already happened
// before this call, in whichever Ref the caller passed in.
func (r *reader) LatestMetricsForVehicles(ctx context.Context, refs []vehicleref.Ref) ([]VehicleStatus, error) {
	rows, err := r.metrics.LatestVehicleMetricsByVehicles(ctx, vehicleref.TeslaIDs(refs))
	if err != nil {
		return nil, err
	}

	out := make([]VehicleStatus, 0, len(rows))
	for _, row := range rows {
		out = append(out, VehicleStatus{
			TeslaID:                row.TeslaID,
			BatteryLevelPct:        int(row.BatteryLevelPct),
			BatteryRangeKm:         row.BatteryRangeKm,
			OdometerKm:             row.OdometerKm,
			InsideTempC:            ptrFloat64FromPg(row.InsideTempC),
			OutsideTempC:           ptrFloat64FromPg(row.OutsideTempC),
			Locked:                 ptrBoolFromPg(row.Locked),
			SentryMode:             ptrBoolFromPg(row.SentryMode),
			CarVersion:             ptrStringFromPg(row.CarVersion),
			ChargingState:          ptrStringFromPg(row.ChargingState),
			ChargeLimitSocPct:      ptrIntFromPg(row.ChargeLimitSocPct),
			CapturedAt:             ptrTimeFromPg(row.CapturedAt),
			MaxRangeChargeCounter:  ptrIntFromPg(row.MaxRangeChargeCounter),
			TpmsPressureFLPSI:      ptrFloat64FromPg(row.TpmsPressureFlPsi),
			TpmsPressureFRPSI:      ptrFloat64FromPg(row.TpmsPressureFrPsi),
			TpmsPressureRLPSI:      ptrFloat64FromPg(row.TpmsPressureRlPsi),
			TpmsPressureRRPSI:      ptrFloat64FromPg(row.TpmsPressureRrPsi),
			DistanceTraveledKmCalc: ptrFloat64FromPg(row.DistanceTraveledKmCalc),
			ConsumedPct:            ptrFloat64FromPg(row.ConsumedPct),
			KmPerPctCalc:           ptrFloat64FromPg(row.KmPerPctCalc),
			TpmsPressureFLPSICalc:  ptrFloat64FromPg(row.TpmsPressureFlPsiCalc),
			TpmsPressureFRPSICalc:  ptrFloat64FromPg(row.TpmsPressureFrPsiCalc),
			TpmsPressureRLPSICalc:  ptrFloat64FromPg(row.TpmsPressureRlPsiCalc),
			TpmsPressureRRPSICalc:  ptrFloat64FromPg(row.TpmsPressureRrPsiCalc),
		})
	}
	return out, nil
}
