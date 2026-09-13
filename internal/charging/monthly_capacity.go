package charging

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	chargingdb "github.com/cristianpena/magus-tesla-api/internal/charging/db"
)

// capacitySample is one valid input row for the monthly effective-capacity
// estimate: the capacity charging's own inferred_capacity_kwh_calc already
// computed for one record (RD1/RD2), and the battery-percentage delta that
// computation divided by. Package-private -- it never leaves the job that
// builds it.
type capacitySample struct {
	CapacityKWh     float64
	BatteryDeltaPct int
}

// minSamples and minDeltaPct are Go constants, never database values (roadmap
// RD3): tuning them is a code change plus a re-run, never a migration.
const (
	minSamples  = 3
	minDeltaPct = 15
)

// estimateEffectiveCapacity applies RD3's gate-then-median rule to one
// vehicle's valid samples for one month: drop samples whose battery delta is
// smaller than minDeltaPct (a small delta amplifies percentage error in the
// implied capacity -- roadmap RD3's own worked example), then require at least
// minSamples surviving samples before reporting a capacity at all (RD4).
// sampleCount is always the count AFTER the delta gate (design.md D1) -- a
// sample the gate drops was never valid evidence, so it does not count as
// "found" either.
//
// The two halves are deliberately separate (design.md D9): everything in this
// function is the GATE -- which samples count as evidence, shared by every
// possible estimation method -- and the single median() call is the METHOD --
// how the surviving samples are reduced to one number. A future second method
// is a new sibling function with median()'s exact signature; it never edits
// median(), and never moves the math into this function's body.
//
// No ctx, no I/O -- pure over its input slice, unit-testable with no container
// (roadmap Tier 1 scope statement).
func estimateEffectiveCapacity(samples []capacitySample) (capacityKWh *float64, sampleCount int) {
	gated := make([]capacitySample, 0, len(samples))
	for _, s := range samples {
		if s.BatteryDeltaPct >= minDeltaPct {
			gated = append(gated, s)
		}
	}
	sampleCount = len(gated)
	if sampleCount < minSamples {
		return nil, sampleCount
	}
	capacity := median(gated)
	return &capacity, sampleCount
}

// median reduces one month's gated samples to a single capacity: the middle
// value on an odd count, the average of the two middle values on an even count
// (roadmap RD3). gated must be non-empty; estimateEffectiveCapacity's
// minSamples check guarantees that.
//
// It takes the whole []capacitySample, not just the capacities, and sorts its
// own copy -- see design.md D9. median() itself ignores BatteryDeltaPct, but
// the signature is the seam a second method plugs into, and a delta-weighted or
// delta-trimmed method needs that field. Passing the whole sample keeps "add a
// method" down to one new function, with no call-site reshape.
func median(gated []capacitySample) float64 {
	values := make([]float64, len(gated))
	for i, s := range gated {
		values[i] = s.CapacityKWh
	}
	sort.Float64s(values)

	n := len(values)
	mid := n / 2
	if n%2 == 1 {
		return values[mid]
	}
	return (values[mid-1] + values[mid]) / 2
}

// monthlyCapacityCalculator is the concrete implementation of
// MonthlyCapacityCalculator. Like sessionVerifier and mirrorWatermarkStore, it
// is a small unexported struct talking directly to chargingdb.Queries, not
// part of service.go's store interface -- this job runs once a month, offline
// from any request path, and is exercised by TEST_DATABASE_URL-gated integration
// tests, not a fake (design.md Context fact 9's reasoning applies here too).
type monthlyCapacityCalculator struct {
	pool *pgxpool.Pool
	q    *chargingdb.Queries
}

func newMonthlyCapacityCalculator(pool *pgxpool.Pool) *monthlyCapacityCalculator {
	return &monthlyCapacityCalculator{pool: pool, q: chargingdb.New(pool)}
}

var _ MonthlyCapacityCalculator = (*monthlyCapacityCalculator)(nil)

// Calculate implements MonthlyCapacityCalculator. See the interface doc comment
// (charging.go) for the full contract.
//
// teslaID non-nil scopes the run to one vehicle by filtering byVehicle in Go
// AFTER both queries run for the whole period, rather than adding a second,
// nearly-identical pair of SQL queries with a tesla_id predicate (design.md
// D7). This job is not on any hot read path (ai/architecture.md §7) and this
// repo has no existing precedent for a nullable-filter SQL idiom
// (sqlc.narg/sqlc.arg do not appear anywhere in this project) -- introducing
// one for a once-a-month debug/backfill path would be exactly the
// over-abstraction CLAUDE.md §Non-negotiables warns against.
func (c *monthlyCapacityCalculator) Calculate(ctx context.Context, period time.Time, teslaID *int64) (MonthlyCapacityReport, error) {
	periodEnd := period.AddDate(0, 1, 0)
	periodStartDate := dateFromTime(period)
	periodEndDate := dateFromTime(periodEnd)
	periodStartTS := pgtype.Timestamptz{Time: period, Valid: true}
	periodEndTS := pgtype.Timestamptz{Time: periodEnd, Valid: true}

	entryRows, err := c.q.ListValidManualEntryCapacitiesForPeriod(ctx, chargingdb.ListValidManualEntryCapacitiesForPeriodParams{
		PeriodStart: periodStartDate,
		PeriodEnd:   periodEndDate,
	})
	if err != nil {
		return MonthlyCapacityReport{}, fmt.Errorf("charging: listing valid manual entry capacities: %w", err)
	}
	sessionRows, err := c.q.ListValidSessionCapacitiesForPeriod(ctx, chargingdb.ListValidSessionCapacitiesForPeriodParams{
		PeriodStart: periodStartTS,
		PeriodEnd:   periodEndTS,
	})
	if err != nil {
		return MonthlyCapacityReport{}, fmt.Errorf("charging: listing valid session capacities: %w", err)
	}

	byVehicle := map[int64][]capacitySample{}
	for _, r := range entryRows {
		capacity := pgNumericToFloat64Ptr(r.InferredCapacityKwhCalc)
		start := pgInt2ToIntPtr(r.StartBatteryPct)
		end := pgInt2ToIntPtr(r.EndBatteryPct)
		if capacity == nil || start == nil || end == nil {
			continue // defensive; the query's own WHERE already guarantees this
		}
		byVehicle[r.TeslaID] = append(byVehicle[r.TeslaID], capacitySample{CapacityKWh: *capacity, BatteryDeltaPct: *end - *start})
	}
	for _, r := range sessionRows {
		capacity := pgNumericToFloat64Ptr(r.InferredCapacityKwhCalc)
		start := pgInt2ToIntPtr(r.StartBatteryPct)
		end := pgInt2ToIntPtr(r.EndBatteryPct)
		if capacity == nil || start == nil || end == nil {
			continue // defensive; the query's own WHERE already guarantees this
		}
		byVehicle[r.TeslaID] = append(byVehicle[r.TeslaID], capacitySample{CapacityKWh: *capacity, BatteryDeltaPct: *end - *start})
	}

	report := MonthlyCapacityReport{Period: period}
	for tid, samples := range byVehicle {
		if teslaID != nil && tid != *teslaID {
			continue
		}
		report.VehiclesFound++
		capacityKWh, sampleCount := estimateEffectiveCapacity(samples)

		var capacityParam pgtype.Float8
		if capacityKWh != nil {
			capacityParam = pgtype.Float8{Float64: *capacityKWh, Valid: true}
			report.Measured++
		} else {
			report.Thin++
		}

		if err := c.q.UpsertMonthlyEffectiveCapacity(ctx, chargingdb.UpsertMonthlyEffectiveCapacityParams{
			TeslaID:              tid,
			EffectivePeriod:      periodStartDate,
			EffectiveCapacityKwh: capacityParam,
			CandidateCount:       int32(len(samples)),
			SampleCount:          int32(sampleCount),
		}); err != nil {
			return report, fmt.Errorf("charging: upserting monthly effective capacity for tesla_id %d: %w", tid, err)
		}
	}
	return report, nil
}
