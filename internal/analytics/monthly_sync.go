// File monthly_sync.go implements the MonthlySyncer port (analytics.go) --
// this module's write path onto vehicle_monthly_metrics. It reads this
// module's own vehicle_metrics table, derives the month's figures in Go
// (monthly_figures.go), copies the pack capacity from charging's own port,
// and upserts the one row for (teslaID, period). Mirrors gap_writer.go's
// split between the public port (analytics.go) and its concrete
// implementation (this file).
package analytics

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	analyticsdb "github.com/cristianpena/magus-tesla-api/internal/analytics/db"
	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// monthlySyncer is the concrete implementation of the MonthlySyncer port.
// Like gapWriter, it talks directly to analyticsdb.Queries rather than
// through the offline-fake-testable Reader/Recalculator seam, because its
// write half is tested with a real database, not a fake store.
type monthlySyncer struct {
	q            *analyticsdb.Queries
	capacity     charging.MonthlyCapacityReader
	charges      charging.Reader
	supercharger charging.SuperchargerSessionAnalyticsReader
}

// newMonthlySyncer is the internal constructor called by the public
// NewMonthlySyncer in analytics.go, so the forward reference compiles
// before this file is parsed (mirrors newGapWriter's identical pattern).
func newMonthlySyncer(pool *pgxpool.Pool, capacity charging.MonthlyCapacityReader, charges charging.Reader, supercharger charging.SuperchargerSessionAnalyticsReader) *monthlySyncer {
	return &monthlySyncer{q: analyticsdb.New(pool), capacity: capacity, charges: charges, supercharger: supercharger}
}

// Compile-time assertion: *monthlySyncer must satisfy the public
// MonthlySyncer interface.
var _ MonthlySyncer = (*monthlySyncer)(nil)

// SyncMonth implements MonthlySyncer. See the interface doc comment
// (analytics.go) for the full contract. Implementation shape:
//
//  1. Fetch the month's vehicle_metrics rows (VehicleMetricsForVehicleAndMonth)
//     and derive the twelve All*/Weekday*/Weekend* figures in Go
//     (deriveMonthlyFigures) -- the only place that math happens.
//  2. Copy the pack capacity from charging.MonthlyCapacityReader, treating
//     "no row" and "a row with no measurement" the same way: capacity 0,
//     not measured.
//  3. Fetch external charges and Supercharger sessions for the month
//     (monthly_charging.go's monthBounds and aggregateChargingMonth) and
//     fold them into the three Ext*/SC* tallies.
//  4. Upsert the row and map RETURNING's own values back into
//     VehicleMonthlyMetrics -- Period, CreatedAt, and UpdatedAt come from
//     the stored row, never recomputed here.
func (s *monthlySyncer) SyncMonth(ctx context.Context, teslaID int64, period time.Time) (VehicleMonthlyMetrics, error) {
	rows, err := s.q.VehicleMetricsForVehicleAndMonth(ctx, analyticsdb.VehicleMetricsForVehicleAndMonthParams{
		TeslaID: teslaID,
		Period:  dateFrom(period),
	})
	if err != nil {
		return VehicleMonthlyMetrics{}, fmt.Errorf("fetching vehicle_metrics for month: %w", err)
	}

	days := make([]monthDay, len(rows))
	for i, r := range rows {
		days[i] = monthDay{
			Date:            dateFromPg(r.MetricDate),
			DistanceKm:      ptrFloat64FromPg(r.DistanceTraveledKmCalc),
			ConsumedPct:     ptrFloat64FromPg(r.ConsumedPct),
			BatteryRangeKm:  r.BatteryRangeKm,
			BatteryLevelPct: int(r.BatteryLevelPct),
		}
	}
	figures := deriveMonthlyFigures(days)

	capacityKWh, found, err := s.capacity.CapacityForMonth(ctx, teslaID, period)
	if err != nil {
		return VehicleMonthlyMetrics{}, fmt.Errorf("reading monthly capacity: %w", err)
	}
	if found && capacityKWh != nil {
		figures.CapacityKWh = *capacityKWh
		figures.CapacityMeasured = true
	}
	figures.Currency = "COP"

	monthStart, monthEnd := monthBounds(period)

	entries, err := s.charges.ListEntriesByVehicleBetween(ctx, teslaID, monthStart, monthEnd)
	if err != nil {
		return VehicleMonthlyMetrics{}, fmt.Errorf("fetching external charges for month: %w", err)
	}
	// Widened one day each side: the port's window is UTC calendar days, and
	// Bogota is UTC-5, so an evening session falls on the next UTC day.
	// aggregateChargingMonth drops the strays by platform-zone day.
	sessions, err := s.supercharger.ListSessionsByVehicleBetween(ctx, teslaID, monthStart.AddDate(0, 0, -1), monthEnd.AddDate(0, 0, 1))
	if err != nil {
		return VehicleMonthlyMetrics{}, fmt.Errorf("fetching supercharger sessions for month: %w", err)
	}
	ac, dc, sc := aggregateChargingMonth(entries, sessions, monthStart, monthEnd)

	row, err := s.q.UpsertVehicleMonthlyMetric(ctx, analyticsdb.UpsertVehicleMonthlyMetricParams{
		TeslaID:                teslaID,
		Period:                 dateFrom(period),
		TeslaRange100PctKmCalc: figures.TeslaRange100PctKmCalc, // delta:allow: a ratio of sums, not a day-over-day delta
		AllDistanceKm:          figures.AllDistanceKm,
		AllConsumedPct:         figures.AllConsumedPct,
		AllKmPerPctCalc:        figures.AllKmPerPctCalc,
		AllDayCount:            int32(figures.AllDayCount),
		WeekdayDistanceKm:      figures.WeekdayDistanceKm,
		WeekdayConsumedPct:     figures.WeekdayConsumedPct,
		WeekdayKmPerPctCalc:    figures.WeekdayKmPerPctCalc,
		WeekdayDayCount:        int32(figures.WeekdayDayCount),
		WeekendDistanceKm:      figures.WeekendDistanceKm,
		WeekendConsumedPct:     figures.WeekendConsumedPct,
		WeekendKmPerPctCalc:    figures.WeekendKmPerPctCalc,
		WeekendDayCount:        int32(figures.WeekendDayCount),
		CapacityKwh:            figures.CapacityKWh,
		CapacityMeasured:       figures.CapacityMeasured,
		Currency:               figures.Currency,
		ExtAcEnergyKwh:         ac.energyKWh,
		ExtAcCost:              pgNumericFromFloat64(ac.cost),
		ExtAcEntryCount:        int32(ac.count),
		ExtAcEndingBatteryDist: jsonFromEndingBatteryDist(ac.dist),
		ExtDcEnergyKwh:         dc.energyKWh,
		ExtDcCost:              pgNumericFromFloat64(dc.cost),
		ExtDcEntryCount:        int32(dc.count),
		ExtDcEndingBatteryDist: jsonFromEndingBatteryDist(dc.dist),
		ScEnergyKwh:            sc.energyKWh,
		ScCost:                 pgNumericFromFloat64(sc.cost),
		ScSessionCount:         int32(sc.count),
		ScEndingBatteryDist:    jsonFromEndingBatteryDist(sc.dist),
	})
	if err != nil {
		return VehicleMonthlyMetrics{}, fmt.Errorf("upserting vehicle_monthly_metrics: %w", err)
	}

	return monthlyMetricsFromRow(row)
}

// monthlyMetricsFromRow maps an UpsertVehicleMonthlyMetric RETURNING row
// into the public VehicleMonthlyMetrics domain type. Period, CreatedAt, and
// UpdatedAt come straight from the row -- never recomputed in Go (D2).
func monthlyMetricsFromRow(row analyticsdb.VehicleMonthlyMetric) (VehicleMonthlyMetrics, error) {
	extACCost, err := float64FromPgNumeric(row.ExtAcCost)
	if err != nil {
		return VehicleMonthlyMetrics{}, fmt.Errorf("converting ext_ac_cost: %w", err)
	}
	extDCCost, err := float64FromPgNumeric(row.ExtDcCost)
	if err != nil {
		return VehicleMonthlyMetrics{}, fmt.Errorf("converting ext_dc_cost: %w", err)
	}
	scCost, err := float64FromPgNumeric(row.ScCost)
	if err != nil {
		return VehicleMonthlyMetrics{}, fmt.Errorf("converting sc_cost: %w", err)
	}

	extACDist, err := endingBatteryDistFromJSON(row.ExtAcEndingBatteryDist)
	if err != nil {
		return VehicleMonthlyMetrics{}, fmt.Errorf("converting ext_ac_ending_battery_dist: %w", err)
	}
	extDCDist, err := endingBatteryDistFromJSON(row.ExtDcEndingBatteryDist)
	if err != nil {
		return VehicleMonthlyMetrics{}, fmt.Errorf("converting ext_dc_ending_battery_dist: %w", err)
	}
	scDist, err := endingBatteryDistFromJSON(row.ScEndingBatteryDist)
	if err != nil {
		return VehicleMonthlyMetrics{}, fmt.Errorf("converting sc_ending_battery_dist: %w", err)
	}

	return VehicleMonthlyMetrics{
		TeslaID: row.TeslaID,
		Period:  dateFromPg(row.Period),

		TeslaRange100PctKmCalc: row.TeslaRange100PctKmCalc, // delta:allow: a ratio of sums, not a day-over-day delta

		AllDistanceKm:   row.AllDistanceKm,
		AllConsumedPct:  row.AllConsumedPct,
		AllKmPerPctCalc: row.AllKmPerPctCalc,
		AllDayCount:     int(row.AllDayCount),

		WeekdayDistanceKm:   row.WeekdayDistanceKm,
		WeekdayConsumedPct:  row.WeekdayConsumedPct,
		WeekdayKmPerPctCalc: row.WeekdayKmPerPctCalc,
		WeekdayDayCount:     int(row.WeekdayDayCount),

		WeekendDistanceKm:   row.WeekendDistanceKm,
		WeekendConsumedPct:  row.WeekendConsumedPct,
		WeekendKmPerPctCalc: row.WeekendKmPerPctCalc,
		WeekendDayCount:     int(row.WeekendDayCount),

		CapacityKWh:      row.CapacityKwh,
		CapacityMeasured: row.CapacityMeasured,

		Currency: row.Currency,

		ExtACEnergyKWh:         row.ExtAcEnergyKwh,
		ExtACCost:              extACCost,
		ExtACEntryCount:        int(row.ExtAcEntryCount),
		ExtACEndingBatteryDist: extACDist,

		ExtDCEnergyKWh:         row.ExtDcEnergyKwh,
		ExtDCCost:              extDCCost,
		ExtDCEntryCount:        int(row.ExtDcEntryCount),
		ExtDCEndingBatteryDist: extDCDist,

		SCEnergyKWh:         row.ScEnergyKwh,
		SCCost:              scCost,
		SCSessionCount:      int(row.ScSessionCount),
		SCEndingBatteryDist: scDist,

		CreatedAt: row.CreatedAt.Time,
		UpdatedAt: row.UpdatedAt.Time,
	}, nil
}
