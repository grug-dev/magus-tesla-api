package logging

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
)

// capture redirects the stdlib log output to a buffer for one test and
// restores it afterwards.
func capture(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

func TestNote_RendersTypeMethodMessage(t *testing.T) {
	buf := capture(t)

	Note("Processor", "recalculateAnalytics", "gap reconciliation: %s → %s", "2026-09-01", "2026-09-14")

	if !strings.Contains(buf.String(), "[Processor] [recalculateAnalytics] gap reconciliation: 2026-09-01 → 2026-09-14") {
		t.Fatalf("unexpected log line: %q", buf.String())
	}
}

func TestNote_NoFormatArgs(t *testing.T) {
	buf := capture(t)

	Note("telemetry", "LogCycle", "cycle complete")

	if !strings.Contains(buf.String(), "[telemetry] [LogCycle] cycle complete") {
		t.Fatalf("unexpected log line: %q", buf.String())
	}
}
