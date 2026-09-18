package charging

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	chargingdb "github.com/cristianpena/magus-tesla-api/internal/charging/db"
)

// monthlyCapacityReader is the concrete implementation of
// MonthlyCapacityReader. A small unexported struct over chargingdb.Queries,
// mirroring sessionReader's and mirrorWatermarkStore's own shape.
type monthlyCapacityReader struct {
	q *chargingdb.Queries
}

func newMonthlyCapacityReader(pool *pgxpool.Pool) *monthlyCapacityReader {
	return &monthlyCapacityReader{q: chargingdb.New(pool)}
}

var _ MonthlyCapacityReader = (*monthlyCapacityReader)(nil)

// CapacityForMonth implements MonthlyCapacityReader. See the interface doc
// comment (charging.go) for the full contract.
func (r *monthlyCapacityReader) CapacityForMonth(ctx context.Context, teslaID int64, month time.Time) (*float64, bool, error) {
	v, err := r.q.EffectiveCapacityForPeriod(ctx, chargingdb.EffectiveCapacityForPeriodParams{
		TeslaID: teslaID,
		Month:   pgtype.Date{Time: month, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("charging: reading monthly capacity for tesla_id %d: %w", teslaID, err)
	}
	return pgFloat8ToFloat64Ptr(v), true, nil
}
