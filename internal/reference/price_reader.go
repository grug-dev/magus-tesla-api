// File price_reader.go implements the Reader port (reference.go) over
// reference.fuel_prices. Mirrors internal/analytics/monthly_reader.go's
// split between the public port and its concrete implementation.
package reference

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	referencedb "github.com/cristianpena/magus-tesla-api/internal/reference/db"
)

// priceStore is a narrow consumer interface over the single query this
// reader needs. Any *referencedb.Queries satisfies it automatically, so
// newReader needs no adapter, and a test supplies canned rows without a
// live database.
type priceStore interface {
	PricesForMonths(ctx context.Context, arg referencedb.PricesForMonthsParams) ([]referencedb.PricesForMonthsRow, error)
}

// priceReader is the concrete implementation of the Reader port.
type priceReader struct {
	q priceStore
}

// newReader is the internal constructor called by the public NewReader in
// reference.go, so the forward reference compiles before this file is
// parsed.
func newReader(pool *pgxpool.Pool) *priceReader {
	return &priceReader{q: referencedb.New(pool)}
}

// Compile-time assertion: *priceReader must satisfy the public Reader
// interface.
var _ Reader = (*priceReader)(nil)

// PricesForMonths implements Reader. See the interface doc comment
// (reference.go) for the full contract.
//
// The month normalization of both bounds happens in SQL (date_trunc), not
// here, so this method passes start and end through untouched.
//
// A row that fails to map aborts the whole read rather than being skipped:
// a bad price numeric means the stored row is corrupt, and silently
// dropping it would show a cost-parity figure that looks real but is
// missing a month.
func (r *priceReader) PricesForMonths(ctx context.Context, start, end time.Time) ([]MonthPrice, error) {
	rows, err := r.q.PricesForMonths(ctx, referencedb.PricesForMonthsParams{
		StartPeriod: pgtype.Date{Time: start, Valid: true},
		EndPeriod:   pgtype.Date{Time: end, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("fetching reference.fuel_prices: %w", err)
	}

	out := make([]MonthPrice, 0, len(rows))
	for _, row := range rows {
		price, err := numericToFloat64(row.Price)
		if err != nil {
			return nil, fmt.Errorf("mapping fuel_prices row for period %s: %w", row.Period.Time.Format("2006-01"), err)
		}
		out = append(out, MonthPrice{
			Period:   row.Period.Time,
			Currency: row.Currency,
			Price:    price,
		})
	}
	return out, nil
}

// numericToFloat64 converts a pgtype.Numeric to float64. Returns an error
// instead of silently truncating: pgtype.Numeric can represent values
// float64 cannot hold exactly, a case this table's own writer never
// produces, but this function does not assume that of every future caller.
// Mirrors analytics.float64FromPgNumeric and charging.pgNumericToFloat64Ptr,
// kept local to this module rather than shared.
func numericToFloat64(v pgtype.Numeric) (float64, error) {
	f8, err := v.Float64Value()
	if err != nil {
		return 0, err
	}
	return f8.Float64, nil
}
