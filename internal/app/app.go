// Package app is the platform's application layer (RM29 tier 7,
// RM29-app-add-process-vehicle-data). It ends roadmap violation #3: before this
// module existed, cmd/poller's reconcilingCollector hand-rolled the exact
// sync + process-charging + recalculate split the roadmap's own diagram calls for,
// because there was nowhere else to put it.
//
// The package exposes exactly one public port, Processor, whose single method
// ProcessVehicleData runs one full vehicle-data cycle as three named steps, in this
// fixed order:
//
//	Scheduler ──┐
//	            ├──> ProcessVehicleData ──┬── Sync Fleet data       (telemetry)
//	API ────────┘                         ├── Process Charging data (the T6 mirror)
//	                                      └── Recalculate Analytics (analytics)
//
// Scheduler and the future manual-rerun API (roadmap tier 8, parked) are peer
// driving adapters that CALL this port — neither is inside Processor.
// ProcessVehicleData has no knowledge of when a cycle runs or how it was triggered
// beyond the telemetry.TriggeredBy value its caller passes in; it only knows what
// one cycle does (design.md D3).
//
// This package also HOSTS the scheduled driving adapter (Scheduler/NewScheduler/Run,
// scheduler.go), relocated from internal/telemetry (design.md D4, carrying RD8,
// which superseded RD5's cmd/poller plan). That is a source-location fact, not a
// composition fact: Processor has no Scheduler field, ProcessVehicleData never
// consults a clock, and Scheduler holds a Processor and calls it from the outside,
// exactly as it did from cmd/poller before the move.
//
// internal/app owns no table, no migration directory and no pool (design.md D1/D2)
// — every read and write this use case performs happens through another module's
// public port.
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
// for the three-step diagram (design.md D3). Scheduler and the future manual-rerun
// API are peer driving adapters that CALL Processor from the outside; neither is
// part of it.
type Processor interface {
	// ProcessVehicleData runs one full cycle for triggeredBy. It generates a fresh
	// RunID (uuid.New()) once per invocation and builds a telemetry.RunContext,
	// passed to telemetry.Collector.CollectAll as step 1 (design.md D5, D8). A
	// non-nil error from step 1 is returned immediately as (report, err) — steps 2
	// (process charging data) and 3 (recalculate analytics) do not run for that
	// invocation. Every per-account/per-vehicle failure inside any step is logged
	// and isolated, never fatal to the cycle. Returns telemetry.CycleReport
	// unchanged: this module introduces no new report/result type of its own
	// (design.md D7).
	ProcessVehicleData(ctx context.Context, triggeredBy telemetry.TriggeredBy) (telemetry.CycleReport, error)
}

// NewProcessor builds a Processor from its collaborators' PUBLIC PORTS only — every
// argument is an interface, not a *pgxpool.Pool or a concrete DB-backed type.
// internal/app owns no table and no pool (design.md D1/D2): every read and write
// this use case performs happens through one of these seven arguments.
//
// loc is required for the same reason it was required by cmd/poller's
// newNightlyReconciler before this module existed: the gap-reconciliation window's
// "yesterday" is resolved in the poller's own configured zone, not UTC
// (roadmap D6/D18) — internal/app needs no *time.Location of its own beyond this
// passthrough, mirroring internal/analytics's existing zone-free design (design.md
// D10).
func NewProcessor(
	collector telemetry.Collector,
	superchargerReader telemetry.SuperchargerReader,
	sessionWriter charging.SessionWriter,
	acct account.Service,
	recalculator analytics.Recalculator,
	analyticsReader analytics.Reader,
	gapWriter analytics.GapWriter,
	loc *time.Location,
) Processor {
	return &processor{
		collector:          collector,
		superchargerReader: superchargerReader,
		sessionWriter:      sessionWriter,
		acct:               acct,
		recalculator:       recalculator,
		analyticsReader:    analyticsReader,
		gapWriter:          gapWriter,
		loc:                loc,
	}
}
