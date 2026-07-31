package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/tesla"
)

// The tests below exercise CollectAll fully OFFLINE: fake account.Service and
// tesla.VehicleService ports plus an in-memory fake store injected through the
// unexported `store` seam. No DATABASE_URL, no network, and — critically — no Tesla
// API call ever fires (the fakes never touch the real adapter).

// --- fake account.Service (only the two methods CollectAll uses are meaningful) ---

type fakeAccount struct {
	vehicles []account.OwnedVehicle
	// tokens maps an account id to the token AccessTokenFor should return; an account
	// absent from the map yields ErrNoTeslaConnection.
	tokens map[uuid.UUID]string
	// allErr, when set, makes AllRegisteredVehicles fail (whole-cycle failure path).
	allErr error
}

func (f *fakeAccount) AllRegisteredVehicles(_ context.Context) ([]account.OwnedVehicle, error) {
	if f.allErr != nil {
		return nil, f.allErr
	}
	return f.vehicles, nil
}

func (f *fakeAccount) AccessTokenFor(_ context.Context, accountID uuid.UUID) (string, error) {
	if tok, ok := f.tokens[accountID]; ok {
		return tok, nil
	}
	return "", account.ErrNoTeslaConnection
}

// The remaining Service methods are unused by CollectAll; they satisfy the interface.
func (f *fakeAccount) UpsertFromOAuth(context.Context, account.OAuthIdentity) (account.Account, error) {
	return account.Account{}, nil
}
func (f *fakeAccount) SaveTeslaTokens(context.Context, uuid.UUID, account.TeslaTokens) error {
	return nil
}
func (f *fakeAccount) RegisteredVehicles(context.Context, uuid.UUID) ([]account.Vehicle, error) {
	return nil, nil
}
func (f *fakeAccount) SeedVehicles(context.Context, uuid.UUID, []account.SeedVehicle) ([]account.Vehicle, error) {
	return nil, nil
}

// --- fake tesla.VehicleService, programmable per vehicle id ---

// vehicleScript describes how the fake tesla port behaves for one vehicle id.
type vehicleScript struct {
	// state is the State reported by ListVehicles/WakeUp ("online" | "asleep" | ...).
	state string
	// wakesOnline, when true, flips state to "online" after the first WakeUp call, so a
	// vehicle reported asleep in the up-front ListVehicles reaches online during the wake
	// poll — exercising the asleep→wake→fetch branch. It is deliberately distinct from a
	// statically-"online" vehicle (which must never be woken at all, R1-01).
	wakesOnline bool
	// wakeErr, when set, is returned by WakeUp (e.g. tesla.ErrUnauthorized).
	wakeErr error
	// dataErr, when set, is returned by VehicleData. If dataErrOnce is true it is
	// returned only on the first call (to test the one-retry recovery).
	dataErr     error
	dataErrOnce bool
	// data is the DTO returned by VehicleData on success.
	data *tesla.VehicleDataTesla
}

type fakeTesla struct {
	mu      sync.Mutex
	scripts map[int64]*vehicleScript
	// listErr, when set, is returned by every ListVehicles call — used to test the
	// account-wide ListVehicles failure short-circuits (401 → all unauthorized; other →
	// all api-error) without making any per-vehicle Tesla call.
	listErr error
	// call counters for assertions.
	wakeCalls map[int64]int
	dataCalls map[int64]int
	listCalls int
	// chargingHistory, when set, is returned by ChargingHistory for all accounts.
	// If chargingHistoryErr is also set, that error is returned instead.
	chargingHistory    *tesla.ChargingHistoryTesla
	chargingHistoryErr error
}

func newFakeTesla() *fakeTesla {
	return &fakeTesla{
		scripts:   map[int64]*vehicleScript{},
		wakeCalls: map[int64]int{},
		dataCalls: map[int64]int{},
	}
}

func (f *fakeTesla) set(id int64, sc *vehicleScript) {
	f.scripts[id] = sc
}

func (f *fakeTesla) ListVehicles(_ context.Context, _ tesla.Credentials) ([]tesla.VehicleTesla, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []tesla.VehicleTesla
	for id, sc := range f.scripts {
		out = append(out, tesla.VehicleTesla{ID: id, State: sc.state})
	}
	return out, nil
}

func (f *fakeTesla) WakeUp(_ context.Context, _ tesla.Credentials, id int64) (*tesla.VehicleTesla, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wakeCalls[id]++
	sc := f.scripts[id]
	if sc == nil {
		return nil, fmt.Errorf("fakeTesla: no script for vehicle %d", id)
	}
	if sc.wakeErr != nil {
		return nil, sc.wakeErr
	}
	if sc.wakesOnline {
		// A woken vehicle comes online: subsequent ListVehicles (the wake poll) report it.
		sc.state = "online"
	}
	return &tesla.VehicleTesla{ID: id, State: sc.state}, nil
}

func (f *fakeTesla) VehicleData(_ context.Context, _ tesla.Credentials, id int64) (*tesla.VehicleDataTesla, json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dataCalls[id]++
	sc := f.scripts[id]
	if sc == nil {
		return nil, nil, fmt.Errorf("fakeTesla: no script for vehicle %d", id)
	}
	if sc.dataErr != nil {
		// Transient error: on the first call only when dataErrOnce is set, so a retry
		// then succeeds.
		if !sc.dataErrOnce || f.dataCalls[id] == 1 {
			return nil, nil, sc.dataErr
		}
	}
	raw := json.RawMessage(fmt.Sprintf(`{"id":%d}`, id))
	return sc.data, raw, nil
}

// ChargingHistory returns the programmable charging history for B7 tests.
// Default (no fields set) returns an empty history with no error.
func (f *fakeTesla) ChargingHistory(_ context.Context, _ tesla.Credentials, _ tesla.ChargingHistoryParams) (*tesla.ChargingHistoryTesla, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.chargingHistoryErr != nil {
		return nil, f.chargingHistoryErr
	}
	if f.chargingHistory != nil {
		return f.chargingHistory, nil
	}
	return &tesla.ChargingHistoryTesla{}, nil
}

// --- fake store (records what CollectAll would persist) ---

type recordedAttempt struct {
	teslaID int64
	reason  Reason
	outcome Outcome
}

type fakeStore struct {
	mu        sync.Mutex
	snapshots []Snapshot
	attempts  []recordedAttempt
	// snapErr, when set, fails the Nth insertSnapshot (1-based) — used to test the
	// store-error → api-error → retry path.
	snapErr      error
	snapErrOnce  bool
	snapInserts  int
	// upsertedSessions records all SuperchargerSession upserts (B7 tests inspect this).
	upsertedSessions []SuperchargerSession
	// upsertErr, when set, is returned by every upsertSuperchargerSession call.
	upsertErr error
}

func (s *fakeStore) insertSnapshot(_ context.Context, snap Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapInserts++
	if s.snapErr != nil && (!s.snapErrOnce || s.snapInserts == 1) {
		return s.snapErr
	}
	s.snapshots = append(s.snapshots, snap)
	return nil
}

func (s *fakeStore) insertPollAttempt(_ context.Context, a Attempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts = append(s.attempts, recordedAttempt{teslaID: a.TeslaID, reason: a.Reason, outcome: a.Outcome})
	return nil
}

// latestSnapshotsByAccount satisfies the extended store seam but is never called by the
// collection service. It is a no-op stub so fakeStore continues to implement the full
// store interface even after the read method was added in task 3.
func (s *fakeStore) latestSnapshotsByAccount(_ context.Context, _ uuid.UUID) ([]Snapshot, error) {
	return []Snapshot{}, nil
}

// snapshotsByVehicleSince satisfies the store seam added by
// telemetry-add-snapshot-history-read-port. The collection service never calls it;
// this stub keeps fakeStore implementing the full store interface.
func (s *fakeStore) snapshotsByVehicleSince(_ context.Context, _ uuid.UUID, _ int64, _ time.Time) ([]Snapshot, error) {
	return []Snapshot{}, nil
}

// upsertedSessions holds all sessions upserted via upsertSuperchargerSession.
// It is a separate field so B7 tests can inspect what was upserted.
//
// upsertSuperchargerSession records the upserted session and returns upsertErr
// (nil by default). Used by B7 collector tests.
func (s *fakeStore) upsertSuperchargerSession(_ context.Context, session SuperchargerSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.upsertErr != nil {
		return s.upsertErr
	}
	s.upsertedSessions = append(s.upsertedSessions, session)
	return nil
}

func (s *fakeStore) attemptsByVehicle() map[int64][]recordedAttempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	byID := map[int64][]recordedAttempt{}
	for _, a := range s.attempts {
		byID[a.teslaID] = append(byID[a.teslaID], a)
	}
	return byID
}

// newTestService builds a *service with the fakes and a fixed clock, bypassing the
// pool-backed NewService constructor (the pool is only needed for the real dbStore).
func newFakeService(acct account.Service, tsla tesla.VehicleService, st store) *service {
	return &service{
		acct:  acct,
		tsla:  tsla,
		store: st,
		cfg: Config{
			WakeTimeout: 50 * time.Millisecond, // short so asleep-timeout is fast
			Clock:       func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		},
		retryBackoff: 0, // no real sleep between retries — keeps offline tests fast
	}
}

// onlineData is a minimal DTO for an online vehicle with a couple of extracted fields.
func onlineData(id int64, sentry *bool) *tesla.VehicleDataTesla {
	d := &tesla.VehicleDataTesla{ID: id}
	d.ChargeState.BatteryLevel = 72
	d.ChargeState.BatteryRange = 200
	d.VehicleState.Odometer = 12345
	d.VehicleState.SentryMode = sentry
	return d
}

func TestCollectAll_OnlineVehicle_NoWakeStraightToFetch(t *testing.T) {
	acctID := uuid.New()
	ft := newFakeTesla()
	// Vehicle is already online per the per-account ListVehicles → it must NOT be woken.
	sc := &vehicleScript{state: "online", data: onlineData(10, nil)}
	ft.set(10, sc)

	fa := &fakeAccount{
		vehicles: []account.OwnedVehicle{{AccountID: acctID, TeslaID: 10}},
		tokens:   map[uuid.UUID]string{acctID: "tok"},
	}
	fs := &fakeStore{}
	svc := newFakeService(fa, ft, fs)

	report, err := svc.CollectAll(context.Background())
	if err != nil {
		t.Fatalf("CollectAll returned whole-cycle error: %v", err)
	}
	if report.Attempted != 1 || report.Succeeded != 1 {
		t.Fatalf("want 1 attempted / 1 succeeded, got %+v", report)
	}
	if len(fs.snapshots) != 1 {
		t.Fatalf("want 1 snapshot stored, got %d", len(fs.snapshots))
	}
	if got := fs.snapshots[0]; got.TeslaID != 10 || got.BatteryLevel != 72 || got.AccountID != acctID {
		t.Errorf("snapshot mapped wrong: %+v", got)
	}
	// R1-01: an already-online vehicle must receive ZERO WakeUp calls (D3/D4) but its
	// VehicleData is still captured (exactly one data call).
	if ft.wakeCalls[10] != 0 {
		t.Errorf("online vehicle must NOT be woken; want 0 wake calls, got %d", ft.wakeCalls[10])
	}
	if ft.dataCalls[10] != 1 {
		t.Errorf("want exactly 1 VehicleData call for the online vehicle, got %d", ft.dataCalls[10])
	}
	// R1-01: ListVehicles is called exactly ONCE per account (the up-front state read),
	// not once per vehicle and not inside a wake poll (no wake happened here).
	if ft.listCalls != 1 {
		t.Errorf("want exactly 1 ListVehicles call per account, got %d", ft.listCalls)
	}
	assertOneAttempt(t, fs, 10, ReasonOK, OutcomeSuccess)
}

func TestCollectAll_AsleepVehicle_WakeFlowRuns(t *testing.T) {
	acctID := uuid.New()
	ft := newFakeTesla()
	// Vehicle is asleep in the up-front list → the bounded wake flow MUST run (WakeUp
	// called), proving the online-vs-asleep branch split. WakeUp flips it online, and the
	// wake poll's ListVehicles then reports it online so VehicleData is captured.
	ft.set(15, &vehicleScript{state: "asleep", wakesOnline: true, data: onlineData(15, nil)})

	fa := &fakeAccount{
		vehicles: []account.OwnedVehicle{{AccountID: acctID, TeslaID: 15}},
		tokens:   map[uuid.UUID]string{acctID: "tok"},
	}
	fs := &fakeStore{}
	svc := newFakeService(fa, ft, fs)

	report, err := svc.CollectAll(context.Background())
	if err != nil {
		t.Fatalf("CollectAll returned whole-cycle error: %v", err)
	}
	if report.Succeeded != 1 {
		t.Fatalf("want the asleep vehicle to be woken and captured (1 succeeded), got %+v", report)
	}
	// The asleep branch DID wake the vehicle (branch split proven).
	if ft.wakeCalls[15] != 1 {
		t.Errorf("asleep vehicle must be woken; want 1 wake call, got %d", ft.wakeCalls[15])
	}
	if ft.dataCalls[15] != 1 {
		t.Errorf("want exactly 1 VehicleData call after wake, got %d", ft.dataCalls[15])
	}
	assertOneAttempt(t, fs, 15, ReasonOK, OutcomeSuccess)
}

func TestCollectAll_AsleepTimeout(t *testing.T) {
	acctID := uuid.New()
	ft := newFakeTesla()
	// Vehicle never reports online → the bounded wake window elapses.
	ft.set(20, &vehicleScript{state: "asleep", data: onlineData(20, nil)})

	fa := &fakeAccount{
		vehicles: []account.OwnedVehicle{{AccountID: acctID, TeslaID: 20}},
		tokens:   map[uuid.UUID]string{acctID: "tok"},
	}
	fs := &fakeStore{}
	svc := newFakeService(fa, ft, fs)

	report, err := svc.CollectAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected whole-cycle error: %v", err)
	}
	if report.Succeeded != 0 || report.FailuresByReason[ReasonAsleepTimeout] != 1 {
		t.Fatalf("want 1 asleep-timeout failure, got %+v", report)
	}
	if ft.dataCalls[20] != 0 {
		t.Errorf("VehicleData must not be called for a vehicle that never woke, got %d", ft.dataCalls[20])
	}
	assertOneAttempt(t, fs, 20, ReasonAsleepTimeout, OutcomeFailure)
}

func TestCollectAll_UnauthorizedNoConnection_SkipsTeslaCalls(t *testing.T) {
	acctID := uuid.New()
	ft := newFakeTesla()
	ft.set(30, &vehicleScript{state: "online", data: onlineData(30, nil)})

	// No token for the account → ErrNoTeslaConnection; every vehicle unauthorized and
	// NO Tesla call is made.
	fa := &fakeAccount{
		vehicles: []account.OwnedVehicle{{AccountID: acctID, TeslaID: 30}},
		tokens:   map[uuid.UUID]string{},
	}
	fs := &fakeStore{}
	svc := newFakeService(fa, ft, fs)

	report, err := svc.CollectAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected whole-cycle error: %v", err)
	}
	if report.FailuresByReason[ReasonUnauthorized] != 1 {
		t.Fatalf("want 1 unauthorized failure, got %+v", report)
	}
	if ft.wakeCalls[30] != 0 || ft.dataCalls[30] != 0 {
		t.Errorf("no Tesla call must fire for an account with no connection (wakes=%d, data=%d)", ft.wakeCalls[30], ft.dataCalls[30])
	}
	assertOneAttempt(t, fs, 30, ReasonUnauthorized, OutcomeFailure)
}

func TestCollectAll_AccountWideListVehicles401_AllUnauthorizedNoPerVehicleCalls(t *testing.T) {
	acctID := uuid.New()
	ft := newFakeTesla()
	ft.set(31, &vehicleScript{state: "online", data: onlineData(31, nil)})
	ft.set(32, &vehicleScript{state: "online", data: onlineData(32, nil)})
	// The single up-front ListVehicles for the account returns 401: the token is rejected
	// fleet-wide, so every vehicle is unauthorized and NO per-vehicle Tesla call fires.
	ft.listErr = fmt.Errorf("list: %w", tesla.ErrUnauthorized)

	fa := &fakeAccount{
		vehicles: []account.OwnedVehicle{
			{AccountID: acctID, TeslaID: 31},
			{AccountID: acctID, TeslaID: 32},
		},
		tokens: map[uuid.UUID]string{acctID: "tok"},
	}
	fs := &fakeStore{}
	svc := newFakeService(fa, ft, fs)

	report, err := svc.CollectAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected whole-cycle error: %v", err)
	}
	if report.FailuresByReason[ReasonUnauthorized] != 2 {
		t.Fatalf("want both vehicles unauthorized, got %+v", report)
	}
	// ListVehicles called exactly once for the account (then short-circuits).
	if ft.listCalls != 1 {
		t.Errorf("want exactly 1 ListVehicles call, got %d", ft.listCalls)
	}
	if ft.wakeCalls[31] != 0 || ft.wakeCalls[32] != 0 || ft.dataCalls[31] != 0 || ft.dataCalls[32] != 0 {
		t.Errorf("no per-vehicle Tesla call must fire after an account-wide 401")
	}
	assertOneAttempt(t, fs, 31, ReasonUnauthorized, OutcomeFailure)
	assertOneAttempt(t, fs, 32, ReasonUnauthorized, OutcomeFailure)
}

func TestCollectAll_Unauthorized401_NotRetried(t *testing.T) {
	acctID := uuid.New()
	ft := newFakeTesla()
	// Online vehicle (so no WakeUp): VehicleData returns a 401 wrapped as ErrUnauthorized
	// → terminal, no retry. This exercises the per-vehicle unauthorized path (distinct
	// from the account-wide ListVehicles 401 covered by another test).
	ft.set(40, &vehicleScript{state: "online", dataErr: fmt.Errorf("data: %w", tesla.ErrUnauthorized)})

	fa := &fakeAccount{
		vehicles: []account.OwnedVehicle{{AccountID: acctID, TeslaID: 40}},
		tokens:   map[uuid.UUID]string{acctID: "tok"},
	}
	fs := &fakeStore{}
	svc := newFakeService(fa, ft, fs)

	report, err := svc.CollectAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected whole-cycle error: %v", err)
	}
	if report.FailuresByReason[ReasonUnauthorized] != 1 {
		t.Fatalf("want 1 unauthorized failure, got %+v", report)
	}
	// Online → never woken; terminal unauthorized → VehicleData called exactly once
	// (not retried).
	if ft.wakeCalls[40] != 0 {
		t.Errorf("online vehicle must not be woken; want 0 wake calls, got %d", ft.wakeCalls[40])
	}
	if ft.dataCalls[40] != 1 {
		t.Errorf("unauthorized must not be retried; want 1 VehicleData call, got %d", ft.dataCalls[40])
	}
	assertOneAttempt(t, fs, 40, ReasonUnauthorized, OutcomeFailure)
}

func TestCollectAll_TransientApiError_RetriedOnceThenSucceeds(t *testing.T) {
	acctID := uuid.New()
	ft := newFakeTesla()
	// VehicleData fails once (transient) then succeeds on the retry.
	ft.set(50, &vehicleScript{
		state:       "online",
		dataErr:     errors.New("tesla: 500 upstream"),
		dataErrOnce: true,
		data:        onlineData(50, nil),
	})

	fa := &fakeAccount{
		vehicles: []account.OwnedVehicle{{AccountID: acctID, TeslaID: 50}},
		tokens:   map[uuid.UUID]string{acctID: "tok"},
	}
	fs := &fakeStore{}
	svc := newFakeService(fa, ft, fs)
	report, err := svc.CollectAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected whole-cycle error: %v", err)
	}
	if report.Succeeded != 1 {
		t.Fatalf("want the retry to succeed (1 succeeded), got %+v", report)
	}
	if ft.dataCalls[50] != 2 {
		t.Errorf("want exactly 2 VehicleData calls (1 fail + 1 retry), got %d", ft.dataCalls[50])
	}
	// Still exactly ONE poll_attempt for the vehicle, recorded as ok.
	assertOneAttempt(t, fs, 50, ReasonOK, OutcomeSuccess)
}

func TestCollectAll_PersistentApiError_RetriedOnceThenFails(t *testing.T) {
	acctID := uuid.New()
	ft := newFakeTesla()
	// VehicleData always fails → after one retry it maps to api-error.
	ft.set(60, &vehicleScript{state: "online", dataErr: errors.New("tesla: 503")})

	fa := &fakeAccount{
		vehicles: []account.OwnedVehicle{{AccountID: acctID, TeslaID: 60}},
		tokens:   map[uuid.UUID]string{acctID: "tok"},
	}
	fs := &fakeStore{}
	svc := newFakeService(fa, ft, fs)

	report, err := svc.CollectAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected whole-cycle error: %v", err)
	}
	if report.FailuresByReason[ReasonAPIError] != 1 {
		t.Fatalf("want 1 api-error failure, got %+v", report)
	}
	if ft.dataCalls[60] != 2 {
		t.Errorf("want exactly 2 VehicleData calls (1 + 1 retry), got %d", ft.dataCalls[60])
	}
	assertOneAttempt(t, fs, 60, ReasonAPIError, OutcomeFailure)
}

func TestCollectAll_PerVehicleIsolation(t *testing.T) {
	acctID := uuid.New()
	ft := newFakeTesla()
	// One healthy vehicle, one that fails at VehicleData persistently.
	ft.set(71, &vehicleScript{state: "online", data: onlineData(71, nil)})
	ft.set(72, &vehicleScript{state: "online", dataErr: errors.New("tesla: boom")})

	fa := &fakeAccount{
		vehicles: []account.OwnedVehicle{
			{AccountID: acctID, TeslaID: 71},
			{AccountID: acctID, TeslaID: 72},
		},
		tokens: map[uuid.UUID]string{acctID: "tok"},
	}
	fs := &fakeStore{}
	svc := newFakeService(fa, ft, fs)

	report, err := svc.CollectAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected whole-cycle error: %v", err)
	}
	if report.Attempted != 2 || report.Succeeded != 1 || report.FailuresByReason[ReasonAPIError] != 1 {
		t.Fatalf("isolation broken; want 2 attempted/1 ok/1 api-error, got %+v", report)
	}
	// The healthy vehicle was still captured despite its sibling failing.
	if len(fs.snapshots) != 1 || fs.snapshots[0].TeslaID != 71 {
		t.Fatalf("healthy vehicle 71 not captured: %+v", fs.snapshots)
	}
	assertOneAttempt(t, fs, 71, ReasonOK, OutcomeSuccess)
	assertOneAttempt(t, fs, 72, ReasonAPIError, OutcomeFailure)
}

func TestCollectAll_MultiAccountMultiVehicle(t *testing.T) {
	acctA, acctB := uuid.New(), uuid.New()
	ft := newFakeTesla()
	ft.set(81, &vehicleScript{state: "online", data: onlineData(81, nil)})
	ft.set(82, &vehicleScript{state: "online", data: onlineData(82, nil)})
	ft.set(91, &vehicleScript{state: "online", data: onlineData(91, nil)})

	fa := &fakeAccount{
		vehicles: []account.OwnedVehicle{
			{AccountID: acctA, TeslaID: 81},
			{AccountID: acctA, TeslaID: 82},
			{AccountID: acctB, TeslaID: 91},
		},
		// acctA has a token; acctB does not → its vehicle is unauthorized.
		tokens: map[uuid.UUID]string{acctA: "tokA"},
	}
	fs := &fakeStore{}
	svc := newFakeService(fa, ft, fs)

	report, err := svc.CollectAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected whole-cycle error: %v", err)
	}
	if report.Attempted != 3 || report.Succeeded != 2 {
		t.Fatalf("want 3 attempted / 2 succeeded, got %+v", report)
	}
	if report.FailuresByReason[ReasonUnauthorized] != 1 {
		t.Fatalf("want acctB's vehicle unauthorized, got %+v", report)
	}
	assertOneAttempt(t, fs, 81, ReasonOK, OutcomeSuccess)
	assertOneAttempt(t, fs, 82, ReasonOK, OutcomeSuccess)
	assertOneAttempt(t, fs, 91, ReasonUnauthorized, OutcomeFailure)
	// acctB made no Tesla calls (no connection).
	if ft.wakeCalls[91] != 0 || ft.dataCalls[91] != 0 {
		t.Errorf("acctB vehicle should make no Tesla calls, got wakes=%d data=%d", ft.wakeCalls[91], ft.dataCalls[91])
	}
}

func TestCollectAll_StoreErrorRetriedThenSucceeds(t *testing.T) {
	acctID := uuid.New()
	ft := newFakeTesla()
	ft.set(100, &vehicleScript{state: "online", data: onlineData(100, nil)})

	fa := &fakeAccount{
		vehicles: []account.OwnedVehicle{{AccountID: acctID, TeslaID: 100}},
		tokens:   map[uuid.UUID]string{acctID: "tok"},
	}
	// The snapshot insert fails once (transient store error) then succeeds on retry.
	fs := &fakeStore{snapErr: errors.New("db: deadlock"), snapErrOnce: true}
	svc := newFakeService(fa, ft, fs)

	report, err := svc.CollectAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected whole-cycle error: %v", err)
	}
	if report.Succeeded != 1 {
		t.Fatalf("store error should have been retried into success, got %+v", report)
	}
	// Two VehicleData calls (the whole attempt re-runs), one stored snapshot.
	if ft.dataCalls[100] != 2 {
		t.Errorf("want 2 VehicleData calls after a store retry, got %d", ft.dataCalls[100])
	}
	assertOneAttempt(t, fs, 100, ReasonOK, OutcomeSuccess)
}

func TestCollectAll_WholeCycleEnumerationError(t *testing.T) {
	fa := &fakeAccount{allErr: errors.New("db: connection refused")}
	fs := &fakeStore{}
	svc := newFakeService(fa, newFakeTesla(), fs)

	_, err := svc.CollectAll(context.Background())
	if err == nil {
		t.Fatal("want a whole-cycle error when AllRegisteredVehicles fails, got nil")
	}
	if len(fs.attempts) != 0 {
		t.Errorf("no attempts should be recorded when enumeration fails, got %d", len(fs.attempts))
	}
}

func TestCollectAll_SentryModeFidelityPreserved(t *testing.T) {
	acctID := uuid.New()
	on := true
	off := false
	ft := newFakeTesla()
	ft.set(110, &vehicleScript{state: "online", data: onlineData(110, &on)})
	ft.set(111, &vehicleScript{state: "online", data: onlineData(111, &off)})
	ft.set(112, &vehicleScript{state: "online", data: onlineData(112, nil)})

	fa := &fakeAccount{
		vehicles: []account.OwnedVehicle{
			{AccountID: acctID, TeslaID: 110},
			{AccountID: acctID, TeslaID: 111},
			{AccountID: acctID, TeslaID: 112},
		},
		tokens: map[uuid.UUID]string{acctID: "tok"},
	}
	fs := &fakeStore{}
	svc := newFakeService(fa, ft, fs)

	if _, err := svc.CollectAll(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byID := map[int64]*bool{}
	for _, s := range fs.snapshots {
		byID[s.TeslaID] = s.SentryMode
	}
	if byID[110] == nil || *byID[110] != true {
		t.Errorf("vehicle 110 sentry should be *true, got %v", byID[110])
	}
	if byID[111] == nil || *byID[111] != false {
		t.Errorf("vehicle 111 sentry should be *false, got %v", byID[111])
	}
	if byID[112] != nil {
		t.Errorf("vehicle 112 sentry should stay nil (not reported), got %v", *byID[112])
	}
}

// assertOneAttempt asserts exactly one poll_attempt was recorded for teslaID with the
// expected reason/outcome — the "exactly one attempt per vehicle per cycle" guarantee.
func assertOneAttempt(t *testing.T, fs *fakeStore, teslaID int64, wantReason Reason, wantOutcome Outcome) {
	t.Helper()
	got := fs.attemptsByVehicle()[teslaID]
	if len(got) != 1 {
		t.Fatalf("vehicle %d: want exactly 1 attempt, got %d (%+v)", teslaID, len(got), got)
	}
	if got[0].reason != wantReason || got[0].outcome != wantOutcome {
		t.Errorf("vehicle %d: want %s/%s, got %s/%s", teslaID, wantOutcome, wantReason, got[0].outcome, got[0].reason)
	}
}

// --- B7: Offline collector tests for ChargingHistory fold-in (design DBS7) ---

// makeChargingSessions builds N minimal ChargingSessionTesla values with distinct
// SessionIDs starting from baseID. VIN defaults to "VIN001" so VIN-to-TeslaID tests
// can control which ones resolve.
func makeChargingSessions(n int, baseID int64, vin string) []tesla.ChargingSessionTesla {
	sessions := make([]tesla.ChargingSessionTesla, n)
	for i := range sessions {
		sessions[i] = tesla.ChargingSessionTesla{
			SessionID: baseID + int64(i),
			VIN:       vin,
			Raw:       []byte(fmt.Sprintf(`{"sessionId":%d}`, baseID+int64(i))),
		}
	}
	return sessions
}

// TestCollectAll_ChargingHistory_SuccessCountsUpserted verifies that when
// ChargingHistory succeeds with N sessions, CycleReport.ChargingSessionsUpserted
// equals N and ChargingFetchFailures equals 0 (design DBS7/B7.1a).
func TestCollectAll_ChargingHistory_SuccessCountsUpserted(t *testing.T) {
	acctID := uuid.New()
	ft := newFakeTesla()
	ft.set(10, &vehicleScript{state: "online", data: onlineData(10, nil)})
	ft.chargingHistory = &tesla.ChargingHistoryTesla{
		Data: makeChargingSessions(3, 1000, "VIN001"),
	}

	fa := &fakeAccount{
		vehicles: []account.OwnedVehicle{{AccountID: acctID, TeslaID: 10, VIN: "VIN001"}},
		tokens:   map[uuid.UUID]string{acctID: "tok"},
	}
	fs := &fakeStore{}
	svc := newFakeService(fa, ft, fs)

	report, err := svc.CollectAll(context.Background())
	if err != nil {
		t.Fatalf("CollectAll returned whole-cycle error: %v", err)
	}
	if report.ChargingSessionsUpserted != 3 {
		t.Errorf("want ChargingSessionsUpserted=3, got %d", report.ChargingSessionsUpserted)
	}
	if report.ChargingFetchFailures != 0 {
		t.Errorf("want ChargingFetchFailures=0, got %d", report.ChargingFetchFailures)
	}
	if len(fs.upsertedSessions) != 3 {
		t.Errorf("want 3 sessions upserted, got %d", len(fs.upsertedSessions))
	}
}

// TestCollectAll_ChargingHistory_FetchFailure_SnapshotUnaffected verifies that a
// ChargingHistory call failure increments ChargingFetchFailures and does NOT abort
// snapshot collection for the account (design DBS7/B7.1b, per-account isolation).
func TestCollectAll_ChargingHistory_FetchFailure_SnapshotUnaffected(t *testing.T) {
	acctID := uuid.New()
	ft := newFakeTesla()
	ft.set(20, &vehicleScript{state: "online", data: onlineData(20, nil)})
	ft.chargingHistoryErr = errors.New("tesla: 503 upstream")

	fa := &fakeAccount{
		vehicles: []account.OwnedVehicle{{AccountID: acctID, TeslaID: 20, VIN: "VINX"}},
		tokens:   map[uuid.UUID]string{acctID: "tok"},
	}
	fs := &fakeStore{}
	svc := newFakeService(fa, ft, fs)

	report, err := svc.CollectAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected whole-cycle error: %v", err)
	}
	// Snapshot collection must be unaffected.
	if report.Succeeded != 1 {
		t.Errorf("want snapshot collection Succeeded=1, got %d", report.Succeeded)
	}
	if len(fs.snapshots) != 1 {
		t.Errorf("want 1 snapshot stored, got %d", len(fs.snapshots))
	}
	// Charging failure counted.
	if report.ChargingFetchFailures != 1 {
		t.Errorf("want ChargingFetchFailures=1, got %d", report.ChargingFetchFailures)
	}
	if report.ChargingSessionsUpserted != 0 {
		t.Errorf("want ChargingSessionsUpserted=0, got %d", report.ChargingSessionsUpserted)
	}
}

// TestCollectAll_ChargingHistory_VINResolution verifies that sessions for a
// recognized VIN get the correct TeslaID, and sessions for an unknown VIN get
// TeslaID == nil (design DBS7/B7.1c, orphan handling).
func TestCollectAll_ChargingHistory_VINResolution(t *testing.T) {
	acctID := uuid.New()
	ft := newFakeTesla()
	ft.set(30, &vehicleScript{state: "online", data: onlineData(30, nil)})

	// Two sessions: one with a VIN matching the registered vehicle, one unknown.
	knownVIN := "VIN_KNOWN"
	unknownVIN := "VIN_UNKNOWN"
	ft.chargingHistory = &tesla.ChargingHistoryTesla{
		Data: []tesla.ChargingSessionTesla{
			{SessionID: 2001, VIN: knownVIN, Raw: []byte(`{"sessionId":2001}`)},
			{SessionID: 2002, VIN: unknownVIN, Raw: []byte(`{"sessionId":2002}`)},
		},
	}

	fa := &fakeAccount{
		vehicles: []account.OwnedVehicle{{AccountID: acctID, TeslaID: 30, VIN: knownVIN}},
		tokens:   map[uuid.UUID]string{acctID: "tok"},
	}
	fs := &fakeStore{}
	svc := newFakeService(fa, ft, fs)

	if _, err := svc.CollectAll(context.Background()); err != nil {
		t.Fatalf("unexpected whole-cycle error: %v", err)
	}
	if len(fs.upsertedSessions) != 2 {
		t.Fatalf("want 2 sessions upserted, got %d", len(fs.upsertedSessions))
	}
	bySessionID := map[int64]SuperchargerSession{}
	for _, s := range fs.upsertedSessions {
		bySessionID[s.SessionID] = s
	}
	knownSess := bySessionID[2001]
	if knownSess.TeslaID == nil || *knownSess.TeslaID != 30 {
		t.Errorf("session 2001 (known VIN): want TeslaID=30, got %v", knownSess.TeslaID)
	}
	unknownSess := bySessionID[2002]
	if unknownSess.TeslaID != nil {
		t.Errorf("session 2002 (unknown VIN): want TeslaID=nil, got %v", unknownSess.TeslaID)
	}
}

// --- B7 Derivation helper unit tests (design DBS2, B7.1d) ---

// TestDeriveEnergyKWh_KWhFeesOnly covers the case where all fees are kWh-billed.
func TestDeriveEnergyKWh_KWhFeesOnly(t *testing.T) {
	fees := []tesla.ChargingFeeTesla{
		{UOM: "kwh", UsageBase: 10.0, UsageTier1: 5.0, UsageTier2: 3.0, UsageTier3: nil, UsageTier4: nil},
		{UOM: "kWh", UsageBase: 2.0, UsageTier1: 1.0}, // mixed case
	}
	got := deriveEnergyKWh(fees)
	if got == nil {
		t.Fatal("want non-nil energy, got nil")
	}
	want := 10.0 + 5.0 + 3.0 + 2.0 + 1.0
	if *got != want {
		t.Errorf("want %v, got %v", want, *got)
	}
}

// TestDeriveEnergyKWh_TimeOnlyFees covers the case where no fee is kWh-billed.
func TestDeriveEnergyKWh_TimeOnlyFees(t *testing.T) {
	fees := []tesla.ChargingFeeTesla{
		{UOM: "min", UsageBase: 30.0},
	}
	got := deriveEnergyKWh(fees)
	if got != nil {
		t.Errorf("want nil for time-only fees, got %v", got)
	}
}

// TestDeriveEnergyKWh_MixedFees covers kWh + time-based fees in the same session.
func TestDeriveEnergyKWh_MixedFees(t *testing.T) {
	tier3 := 1.5
	fees := []tesla.ChargingFeeTesla{
		{UOM: "kwh", UsageBase: 20.0, UsageTier1: 5.0, UsageTier2: 0.0, UsageTier3: &tier3},
		{UOM: "min", UsageBase: 60.0}, // ignored
	}
	got := deriveEnergyKWh(fees)
	if got == nil {
		t.Fatal("want non-nil energy for mixed fees, got nil")
	}
	want := 20.0 + 5.0 + 0.0 + 1.5
	if *got != want {
		t.Errorf("want %v, got %v", want, *got)
	}
}

// TestDeriveEnergyKWh_EmptyFees covers the empty fees case.
func TestDeriveEnergyKWh_EmptyFees(t *testing.T) {
	got := deriveEnergyKWh(nil)
	if got != nil {
		t.Errorf("want nil for empty fees, got %v", got)
	}
}

// TestDeriveTotalCost covers sum of totalDue and empty fees.
func TestDeriveTotalCost(t *testing.T) {
	t.Run("non-empty", func(t *testing.T) {
		fees := []tesla.ChargingFeeTesla{
			{TotalDue: 3.50},
			{TotalDue: 1.25},
		}
		got := deriveTotalCost(fees)
		if got == nil {
			t.Fatal("want non-nil cost, got nil")
		}
		want := 4.75
		if *got != want {
			t.Errorf("want %v, got %v", want, *got)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if got := deriveTotalCost(nil); got != nil {
			t.Errorf("want nil for empty fees, got %v", got)
		}
	})
}

// TestDeriveCurrency covers first-fee currency and empty fees.
func TestDeriveCurrency(t *testing.T) {
	t.Run("non-empty", func(t *testing.T) {
		fees := []tesla.ChargingFeeTesla{
			{CurrencyCode: "USD"},
			{CurrencyCode: "USD"},
		}
		got := deriveCurrency(fees)
		if got == nil || *got != "USD" {
			t.Errorf("want *USD, got %v", got)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if got := deriveCurrency(nil); got != nil {
			t.Errorf("want nil for empty fees, got %v", got)
		}
	})
}

// TestDeriveIsPaid covers all-paid, one-unpaid, and empty fees.
func TestDeriveIsPaid(t *testing.T) {
	t.Run("all-paid", func(t *testing.T) {
		fees := []tesla.ChargingFeeTesla{
			{IsPaid: true},
			{IsPaid: true},
		}
		got := deriveIsPaid(fees)
		if got == nil || !*got {
			t.Errorf("want *true for all-paid, got %v", got)
		}
	})
	t.Run("one-unpaid", func(t *testing.T) {
		fees := []tesla.ChargingFeeTesla{
			{IsPaid: true},
			{IsPaid: false},
		}
		got := deriveIsPaid(fees)
		if got == nil || *got {
			t.Errorf("want *false for one-unpaid, got %v", got)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if got := deriveIsPaid(nil); got != nil {
			t.Errorf("want nil for empty fees, got %v", got)
		}
	})
}
