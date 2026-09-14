package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/analytics"
	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// --- Fixtures P1/P2: buildPollRun, pure — no fakes needed (design.md Test
// Contract, RM36-app-record-poll-run) ---

// TestBuildPollRun_SuccessfulRun is Fixture P1: a representative successful
// run's field-by-field mapping onto telemetry.PollRun.
func TestBuildPollRun_SuccessfulRun(t *testing.T) {
	fixedRunID := uuid.New()
	run := telemetry.RunContext{RunID: fixedRunID, TriggeredBy: telemetry.TriggeredByScheduler}
	report := telemetry.CycleReport{
		Attempted:                3,
		Succeeded:                2,
		FailuresByReason:         map[telemetry.Reason]int{telemetry.ReasonUnauthorized: 1},
		AccountsAttempted:        2,
		AccountsSucceeded:        1,
		AccountsFailed:           1,
		TeslaAPICalls:            5,
		ChargingSessionsUpserted: 4,
		ChargingFetchFailures:    0,
		ConfigCaptureFailures:    1,
	}
	start := time.Date(2026, 8, 31, 3, 30, 0, 0, time.UTC)
	finish := start.Add(2500 * time.Millisecond)

	got := buildPollRun(run, report, start, finish)

	want := telemetry.PollRun{
		RunID:                    fixedRunID,
		TriggeredBy:              telemetry.TriggeredByScheduler,
		StartedAt:                start,
		FinishedAt:               finish,
		DurationSeconds:          2.5,
		AccountsAttempted:        2,
		AccountsSucceeded:        1,
		AccountsFailed:           1,
		VehiclesAttempted:        3,
		VehiclesSucceeded:        2,
		FailuresAsleepTimeout:    0,
		FailuresUnauthorized:     1,
		FailuresAPIError:         0,
		TeslaAPICalls:            5,
		ChargingSessionsUpserted: 4,
		ChargingFetchFailures:    0,
		ConfigCaptureFailures:    1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildPollRun() = %+v, want %+v", got, want)
	}
}

// TestBuildPollRun_WholeCycleFailureShape is Fixture P2: the step-1
// whole-cycle-failure shape, mirroring tier 1's own Fixture 5b — every count
// field 0, including the three FailuresByReason lookups reading a nil map
// safely (no panic), while StartedAt/FinishedAt/DurationSeconds and
// RunID/TriggeredBy still copy through unchanged.
func TestBuildPollRun_WholeCycleFailureShape(t *testing.T) {
	fixedRunID := uuid.New()
	run := telemetry.RunContext{RunID: fixedRunID, TriggeredBy: telemetry.TriggeredByScheduler}
	report := telemetry.CycleReport{} // zero value — CollectAll's early-return branch
	start := time.Date(2026, 8, 31, 3, 30, 0, 0, time.UTC)
	finish := start.Add(150 * time.Millisecond)

	got := buildPollRun(run, report, start, finish)

	want := telemetry.PollRun{
		RunID:           fixedRunID,
		TriggeredBy:     telemetry.TriggeredByScheduler,
		StartedAt:       start,
		FinishedAt:      finish,
		DurationSeconds: 0.15,
		// every other field left at its zero value — the all-zero-counts shape.
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildPollRun() = %+v, want %+v", got, want)
	}
}

// --- Fake roster for Fixtures P3-P5 (design D9): one fake per Processor
// collaborator. Only fakeCollector, fakeRunWriter and fakeAccountEmpty need
// real behavior; an empty vehicle list makes steps 2 and 3 no-ops, so every
// other fake below only needs to compile and stay out of the way. ---

// fakeCollector satisfies telemetry.Collector. Records the RunContext it was
// called with and returns a configurable (CycleReport, error).
type fakeCollector struct {
	report telemetry.CycleReport
	err    error
	gotRun telemetry.RunContext
}

func (f *fakeCollector) CollectAll(_ context.Context, run telemetry.RunContext) (telemetry.CycleReport, error) {
	f.gotRun = run
	return f.report, f.err
}

var _ telemetry.Collector = (*fakeCollector)(nil)

// fakeRunWriter satisfies telemetry.RunWriter. Records the PollRun it was
// called with, increments a call counter, and returns a configurable error.
type fakeRunWriter struct {
	err   error
	got   telemetry.PollRun
	calls int
}

func (f *fakeRunWriter) RecordRun(_ context.Context, run telemetry.PollRun) error {
	f.got = run
	f.calls++
	return f.err
}

var _ telemetry.RunWriter = (*fakeRunWriter)(nil)

// fakeAccountEmpty satisfies account.Service (9 methods). AllRegisteredVehicles
// returns (nil, nil) and records whether it was called — Fixture P4 asserts it
// is NOT called on the whole-cycle-failure path, proving the short-circuit
// still holds. The other 8 methods are unreachable given the empty vehicle
// list and stub to their types' zero values (design D9).
type fakeAccountEmpty struct {
	allRegisteredVehiclesCalled bool
}

func (f *fakeAccountEmpty) UpsertFromOAuth(_ context.Context, _ account.OAuthIdentity) (account.Account, error) {
	return account.Account{}, nil
}

func (f *fakeAccountEmpty) SaveTeslaTokens(_ context.Context, _ uuid.UUID, _ account.TeslaTokens) error {
	return nil
}

func (f *fakeAccountEmpty) AccessTokenFor(_ context.Context, _ uuid.UUID) (string, error) {
	return "", nil
}

func (f *fakeAccountEmpty) RegisteredVehicles(_ context.Context, _ uuid.UUID) ([]account.Vehicle, error) {
	return nil, nil
}

func (f *fakeAccountEmpty) AllRegisteredVehicles(_ context.Context) ([]account.OwnedVehicle, error) {
	f.allRegisteredVehiclesCalled = true
	return nil, nil
}

func (f *fakeAccountEmpty) SeedVehicles(_ context.Context, _ uuid.UUID, _ []account.SeedVehicle) ([]account.Vehicle, error) {
	return nil, nil
}

func (f *fakeAccountEmpty) SetVehicleConfigIfEmpty(_ context.Context, _ uuid.UUID, _ int64, _, _ string) error {
	return nil
}

func (f *fakeAccountEmpty) LanguageFor(_ context.Context, _ uuid.UUID) (string, error) {
	return "", nil
}

func (f *fakeAccountEmpty) SetLanguage(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}

// PreferencesFor / ThemeFor / SetTheme satisfy the widened account.Service
// (RM42 tier 1). The processor never reads a user preference, so all three are
// inert stubs.
func (f *fakeAccountEmpty) PreferencesFor(_ context.Context, _ uuid.UUID) (account.Settings, error) {
	return account.Settings{}, nil
}

func (f *fakeAccountEmpty) ThemeFor(_ context.Context, _ uuid.UUID) (string, error) {
	return "", nil
}

func (f *fakeAccountEmpty) SetTheme(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}

// AnalysisStartDateFor satisfies the widened account.Service (RM49 tier 1).
// The processor never reads the analysis start date, so this is an inert stub.
func (f *fakeAccountEmpty) AnalysisStartDateFor(_ context.Context, _ uuid.UUID) (time.Time, error) {
	return time.Time{}, nil
}

var _ account.Service = (*fakeAccountEmpty)(nil)

// fakeSuperchargerHistoryReader satisfies telemetry.SuperchargerHistoryReader. Every method
// is unreachable in Fixtures P3-P5 (an empty vehicle list means the
// per-account loop that would call it never iterates) and stubs to its zero
// value (design D9).
type fakeSuperchargerHistoryReader struct{}

func (fakeSuperchargerHistoryReader) SuperchargerHistoryByVehicle(_ context.Context, _ int64, _ int) ([]telemetry.SuperchargerHistory, error) {
	return nil, nil
}

func (fakeSuperchargerHistoryReader) SuperchargerHistoryByVehicleBetween(_ context.Context, _ int64, _, _ time.Time) ([]telemetry.SuperchargerHistory, error) {
	return nil, nil
}

func (fakeSuperchargerHistoryReader) SuperchargerHistoryByVehicleUpdatedSince(_ context.Context, _ int64, _ time.Time) ([]telemetry.SuperchargerHistory, error) {
	return nil, nil
}

var _ telemetry.SuperchargerHistoryReader = fakeSuperchargerHistoryReader{}

// fakeSessionWriter satisfies charging.SessionWriter. Unreachable in
// Fixtures P3-P5 (design D9).
type fakeSessionWriter struct{}

func (fakeSessionWriter) MirrorSessions(_ context.Context, _ []charging.SessionMirror) error {
	return nil
}

var _ charging.SessionWriter = fakeSessionWriter{}

// fakeRecalculator satisfies analytics.Recalculator. Unreachable in
// Fixtures P3-P5 (design D9).
type fakeRecalculator struct{}

func (fakeRecalculator) Recalculate(_ context.Context, _ uuid.UUID, _ int64, _, _ time.Time) error {
	return nil
}

func (fakeRecalculator) Reconcile(_ context.Context, _ uuid.UUID, _ int64) error {
	return nil
}

var _ analytics.Recalculator = fakeRecalculator{}

// fakeAnalyticsReader satisfies analytics.Reader. Unreachable in
// Fixtures P3-P5 (design D9).
type fakeAnalyticsReader struct{}

func (fakeAnalyticsReader) RecentEfficiency(_ context.Context, _ uuid.UUID, _ int64) (analytics.Efficiency, bool, error) {
	return analytics.Efficiency{}, false, nil
}

func (fakeAnalyticsReader) ConsumedByDay(_ context.Context, _ uuid.UUID, _ int64, _, _ time.Time) ([]analytics.DayConsumption, error) {
	return nil, nil
}

func (fakeAnalyticsReader) OdometerDeltaByDay(_ context.Context, _ uuid.UUID, _ int64, _, _ time.Time) ([]analytics.DayDistance, error) {
	return nil, nil
}

func (fakeAnalyticsReader) LatestMetricsByAccount(_ context.Context, _ uuid.UUID) ([]analytics.VehicleStatus, error) {
	return nil, nil
}

func (fakeAnalyticsReader) BatteryLevelByDay(_ context.Context, _ uuid.UUID, _ int64, _, _ time.Time) ([]analytics.DayBattery, error) {
	return nil, nil
}

var _ analytics.Reader = fakeAnalyticsReader{}

// fakeGapWriter satisfies analytics.GapWriter. Unreachable in Fixtures P3-P5
// (design D9).
type fakeGapWriter struct{}

func (fakeGapWriter) ReconcileWindow(_ context.Context, _ int64, _, _ time.Time, _ []analytics.ChargeGap) error {
	return nil
}

var _ analytics.GapWriter = fakeGapWriter{}

// newTestProcessor wires the fake roster above into a Processor, mirroring
// NewProcessor's real parameter order (design D1). monthlyCapacityCalculator
// sits right before acct, matching NewProcessor's own position for the new
// charging port (RM52 tier 2 design.md D3).
func newTestProcessor(collector telemetry.Collector, runWriter telemetry.RunWriter, monthlyCapacityCalculator charging.MonthlyCapacityCalculator, acct account.Service) Processor {
	return NewProcessor(
		collector,
		fakeSuperchargerHistoryReader{},
		runWriter,
		fakeSessionWriter{},
		&fakeMirrorWatermarkStore{},
		monthlyCapacityCalculator,
		acct,
		fakeRecalculator{},
		fakeAnalyticsReader{},
		fakeGapWriter{},
		time.UTC,
	)
}

// --- Fixtures P3-P5: ProcessVehicleData, via NewProcessor + the fake roster ---

// TestProcessVehicleData_SuccessfulRunRecordsOneRow is Fixture P3: a
// successful 3-step run records exactly one poll_runs row.
func TestProcessVehicleData_SuccessfulRunRecordsOneRow(t *testing.T) {
	collector := &fakeCollector{
		report: telemetry.CycleReport{
			Attempted:        1,
			Succeeded:        1,
			FailuresByReason: map[telemetry.Reason]int{},
			TeslaAPICalls:    2,
		},
	}
	acct := &fakeAccountEmpty{}
	runWriter := &fakeRunWriter{}
	monthlyCapacity := &fakeMonthlyCapacityCalculator{}
	p := newTestProcessor(collector, runWriter, monthlyCapacity, acct)

	before := time.Now()
	report, err := p.ProcessVehicleData(context.Background(), telemetry.TriggeredByScheduler)
	after := time.Now()

	if err != nil {
		t.Fatalf("ProcessVehicleData() error = %v, want nil", err)
	}
	if report.Attempted != 1 || report.Succeeded != 1 {
		t.Errorf("report = %+v, want Attempted=1 Succeeded=1 (the fake's report, unmodified except .Duration)", report)
	}
	if report.Duration < 0 {
		t.Errorf("report.Duration = %v, want >= 0", report.Duration)
	}

	if runWriter.calls != 1 {
		t.Fatalf("runWriter.calls = %d, want 1", runWriter.calls)
	}
	got := runWriter.got

	if got.StartedAt.Before(before) || got.StartedAt.After(after) {
		t.Errorf("RecordRun's StartedAt = %v, want within [%v, %v]", got.StartedAt, before, after)
	}
	if got.FinishedAt.Before(before) || got.FinishedAt.After(after) {
		t.Errorf("RecordRun's FinishedAt = %v, want within [%v, %v]", got.FinishedAt, before, after)
	}
	if wantDuration := got.FinishedAt.Sub(got.StartedAt).Seconds(); got.DurationSeconds != wantDuration {
		t.Errorf("DurationSeconds = %v, want %v (FinishedAt.Sub(StartedAt).Seconds())", got.DurationSeconds, wantDuration)
	}
	if got.RunID != collector.gotRun.RunID {
		t.Errorf("RecordRun's RunID = %v, want %v (CollectAll's RunContext.RunID) — same run identity must flow through both calls", got.RunID, collector.gotRun.RunID)
	}
	if got.VehiclesAttempted != 1 || got.VehiclesSucceeded != 1 || got.TeslaAPICalls != 2 {
		t.Errorf("got = %+v, want VehiclesAttempted=1 VehiclesSucceeded=1 TeslaAPICalls=2", got)
	}
	if !acct.allRegisteredVehiclesCalled {
		t.Error("AllRegisteredVehicles was not called; want steps 2/3 to have run after a successful step 1")
	}
}

// TestProcessVehicleData_WholeCycleFailureStillRecordsRow is Fixture P4: the
// step-1 whole-cycle-failure path still records an all-zero-counts row, with
// real start/finish/duration — the case roadmap D1 exists for.
func TestProcessVehicleData_WholeCycleFailureStillRecordsRow(t *testing.T) {
	errBoom := errors.New("boom")
	collector := &fakeCollector{report: telemetry.CycleReport{}, err: errBoom}
	acct := &fakeAccountEmpty{}
	runWriter := &fakeRunWriter{}
	monthlyCapacity := &fakeMonthlyCapacityCalculator{}
	p := newTestProcessor(collector, runWriter, monthlyCapacity, acct)

	report, err := p.ProcessVehicleData(context.Background(), telemetry.TriggeredByScheduler)

	if !errors.Is(err, errBoom) {
		t.Errorf("ProcessVehicleData() error = %v, want errBoom (the exact error CollectAll produced, unwrapped and unchanged)", err)
	}
	if report.Attempted != 0 || report.Succeeded != 0 {
		t.Errorf("report = %+v, want the zero-value report (Attempted=0 Succeeded=0)", report)
	}

	if runWriter.calls != 1 {
		t.Fatalf("runWriter.calls = %d, want 1 (the row is recorded despite the failure)", runWriter.calls)
	}
	got := runWriter.got

	if got.AccountsAttempted != 0 || got.AccountsSucceeded != 0 || got.AccountsFailed != 0 ||
		got.VehiclesAttempted != 0 || got.VehiclesSucceeded != 0 ||
		got.FailuresAsleepTimeout != 0 || got.FailuresUnauthorized != 0 || got.FailuresAPIError != 0 ||
		got.TeslaAPICalls != 0 || got.ChargingSessionsUpserted != 0 || got.ChargingFetchFailures != 0 ||
		got.ConfigCaptureFailures != 0 {
		t.Errorf("got = %+v, want every count field 0 (Fixture P2's exact shape)", got)
	}
	if got.StartedAt.IsZero() || got.FinishedAt.IsZero() {
		t.Errorf("got = %+v, want non-zero StartedAt/FinishedAt", got)
	}
	if report.Duration.Seconds() != got.DurationSeconds {
		t.Errorf("report.Duration.Seconds() = %v, RecordRun's DurationSeconds = %v, want equal (same start/finish feed both)", report.Duration.Seconds(), got.DurationSeconds)
	}

	if acct.allRegisteredVehiclesCalled {
		t.Error("AllRegisteredVehicles was called; want steps 2/3 skipped on the step-1 whole-cycle-failure path (short-circuit preserved)")
	}
	if monthlyCapacity.calls != 0 {
		t.Errorf("monthlyCapacityCalculator.calls = %d, want 0 (Test Contract C1: step 4 is skipped by the same short-circuit as steps 2/3)", monthlyCapacity.calls)
	}
}

// TestProcessVehicleData_RecordRunFailureDoesNotMaskCycleOutcome is
// Fixture P5: a RecordRun failure is logged and does NOT propagate —
// ProcessVehicleData still returns step 1's own (report, err).
func TestProcessVehicleData_RecordRunFailureDoesNotMaskCycleOutcome(t *testing.T) {
	collector := &fakeCollector{
		report: telemetry.CycleReport{
			Attempted:        1,
			Succeeded:        1,
			FailuresByReason: map[telemetry.Reason]int{},
		},
	}
	acct := &fakeAccountEmpty{}
	errRunWriterDown := errors.New("run writer down")
	runWriter := &fakeRunWriter{err: errRunWriterDown}
	monthlyCapacity := &fakeMonthlyCapacityCalculator{}
	p := newTestProcessor(collector, runWriter, monthlyCapacity, acct)

	report, err := p.ProcessVehicleData(context.Background(), telemetry.TriggeredByScheduler)

	if err != nil {
		t.Errorf("ProcessVehicleData() error = %v, want nil (RecordRun's failure must not propagate)", err)
	}
	if report.Attempted != 1 || report.Succeeded != 1 {
		t.Errorf("report = %+v, want Attempted=1 Succeeded=1 (the collector's real outcome, untouched)", report)
	}
	if runWriter.calls != 1 {
		t.Errorf("runWriter.calls = %d, want 1 (the attempt to record was still made, not skipped pre-emptively)", runWriter.calls)
	}
}

// --- Fixtures T-app-1..T-app-3: the watermark-bounded Supercharger mirror
// (RM44-platform-add-mirror-watermark) ---

// fakeMirrorWatermarkStore satisfies charging.MirrorWatermarkStore. It keeps one
// cursor per vehicle in memory and counts advance calls, so a test can tell
// "advanced to the same value" apart from "never advanced at all". Every vehicle
// starts at seed; cursors then holds whatever each one advanced to. advancedFor
// records the vehicle of each advance, so a test can prove one car's cursor moved
// while another's did not.
type fakeMirrorWatermarkStore struct {
	seed         time.Time
	cursors      map[int64]time.Time
	advanceCalls int
	advancedTo   []time.Time
	advancedFor  []int64
	advanceErr   error
}

// cursorFor is what the store would hand the mirror for teslaID right now.
func (f *fakeMirrorWatermarkStore) cursorFor(teslaID int64) time.Time {
	if c, ok := f.cursors[teslaID]; ok {
		return c
	}
	return f.seed
}

func (f *fakeMirrorWatermarkStore) MirrorWatermark(_ context.Context, teslaID int64) (time.Time, error) {
	return f.cursorFor(teslaID), nil
}

func (f *fakeMirrorWatermarkStore) AdvanceMirrorWatermark(_ context.Context, teslaID int64, observed time.Time) error {
	f.advanceCalls++
	f.advancedTo = append(f.advancedTo, observed)
	f.advancedFor = append(f.advancedFor, teslaID)
	if f.advanceErr != nil {
		return f.advanceErr
	}
	if f.cursors == nil {
		f.cursors = make(map[int64]time.Time)
	}
	f.cursors[teslaID] = observed
	return nil
}

var _ charging.MirrorWatermarkStore = (*fakeMirrorWatermarkStore)(nil)

// stubSuperchargerHistoryReader answers per vehicle and records what it was
// asked for, so a test can assert the window the mirror requested, which
// vehicles it fanned out over, and what it did with the answers. A tesla_id
// missing from byVehicle returns nothing, which is the normal "no new sessions"
// case. failOn makes that one vehicle's read fail.
type stubSuperchargerHistoryReader struct {
	fakeSuperchargerHistoryReader
	byVehicle    map[int64][]telemetry.SuperchargerHistory
	failOn       int64
	sinceSeen    []time.Time
	vehiclesSeen []int64
}

func (s *stubSuperchargerHistoryReader) SuperchargerHistoryByVehicleUpdatedSince(_ context.Context, teslaID int64, since time.Time) ([]telemetry.SuperchargerHistory, error) {
	s.sinceSeen = append(s.sinceSeen, since)
	s.vehiclesSeen = append(s.vehiclesSeen, teslaID)
	if s.failOn != 0 && teslaID == s.failOn {
		return nil, errors.New("read boom")
	}
	return s.byVehicle[teslaID], nil
}

var _ telemetry.SuperchargerHistoryReader = (*stubSuperchargerHistoryReader)(nil)

// failingSessionWriter satisfies charging.SessionWriter and always fails, for the
// partial-failure path in T-app-3.
type failingSessionWriter struct{}

func (failingSessionWriter) MirrorSessions(_ context.Context, _ []charging.SessionMirror) error {
	return errors.New("mirror boom")
}

var _ charging.SessionWriter = failingSessionWriter{}

// fakeAccountOneAccount reuses fakeAccountEmpty's eight unreachable methods and
// overrides only AllRegisteredVehicles, so the mirror loop runs for exactly one
// account. teslaIDs are that account's registered cars; empty means one car, 1.
// Several cars on one account is the case the fan-out exists for.
type fakeAccountOneAccount struct {
	*fakeAccountEmpty
	accountID uuid.UUID
	teslaIDs  []int64
}

func (f *fakeAccountOneAccount) AllRegisteredVehicles(_ context.Context) ([]account.OwnedVehicle, error) {
	ids := f.teslaIDs
	if len(ids) == 0 {
		ids = []int64{1}
	}
	owned := make([]account.OwnedVehicle, 0, len(ids))
	for _, id := range ids {
		owned = append(owned, account.OwnedVehicle{
			AccountID: f.accountID,
			TeslaID:   id,
			VIN:       fmt.Sprintf("VIN%d", id),
		})
	}
	return owned, nil
}

var _ account.Service = (*fakeAccountOneAccount)(nil)

// newMirrorTestProcessor wires only the collaborators processChargingData touches.
// The rest of the roster stays the package's shared no-op fakes.
func newMirrorTestProcessor(
	reader telemetry.SuperchargerHistoryReader,
	writer charging.SessionWriter,
	watermarks charging.MirrorWatermarkStore,
	acct account.Service,
) *processor {
	return &processor{
		collector:                 nil,
		superchargerHistoryReader: reader,
		runWriter:                 nil,
		sessionWriter:             writer,
		mirrorWatermarks:          watermarks,
		monthlyCapacityCalculator: &fakeMonthlyCapacityCalculator{},
		acct:                      acct,
		recalculator:              fakeRecalculator{},
		analyticsReader:           fakeAnalyticsReader{},
		gapWriter:                 fakeGapWriter{},
		loc:                       time.UTC,
	}
}

// session builds a telemetry.SuperchargerHistory carrying only the fields the
// mirror maps plus the updated_at the watermark rule turns on. It carries no
// account: the mirror takes that from the pass being run.
func session(sessionID, teslaID int64, updatedAt time.Time) telemetry.SuperchargerHistory {
	return telemetry.SuperchargerHistory{
		SessionID: sessionID,
		TeslaID:   teslaID,
		VIN:       fmt.Sprintf("VIN%d", teslaID),
		UpdatedAt: updatedAt,
	}
}

// TestProcessChargingData_EmptyReadLeavesWatermarkUntouched is T-app-1. A run
// whose bounded read returns zero rows must not advance the watermark. Advancing
// it would push the cursor past a row that commits a moment later, and that row
// would never be mirrored again.
func TestProcessChargingData_EmptyReadLeavesWatermarkUntouched(t *testing.T) {
	accountID := uuid.New()
	x := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	watermarks := &fakeMirrorWatermarkStore{seed: x}
	reader := &stubSuperchargerHistoryReader{}
	p := newMirrorTestProcessor(reader, fakeSessionWriter{}, watermarks,
		&fakeAccountOneAccount{fakeAccountEmpty: &fakeAccountEmpty{}, accountID: accountID, teslaIDs: []int64{11, 22}})

	p.processChargingData(context.Background())

	if watermarks.advanceCalls != 0 {
		t.Errorf("AdvanceMirrorWatermark called %d time(s), want 0", watermarks.advanceCalls)
	}
	for _, teslaID := range []int64{11, 22} {
		if got := watermarks.cursorFor(teslaID); !got.Equal(x) {
			t.Errorf("vehicle %d watermark = %v, want it unchanged at %v", teslaID, got, x)
		}
	}

	// Every registered car gets its own pass.
	if got := reader.vehiclesSeen; !slices.Equal(got, []int64{11, 22}) {
		t.Errorf("vehicles read = %v, want [11 22]", got)
	}

	// Each read must also be bounded, not a full sweep: one overlap before X.
	wantSince := x.Add(-mirrorOverlap)
	for i, got := range reader.sinceSeen {
		if !got.Equal(wantSince) {
			t.Errorf("read %d since = %v, want %v", i, got, wantSince)
		}
	}
	if len(reader.sinceSeen) != 2 {
		t.Errorf("reads = %d, want 2 (one per car)", len(reader.sinceSeen))
	}
}

// TestProcessChargingData_AdvancesWatermarkToMaxObserved is T-app-2. Each vehicle
// advances to the highest updated_at that vehicle actually returned — never to
// clock.Now(), never to a lower row's value, and never to another car's value.
func TestProcessChargingData_AdvancesWatermarkToMaxObserved(t *testing.T) {
	accountID := uuid.New()
	a := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	b := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	c := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)

	watermarks := &fakeMirrorWatermarkStore{seed: a}
	// Car 11's rows arrive out of order, so passing cannot depend on B arriving
	// last. Car 22 carries the later time C. Each car owns its own cursor, so
	// car 11 must stop at B and must NOT be dragged up to C.
	reader := &stubSuperchargerHistoryReader{byVehicle: map[int64][]telemetry.SuperchargerHistory{
		11: {session(1, 11, b), session(3, 11, a)},
		22: {session(2, 22, c)},
	}}
	p := newMirrorTestProcessor(reader, fakeSessionWriter{}, watermarks,
		&fakeAccountOneAccount{fakeAccountEmpty: &fakeAccountEmpty{}, accountID: accountID, teslaIDs: []int64{11, 22}})

	p.processChargingData(context.Background())

	if watermarks.advanceCalls != 2 {
		t.Fatalf("AdvanceMirrorWatermark called %d time(s), want 2 (one per car)", watermarks.advanceCalls)
	}
	if got := watermarks.cursorFor(11); !got.Equal(b) {
		t.Errorf("vehicle 11 watermark = %v, want its own maximum %v", got, b)
	}
	if got := watermarks.cursorFor(22); !got.Equal(c) {
		t.Errorf("vehicle 22 watermark = %v, want its own maximum %v", got, c)
	}
}

// TestProcessChargingData_FailedMirrorDoesNotAdvanceWatermark is T-app-3. When
// MirrorSessions fails the cursor must stay put, so the next run re-reads the same
// window instead of skipping it.
func TestProcessChargingData_FailedMirrorDoesNotAdvanceWatermark(t *testing.T) {
	accountID := uuid.New()
	x := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	later := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	watermarks := &fakeMirrorWatermarkStore{seed: x}
	reader := &stubSuperchargerHistoryReader{byVehicle: map[int64][]telemetry.SuperchargerHistory{
		1: {session(1, 1, later)},
	}}
	p := newMirrorTestProcessor(reader, failingSessionWriter{}, watermarks,
		&fakeAccountOneAccount{fakeAccountEmpty: &fakeAccountEmpty{}, accountID: accountID})

	p.processChargingData(context.Background())

	if watermarks.advanceCalls != 0 {
		t.Errorf("AdvanceMirrorWatermark called %d time(s), want 0 after a failed mirror", watermarks.advanceCalls)
	}
	if got := watermarks.cursorFor(1); !got.Equal(x) {
		t.Errorf("watermark = %v, want it unchanged at %v", got, x)
	}
}

// TestProcessChargingData_OneVehicleReadFailureSkipsOnlyThatVehicle is T-app-4.
// Each car owns its cursor, so one car's failed read must not stop another car
// from being mirrored — and must still leave the failing car's own cursor where
// it was, so the next run retries that window.
func TestProcessChargingData_OneVehicleReadFailureSkipsOnlyThatVehicle(t *testing.T) {
	accountID := uuid.New()
	x := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	later := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	watermarks := &fakeMirrorWatermarkStore{seed: x}
	writer := &recordingSessionWriter{}
	// Car 11 answers with a row; car 22's read fails.
	reader := &stubSuperchargerHistoryReader{
		byVehicle: map[int64][]telemetry.SuperchargerHistory{11: {session(1, 11, later)}},
		failOn:    22,
	}
	p := newMirrorTestProcessor(reader, writer, watermarks,
		&fakeAccountOneAccount{fakeAccountEmpty: &fakeAccountEmpty{}, accountID: accountID, teslaIDs: []int64{11, 22}})

	p.processChargingData(context.Background())

	if writer.calls != 1 {
		t.Errorf("MirrorSessions called %d time(s), want 1 — the healthy car still mirrors", writer.calls)
	}
	if got := writer.vehiclesWritten; !slices.Equal(got, []int64{11}) {
		t.Errorf("vehicles written = %v, want [11] only", got)
	}
	if got := watermarks.advancedFor; !slices.Equal(got, []int64{11}) {
		t.Errorf("watermarks advanced for %v, want [11] only", got)
	}
	if got := watermarks.cursorFor(11); !got.Equal(later) {
		t.Errorf("vehicle 11 watermark = %v, want %v", got, later)
	}
	if got := watermarks.cursorFor(22); !got.Equal(x) {
		t.Errorf("vehicle 22 watermark = %v, want it unchanged at %v after its read failed", got, x)
	}
}

// TestProcessChargingData_SharedVehicleIsMirroredOnce covers the loop's
// de-duplication. The same car registered to two accounts appears twice in
// AllRegisteredVehicles. Mirroring it once per account would repeat the same
// upserts for the same rows, and would advance one cursor twice.
func TestProcessChargingData_SharedVehicleIsMirroredOnce(t *testing.T) {
	x := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	later := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	watermarks := &fakeMirrorWatermarkStore{seed: x}
	writer := &recordingSessionWriter{}
	reader := &stubSuperchargerHistoryReader{byVehicle: map[int64][]telemetry.SuperchargerHistory{
		7: {session(1, 7, later)},
	}}
	p := newMirrorTestProcessor(reader, writer, watermarks,
		&fakeAccountSharedVehicle{fakeAccountEmpty: &fakeAccountEmpty{}, teslaID: 7})

	p.processChargingData(context.Background())

	if got := reader.vehiclesSeen; !slices.Equal(got, []int64{7}) {
		t.Errorf("vehicles read = %v, want [7] once, not once per account", got)
	}
	if writer.calls != 1 {
		t.Errorf("MirrorSessions called %d time(s), want 1", writer.calls)
	}
	if watermarks.advanceCalls != 1 {
		t.Errorf("AdvanceMirrorWatermark called %d time(s), want 1", watermarks.advanceCalls)
	}
}

// fakeAccountSharedVehicle reports ONE car registered to two different accounts,
// which is what the mirror loop must collapse into a single pass.
type fakeAccountSharedVehicle struct {
	*fakeAccountEmpty
	teslaID int64
}

func (f *fakeAccountSharedVehicle) AllRegisteredVehicles(_ context.Context) ([]account.OwnedVehicle, error) {
	vin := fmt.Sprintf("VIN%d", f.teslaID)
	return []account.OwnedVehicle{
		{AccountID: uuid.New(), TeslaID: f.teslaID, VIN: vin},
		{AccountID: uuid.New(), TeslaID: f.teslaID, VIN: vin},
	}, nil
}

var _ account.Service = (*fakeAccountSharedVehicle)(nil)

// recordingSessionWriter counts MirrorSessions calls and records the vehicle of
// every session written, so a test can assert the mirror wrote nothing, or wrote
// only one car's rows.
type recordingSessionWriter struct {
	calls           int
	vehiclesWritten []int64
}

func (w *recordingSessionWriter) MirrorSessions(_ context.Context, sessions []charging.SessionMirror) error {
	w.calls++
	for _, s := range sessions {
		w.vehiclesWritten = append(w.vehiclesWritten, s.TeslaID)
	}
	return nil
}

var _ charging.SessionWriter = (*recordingSessionWriter)(nil)
