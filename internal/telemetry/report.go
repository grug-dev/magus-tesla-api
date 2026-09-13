package telemetry

import (
	"fmt"
	"log"
	"sort"
)

// LogCycle emits a single operational line summarizing one collection cycle so an
// unattended poller's nightly outcome is visible in logs (vehicle-grain attempted /
// succeeded / failures-by-reason, the per-account Supercharger-history outcome
// including sessions skipped for an unregistered VIN, the account-grain
// attempt/outcome counts, the Tesla API call count, and the run duration), plus a
// separate line for any whole-cycle error. It uses the standard library log
// package, matching the rest of the repo (cmd/web, cmd/poller).
//
// It is exported so cmd/poller (one-shot mode) and Scheduler.Run share the exact same
// report formatter — a future change to the line shape lands in one place and both
// paths update, avoiding silent divergence between the scheduled and on-demand
// collections.
//
// The vehicle-grain labels are printed as vehicles_attempted/vehicles_succeeded
// (RM36-telemetry-add-poll-runs design D8 / roadmap D7) to disambiguate them from
// the newly-added account-grain accounts_attempted/accounts_succeeded/
// accounts_failed counts on the same line — this is a PRINTED-TEXT change only; the
// underlying CycleReport.Attempted/.Succeeded Go field names are unchanged (see
// report.Attempted/report.Succeeded below), since renaming them would break
// internal/app/scheduler_test.go, a file outside this module's sandbox.
// report.Duration is populated only by the caller (design D6) — CollectAll itself
// leaves it zero, so a caller that never sets it prints duration=0s.
func LogCycle(report CycleReport, err error) {
	if err != nil {
		log.Printf("telemetry cycle: whole-cycle error: %v", err)
	}
	log.Printf("telemetry cycle: vehicles_attempted=%d vehicles_succeeded=%d failures={%s} charging_upserted=%d charging_failures=%d charging_skipped_unregistered=%d config_capture_failures=%d accounts_attempted=%d accounts_succeeded=%d accounts_failed=%d tesla_api_calls=%d duration=%s",
		report.Attempted, report.Succeeded, formatFailures(report.FailuresByReason),
		report.ChargingSessionsUpserted, report.ChargingFetchFailures, report.ChargingSessionsSkippedUnregistered, report.ConfigCaptureFailures,
		report.AccountsAttempted, report.AccountsSucceeded, report.AccountsFailed,
		report.TeslaAPICalls, report.Duration)
}

// formatFailures renders the failures-by-reason map in a stable (reason-sorted) order so
// the log line is deterministic and greppable regardless of Go's map iteration order.
func formatFailures(byReason map[Reason]int) string {
	if len(byReason) == 0 {
		return ""
	}
	reasons := make([]string, 0, len(byReason))
	for r := range byReason {
		reasons = append(reasons, string(r))
	}
	sort.Strings(reasons)
	parts := make([]string, 0, len(reasons))
	for _, r := range reasons {
		parts = append(parts, fmt.Sprintf("%s=%d", r, byReason[Reason(r)]))
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += " " + p
	}
	return out
}
