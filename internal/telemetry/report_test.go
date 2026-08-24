package telemetry

import "testing"

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
