package telemetry

import (
	"fmt"
	"log"
	"sort"
)

// LogCycle emits a single operational line summarizing one collection cycle so an
// unattended poller's nightly outcome is visible in logs (attempted / succeeded /
// failures-by-reason, plus the per-account Supercharger-history outcome), plus a
// separate line for any whole-cycle error. It uses the standard library log package,
// matching the rest of the repo (cmd/web, cmd/poller).
//
// It is exported so cmd/poller (one-shot mode) and Scheduler.Run share the exact same
// report formatter — a future change to the line shape lands in one place and both
// paths update, avoiding silent divergence between the scheduled and on-demand
// collections.
func LogCycle(report CycleReport, err error) {
	if err != nil {
		log.Printf("telemetry cycle: whole-cycle error: %v", err)
	}
	log.Printf("telemetry cycle: attempted=%d succeeded=%d failures={%s} charging_upserted=%d charging_failures=%d config_capture_failures=%d",
		report.Attempted, report.Succeeded, formatFailures(report.FailuresByReason),
		report.ChargingSessionsUpserted, report.ChargingFetchFailures, report.ConfigCaptureFailures)
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
