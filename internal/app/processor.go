package app

import (
	"context"
	"log"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/analytics"
	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/clock"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// processor is the concrete Processor implementation (design.md D10).
// mirrorOverlap is how far BEFORE the stored watermark the Supercharger mirror
// starts its bounded read. It is internal/analytics' recalcOverlap twin (roadmap
// D6), deliberately a separate constant rather than a shared import: that one is
// unexported, and the two overlaps guard different reads of different tables. A
// shared constant would couple two modules' windows so that tuning one silently
// retunes the other.
//
// It exists because a row's updated_at is assigned before its transaction commits.
// A row can therefore appear in the table with an updated_at that a previous run
// already read past. Re-reading one day of already-mirrored rows costs a no-op
// upsert; missing one loses a session from the ledger for good.
const mirrorOverlap = 24 * time.Hour

type processor struct {
	collector                 telemetry.Collector
	superchargerHistoryReader telemetry.SuperchargerHistoryReader
	runWriter                 telemetry.RunWriter
	sessionWriter             charging.SessionWriter
	mirrorWatermarks          charging.MirrorWatermarkStore
	monthlyCapacityCalculator charging.MonthlyCapacityCalculator
	acct                      account.Service
	recalculator              analytics.Recalculator
	analyticsReader           analytics.Reader
	gapWriter                 analytics.GapWriter
	loc                       *time.Location
}

var _ Processor = (*processor)(nil)

// ProcessVehicleData reproduces reconcilingCollector.CollectAll's exact control flow
// from cmd/poller (design.md D8): generate a fresh RunContext, sync fleet data
// (step 1), and — only if that succeeds — process charging data (step 2), then
// recalculate analytics (step 3), then measure monthly vehicle capacity (step 4,
// RM52 tier 2, RD6/RD7). Step 4 runs only on the first day of the month, in the
// platform's default zone — see runMonthlyCapacityStep and monthlyCapacityPeriod.
// Since RM36-app-record-poll-run tier 2, every invocation also measures its own
// start-to-finish span with internal/clock and records exactly one poll_runs
// summary row via recordRun — on every exit path, including the step-1
// whole-cycle-failure short-circuit below (design.md D4/D6).
//
// Both preserved properties are load-bearing, not incidental:
//
//   - The short-circuit. If step 1's whole-cycle enumeration fails (e.g.
//     account.AllRegisteredVehicles itself errors), steps 2, 3 and 4 are skipped
//     entirely — exactly reconcilingCollector.CollectAll's existing behavior, and
//     for the same reason: the later steps read data step 1 was supposed to have
//     just written; running them against a cycle that never happened would
//     reconcile against stale or absent input. RM36 tier 2 reshapes the early
//     return into a guarded fall-through (`if err == nil { step2; step3; step4 }`)
//     so every path shares one measurement/record tail, but the short-circuit's
//     own behavior — steps 2/3/4 run if and only if step 1 succeeded — is
//     unchanged (design.md D4; RM52 tier 2 design.md extends it to step 4).
//   - The order. Charging-data processing (step 2) runs BEFORE analytics
//     recalculation (step 3) — the same order T6's own design.md D7 established
//     ("propagate data, then derive from it"). Monthly capacity measurement
//     (step 4) runs last, after analytics, since it reads charge data analytics
//     has already had a chance to process this cycle.
func (p *processor) ProcessVehicleData(ctx context.Context, triggeredBy telemetry.TriggeredBy) (telemetry.CycleReport, error) {
	run := telemetry.RunContext{RunID: uuid.New(), TriggeredBy: triggeredBy}
	start := clock.Now()

	report, err := p.collector.CollectAll(ctx, run) // step 1 — sync fleet data
	if err == nil {
		p.processChargingData(ctx)    // step 2 — was newSessionMirrorer
		p.recalculateAnalytics(ctx)   // step 3 — was newNightlyReconciler
		p.runMonthlyCapacityStep(ctx) // step 4 — measure monthly vehicle capacity
	}

	finish := clock.Now()
	report.Duration = finish.Sub(start)
	p.recordRun(ctx, run, report, start, finish)

	return report, err
}

// buildPollRun maps one measured ProcessVehicleData invocation onto the
// telemetry.PollRun tier 1's RunWriter.RecordRun persists (RM36-app-record-poll-run
// design D7). Pure: no I/O, no clock read of its own — start/finish are supplied
// by the caller so this function is directly unit-testable without any of
// Processor's collaborator ports (design D9, Test Contract fixtures P1/P2).
//
// VehiclesAttempted/VehiclesSucceeded read report.Attempted/.Succeeded BY FIELD,
// not by name — tier 1's own design D8 deliberately kept those Go field names
// while renaming only the printed labels, to avoid breaking
// internal/app/scheduler_test.go, a file tier 1 could not touch.
// report.FailuresByReason is read defensively: on the step-1 whole-cycle-failure
// path report is the zero value, so FailuresByReason is a nil map — a Go map read
// on a nil map returns the zero value for a missing key rather than panicking, so
// every FailuresX field below is simply 0 in that case.
func buildPollRun(run telemetry.RunContext, report telemetry.CycleReport, start, finish time.Time) telemetry.PollRun {
	return telemetry.PollRun{
		RunID:                    run.RunID,
		TriggeredBy:              run.TriggeredBy,
		StartedAt:                start,
		FinishedAt:               finish,
		DurationSeconds:          finish.Sub(start).Seconds(),
		AccountsAttempted:        report.AccountsAttempted,
		AccountsSucceeded:        report.AccountsSucceeded,
		AccountsFailed:           report.AccountsFailed,
		VehiclesAttempted:        report.Attempted,
		VehiclesSucceeded:        report.Succeeded,
		FailuresAsleepTimeout:    report.FailuresByReason[telemetry.ReasonAsleepTimeout],
		FailuresUnauthorized:     report.FailuresByReason[telemetry.ReasonUnauthorized],
		FailuresAPIError:         report.FailuresByReason[telemetry.ReasonAPIError],
		TeslaAPICalls:            report.TeslaAPICalls,
		ChargingSessionsUpserted: report.ChargingSessionsUpserted,
		ChargingFetchFailures:    report.ChargingFetchFailures,
		ConfigCaptureFailures:    report.ConfigCaptureFailures,
	}
}

// recordRun persists one poll_runs row for this invocation via p.runWriter
// (RM36-app-record-poll-run design D5). It returns nothing and cannot influence
// ProcessVehicleData's own return values — the compiler enforces this, not just
// convention. A RecordRun failure is logged, never fatal: it mirrors the exact
// "errors are logged, never fatal" pattern this file already uses for
// processChargingData/recalculateAnalytics, extended here because a RecordRun
// hiccup is an observability-write failure, not a cycle failure — it must never
// change a cycle's own reported outcome (design.md D5, roadmap-driven).
func (p *processor) recordRun(ctx context.Context, run telemetry.RunContext, report telemetry.CycleReport, start, finish time.Time) {
	if err := p.runWriter.RecordRun(ctx, buildPollRun(run, report, start, finish)); err != nil {
		log.Printf("poll run: recording run %s: %v", run.RunID, err)
	}
}

// processChargingData is the "process charging data" step, relocated wholesale
// from cmd/poller/main.go's newSessionMirrorer, which ran BEFORE reconciliation
// inside the same post-cycle work.
//
// What it does: for each registered vehicle, read that vehicle's Supercharger
// sessions from internal/telemetry and mirror them into internal/charging's
// supercharger_sessions, so charging owns a queryable record of how a vehicle was
// charged — the window, the site, the energy, the cost — without any caller having
// to compose two modules' ports.
//
// The read is BOUNDED by a watermark, not a full sweep. charging owns a
// mirror_watermarks row per vehicle holding the highest telemetry updated_at it has
// already copied; this step reads only sessions updated at or after that cursor
// minus mirrorOverlap, and moves the cursor to the highest updated_at it actually
// saw. The cursor never moves to now(), and never moves at all on a read that
// returns nothing. Before the watermark existed this step re-read the entire
// history every night, which kept every downstream recalculation permanently
// unbounded.
//
// The mapping is field-name-for-field-name with no renames and no derivation. That
// is deliberate: it keeps the mirror auditable by inspection, and it is why
// supercharger_sessions kept telemetry's column names rather than aligning with
// internal/charging's own manual_charge_entries vocabulary.
//
// It CANNOT carry a verified battery percentage, and that is structural rather than
// disciplinary: charging.SessionMirror has no percentage field, so a nightly poll
// overwriting a human's verified reading would not compile. telemetry protects the
// same five columns with a comment; here the type does it.
//
// Why per VEHICLE: the cursor is per vehicle, so the pass that advances it must be
// too. One pass per account would move one car's cursor past another car's unread
// rows, and those rows would never be mirrored again — a silent, unrecoverable
// loss. A consequence worth knowing: a car registered to two accounts is mirrored
// once, not once per account, because the loop runs over distinct tesla_ids.
//
// Why BEFORE reconciliation: reconciliation is the step that reads derived state, so
// this composition refreshes owned records first and derives second.
//
// Errors are logged, never fatal, with per-vehicle isolation mirroring the
// reconciler: one vehicle's failure never aborts another's, and a missed mirror
// self-heals next cycle because MirrorSessions is idempotent — it upserts, rather
// than accumulating, and a failed run leaves the watermark unadvanced, so the next
// run re-reads the same window. Log lines are prefixed "session mirror:" so they
// stay greppable alongside "metrics reconciliation:" and "gap reconciliation:".
func (p *processor) processChargingData(ctx context.Context) {
	vehicles, err := p.acct.AllRegisteredVehicles(ctx)
	if err != nil {
		// Whole-cycle failure, mirroring the reconciler's enumeration-failure shape.
		log.Printf("session mirror: listing vehicles: %v", err)
		return
	}

	// One pass per distinct tesla_id. The same car registered to two accounts
	// appears twice in vehicles, and mirroring it twice would do the same upsert
	// work for the same rows. Vehicle order is not significant: vehicles are
	// independent and each pass is idempotent.
	teslaIDs := make([]int64, 0, len(vehicles))
	for _, v := range vehicles {
		if slices.Contains(teslaIDs, v.TeslaID) {
			continue
		}
		teslaIDs = append(teslaIDs, v.TeslaID)
	}

	for _, teslaID := range teslaIDs {
		// The watermark is the highest telemetry updated_at this vehicle's mirror
		// has already copied. A zero time means "never mirrored", so the read
		// below starts at the epoch and copies the whole history once.
		cursor, err := p.mirrorWatermarks.MirrorWatermark(ctx, teslaID)
		if err != nil {
			log.Printf("session mirror: vehicle %d: reading watermark: %v", teslaID, err)
			continue
		}

		// Bounded read, replacing the old unbounded "every session" sweep. The
		// window starts one overlap before the cursor for the same commit-skew
		// reason analytics keeps its own overlap: a row whose updated_at was
		// assigned before the previous run committed can land in the table after
		// that run read it. Re-reading it is free — MirrorSessions is idempotent.
		sessions, err := p.superchargerHistoryReader.SuperchargerHistoryByVehicleUpdatedSince(ctx, teslaID, cursor.Add(-mirrorOverlap))
		if err != nil {
			log.Printf("session mirror: vehicle %d: reading sessions: %v", teslaID, err)
			continue
		}
		if len(sessions) == 0 {
			// Zero rows leaves the watermark exactly where it was. Advancing it
			// here — to now(), or to anything else — would push the cursor past a
			// row that commits a moment later, and that row would never be
			// mirrored again. The loss is silent and undetectable.
			continue
		}

		// The new cursor is the highest updated_at actually observed, never
		// clock.Now(). Same rule and same reason as the zero-row case above.
		mirrored := make([]charging.SessionMirror, 0, len(sessions))
		var maxUpdated time.Time
		for _, s := range sessions {
			if s.UpdatedAt.After(maxUpdated) {
				maxUpdated = s.UpdatedAt
			}
			mirrored = append(mirrored, charging.SessionMirror{
				VIN:                 s.VIN,
				TeslaID:             s.TeslaID,
				SessionID:           s.SessionID,
				ChargeStartDateTime: s.ChargeStartDateTime,
				ChargeStopDateTime:  s.ChargeStopDateTime,
				SiteLocationName:    s.SiteLocationName,
				EnergyKWh:           s.EnergyKWh,
				TotalCost:           s.TotalCost,
				Currency:            s.Currency,
				IsPaid:              s.IsPaid,
			})
		}

		if err := p.sessionWriter.MirrorSessions(ctx, mirrored); err != nil {
			log.Printf("session mirror: vehicle %d: %v", teslaID, err)
			continue
		}
		// Advance only after a successful mirror. A crash or an error between the
		// two leaves the cursor behind, so the next run re-reads the same window.
		// That repeats work; it never loses a row.
		if err := p.mirrorWatermarks.AdvanceMirrorWatermark(ctx, teslaID, maxUpdated); err != nil {
			log.Printf("session mirror: vehicle %d: advancing watermark: %v", teslaID, err)
			continue
		}
		log.Printf("session mirror: vehicle %d: %d session(s)", teslaID, len(mirrored))
	}
}

// recalculateAnalytics is the "recalculate analytics" step. It runs TWO halves
// per distinct vehicle, in this order:
//
//  1. analytics.Recalculator.Reconcile — advances the module's own precomputed read
//     model, vehicle_metrics, from its three per-source watermarks.
//  2. charge-gap reconciliation — recomputes the trailing
//     analytics.GapReconciliationWindow of consumed-per-day figures and hands the
//     flagged days to analytics's own gap writer, which upserts the days that flag
//     and deletes the days that no longer do.
//
// Both halves are keyed on the car, not on the account that registered it, so the
// loop runs once per distinct tesla_id.
//
// The order is a correctness requirement, not a preference. ConsumedByDay no
// longer derives anything: it is a SELECT over vehicle_metrics, so without step 1
// first, step 2 would reconcile the night's charge gaps against yesterday's
// metrics and never see the snapshot just collected. This is also the production
// caller of Reconcile — the gateway's post-write Recalculate only covers days a
// manual charge write touches, so without this call a vehicle's charts would stop
// advancing entirely.
//
// A vehicle whose Reconcile fails is skipped for step 2 as well, rather than
// falling through to it: the gap writer's ReconcileWindow DELETES flags for days
// that no longer flag, so running it against knowingly-stale metrics would clear
// gap rows on the strength of data we just failed to refresh. Skipping costs one
// cycle; falling through would destroy state.
//
// Errors are logged, never fatal: a missed reconciliation self-heals on the next
// cycle, because both halves are recomputed from scratch every run rather than
// accumulated. Log lines stay prefixed by their half ("metrics reconciliation:",
// "gap reconciliation:") so they remain greppable and unambiguous.
func (p *processor) recalculateAnalytics(ctx context.Context) {
	// "Yesterday" is resolved in the POLLER'S OWN ZONE, not UTC (roadmap D6/D18,
	// tier-3 design D-B12): this composition owns the zone that answers "which days
	// am I asking about", while internal/analytics needs no *time.Location of its
	// own because each row's bucket day travels with it. time.Now().UTC() here
	// would ask for the wrong day for 5 hours out of every 24. The window ends
	// yesterday because today's data is not captured until tomorrow's poll.
	//
	// clock.CalendarDay(clock.Now(), p.loc) replaces the hand-rolled
	// "y, m, d := time.Now().In(p.loc).Date(); time.Date(y, m, d, 0, 0, 0, 0,
	// time.UTC)" truncation — algebraically identical (RM35-app-adopt-clock design.md
	// D-app-1) — while p.loc, the poller's own configured zone, is preserved
	// unchanged: it is still what decides which calendar day "yesterday" is, exactly
	// as roadmap D6/D18 and tier-3 design D-B12 require. clock.Now() is used for the
	// instant rather than a bare time.Now() per ai/go-conventions.md's "never call
	// raw time.Now() outside internal/clock"; the two are the same instant, so this
	// is not a behavior change (design.md D-app-2).
	end := clock.CalendarDay(clock.Now(), p.loc).AddDate(0, 0, -1)
	start := end.AddDate(0, 0, -int(analytics.GapReconciliationWindow.Hours()/24)+1)

	log.Printf("gap reconciliation: %s → %s", start, end)

	vehicles, err := p.acct.AllRegisteredVehicles(ctx)
	if err != nil {
		// Whole-cycle failure, mirroring ProcessVehicleData's own enumeration-failure
		// shape.
		log.Printf("gap reconciliation: listing vehicles: %v", err)
		return
	}

	// One pass per distinct tesla_id. The same car registered to two accounts
	// appears twice in vehicles, and both halves below are now keyed on the car
	// alone, so a second pass would redo identical work on the same rows. The
	// first entry for each car is kept as its representative: VIN is a property
	// of the car, so it does not vary across the accounts that registered it.
	distinct := make([]account.OwnedVehicle, 0, len(vehicles))
	for _, v := range vehicles {
		if slices.ContainsFunc(distinct, func(d account.OwnedVehicle) bool { return d.TeslaID == v.TeslaID }) {
			continue
		}
		distinct = append(distinct, v)
	}

	for _, v := range distinct {
		// Step 1 — advance vehicle_metrics before anything reads it. Reconcile
		// derives its own affected window from its watermarks, so it takes no
		// start/end from here: the [start, end] below is the gap step's trailing
		// window, a different and unrelated question.
		if err := p.recalculator.Reconcile(ctx, v.TeslaID); err != nil {
			// Per-vehicle isolation. Skips this vehicle's gap step too — see the doc
			// comment: reconciling gaps against metrics we just failed to refresh
			// would delete gap rows on stale evidence.
			log.Printf("metrics reconciliation: vehicle %d: %v", v.TeslaID, err)
			continue
		}

		// Step 2 — charge gaps, now reading the model step 1 just advanced.
		days, err := p.analyticsReader.ConsumedByDay(ctx, v.TeslaID, start, end)
		if err != nil {
			// Per-vehicle isolation: one vehicle's failure never aborts another
			// vehicle's reconciliation.
			log.Printf("gap reconciliation: vehicle %d: consumed-by-day: %v", v.TeslaID, err)
			continue
		}

		var flagged []analytics.ChargeGap
		for _, day := range days {
			if !day.Flagged {
				continue
			}
			flagged = append(flagged, analytics.ChargeGap{
				TeslaID:             v.TeslaID,
				VIN:                 v.VIN,
				Date:                day.Date,
				MissingChargingType: day.MissingChargingType,
			})
		}

		if err := p.gapWriter.ReconcileWindow(ctx, v.TeslaID, start, end, flagged); err != nil {
			log.Printf("gap reconciliation: vehicle %d: reconcile window: %v", v.TeslaID, err)
			continue
		}
	}
}

// monthlyCapacityPeriod applies RD6/RD7's gate: pure over its inputs, no clock
// read of its own -- mirrors nextRun's shape (scheduler.go). now is a moment
// already resolved to the platform's own zone; loc is the SAME zone, passed
// explicitly so the function's own behavior does not depend on which zone now
// happens to already be expressed in. Returns run=false on every day but the
// first of the month. On the first, returns the PREVIOUS month's first
// instant -- charging.MonthlyCapacityCalculator.Calculate's own contract
// ("period must be the first instant of the month to compute").
func monthlyCapacityPeriod(now time.Time, loc *time.Location) (period time.Time, run bool) {
	today := clock.CalendarDay(now, loc)
	if today.Day() != 1 {
		return time.Time{}, false
	}
	return today.AddDate(0, -1, 0), true
}

// runMonthlyCapacityStep is the "monthly capacity" step (design.md D2, RM52 tier
// 2). Reads the real clock (D1) and delegates the actual gate decision to
// monthlyCapacityPeriod, which is pure and fully unit-tested. This function's
// own body -- the clock.Now()/clock.Zone() read plus the branch on run -- is
// deliberately NOT unit-tested, for the same accepted reason
// recalculateAnalytics's own "yesterday" line is not: it reads the real wall
// clock directly, with no injectable seam, exactly like every other line in
// this file that calls clock.Now() (Context fact 11).
//
// It also compares p.loc (the poller's own configurable POLLER_TIMEZONE) with
// clock.Zone() (the platform's fixed default) and logs one warning when they
// name different zones (task 2.4). This is a diagnostic only: the gate always
// follows clock.Zone(), per RD7 and D1 -- this check never changes that, never
// fails the step, and never skips it. It exists so a silent divergence (the
// cycle firing on a moment that is the 1st in p.loc but a different day in
// clock.Zone(), or the reverse) becomes a visible log line instead of an
// unnoticed skipped month.
func (p *processor) runMonthlyCapacityStep(ctx context.Context) {
	zone := clock.Zone()
	if p.loc.String() != zone.String() {
		log.Printf("monthly capacity: poller zone %s differs from platform zone %s; the monthly gate follows the platform zone", p.loc, zone)
	}

	period, run := monthlyCapacityPeriod(clock.Now(), zone)
	if !run {
		return
	}
	p.callMonthlyCapacityCalculator(ctx, period)
}

// callMonthlyCapacityCalculator calls the tier-1 port for one period and logs
// the outcome. Split out from runMonthlyCapacityStep so this half -- the part
// that actually calls the calculator and decides what to log -- is testable
// with a fake and a fixed period, with no clock involved (design.md D2).
// Errors are logged, never fatal: the same "errors are logged, isolated"
// pattern processChargingData and recalculateAnalytics already use -- a missed
// month self-heals next month, and RD9's manual tool covers backfill.
func (p *processor) callMonthlyCapacityCalculator(ctx context.Context, period time.Time) {
	report, err := p.monthlyCapacityCalculator.Calculate(ctx, period, nil)
	if err != nil {
		log.Printf("monthly capacity: period %s: %v", period.Format("2006-01"), err)
		return
	}
	log.Printf("monthly capacity: period %s: %d vehicle(s) found, %d measured, %d thin",
		period.Format("2006-01"), report.VehiclesFound, report.Measured, report.Thin)
}
