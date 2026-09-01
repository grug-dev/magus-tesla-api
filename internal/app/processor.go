package app

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/analytics"
	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/clock"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// processor is the concrete Processor implementation (design.md D10).
type processor struct {
	collector          telemetry.Collector
	superchargerReader telemetry.SuperchargerReader
	runWriter          telemetry.RunWriter
	sessionWriter      charging.SessionWriter
	acct               account.Service
	recalculator       analytics.Recalculator
	analyticsReader    analytics.Reader
	gapWriter          analytics.GapWriter
	loc                *time.Location
}

var _ Processor = (*processor)(nil)

// ProcessVehicleData reproduces reconcilingCollector.CollectAll's exact control flow
// from cmd/poller (design.md D8): generate a fresh RunContext, sync fleet data
// (step 1), and — only if that succeeds — process charging data (step 2) then
// recalculate analytics (step 3). Since RM36-app-record-poll-run tier 2, every
// invocation also measures its own start-to-finish span with internal/clock and
// records exactly one poll_runs summary row via recordRun — on every exit path,
// including the step-1 whole-cycle-failure short-circuit below (design.md D4/D6).
//
// Both preserved properties are load-bearing, not incidental:
//
//   - The short-circuit. If step 1's whole-cycle enumeration fails (e.g.
//     account.AllRegisteredVehicles itself errors), steps 2 and 3 are skipped
//     entirely — exactly reconcilingCollector.CollectAll's existing behavior, and
//     for the same reason: both later steps read data step 1 was supposed to have
//     just written; running them against a cycle that never happened would
//     reconcile against stale or absent input. RM36 tier 2 reshapes the early
//     return into a guarded fall-through (`if err == nil { step2; step3 }`) so both
//     paths share one measurement/record tail, but the short-circuit's own
//     behavior — steps 2/3 run if and only if step 1 succeeded — is unchanged
//     (design.md D4).
//   - The order. Charging-data processing (step 2) runs BEFORE analytics
//     recalculation (step 3) — the same order T6's own design.md D7 established
//     ("propagate data, then derive from it").
func (p *processor) ProcessVehicleData(ctx context.Context, triggeredBy telemetry.TriggeredBy) (telemetry.CycleReport, error) {
	run := telemetry.RunContext{RunID: uuid.New(), TriggeredBy: triggeredBy}
	start := clock.Now()

	report, err := p.collector.CollectAll(ctx, run) // step 1 — sync fleet data
	if err == nil {
		p.processChargingData(ctx)  // step 2 — was newSessionMirrorer
		p.recalculateAnalytics(ctx) // step 3 — was newNightlyReconciler
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

// processChargingData is the "process charging data" step (design.md D6), relocated
// wholesale from cmd/poller/main.go's newSessionMirrorer, which ran BEFORE
// reconciliation inside the same post-cycle work.
//
// What it does: for each distinct account holding a registered vehicle, read that
// account's Supercharger sessions from internal/telemetry and mirror them into
// internal/charging's charge_sessions, so charging owns a queryable record of how a
// vehicle was charged — the window, the site, the energy, the cost — without any
// caller having to compose two modules' ports.
//
// The mapping is field-name-for-field-name with no renames and no derivation (T6
// D7). That is deliberate: it keeps the mirror auditable by inspection, and it is
// why charge_sessions kept telemetry's column names rather than aligning with
// internal/charging's own manual_charge_entries vocabulary.
//
// It CANNOT carry a verified battery percentage, and that is structural rather than
// disciplinary: charging.SessionMirror has no percentage field, so a nightly poll
// overwriting a human's verified reading would not compile. telemetry protects the
// same five columns with a comment; here the type does it.
//
// Why per ACCOUNT and not per vehicle: a Supercharger session is keyed by VIN and
// carries a tesla_id that telemetry re-resolves — NULL when the VIN is not currently
// registered. Enumerating per account mirrors those sessions too, so a vehicle that
// is unregistered and later re-registered does not leave a hole in the ledger.
//
// Why BEFORE reconciliation: reconciliation is the step that reads derived state, so
// this composition refreshes owned records first and derives second. Nothing reads
// charge_sessions yet (T6 D9), so the order is not yet load-bearing — but the moment
// a reader exists it is, and the cheap time to establish it is now rather than in
// the change that adds the reader.
//
// Errors are logged, never fatal, with per-account isolation mirroring the
// reconciler: one account's failure never aborts another's, and a missed mirror
// self-heals next cycle because MirrorSessions is idempotent — it re-reads every
// session and upserts, rather than accumulating. Log lines are prefixed
// "session mirror:" so they stay greppable alongside "metrics reconciliation:" and
// "gap reconciliation:".
func (p *processor) processChargingData(ctx context.Context) {
	vehicles, err := p.acct.AllRegisteredVehicles(ctx)
	if err != nil {
		// Whole-cycle failure, mirroring the reconciler's enumeration-failure shape.
		log.Printf("session mirror: listing vehicles: %v", err)
		return
	}

	// One account may hold several registered vehicles, and the read below is
	// account-wide, so mirroring per vehicle would re-mirror the same sessions
	// once per vehicle. Deduplicate to one pass per account. Order is not
	// significant: accounts are independent and each pass is idempotent.
	seen := make(map[uuid.UUID]struct{}, len(vehicles))
	for _, v := range vehicles {
		if _, done := seen[v.AccountID]; done {
			continue
		}
		seen[v.AccountID] = struct{}{}

		// limit 0 means "every session": telemetry's resolveLimit maps a
		// non-positive limit to math.MaxInt32. The mirror is a full
		// reconciliation, not a recent-window sweep, so it must not be capped.
		sessions, err := p.superchargerReader.SuperchargerSessionsByAccount(ctx, v.AccountID, 0)
		if err != nil {
			log.Printf("session mirror: account %s: reading sessions: %v", v.AccountID, err)
			continue
		}
		if len(sessions) == 0 {
			continue
		}

		mirrored := make([]charging.SessionMirror, 0, len(sessions))
		for _, s := range sessions {
			mirrored = append(mirrored, charging.SessionMirror{
				AccountID:           s.AccountID,
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

		if err := p.sessionWriter.MirrorSessions(ctx, v.AccountID, mirrored); err != nil {
			log.Printf("session mirror: account %s: %v", v.AccountID, err)
			continue
		}
		log.Printf("session mirror: account %s: %d session(s)", v.AccountID, len(mirrored))
	}
}

// recalculateAnalytics is the "recalculate analytics" step (design.md D8),
// relocated wholesale from cmd/poller/main.go's newNightlyReconciler, which since
// RM29 tier 3 has TWO halves run per vehicle, in this order:
//
//  1. analytics.Recalculator.Reconcile — advances the module's own precomputed read
//     model, vehicle_metrics, from its three per-source watermarks (design.md D7/D8
//     of the tier-3 change).
//  2. charge-gap reconciliation (D4/D4a, D7b of that change) — recomputes the
//     trailing analytics.GapReconciliationWindow of consumed-per-day figures and
//     hands the flagged days to analytics's own gap writer, which upserts the days
//     that flag and deletes the days that no longer do.
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

	for _, v := range vehicles {
		// Step 1 — advance vehicle_metrics before anything reads it. Reconcile
		// derives its own affected window from its watermarks, so it takes no
		// start/end from here: the [start, end] below is the gap step's trailing
		// window, a different and unrelated question.
		if err := p.recalculator.Reconcile(ctx, v.AccountID, v.TeslaID); err != nil {
			// Per-vehicle isolation. Skips this vehicle's gap step too — see the doc
			// comment: reconciling gaps against metrics we just failed to refresh
			// would delete gap rows on stale evidence.
			log.Printf("metrics reconciliation: vehicle %d: %v", v.TeslaID, err)
			continue
		}

		// Step 2 — charge gaps, now reading the model step 1 just advanced.
		days, err := p.analyticsReader.ConsumedByDay(ctx, v.AccountID, v.TeslaID, start, end)
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
				AccountID:           v.AccountID,
				TeslaID:             v.TeslaID,
				VIN:                 v.VIN,
				Date:                day.Date,
				MissingChargingType: day.MissingChargingType,
			})
		}

		if err := p.gapWriter.ReconcileWindow(ctx, v.AccountID, v.TeslaID, start, end, flagged); err != nil {
			log.Printf("gap reconciliation: vehicle %d: reconcile window: %v", v.TeslaID, err)
			continue
		}
	}
}
