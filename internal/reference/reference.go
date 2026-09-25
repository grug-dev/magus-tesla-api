// Package reference holds external reference values that belong to no
// vehicle and no user -- facts the platform needs but does not itself
// observe from a car or a person. Today it holds exactly one such fact: the
// price of a gallon of gasoline, by calendar month, used elsewhere to turn a
// vehicle's charging cost into a Colombian cost-parity comparison. It
// computes nothing derived from that fact -- a consuming module reads the
// price and does its own arithmetic.
package reference

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Reader is this module's only port. It answers "what gasoline price applies
// to each of these months" -- the single external fact this module owns.
type Reader interface {
	// PricesForMonths returns one entry per month in [start, end] that
	// resolves to a stored price: the exact row for that month, or the
	// newest row at or before it. Only the year and calendar month of start
	// and end matter; any day within a month selects that whole month.
	//
	// The result is SPARSE: a month with nothing to fall back to -- every
	// stored row is later than that month -- is simply absent, never a zero
	// or nil-currency entry. A caller must not assume one entry per
	// requested month, and must not treat absence as a price of zero.
	PricesForMonths(ctx context.Context, start, end time.Time) ([]MonthPrice, error)
}

// MonthPrice is one calendar month's resolved gasoline price: the exact row
// stored for that month, or the newest row at or before it.
type MonthPrice struct {
	Period   time.Time // first day of the calendar month
	Currency string
	Price    float64
}

// NewReader constructs a Reader over this module's own database pool. The
// implementation lives in price_reader.go.
func NewReader(pool *pgxpool.Pool) Reader {
	return newReader(pool)
}
