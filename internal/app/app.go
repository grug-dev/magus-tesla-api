// Package app is the platform's application layer. Before this module existed,
// cmd/poller hand-rolled the sync + process-charging + recalculate split itself,
// because there was nowhere else to put it.
//
// The package exposes exactly one public port, Processor, whose single method
// ProcessVehicleData runs one full vehicle-data cycle as five named steps, in this
// fixed order:
//
//	Scheduler ──┐
//	            ├──> ProcessVehicleData ──┬── Sync Fleet data              (telemetry)
//	API ────────┘                         ├── Process Charging data       (the mirror)
//	                                      ├── Recalculate Analytics       (analytics)
//	                                      ├── Measure Monthly Capacity    (charging)
//	                                      └── Sync Monthly Metrics        (analytics)
//
// The fourth step runs only on the first calendar day of the month, in the
// platform's default zone, and measures the previous month. The fifth step runs
// on every invocation, whatever the calendar day: it syncs the current and the
// previous calendar month's metrics for every vehicle.
//
// Scheduler and the manual-rerun HTTP listener in cmd/poller are peer driving
// adapters that CALL this port — neither is inside Processor. ProcessVehicleData
// has no knowledge of when a cycle runs or how it was triggered beyond the
// telemetry.TriggeredBy value its caller passes in; it only knows what one cycle
// does.
//
// This package also HOSTS the scheduled driving adapter (Scheduler/NewScheduler/Run,
// scheduler.go). That is a source-location fact, not a composition fact: Processor
// has no Scheduler field, ProcessVehicleData never consults a clock, and Scheduler
// holds a Processor and calls it from the outside, exactly as it did from
// cmd/poller before the move.
//
// internal/app owns no table, no migration directory and no pool — every read and
// write this use case performs happens through another module's public port.
package app

import (
	"context"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/analytics"
	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// Processor is the platform's application-layer port — see the package doc comment
// for the five-step diagram. Scheduler and the manual-rerun API are peer driving
// adapters that CALL Processor from the outside; neither is part of it.
type Processor interface {
	// ProcessVehicleData runs one full cycle for triggeredBy. It generates a fresh
	// RunID (uuid.New()) once per invocation and builds a telemetry.RunContext,
	// passed to telemetry.Collector.CollectAll as step 1. A non-nil error from
	// step 1 is returned immediately as (report, err) — steps 2 (process charging
	// data), 3 (recalculate analytics), 4 (measure monthly vehicle capacity) and
	// 5 (sync monthly vehicle metrics) do not run for that invocation. Step 5,
	// unlike step 4, runs on every invocation regardless of the calendar day.
	// Every per-account/per-vehicle failure inside any step is logged and
	// isolated, never fatal to the cycle. Returns telemetry.CycleReport
	// unchanged: this module owns no report type, so a caller reads one shape
	// whichever adapter triggered the cycle.
	ProcessVehicleData(ctx context.Context, triggeredBy telemetry.TriggeredBy) (telemetry.CycleReport, error)
}

// NewProcessor builds a Processor from its collaborators' PUBLIC PORTS only — every
// argument is an interface, not a *pgxpool.Pool or a concrete DB-backed type.
// internal/app owns no table and no pool: every read and write this use case
// performs happens through one of these twelve arguments.
//
// mirrorWatermarks is charging's second port here. It holds, per account, the
// highest telemetry updated_at the Supercharger mirror has already copied, so
// processChargingData reads a bounded window instead of the whole history.
//
// monthlyCapacityCalculator is charging's third port here. ProcessVehicleData
// calls it once a month, on the first calendar day of the month, for the previous
// month — never per vehicle, never on any other day (see runMonthlyCapacityStep
// and monthlyCapacityPeriod in processor.go).
//
// monthlySyncer is analytics's fourth port here. ProcessVehicleData calls it
// twice per vehicle, every night: once for the current calendar month, once
// for the previous one (see runMonthlyMetricsStep and monthlyMetricsPeriods
// in processor.go).
//
// runWriter is telemetry's third port here, grouped with collector and
// superchargerHistoryReader. ProcessVehicleData calls it exactly once per
// invocation, after measuring the run's start-to-finish span, to record a
// poll_runs summary row.
//
// loc is the poller's own configured zone, and only the gap-reconciliation
// window uses it: "yesterday" is resolved there, not in UTC. internal/app needs
// no *time.Location of its own beyond this passthrough. Both monthly steps
// deliberately ignore loc — they read the platform's fixed default zone through
// internal/clock instead, so a poller configured for another zone cannot shift
// which month they write.
func NewProcessor(
	collector telemetry.Collector,
	superchargerHistoryReader telemetry.SuperchargerHistoryReader,
	runWriter telemetry.RunWriter,
	sessionWriter charging.SessionWriter,
	mirrorWatermarks charging.MirrorWatermarkStore,
	monthlyCapacityCalculator charging.MonthlyCapacityCalculator,
	acct account.Service,
	recalculator analytics.Recalculator,
	analyticsReader analytics.Reader,
	gapWriter analytics.GapWriter,
	monthlySyncer analytics.MonthlySyncer,
	loc *time.Location,
) Processor {
	return &processor{
		collector:                 collector,
		superchargerHistoryReader: superchargerHistoryReader,
		runWriter:                 runWriter,
		sessionWriter:             sessionWriter,
		mirrorWatermarks:          mirrorWatermarks,
		monthlyCapacityCalculator: monthlyCapacityCalculator,
		acct:                      acct,
		recalculator:              recalculator,
		analyticsReader:           analyticsReader,
		gapWriter:                 gapWriter,
		monthlySyncer:             monthlySyncer,
		loc:                       loc,
	}
}
