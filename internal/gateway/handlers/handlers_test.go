package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
	"github.com/cristianpena/magus-tesla-api/internal/tesla"
)

// --- fakes for the account and tesla interfaces (no DB, no real Tesla) ---

// fakeAccount simulates the account module's per-user data. It carries either a
// set of already-registered vehicles (so the dashboard reads from the DB and
// never calls Tesla), or a token + the scenario for a one-time Tesla seed path.
type fakeAccount struct {
	registered []account.Vehicle
	regErr     error
	token      string
	tokenErr   error
	seedErr    error
	// seedCalls captures the SeedVehicles invocation so a test can assert the
	// one-time Tesla call was persisted.
	seedCalls int
	// lastSeedVehicles captures the SeedVehicle list from the most recent SeedVehicles call.
	lastSeedVehicles []account.SeedVehicle
}

func (f fakeAccount) UpsertFromOAuth(context.Context, account.OAuthIdentity) (account.Account, error) {
	return account.Account{}, nil
}
func (f fakeAccount) SaveTeslaTokens(context.Context, uuid.UUID, account.TeslaTokens) error {
	return nil
}
func (f fakeAccount) AccessTokenFor(context.Context, uuid.UUID) (string, error) {
	return f.token, f.tokenErr
}
func (f fakeAccount) RegisteredVehicles(context.Context, uuid.UUID) ([]account.Vehicle, error) {
	return f.registered, f.regErr
}

// AllRegisteredVehicles satisfies the widened account.Service (used by the telemetry
// collector, not by the gateway); the gateway never calls it, so a stub suffices.
func (f fakeAccount) AllRegisteredVehicles(context.Context) ([]account.OwnedVehicle, error) {
	return nil, nil
}
func (f *fakeAccount) SeedVehicles(_ context.Context, _ uuid.UUID, vs []account.SeedVehicle) ([]account.Vehicle, error) {
	f.seedCalls++
	f.lastSeedVehicles = vs
	if f.seedErr != nil {
		return nil, f.seedErr
	}
	out := make([]account.Vehicle, 0, len(vs))
	for _, v := range vs {
		out = append(out, account.Vehicle{TeslaID: v.TeslaID, VIN: v.VIN, DisplayName: v.DisplayName})
	}
	return out, nil
}

type fakeTesla struct {
	vehicles []tesla.VehicleTesla
	listErr  error
}

func (f fakeTesla) ListVehicles(context.Context, tesla.Credentials) ([]tesla.VehicleTesla, error) {
	return f.vehicles, f.listErr
}
func (f fakeTesla) VehicleData(context.Context, tesla.Credentials, int64) (*tesla.VehicleDataTesla, json.RawMessage, error) {
	return nil, nil, nil
}
func (f fakeTesla) WakeUp(context.Context, tesla.Credentials, int64) (*tesla.VehicleTesla, error) {
	return nil, nil
}
func (f fakeTesla) ChargingHistory(context.Context, tesla.Credentials, tesla.ChargingHistoryParams) (*tesla.ChargingHistoryTesla, error) {
	return nil, nil
}

// fakeReader is a test double for telemetry.Reader. Returns the configured
// snapshots or error — no DB or network.
type fakeReader struct {
	snapshots []telemetry.Snapshot
	err       error
}

func (f *fakeReader) LatestSnapshotsByAccount(_ context.Context, _ uuid.UUID) ([]telemetry.Snapshot, error) {
	return f.snapshots, f.err
}

// newHandler builds a Handler for tests that don't involve telemetry (seeds, connect
// flows, etc.). The TelemetryReader is left nil — it won't be reached in those paths.
func newHandler(acct account.Service, tsvc tesla.VehicleService) *Handler {
	return New(Deps{Account: acct, Tesla: tsvc})
}

// newHandlerWithReader builds a Handler with a fake telemetry.Reader for tests that
// exercise the enriched-vehicle path.
func newHandlerWithReader(acct account.Service, tsvc tesla.VehicleService, reader telemetry.Reader) *Handler {
	return New(Deps{Account: acct, Tesla: tsvc, TelemetryReader: reader})
}

// --- pre-existing tests (unchanged behavior) ---

func TestVehiclesFor_RegisteredRendersWithoutTeslaCall(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1, VIN: "VIN1", DisplayName: "Magus"},
		{TeslaID: 2, VIN: "VIN2", DisplayName: "Second"},
	}}
	tsvc := &fakeTesla{vehicles: []tesla.VehicleTesla{
		{DisplayName: "SHOULD NOT BE CALLED", VIN: "NEVER"},
	}}
	reader := &fakeReader{snapshots: []telemetry.Snapshot{}}
	h := newHandlerWithReader(acct, tsvc, reader)
	d := h.vehiclesFor(context.Background(), uuid.New())
	if len(d.Vehicles) != 2 {
		t.Fatalf("want 2 vehicles from the registry, got %d (%+v)", len(d.Vehicles), d)
	}
	if d.Vehicles[0].DisplayName != "Magus" || d.Vehicles[0].VIN != "VIN1" {
		t.Errorf("unexpected mapping: %+v", d.Vehicles[0])
	}
	if acct.seedCalls != 0 {
		t.Errorf("expected no Tesla seed when vehicles are already registered, got %d seed calls", acct.seedCalls)
	}
}

func TestVehiclesFor_EmptyRegistryTriggersTeslaSeed(t *testing.T) {
	acct := &fakeAccount{token: "tok"}
	tsvc := &fakeTesla{vehicles: []tesla.VehicleTesla{
		{ID: 10, DisplayName: "Magus", VIN: "VIN10", State: "online"},
		{ID: 20, DisplayName: "Second", VIN: "VIN20", State: "asleep"},
	}}
	h := newHandler(acct, tsvc)
	d := h.vehiclesFor(context.Background(), uuid.New())
	if len(d.Vehicles) != 2 {
		t.Fatalf("want 2 seeded vehicles, got %d (%+v)", len(d.Vehicles), d)
	}
	if d.Vehicles[0].DisplayName != "Magus" || d.Vehicles[0].VIN != "VIN10" {
		t.Errorf("unexpected seeded mapping: %+v", d.Vehicles[0])
	}
	if acct.seedCalls != 1 {
		t.Errorf("expected exactly one SeedVehicles call, got %d", acct.seedCalls)
	}
}

func TestVehiclesFor_NoStateRendered(t *testing.T) {
	// Registered vehicles carry no State; the presentation model has no State field.
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1, VIN: "VIN1", DisplayName: "Magus"},
	}}
	reader := &fakeReader{snapshots: []telemetry.Snapshot{}}
	h := newHandlerWithReader(acct, fakeTesla{}, reader)
	d := h.vehiclesFor(context.Background(), uuid.New())
	if len(d.Vehicles) != 1 {
		t.Fatalf("want 1 vehicle, got %d", len(d.Vehicles))
	}
	// fragments.Vehicle has no State field — this assertion is enforced by the
	// compiler; here we just confirm display name + VIN are the surfaced fields.
	if d.Vehicles[0].DisplayName != "Magus" || d.Vehicles[0].VIN != "VIN1" {
		t.Errorf("unexpected vehicle fields: %+v", d.Vehicles[0])
	}
}

func TestVehiclesFor_NoConnectionPromptsConnect(t *testing.T) {
	h := newHandler(&fakeAccount{tokenErr: account.ErrNoTeslaConnection}, fakeTesla{})
	d := h.vehiclesFor(context.Background(), uuid.New())
	if !d.NeedsConnect || len(d.Vehicles) != 0 {
		t.Fatalf("want NeedsConnect and no vehicles, got %+v", d)
	}
}

func TestVehiclesFor_UnauthorizedPromptsReconnect(t *testing.T) {
	h := newHandler(&fakeAccount{token: "tok"}, fakeTesla{listErr: tesla.ErrUnauthorized})
	d := h.vehiclesFor(context.Background(), uuid.New())
	if !d.NeedsConnect || d.Notice == "" {
		t.Fatalf("want a reconnect notice with NeedsConnect, got %+v", d)
	}
}

func TestVehiclesFor_EmptyTeslaListShowsNotice(t *testing.T) {
	h := newHandler(&fakeAccount{token: "tok"}, fakeTesla{vehicles: nil})
	d := h.vehiclesFor(context.Background(), uuid.New())
	if len(d.Vehicles) != 0 || d.NeedsConnect || d.Notice == "" {
		t.Fatalf("want a plain notice (no vehicles, no connect prompt), got %+v", d)
	}
}

// --- new tests for telemetry enrichment (task 3.2) ---

// boolPtr is a helper to take the address of a bool literal in tests.
func boolPtr(b bool) *bool { return &b }

func TestVehiclesFor_EnrichedCard(t *testing.T) {
	capturedAt := time.Now().Add(-1 * time.Hour) // fresh, within 36 h
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 42, VIN: "VIN42", DisplayName: "Magus"},
	}}
	reader := &fakeReader{snapshots: []telemetry.Snapshot{
		{
			TeslaID:       42,
			CapturedAt:    capturedAt,
			BatteryLevel:  80,
			BatteryRange:  200.0, // miles → BatteryRangeKm = 200 * 1.609344
			ChargingState: "Disconnected",
			Odometer:      12000.0, // miles → OdometerKm = 12000 * 1.609344
			InsideTemp:    22.5,
			OutsideTemp:   15.0,
			Locked:        true,
			SentryMode:    boolPtr(true),
		},
	}}
	h := newHandlerWithReader(acct, fakeTesla{}, reader)
	d := h.vehiclesFor(context.Background(), uuid.New())

	if len(d.Vehicles) != 1 {
		t.Fatalf("want 1 vehicle, got %d", len(d.Vehicles))
	}
	v := d.Vehicles[0]
	if !v.HasSnapshot {
		t.Errorf("want HasSnapshot true, got false")
	}
	if v.Battery != "80%" {
		t.Errorf("want Battery %q, got %q", "80%", v.Battery)
	}
	snap := telemetry.Snapshot{BatteryRange: 200.0, Odometer: 12000.0}
	wantBatteryRange := fmt.Sprintf("%.1f km", snap.BatteryRangeKm())
	if v.BatteryRange != wantBatteryRange {
		t.Errorf("want BatteryRange %q, got %q", wantBatteryRange, v.BatteryRange)
	}
	if v.ChargingState != "Disconnected" {
		t.Errorf("want ChargingState Disconnected, got %q", v.ChargingState)
	}
	wantOdometer := fmt.Sprintf("%.1f km", snap.OdometerKm())
	if v.Odometer != wantOdometer {
		t.Errorf("want Odometer %q, got %q", wantOdometer, v.Odometer)
	}
	if v.InsideTemp != "22.5 °C" {
		t.Errorf("want InsideTemp %q, got %q", "22.5 °C", v.InsideTemp)
	}
	if v.OutsideTemp != "15.0 °C" {
		t.Errorf("want OutsideTemp %q, got %q", "15.0 °C", v.OutsideTemp)
	}
	if !v.Locked {
		t.Errorf("want Locked true, got false")
	}
	if v.SentryMode == nil || !*v.SentryMode {
		t.Errorf("want SentryMode *true, got %v", v.SentryMode)
	}
	if v.LastUpdated == "" {
		t.Errorf("want LastUpdated set, got empty string")
	}
	if v.IsStale {
		t.Errorf("want IsStale false for a 1h-old snapshot, got true")
	}
}

func TestVehiclesFor_PlaceholderCard(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 7, VIN: "VIN7", DisplayName: "Ghost"},
	}}
	// Reader returns an empty slice — no snapshot for this vehicle.
	reader := &fakeReader{snapshots: []telemetry.Snapshot{}}
	h := newHandlerWithReader(acct, fakeTesla{}, reader)
	d := h.vehiclesFor(context.Background(), uuid.New())

	if len(d.Vehicles) != 1 {
		t.Fatalf("want 1 vehicle, got %d", len(d.Vehicles))
	}
	v := d.Vehicles[0]
	if v.HasSnapshot {
		t.Errorf("want HasSnapshot false, got true")
	}
	if v.Battery != "" || v.BatteryRange != "" || v.Odometer != "" || v.InsideTemp != "" || v.OutsideTemp != "" {
		t.Errorf("want all display string fields empty for placeholder, got Battery=%q BatteryRange=%q Odometer=%q InsideTemp=%q OutsideTemp=%q",
			v.Battery, v.BatteryRange, v.Odometer, v.InsideTemp, v.OutsideTemp)
	}
	if v.LastUpdated != "" {
		t.Errorf("want LastUpdated empty for placeholder, got %q", v.LastUpdated)
	}
	if v.IsStale {
		t.Errorf("placeholder card must not have IsStale set")
	}
}

func TestVehiclesFor_GracefulDegradation(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 9, VIN: "VIN9", DisplayName: "Bricked"},
	}}
	reader := &fakeReader{err: errors.New("db unavailable")}
	h := newHandlerWithReader(acct, fakeTesla{}, reader)
	d := h.vehiclesFor(context.Background(), uuid.New())

	if d.Notice == "" {
		t.Errorf("want a degradation notice when Reader errors, got empty notice")
	}
	if len(d.Vehicles) != 1 {
		t.Fatalf("want 1 vehicle in placeholder state, got %d", len(d.Vehicles))
	}
	if d.Vehicles[0].HasSnapshot {
		t.Errorf("want HasSnapshot false on all vehicles after Reader error")
	}
}

func TestVehiclesFor_StaleBoundary(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1, VIN: "VIN1", DisplayName: "Magus"},
	}}

	// Clearly within the threshold (10 s of headroom) — should NOT be stale.
	withinThreshold := time.Now().Add(-stalenessThreshold + 10*time.Second)
	reader := &fakeReader{snapshots: []telemetry.Snapshot{
		{TeslaID: 1, CapturedAt: withinThreshold},
	}}
	h := newHandlerWithReader(acct, fakeTesla{}, reader)
	d := h.vehiclesFor(context.Background(), uuid.New())
	if len(d.Vehicles) != 1 {
		t.Fatalf("want 1 vehicle, got %d", len(d.Vehicles))
	}
	if d.Vehicles[0].IsStale {
		t.Errorf("want IsStale false when 10 s within threshold, got true")
	}

	// One second past the threshold — should be stale.
	pastThreshold := time.Now().Add(-stalenessThreshold - time.Second)
	reader2 := &fakeReader{snapshots: []telemetry.Snapshot{
		{TeslaID: 1, CapturedAt: pastThreshold},
	}}
	h2 := newHandlerWithReader(acct, fakeTesla{}, reader2)
	d2 := h2.vehiclesFor(context.Background(), uuid.New())
	if len(d2.Vehicles) != 1 {
		t.Fatalf("want 1 vehicle, got %d", len(d2.Vehicles))
	}
	if !d2.Vehicles[0].IsStale {
		t.Errorf("want IsStale true when 1 s past threshold, got false")
	}
}

// TestIsStale verifies the exact-at-threshold and boundary semantics of the isStale
// helper using a fixed clock — no wall-clock dependency, fully deterministic.
func TestIsStale(t *testing.T) {
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	// Exactly at threshold: duration == stalenessThreshold, NOT stale (strict >).
	if isStale(now.Add(-stalenessThreshold), now) {
		t.Errorf("want isStale false at exactly threshold (strict >), got true")
	}

	// One nanosecond past threshold: duration > stalenessThreshold, IS stale.
	if !isStale(now.Add(-stalenessThreshold-time.Nanosecond), now) {
		t.Errorf("want isStale true one nanosecond past threshold, got false")
	}

	// One nanosecond within threshold: duration < stalenessThreshold, NOT stale.
	if isStale(now.Add(-stalenessThreshold+time.Nanosecond), now) {
		t.Errorf("want isStale false one nanosecond within threshold, got true")
	}
}

// TestConnectedAt verifies the exact-at-threshold and boundary semantics of the
// connectedAt nav-header helper using a fixed clock — fully deterministic. This
// mirrors TestIsStale, but for the 48 h connectedFreshnessWindow boundary (DD3).
func TestConnectedAt(t *testing.T) {
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	// Exactly at window: duration == connectedFreshnessWindow, STILL connected
	// (<= window — the strict inverse of isStale's strict >).
	if !connectedAt(now.Add(-connectedFreshnessWindow), now) {
		t.Errorf("want connectedAt true at exactly window (<= boundary), got false")
	}

	// One nanosecond past the window: duration > window, NOT connected (asleep).
	if connectedAt(now.Add(-connectedFreshnessWindow-time.Nanosecond), now) {
		t.Errorf("want connectedAt false one nanosecond past window, got true")
	}

	// One nanosecond within the window: connected.
	if !connectedAt(now.Add(-connectedFreshnessWindow+time.Nanosecond), now) {
		t.Errorf("want connectedAt true one nanosecond within window, got false")
	}
}

func TestVehiclesFor_SentryModeThreeStates(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1, VIN: "VIN1", DisplayName: "Nil"},
		{TeslaID: 2, VIN: "VIN2", DisplayName: "Off"},
		{TeslaID: 3, VIN: "VIN3", DisplayName: "On"},
	}}
	now := time.Now()
	reader := &fakeReader{snapshots: []telemetry.Snapshot{
		{TeslaID: 1, CapturedAt: now, SentryMode: nil},
		{TeslaID: 2, CapturedAt: now, SentryMode: boolPtr(false)},
		{TeslaID: 3, CapturedAt: now, SentryMode: boolPtr(true)},
	}}
	h := newHandlerWithReader(acct, fakeTesla{}, reader)
	d := h.vehiclesFor(context.Background(), uuid.New())

	if len(d.Vehicles) != 3 {
		t.Fatalf("want 3 vehicles, got %d", len(d.Vehicles))
	}
	// Build a lookup by DisplayName for readability.
	byName := make(map[string]*bool)
	for _, v := range d.Vehicles {
		byName[v.DisplayName] = v.SentryMode
	}

	if byName["Nil"] != nil {
		t.Errorf("want SentryMode nil for 'Nil' vehicle, got %v", byName["Nil"])
	}
	if byName["Off"] == nil || *byName["Off"] != false {
		t.Errorf("want SentryMode *false for 'Off' vehicle, got %v", byName["Off"])
	}
	if byName["On"] == nil || *byName["On"] != true {
		t.Errorf("want SentryMode *true for 'On' vehicle, got %v", byName["On"])
	}
}

// --- Sub-task D: seed AccessType mapping tests (sub-task C) ---

// TestSeedAccessTypeMapping_NonEmpty verifies that a non-empty VehicleTesla.AccessType
// is mapped to a non-nil *string on the SeedVehicle passed to account.SeedVehicles.
func TestSeedAccessTypeMapping_NonEmpty(t *testing.T) {
	acct := &fakeAccount{token: "tok"}
	tsvc := &fakeTesla{vehicles: []tesla.VehicleTesla{
		{ID: 10, DisplayName: "Magus", VIN: "VIN10", State: "online", AccessType: "OWNER"},
	}}
	// newHandler: telemetryReader is nil; the seed path never reaches it.
	h := newHandler(acct, tsvc)
	_ = h.vehiclesFor(context.Background(), uuid.New())

	if acct.seedCalls != 1 {
		t.Fatalf("want exactly 1 SeedVehicles call, got %d", acct.seedCalls)
	}
	if len(acct.lastSeedVehicles) != 1 {
		t.Fatalf("want 1 SeedVehicle, got %d", len(acct.lastSeedVehicles))
	}
	sv := acct.lastSeedVehicles[0]
	if sv.AccessType == nil {
		t.Errorf("want non-nil AccessType for OWNER vehicle, got nil")
	} else if *sv.AccessType != "OWNER" {
		t.Errorf("want AccessType=OWNER, got %q", *sv.AccessType)
	}
}

// TestSeedAccessTypeMapping_Empty verifies that an empty VehicleTesla.AccessType
// maps to nil on SeedVehicle.AccessType (boundary-nil convention).
func TestSeedAccessTypeMapping_Empty(t *testing.T) {
	acct := &fakeAccount{token: "tok"}
	tsvc := &fakeTesla{vehicles: []tesla.VehicleTesla{
		{ID: 20, DisplayName: "Unknown", VIN: "VIN20", State: "asleep", AccessType: ""},
	}}
	h := newHandler(acct, tsvc)
	_ = h.vehiclesFor(context.Background(), uuid.New())

	if acct.seedCalls != 1 {
		t.Fatalf("want exactly 1 SeedVehicles call, got %d", acct.seedCalls)
	}
	if len(acct.lastSeedVehicles) != 1 {
		t.Fatalf("want 1 SeedVehicle, got %d", len(acct.lastSeedVehicles))
	}
	sv := acct.lastSeedVehicles[0]
	if sv.AccessType != nil {
		t.Errorf("want nil AccessType for empty-string vehicle, got %q", *sv.AccessType)
	}
}

// --- nav-header fragment (T4) ---
//
// navHeaderFor is the gin-free helper; these unit tests cover the six enumerated
// cases purely (no HTTP, no session), mirroring the vehiclesFor tests above. The
// fragment route's anonymous -> /login redirect is covered in gateway_test.go.

// newNavHeaderHandler builds a Handler wired with account + telemetry fakes for
// the nav-header helper tests (Tesla is never reached by navHeaderFor).
func newNavHeaderHandler(acct account.Service, reader telemetry.Reader) *Handler {
	return newHandlerWithReader(acct, fakeTesla{}, reader)
}

func TestNavHeaderFor_Connected(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 42, VIN: "VIN42", DisplayName: "Magus"},
	}}
	// 1 h old — well within the 48 h window.
	reader := &fakeReader{snapshots: []telemetry.Snapshot{
		{TeslaID: 42, CapturedAt: time.Now().Add(-1 * time.Hour), BatteryLevel: 94},
	}}
	h := newNavHeaderHandler(acct, reader)
	vm := h.navHeaderFor(context.Background(), uuid.New())

	if vm.NeedsConnect {
		t.Fatalf("want NeedsConnect false, got true")
	}
	if vm.VehicleName != "Magus" {
		t.Errorf("want VehicleName Magus, got %q", vm.VehicleName)
	}
	if vm.Status != "connected" {
		t.Errorf("want Status connected, got %q", vm.Status)
	}
	if vm.StatusLabel != "Connected" {
		t.Errorf("want StatusLabel Connected, got %q", vm.StatusLabel)
	}
	if vm.BatteryPct != "94%" {
		t.Errorf("want BatteryPct 94%%, got %q", vm.BatteryPct)
	}
	if vm.LastSeenLabel != "" {
		t.Errorf("want no LastSeenLabel when connected, got %q", vm.LastSeenLabel)
	}
}

func TestNavHeaderFor_Asleep(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 42, VIN: "VIN42", DisplayName: "Magus"},
	}}
	// 3 days old — past the 48 h window -> asleep.
	reader := &fakeReader{snapshots: []telemetry.Snapshot{
		{TeslaID: 42, CapturedAt: time.Now().Add(-72 * time.Hour), BatteryLevel: 50},
	}}
	h := newNavHeaderHandler(acct, reader)
	vm := h.navHeaderFor(context.Background(), uuid.New())

	if vm.VehicleName != "Magus" {
		t.Errorf("want VehicleName Magus, got %q", vm.VehicleName)
	}
	if vm.Status != "asleep" {
		t.Errorf("want Status asleep, got %q", vm.Status)
	}
	if vm.StatusLabel != "Asleep" {
		t.Errorf("want StatusLabel Asleep, got %q", vm.StatusLabel)
	}
	if vm.BatteryPct != "" {
		t.Errorf("want no BatteryPct when asleep, got %q", vm.BatteryPct)
	}
	// Relative label pre-computed by the handler, present, and references "days".
	if vm.LastSeenLabel == "" {
		t.Errorf("want non-empty LastSeenLabel when asleep, got empty")
	}
	if !strings.Contains(vm.LastSeenLabel, "days ago") {
		t.Errorf("want LastSeenLabel to be a days-ago relative label (72 h old), got %q", vm.LastSeenLabel)
	}
}

func TestNavHeaderFor_Awaiting(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 42, VIN: "VIN42", DisplayName: "Magus"},
	}}
	// Reader returns an empty slice — no snapshot for the primary vehicle.
	reader := &fakeReader{snapshots: []telemetry.Snapshot{}}
	h := newNavHeaderHandler(acct, reader)
	vm := h.navHeaderFor(context.Background(), uuid.New())

	if vm.NeedsConnect {
		t.Fatalf("want NeedsConnect false (vehicle registered), got true")
	}
	if vm.VehicleName != "Magus" {
		t.Errorf("want VehicleName Magus, got %q", vm.VehicleName)
	}
	if vm.Status != "awaiting" {
		t.Errorf("want Status awaiting, got %q", vm.Status)
	}
	if vm.StatusLabel == "" {
		t.Errorf("want non-empty StatusLabel for awaiting, got empty")
	}
	if vm.BatteryPct != "" || vm.LastSeenLabel != "" {
		t.Errorf("want no battery/last-seen for awaiting, got battery=%q lastSeen=%q", vm.BatteryPct, vm.LastSeenLabel)
	}
}

func TestNavHeaderFor_NeedsConnect(t *testing.T) {
	// No registered vehicles -> NeedsConnect state (connect link, no dot/name).
	acct := &fakeAccount{registered: nil}
	reader := &fakeReader{snapshots: []telemetry.Snapshot{}}
	h := newNavHeaderHandler(acct, reader)
	vm := h.navHeaderFor(context.Background(), uuid.New())

	if !vm.NeedsConnect {
		t.Fatalf("want NeedsConnect true when no vehicles registered, got false")
	}
	if vm.VehicleName != "" {
		t.Errorf("want no VehicleName in NeedsConnect state, got %q", vm.VehicleName)
	}
	if vm.Status != "" {
		t.Errorf("want empty Status in NeedsConnect state (no dot rendered), got %q", vm.Status)
	}
	if vm.BatteryPct != "" {
		t.Errorf("want no BatteryPct in NeedsConnect state, got %q", vm.BatteryPct)
	}
}

func TestNavHeaderFor_AccountError(t *testing.T) {
	// RegisteredVehicles errors -> degraded Unavailable, no name.
	acct := &fakeAccount{regErr: errFake}
	reader := &fakeReader{snapshots: []telemetry.Snapshot{}}
	h := newNavHeaderHandler(acct, reader)
	vm := h.navHeaderFor(context.Background(), uuid.New())

	if vm.NeedsConnect {
		t.Errorf("want NeedsConnect false on account error (don't pretend to know), got true")
	}
	if vm.VehicleName != "" {
		t.Errorf("want no VehicleName on account error, got %q", vm.VehicleName)
	}
	if vm.Status != "unavailable" {
		t.Errorf("want Status unavailable, got %q", vm.Status)
	}
	if vm.StatusLabel == "" {
		t.Errorf("want non-empty StatusLabel for unavailable, got empty")
	}
}

func TestNavHeaderFor_TelemetryReaderError(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 42, VIN: "VIN42", DisplayName: "Magus"},
	}}
	reader := &fakeReader{err: errFake}
	h := newNavHeaderHandler(acct, reader)
	vm := h.navHeaderFor(context.Background(), uuid.New())

	// Name preserved (account read ok), status degraded, no battery.
	if vm.VehicleName != "Magus" {
		t.Errorf("want VehicleName preserved on telemetry error, got %q", vm.VehicleName)
	}
	if vm.Status != "unavailable" {
		t.Errorf("want Status unavailable on telemetry error, got %q", vm.Status)
	}
	if vm.BatteryPct != "" {
		t.Errorf("want no BatteryPct on telemetry error, got %q", vm.BatteryPct)
	}
}

// navHeaderEngine builds a minimal Gin engine with session middleware, a /_session
// route that seeds the uid (matching the sessionCookie contract from
// charges_test.go), and the nav-header fragment route. Reuses sessionCookie.
func navHeaderEngine(h *Handler, uid uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := cookie.NewStore([]byte("test-secret"))
	r.Use(sessions.Sessions("test", store))
	r.GET("/_session", func(c *gin.Context) {
		sess := sessions.Default(c)
		sess.Set("uid", uid.String())
		_ = sess.Save()
		c.String(http.StatusOK, "ok")
	})
	r.GET("/ui/nav-header", h.NavHeaderFragment)
	return r
}

// TestNavHeaderFragment_ConnectedHTTP exercises the full fragment route with a
// seeded session: an authenticated GET /ui/nav-header returns 200 and the rendered
// fragment contains the vehicle name, the success-colored status dot
// (badge-success), and the pre-computed battery percentage.
func TestNavHeaderFragment_ConnectedHTTP(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 42, VIN: "VIN42", DisplayName: "Magus"},
	}}
	reader := &fakeReader{snapshots: []telemetry.Snapshot{
		{TeslaID: 42, CapturedAt: time.Now().Add(-1 * time.Hour), BatteryLevel: 94},
	}}
	h := newNavHeaderHandler(acct, reader)
	eng := navHeaderEngine(h, uid)
	cookie := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/nav-header", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for authenticated nav-header fragment, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		`id="nav-header"`,
		"Magus",
		"badge-success",
		"94%",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("nav-header body missing %q\n%s", want, body)
		}
	}
}

// homeEngine builds a minimal Gin engine with session middleware, a /_session route
// that seeds uid + email (so sessionCookie can forge a signed-in cookie), and the
// home route wired to h.Home. Mirrors the navHeaderEngine pattern.
func homeEngine(h *Handler, uid uuid.UUID, email string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := cookie.NewStore([]byte("test-secret"))
	r.Use(sessions.Sessions("test", store))
	r.GET("/_session", func(c *gin.Context) {
		sess := sessions.Default(c)
		sess.Set("uid", uid.String())
		sess.Set("email", email)
		_ = sess.Save()
		c.String(http.StatusOK, "ok")
	})
	r.GET("/", h.Home)
	return r
}

// TestHome_SignedInRendersUserContent verifies the signed-in home state: the page
// shows the account email, the "View your vehicles" link, the "Connect your Tesla"
// link, and the "Log out" button — and does NOT contain the removed debug bits
// ("Visits this session", "Check database") that were stripped in T1.
func TestHome_SignedInRendersUserContent(t *testing.T) {
	uid := uuid.New()
	const email = "driver@example.com"

	h := New(Deps{Account: &fakeAccount{}, Tesla: &fakeTesla{}, TelemetryReader: &fakeReader{}})
	eng := homeEngine(h, uid, email)

	// homeEngine's /_session seeds both uid and email in one request; sessionCookie
	// calls /_session and returns the resulting session cookie.
	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for signed-in home, got %d", w.Code)
	}
	body := w.Body.String()

	// Signed-in content must be present.
	for _, want := range []string{
		email,
		`href="/dashboard"`,
		`href="/connect/tesla"`,
		"Log out",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("signed-in home body missing %q\n%s", want, body)
		}
	}

	// Debug bits removed by T1 must be absent.
	for _, gone := range []string{"Visits this session", "Check database"} {
		if strings.Contains(body, gone) {
			t.Errorf("signed-in home body should NOT contain %q", gone)
		}
	}

	// Anonymous sign-in link must NOT appear when signed in.
	if strings.Contains(body, `href="/login"`) {
		t.Errorf("signed-in home should NOT contain sign-in link")
	}
}
