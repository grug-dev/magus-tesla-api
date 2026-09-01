package telemetry

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

func TestFormatFailures(t *testing.T) {
	if got := formatFailures(nil); got != "" {
		t.Errorf("empty map should render empty, got %q", got)
	}
	// Deterministic, reason-sorted output regardless of map order.
	got := formatFailures(map[Reason]int{ReasonUnauthorized: 2, ReasonAPIError: 1})
	if got != "api-error=1 unauthorized=2" {
		t.Errorf("want sorted failures, got %q", got)
	}
}

// TestLogCycle_RelabeledLine implements Wave 5 task 5.2: it asserts LogCycle's
// relabeled line (RM36-telemetry-add-poll-runs design D7/D8) prints
// vehicles_attempted/vehicles_succeeded (not bare attempted=/succeeded=), plus the
// new account-grain, Tesla-API-call, and duration fields. Captures log.Printf
// output via log.SetOutput to a buffer, mirroring
// internal/gateway/handlers/supercharger_test.go's existing pattern.
func TestLogCycle_RelabeledLine(t *testing.T) {
	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	}()

	report := CycleReport{
		Attempted:                3,
		Succeeded:                2,
		FailuresByReason:         map[Reason]int{ReasonAPIError: 1},
		ChargingSessionsUpserted: 5,
		ChargingFetchFailures:    1,
		ConfigCaptureFailures:    0,
		TeslaAPICalls:            9,
		AccountsAttempted:        2,
		AccountsSucceeded:        1,
		AccountsFailed:           1,
		Duration:                 42 * time.Second,
	}
	LogCycle(report, nil)

	out := buf.String()

	// The vehicle-grain labels are relabeled unambiguously (design D8's own point:
	// the ticket complained an operator could not tell which grain "attempted"
	// counted).
	if !strings.Contains(out, "vehicles_attempted=3") {
		t.Errorf("want vehicles_attempted=3 in the log line, got %q", out)
	}
	if !strings.Contains(out, "vehicles_succeeded=2") {
		t.Errorf("want vehicles_succeeded=2 in the log line, got %q", out)
	}
	// The bare, unqualified labels must be gone — a leading space before
	// "attempted"/"succeeded" would mean a standalone (non-vehicles_/accounts_-
	// prefixed) field still exists on the line.
	if strings.Contains(out, " attempted=") || strings.Contains(out, " succeeded=") {
		t.Errorf("want no bare attempted=/succeeded= label, got %q", out)
	}

	// New account-grain counts (design D7/roadmap D5).
	if !strings.Contains(out, "accounts_attempted=2") {
		t.Errorf("want accounts_attempted=2 in the log line, got %q", out)
	}
	if !strings.Contains(out, "accounts_succeeded=1") {
		t.Errorf("want accounts_succeeded=1 in the log line, got %q", out)
	}
	if !strings.Contains(out, "accounts_failed=1") {
		t.Errorf("want accounts_failed=1 in the log line, got %q", out)
	}

	// New Tesla API call count (design D7/roadmap D2).
	if !strings.Contains(out, "tesla_api_calls=9") {
		t.Errorf("want tesla_api_calls=9 in the log line, got %q", out)
	}

	// New run duration (design D6).
	if !strings.Contains(out, "duration=42s") {
		t.Errorf("want duration=42s in the log line, got %q", out)
	}
}
