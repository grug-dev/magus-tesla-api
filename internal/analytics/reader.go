package analytics

import (
	"context"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	analyticsdb "github.com/cristianpena/magus-tesla-api/internal/analytics/db"
	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// chargingSourceLimit bounds each of the two charging-cost source reads (design.md D6).
// Neither port supports a since filter; analytics fetches this many newest-first rows and
// filters to the window in Go. Generous relative to any plausible 30-day session count.
const chargingSourceLimit = 200

// vehicleLookup is a narrow consumer interface over account.Service — this module needs
// only CarType resolution for one vehicle (ai/go-conventions.md "accept interfaces").
// Any account.Service implementation satisfies this automatically.
type vehicleLookup interface {
	RegisteredVehicles(ctx context.Context, accountID uuid.UUID) ([]account.Vehicle, error)
}

// vehicleMetricsStore is a narrow consumer interface over the two D13
// filtered SELECTs analyticsdb.Queries exposes (ai/go-conventions.md "accept
// interfaces") — ConsumedByDay/OdometerDeltaByDay's only two dependencies
// now that both read exclusively from vehicle_metrics (design.md
// D-precompute). Any *analyticsdb.Queries satisfies this automatically
// (structural typing, no adapter needed at NewReader's call site); a test
// fake supplies canned rows without a live database, mirroring this file's
// vehicleLookup interface one level up.
type vehicleMetricsStore interface {
	VehicleMetricsConsumedByVehicleBetween(ctx context.Context, arg analyticsdb.VehicleMetricsConsumedByVehicleBetweenParams) ([]analyticsdb.VehicleMetricsConsumedByVehicleBetweenRow, error)
	VehicleMetricsOdometerByVehicleBetween(ctx context.Context, arg analyticsdb.VehicleMetricsOdometerByVehicleBetweenParams) ([]analyticsdb.VehicleMetricsOdometerByVehicleBetweenRow, error)
	LatestVehicleMetricsByAccount(ctx context.Context, accountID uuid.UUID) ([]analyticsdb.LatestVehicleMetricsByAccountRow, error)
	// VehicleMetricsBatteryByVehicleBetween backs BatteryLevelByDay
	// (RM40-analytics-add-battery-level-read design.md D2). *analyticsdb.
	// Queries satisfies it automatically, no adapter needed, mirroring this
	// interface's other methods.
	VehicleMetricsBatteryByVehicleBetween(ctx context.Context, arg analyticsdb.VehicleMetricsBatteryByVehicleBetweenParams) ([]analyticsdb.VehicleMetricsBatteryByVehicleBetweenRow, error)
}

type reader struct {
	metrics      vehicleMetricsStore
	telemetry    telemetry.Reader
	supercharger charging.SuperchargerSessionAnalyticsReader
	manual       charging.Reader
	account      vehicleLookup
	window       time.Duration
	now          func() time.Time
}

// Compile-time assertion: *reader must satisfy the public Reader interface.
var _ Reader = (*reader)(nil)

// NewReader constructs a Reader over the module's own database pool (backing
// ConsumedByDay/OdometerDeltaByDay's precomputed reads, design.md D-precompute
// and task 3.3) plus the three sibling ports RecentEfficiency still reads
// live and a narrow account lookup, with window fixed at construction time
// (design.md D3).
func NewReader(pool *pgxpool.Pool, telemetryReader telemetry.Reader, supercharger charging.SuperchargerSessionAnalyticsReader, manual charging.Reader, acct vehicleLookup, window time.Duration) Reader {
	return &reader{
		metrics:      analyticsdb.New(pool),
		telemetry:    telemetryReader,
		supercharger: supercharger,
		manual:       manual,
		account:      acct,
		window:       window,
		now:          time.Now,
	}
}

// carTypeFor resolves the car_type code for teslaID within accountID's registered
// vehicles. Returns "" (never an error solely for "not found") when the vehicle is
// absent from the list or its CarType is nil — capacityFor("") naturally reports
// known=false, collapsing into the D1b "unknown" path with no special casing.
func carTypeFor(ctx context.Context, acct vehicleLookup, accountID uuid.UUID, teslaID int64) (string, error) {
	vehicles, err := acct.RegisteredVehicles(ctx, accountID)
	if err != nil {
		return "", err
	}
	for _, v := range vehicles {
		if v.TeslaID == teslaID && v.CarType != nil {
			return *v.CarType, nil
		}
	}
	return "", nil
}

// sumSuperchargerKWh sums EnergyKWh across sessions at or after since. Sessions
// before since are skipped — this is the D6 in-Go date filter, since
// ListSessionsByVehicle only supports a limit, not a since parameter.
func sumSuperchargerKWh(sessions []charging.Session, since time.Time) float64 {
	var total float64
	for _, s := range sessions {
		if s.ChargeStartDateTime.Before(since) {
			continue
		}
		if s.EnergyKWh != nil {
			total += *s.EnergyKWh
		}
	}
	return total
}

// sumManualKWh sums EnergyAddedKWh across entries at or after since — the D6 in-Go
// date filter for the manual-charge source (ListEntriesByVehicle also has no
// since parameter). EnergyAddedKWh is nullable since MAG-18/RM33; an entry with
// unknown energy (nil) contributes nothing to the total, consistent with how
// sumSuperchargerKWh handles sessions with nil EnergyKWh (design.md D2).
func sumManualKWh(entries []charging.Entry, since time.Time) float64 {
	var total float64
	for _, e := range entries {
		if e.ChargedOn.Before(since) {
			continue
		}
		if e.EnergyAddedKWh != nil {
			total += *e.EnergyAddedKWh
		}
	}
	return total
}

// RecentEfficiency implements Reader (design.md "Public Surface", D4 accountID scoping).
// It issues exactly one call per consumed port (telemetry, supercharger, manual,
// account), never a per-snapshot or per-session lookup (ai/architecture.md §7
// read-heavy profile).
func (r *reader) RecentEfficiency(ctx context.Context, accountID uuid.UUID, teslaID int64) (Efficiency, bool, error) {
	since := r.now().Add(-r.window)

	snapshots, err := r.telemetry.SnapshotsByVehicleSince(ctx, accountID, teslaID, since)
	if err != nil {
		return Efficiency{}, false, err
	}

	sessions, err := r.supercharger.ListSessionsByVehicle(ctx, accountID, teslaID, chargingSourceLimit)
	if err != nil {
		return Efficiency{}, false, err
	}

	entries, err := r.manual.ListEntriesByVehicle(ctx, accountID, teslaID, chargingSourceLimit)
	if err != nil {
		return Efficiency{}, false, err
	}

	carType, err := carTypeFor(ctx, r.account, accountID, teslaID)
	if err != nil {
		return Efficiency{}, false, err
	}

	kWhIn := sumSuperchargerKWh(sessions, since) + sumManualKWh(entries, since)
	capacity, known := capacityFor(carType)

	eff, ok := deriveEfficiency(snapshots, kWhIn, capacity, known)
	return eff, ok, nil
}

// ConsumedByDay implements Reader (design.md D13, D-precompute). It SELECTs
// from vehicle_metrics via VehicleMetricsConsumedByVehicleBetween (the
// battery_used_pct_calc IS NOT NULL-filtered query, analyticsdb, 2.3/3.3) and
// maps row-by-row into DayConsumption — no derivation logic here, no
// recomputation on read (that moved to Recalculate, recalculate.go). Every
// row that passes the filter is guaranteed non-NULL
// distance_traveled_km_calc/days_spanned_calc too (D9's "co-occur"
// guarantee), so the mapping needs no nil-check and no fallback-to-1 branch.
func (r *reader) ConsumedByDay(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]DayConsumption, error) {
	rows, err := r.metrics.VehicleMetricsConsumedByVehicleBetween(ctx, analyticsdb.VehicleMetricsConsumedByVehicleBetweenParams{
		AccountID: accountID,
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
func (r *reader) OdometerDeltaByDay(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]DayDistance, error) {
	rows, err := r.metrics.VehicleMetricsOdometerByVehicleBetween(ctx, analyticsdb.VehicleMetricsOdometerByVehicleBetweenParams{
		AccountID: accountID,
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
func (r *reader) BatteryLevelByDay(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]DayBattery, error) {
	rows, err := r.metrics.VehicleMetricsBatteryByVehicleBetween(ctx, analyticsdb.VehicleMetricsBatteryByVehicleBetweenParams{
		AccountID: accountID,
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

// LatestMetricsByAccount implements Reader (RM38-analytics-add-vehicle-status-columns
// design.md D5/D6). It SELECTs the latest vehicle_metrics row per vehicle via
// LatestVehicleMetricsByAccount and maps row-by-row into VehicleStatus — no
// derivation logic here, pure row-to-domain mapping, mirroring ConsumedByDay's
// own "no derivation logic here" convention.
func (r *reader) LatestMetricsByAccount(ctx context.Context, accountID uuid.UUID) ([]VehicleStatus, error) {
	rows, err := r.metrics.LatestVehicleMetricsByAccount(ctx, accountID)
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
			TpmsPressureFLPSICalc:  ptrFloat64FromPg(row.TpmsPressureFlPsiCalc),
			TpmsPressureFRPSICalc:  ptrFloat64FromPg(row.TpmsPressureFrPsiCalc),
			TpmsPressureRLPSICalc:  ptrFloat64FromPg(row.TpmsPressureRlPsiCalc),
			TpmsPressureRRPSICalc:  ptrFloat64FromPg(row.TpmsPressureRrPsiCalc),
		})
	}
	return out, nil
}
