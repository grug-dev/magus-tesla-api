package handlers

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
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
func (f *fakeAccount) SeedVehicles(_ context.Context, _ uuid.UUID, vs []account.SeedVehicle) ([]account.Vehicle, error) {
	f.seedCalls++
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
func (f fakeTesla) VehicleData(context.Context, tesla.Credentials, int64) (*tesla.VehicleDataTesla, error) {
	return nil, nil
}
func (f fakeTesla) WakeUp(context.Context, tesla.Credentials, int64) (*tesla.VehicleTesla, error) {
	return nil, nil
}

func newHandler(acct account.Service, tsvc tesla.VehicleService) *Handler {
	return New(Deps{Account: acct, Tesla: tsvc})
}

func TestVehiclesFor_RegisteredRendersWithoutTeslaCall(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1, VIN: "VIN1", DisplayName: "Magus"},
		{TeslaID: 2, VIN: "VIN2", DisplayName: "Second"},
	}}
	tsvc := &fakeTesla{vehicles: []tesla.VehicleTesla{
		{DisplayName: "SHOULD NOT BE CALLED", VIN: "NEVER"},
	}}
	h := newHandler(acct, tsvc)
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
	h := newHandler(acct, fakeTesla{})
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
