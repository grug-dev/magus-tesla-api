package telemetry

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// This file tests the four logging decorators in query_log.go against
// RM44-telemetry-add-query-logging design.md's Test Contract (Group A, A.1,
// A.2, and the raw_data half of Group C). Every expected log line below was
// transcribed from design.md D6's tables BEFORE re-reading query_log.go's
// format strings — the assertions pin what the design specifies, not what the
// code happens to print. All tests are pure offline: no DATABASE_URL, no
// network, a fake `inner` per decorated interface.

// captureLog redirects the standard logger to an in-memory buffer for the
// duration of the calling test and strips the default timestamp prefix
// (log.SetFlags(0)) so line assertions are exact rather than fuzzy. Both the
// output and the flags are restored via t.Cleanup. Shared by
// query_log_test.go and call_counter_test.go (both package telemetry).
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	origOut := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(origOut)
		log.SetFlags(origFlags)
	})
	return &buf
}

// --- fakes: one small fake per decorated interface, reused across subtests ---

// fakeQueryLogStore is a minimal fake `store` implementation used only by this
// file's loggingStore tests — deliberately separate from service_test.go's
// fake, so this file compiles and passes independently of that file's fixtures.
type fakeQueryLogStore struct {
	snapshots         []Snapshot
	precedingSnapshot *Snapshot
}

func (f *fakeQueryLogStore) insertSnapshot(context.Context, Snapshot) error   { return nil }
func (f *fakeQueryLogStore) insertPollAttempt(context.Context, Attempt) error { return nil }
func (f *fakeQueryLogStore) latestSnapshotsByAccount(context.Context, uuid.UUID) ([]Snapshot, error) {
	return f.snapshots, nil
}
func (f *fakeQueryLogStore) snapshotsByVehicleSince(context.Context, uuid.UUID, int64, time.Time) ([]Snapshot, error) {
	return f.snapshots, nil
}
func (f *fakeQueryLogStore) snapshotsByVehicleBetween(context.Context, uuid.UUID, int64, time.Time, time.Time) ([]Snapshot, error) {
	return f.snapshots, nil
}
func (f *fakeQueryLogStore) snapshotsByVehicleUpdatedSince(context.Context, uuid.UUID, int64, time.Time) ([]Snapshot, error) {
	return f.snapshots, nil
}
func (f *fakeQueryLogStore) upsertSuperchargerHistory(context.Context, SuperchargerHistory) error {
	return nil
}
func (f *fakeQueryLogStore) snapshotPrecedingDay(context.Context, uuid.UUID, int64, time.Time) (*Snapshot, error) {
	return f.precedingSnapshot, nil
}

var _ store = (*fakeQueryLogStore)(nil)

// fakeQueryLogReader is a minimal fake Reader for loggingReader tests.
type fakeQueryLogReader struct {
	snapshots []Snapshot
	preceding *Snapshot
}

func (f *fakeQueryLogReader) LatestSnapshotsByAccount(context.Context, uuid.UUID) ([]Snapshot, error) {
	return f.snapshots, nil
}
func (f *fakeQueryLogReader) SnapshotsByVehicleSince(context.Context, uuid.UUID, int64, time.Time) ([]Snapshot, error) {
	return f.snapshots, nil
}
func (f *fakeQueryLogReader) SnapshotsByVehicleBetween(context.Context, uuid.UUID, int64, time.Time, time.Time) ([]Snapshot, error) {
	return f.snapshots, nil
}
func (f *fakeQueryLogReader) SnapshotsByVehicleUpdatedSince(context.Context, uuid.UUID, int64, time.Time) ([]Snapshot, error) {
	return f.snapshots, nil
}
func (f *fakeQueryLogReader) SnapshotPrecedingDay(context.Context, uuid.UUID, int64, time.Time) (*Snapshot, error) {
	return f.preceding, nil
}

var _ Reader = (*fakeQueryLogReader)(nil)

// fakeQueryLogSCHReader is a minimal fake SuperchargerHistoryReader for
// loggingSuperchargerHistoryReader tests.
type fakeQueryLogSCHReader struct {
	history []SuperchargerHistory
}

func (f *fakeQueryLogSCHReader) SuperchargerHistoryByAccount(context.Context, uuid.UUID, int) ([]SuperchargerHistory, error) {
	return f.history, nil
}
func (f *fakeQueryLogSCHReader) SuperchargerHistoryByVehicle(context.Context, uuid.UUID, int64, int) ([]SuperchargerHistory, error) {
	return f.history, nil
}
func (f *fakeQueryLogSCHReader) SuperchargerHistoryByVehicleBetween(context.Context, uuid.UUID, int64, time.Time, time.Time) ([]SuperchargerHistory, error) {
	return f.history, nil
}
func (f *fakeQueryLogSCHReader) SuperchargerHistoryByVehicleUpdatedSince(context.Context, uuid.UUID, int64, time.Time) ([]SuperchargerHistory, error) {
	return f.history, nil
}

var _ SuperchargerHistoryReader = (*fakeQueryLogSCHReader)(nil)

// fakeQueryLogRunWriter is a minimal fake RunWriter for loggingRunWriter tests.
type fakeQueryLogRunWriter struct{}

func (f *fakeQueryLogRunWriter) RecordRun(context.Context, PollRun) error { return nil }

var _ RunWriter = (*fakeQueryLogRunWriter)(nil)

// --- fixtures shared across subtests ---

var (
	qlAccountID = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	qlTeslaID   = int64(42)
)

// two-element fixture used for every "many rows" method, per the Test
// Contract's "a fixed, known result (a 2-element slice for the many methods)".
func twoSnapshots() []Snapshot          { return []Snapshot{{}, {}} }
func twoSCHRows() []SuperchargerHistory { return []SuperchargerHistory{{}, {}} }

// ============================================================
// Group A — loggingStore's 3 logged write methods
// ============================================================

func TestLoggingStore_InsertSnapshot_LogsExpectedLine(t *testing.T) {
	buf := captureLog(t)
	l := newLoggingStore(&fakeQueryLogStore{})

	capturedAt := time.Date(2026, 1, 15, 3, 30, 0, 0, time.UTC)
	rawData := []byte("RAWDATA") // len 7

	if err := l.insertSnapshot(context.Background(), Snapshot{
		AccountID:  qlAccountID,
		TeslaID:    qlTeslaID,
		CapturedAt: capturedAt,
		RawData:    rawData,
	}); err != nil {
		t.Fatalf("insertSnapshot: unexpected error: %v", err)
	}

	want := fmt.Sprintf(
		"telemetry query: insertSnapshot account=%s tesla_id=%d captured_at=%s raw_data_bytes=%d\n",
		qlAccountID, qlTeslaID, capturedAt.Format(time.RFC3339), len(rawData))
	if got := buf.String(); got != want {
		t.Fatalf("log line mismatch:\n got:  %q\n want: %q", got, want)
	}
}

func TestLoggingStore_InsertPollAttempt_LogsExpectedLine(t *testing.T) {
	buf := captureLog(t)
	l := newLoggingStore(&fakeQueryLogStore{})

	attemptedAt := time.Date(2026, 2, 1, 3, 31, 0, 0, time.UTC)

	if err := l.insertPollAttempt(context.Background(), Attempt{
		AccountID:   qlAccountID,
		TeslaID:     qlTeslaID,
		AttemptedAt: attemptedAt,
		Outcome:     OutcomeSuccess,
		Reason:      ReasonOK,
	}); err != nil {
		t.Fatalf("insertPollAttempt: unexpected error: %v", err)
	}

	want := fmt.Sprintf(
		"telemetry query: insertPollAttempt account=%s tesla_id=%d attempted_at=%s outcome=%s reason=%s\n",
		qlAccountID, qlTeslaID, attemptedAt.Format(time.RFC3339), OutcomeSuccess, ReasonOK)
	if got := buf.String(); got != want {
		t.Fatalf("log line mismatch:\n got:  %q\n want: %q", got, want)
	}
}

func TestLoggingStore_UpsertSuperchargerHistory_LogsExpectedLine(t *testing.T) {
	buf := captureLog(t)
	l := newLoggingStore(&fakeQueryLogStore{})

	start := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	stop := time.Date(2026, 3, 1, 10, 45, 0, 0, time.UTC)
	rawData := []byte("SESSIONRAW") // len 10
	teslaID := qlTeslaID

	if err := l.upsertSuperchargerHistory(context.Background(), SuperchargerHistory{
		AccountID:           qlAccountID,
		TeslaID:             &teslaID,
		SessionID:           987654,
		ChargeStartDateTime: start,
		ChargeStopDateTime:  stop,
		RawData:             rawData,
	}); err != nil {
		t.Fatalf("upsertSuperchargerHistory: unexpected error: %v", err)
	}

	want := fmt.Sprintf(
		"telemetry query: upsertSuperchargerHistory account=%s tesla_id=%s session_id=%d charge_start=%s charge_stop=%s raw_data_bytes=%d\n",
		qlAccountID, "42", int64(987654), start.Format(time.RFC3339), stop.Format(time.RFC3339), len(rawData))
	if got := buf.String(); got != want {
		t.Fatalf("log line mismatch:\n got:  %q\n want: %q", got, want)
	}
}

// Group A.1 — loggingStore's 5 silent pass-through methods produce ZERO log
// output (design D1). Calls all 5 and asserts buf.Len() == 0 after each.
func TestLoggingStore_SilentReadMethods_ProduceNoOutput(t *testing.T) {
	buf := captureLog(t)
	fake := &fakeQueryLogStore{
		snapshots:         twoSnapshots(),
		precedingSnapshot: &Snapshot{},
	}
	l := newLoggingStore(fake)
	ctx := context.Background()

	if _, err := l.latestSnapshotsByAccount(ctx, qlAccountID); err != nil {
		t.Fatalf("latestSnapshotsByAccount: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("latestSnapshotsByAccount logged output, want silence: %q", buf.String())
	}

	if _, err := l.snapshotsByVehicleSince(ctx, qlAccountID, qlTeslaID, time.Now()); err != nil {
		t.Fatalf("snapshotsByVehicleSince: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("snapshotsByVehicleSince logged output, want silence: %q", buf.String())
	}

	if _, err := l.snapshotsByVehicleBetween(ctx, qlAccountID, qlTeslaID, time.Now(), time.Now()); err != nil {
		t.Fatalf("snapshotsByVehicleBetween: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("snapshotsByVehicleBetween logged output, want silence: %q", buf.String())
	}

	if _, err := l.snapshotsByVehicleUpdatedSince(ctx, qlAccountID, qlTeslaID, time.Now()); err != nil {
		t.Fatalf("snapshotsByVehicleUpdatedSince: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("snapshotsByVehicleUpdatedSince logged output, want silence: %q", buf.String())
	}

	if _, err := l.snapshotPrecedingDay(ctx, qlAccountID, qlTeslaID, time.Now()); err != nil {
		t.Fatalf("snapshotPrecedingDay: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("snapshotPrecedingDay logged output, want silence: %q", buf.String())
	}
}

// ============================================================
// Group A — loggingReader's 5 methods
// ============================================================

func TestLoggingReader_LatestSnapshotsByAccount_LogsExpectedLine(t *testing.T) {
	buf := captureLog(t)
	l := newLoggingReader(&fakeQueryLogReader{snapshots: twoSnapshots()})

	if _, err := l.LatestSnapshotsByAccount(context.Background(), qlAccountID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := fmt.Sprintf("telemetry query: LatestSnapshotsByAccount account=%s rows=%d\n", qlAccountID, 2)
	if got := buf.String(); got != want {
		t.Fatalf("log line mismatch:\n got:  %q\n want: %q", got, want)
	}
}

func TestLoggingReader_SnapshotsByVehicleSince_LogsExpectedLine(t *testing.T) {
	buf := captureLog(t)
	l := newLoggingReader(&fakeQueryLogReader{snapshots: twoSnapshots()})

	since := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	if _, err := l.SnapshotsByVehicleSince(context.Background(), qlAccountID, qlTeslaID, since); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := fmt.Sprintf("telemetry query: SnapshotsByVehicleSince account=%s tesla_id=%d since=%s rows=%d\n",
		qlAccountID, qlTeslaID, since.Format(time.RFC3339), 2)
	if got := buf.String(); got != want {
		t.Fatalf("log line mismatch:\n got:  %q\n want: %q", got, want)
	}
}

func TestLoggingReader_SnapshotsByVehicleBetween_LogsExpectedLine(t *testing.T) {
	buf := captureLog(t)
	l := newLoggingReader(&fakeQueryLogReader{snapshots: twoSnapshots()})

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	if _, err := l.SnapshotsByVehicleBetween(context.Background(), qlAccountID, qlTeslaID, start, end); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := fmt.Sprintf("telemetry query: SnapshotsByVehicleBetween account=%s tesla_id=%d start=%s end=%s rows=%d\n",
		qlAccountID, qlTeslaID, start.Format("2006-01-02"), end.Format("2006-01-02"), 2)
	if got := buf.String(); got != want {
		t.Fatalf("log line mismatch:\n got:  %q\n want: %q", got, want)
	}
}

func TestLoggingReader_SnapshotsByVehicleUpdatedSince_LogsExpectedLine(t *testing.T) {
	buf := captureLog(t)
	l := newLoggingReader(&fakeQueryLogReader{snapshots: twoSnapshots()})

	since := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	if _, err := l.SnapshotsByVehicleUpdatedSince(context.Background(), qlAccountID, qlTeslaID, since); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := fmt.Sprintf("telemetry query: SnapshotsByVehicleUpdatedSince account=%s tesla_id=%d since=%s rows=%d\n",
		qlAccountID, qlTeslaID, since.Format(time.RFC3339), 2)
	if got := buf.String(); got != want {
		t.Fatalf("log line mismatch:\n got:  %q\n want: %q", got, want)
	}
}

func TestLoggingReader_SnapshotPrecedingDay_LogsExpectedLine(t *testing.T) {
	day := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	t.Run("found", func(t *testing.T) {
		buf := captureLog(t)
		l := newLoggingReader(&fakeQueryLogReader{preceding: &Snapshot{}})

		if _, err := l.SnapshotPrecedingDay(context.Background(), qlAccountID, qlTeslaID, day); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		want := fmt.Sprintf("telemetry query: SnapshotPrecedingDay account=%s tesla_id=%d day=%s found=%t\n",
			qlAccountID, qlTeslaID, day.Format("2006-01-02"), true)
		if got := buf.String(); got != want {
			t.Fatalf("log line mismatch:\n got:  %q\n want: %q", got, want)
		}
	})

	t.Run("not found", func(t *testing.T) {
		buf := captureLog(t)
		l := newLoggingReader(&fakeQueryLogReader{preceding: nil})

		if _, err := l.SnapshotPrecedingDay(context.Background(), qlAccountID, qlTeslaID, day); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		want := fmt.Sprintf("telemetry query: SnapshotPrecedingDay account=%s tesla_id=%d day=%s found=%t\n",
			qlAccountID, qlTeslaID, day.Format("2006-01-02"), false)
		if got := buf.String(); got != want {
			t.Fatalf("log line mismatch:\n got:  %q\n want: %q", got, want)
		}
	})
}

// ============================================================
// Group A / A.2 — loggingSuperchargerHistoryReader's 4 methods
// ============================================================

// Group A.2: SuperchargerHistoryByAccount logs the RESOLVED limit
// (resolveLimit(0) == math.MaxInt32), not the caller's raw 0, and the
// literal date_bound=none.
func TestLoggingSuperchargerHistoryReader_ByAccount_LogsResolvedLimit(t *testing.T) {
	cases := []struct {
		name      string
		rawLimit  int
		wantLimit int64
	}{
		{name: "unbounded (limit=0 resolves to math.MaxInt32)", rawLimit: 0, wantLimit: math.MaxInt32},
		{name: "bounded (limit=25 stays 25)", rawLimit: 25, wantLimit: 25},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := captureLog(t)
			l := newLoggingSuperchargerHistoryReader(&fakeQueryLogSCHReader{history: twoSCHRows()})

			if _, err := l.SuperchargerHistoryByAccount(context.Background(), qlAccountID, tc.rawLimit); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			want := fmt.Sprintf("telemetry query: SuperchargerHistoryByAccount account=%s limit=%d date_bound=none rows=%d\n",
				qlAccountID, tc.wantLimit, 2)
			if got := buf.String(); got != want {
				t.Fatalf("log line mismatch:\n got:  %q\n want: %q", got, want)
			}
		})
	}
}

// Group A.2 (continued): SuperchargerHistoryByVehicle also logs the resolved
// limit, without the date_bound field (design D6's table has no date_bound
// column for this method).
func TestLoggingSuperchargerHistoryReader_ByVehicle_LogsResolvedLimit(t *testing.T) {
	cases := []struct {
		name      string
		rawLimit  int
		wantLimit int64
	}{
		{name: "unbounded (limit=0 resolves to math.MaxInt32)", rawLimit: 0, wantLimit: math.MaxInt32},
		{name: "bounded (limit=25 stays 25)", rawLimit: 25, wantLimit: 25},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := captureLog(t)
			l := newLoggingSuperchargerHistoryReader(&fakeQueryLogSCHReader{history: twoSCHRows()})

			if _, err := l.SuperchargerHistoryByVehicle(context.Background(), qlAccountID, qlTeslaID, tc.rawLimit); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			want := fmt.Sprintf("telemetry query: SuperchargerHistoryByVehicle account=%s tesla_id=%d limit=%d rows=%d\n",
				qlAccountID, qlTeslaID, tc.wantLimit, 2)
			if got := buf.String(); got != want {
				t.Fatalf("log line mismatch:\n got:  %q\n want: %q", got, want)
			}
		})
	}
}

func TestLoggingSuperchargerHistoryReader_ByVehicleUpdatedSince_LogsExpectedLine(t *testing.T) {
	buf := captureLog(t)
	l := newLoggingSuperchargerHistoryReader(&fakeQueryLogSCHReader{history: twoSCHRows()})

	since := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	if _, err := l.SuperchargerHistoryByVehicleUpdatedSince(context.Background(), qlAccountID, qlTeslaID, since); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := fmt.Sprintf("telemetry query: SuperchargerHistoryByVehicleUpdatedSince account=%s tesla_id=%d since=%s rows=%d\n",
		qlAccountID, qlTeslaID, since.Format(time.RFC3339), 2)
	if got := buf.String(); got != want {
		t.Fatalf("log line mismatch:\n got:  %q\n want: %q", got, want)
	}
}

func TestLoggingSuperchargerHistoryReader_ByVehicleBetween_LogsExpectedLine(t *testing.T) {
	buf := captureLog(t)
	l := newLoggingSuperchargerHistoryReader(&fakeQueryLogSCHReader{history: twoSCHRows()})

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	if _, err := l.SuperchargerHistoryByVehicleBetween(context.Background(), qlAccountID, qlTeslaID, start, end); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := fmt.Sprintf("telemetry query: SuperchargerHistoryByVehicleBetween account=%s tesla_id=%d start=%s end=%s rows=%d\n",
		qlAccountID, qlTeslaID, start.Format("2006-01-02"), end.Format("2006-01-02"), 2)
	if got := buf.String(); got != want {
		t.Fatalf("log line mismatch:\n got:  %q\n want: %q", got, want)
	}
}

// ============================================================
// Group A — loggingRunWriter's 1 method
// ============================================================

func TestLoggingRunWriter_RecordRun_LogsExpectedLine(t *testing.T) {
	buf := captureLog(t)
	l := newLoggingRunWriter(&fakeQueryLogRunWriter{})

	runID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	if err := l.RecordRun(context.Background(), PollRun{
		RunID:       runID,
		TriggeredBy: TriggeredByScheduler,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := fmt.Sprintf("telemetry query: RecordRun run_id=%s triggered_by=%s\n", runID, TriggeredByScheduler)
	if got := buf.String(); got != want {
		t.Fatalf("log line mismatch:\n got:  %q\n want: %q", got, want)
	}
}

// ============================================================
// Group C (design D5/D9) — raw_data content must never reach a log line
// ============================================================

// TestQueryLog_NeverLogsRawDataContent is the concrete, structural proof
// (not a code-review-only guarantee) that RawData content never reaches a
// query_log.go log line — only its byte count does.
func TestQueryLog_NeverLogsRawDataContent(t *testing.T) {
	buf := captureLog(t)
	l := newLoggingStore(&fakeQueryLogStore{})

	const marker = "MARKER_RAW_DATA_MUST_NOT_APPEAR_IN_LOG"
	rawData := []byte(marker)
	now := time.Date(2026, 3, 1, 3, 30, 0, 0, time.UTC)
	teslaID := qlTeslaID

	if err := l.insertSnapshot(context.Background(), Snapshot{
		AccountID:  qlAccountID,
		TeslaID:    qlTeslaID,
		CapturedAt: now,
		RawData:    rawData,
	}); err != nil {
		t.Fatalf("insertSnapshot: unexpected error: %v", err)
	}

	if err := l.upsertSuperchargerHistory(context.Background(), SuperchargerHistory{
		AccountID:           qlAccountID,
		TeslaID:             &teslaID,
		SessionID:           1,
		ChargeStartDateTime: now,
		ChargeStopDateTime:  now,
		RawData:             rawData,
	}); err != nil {
		t.Fatalf("upsertSuperchargerHistory: unexpected error: %v", err)
	}

	got := buf.String()
	if strings.Contains(got, marker) {
		t.Fatalf("log output leaked raw_data content; got:\n%s", got)
	}

	wantBytesField := fmt.Sprintf("raw_data_bytes=%d", len(rawData))
	if count := strings.Count(got, wantBytesField); count != 2 {
		t.Fatalf("expected %q to appear twice (once per call), got %d times; log:\n%s",
			wantBytesField, count, got)
	}
}
