// File monthly_reader.go implements the MonthlyReader port (analytics.go) --
// this module's read path over vehicle_monthly_metrics, the table
// monthly_sync.go writes. It runs one query and maps each row through the
// SAME monthlyMetricsFromRow the write path uses, so a column added to the
// table reaches both paths from one place. Mirrors monthly_sync.go's split
// between the public port (analytics.go) and its concrete implementation
// (this file).
package analytics

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	analyticsdb "github.com/cristianpena/magus-tesla-api/internal/analytics/db"
)

// monthlyMetricsStore is a narrow consumer interface over the single query
// this reader needs (ai/go-conventions.md "accept interfaces"). Any
// *analyticsdb.Queries satisfies it automatically, so NewMonthlyReader needs
// no adapter, and a test supplies canned rows without a live database.
type monthlyMetricsStore interface {
	VehicleMonthlyMetricsForVehicleBetween(ctx context.Context, arg analyticsdb.VehicleMonthlyMetricsForVehicleBetweenParams) ([]analyticsdb.VehicleMonthlyMetric, error)
}

// monthlyReader is the concrete implementation of the MonthlyReader port.
type monthlyReader struct {
	q monthlyMetricsStore
}

// newMonthlyReader is the internal constructor called by the public
// NewMonthlyReader in analytics.go, so the forward reference compiles before
// this file is parsed (mirrors newMonthlySyncer's identical pattern).
func newMonthlyReader(pool *pgxpool.Pool) *monthlyReader {
	return &monthlyReader{q: analyticsdb.New(pool)}
}

// Compile-time assertion: *monthlyReader must satisfy the public
// MonthlyReader interface.
var _ MonthlyReader = (*monthlyReader)(nil)

// MonthlyMetricsBetween implements MonthlyReader. See the interface doc
// comment (analytics.go) for the full contract.
//
// The month normalization of both bounds happens in SQL (date_trunc), not
// here, so this method passes start and end through untouched -- the same
// division of labour SyncMonth uses with its own period argument.
//
// A row that fails to map aborts the whole read rather than being skipped:
// a bad ext_*_cost numeric or a malformed *_ending_battery_dist JSON means
// the stored row is corrupt, and silently dropping it would show the page a
// smaller total that looks like a real, smaller month.
func (r *monthlyReader) MonthlyMetricsBetween(ctx context.Context, teslaID int64, start, end time.Time) ([]VehicleMonthlyMetrics, error) {
	rows, err := r.q.VehicleMonthlyMetricsForVehicleBetween(ctx, analyticsdb.VehicleMonthlyMetricsForVehicleBetweenParams{
		TeslaID:     teslaID,
		StartPeriod: dateFrom(start),
		EndPeriod:   dateFrom(end),
	})
	if err != nil {
		return nil, fmt.Errorf("fetching vehicle_monthly_metrics: %w", err)
	}

	out := make([]VehicleMonthlyMetrics, 0, len(rows))
	for _, row := range rows {
		m, err := monthlyMetricsFromRow(row)
		if err != nil {
			return nil, fmt.Errorf("mapping vehicle_monthly_metrics row for period %s: %w", dateFromPg(row.Period).Format("2006-01"), err)
		}
		out = append(out, m)
	}
	return out, nil
}
