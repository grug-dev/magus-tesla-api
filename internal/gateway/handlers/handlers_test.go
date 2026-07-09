package handlers

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/tesla"
)

// --- fakes for the account and tesla interfaces (no DB, no real Tesla) ---

type fakeAccount struct {
	token    string
	tokenErr error
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

func TestVehiclesFor_ListsAndMapsVehicles(t *testing.T) {
	h := newHandler(
		fakeAccount{token: "tok"},
		fakeTesla{vehicles: []tesla.VehicleTesla{
			{DisplayName: "Magus", VIN: "VIN1", State: "online"},
			{DisplayName: "Second", VIN: "VIN2", State: "asleep"},
		}},
	)
	d := h.vehiclesFor(context.Background(), uuid.New())
	if len(d.Vehicles) != 2 {
		t.Fatalf("want 2 vehicles, got %d (%+v)", len(d.Vehicles), d)
	}
	if d.Vehicles[0].DisplayName != "Magus" || d.Vehicles[0].VIN != "VIN1" || d.Vehicles[0].State != "online" {
		t.Errorf("unexpected mapping of vendor DTO: %+v", d.Vehicles[0])
	}
}

func TestVehiclesFor_NoConnectionPromptsConnect(t *testing.T) {
	h := newHandler(fakeAccount{tokenErr: account.ErrNoTeslaConnection}, fakeTesla{})
	d := h.vehiclesFor(context.Background(), uuid.New())
	if !d.NeedsConnect || len(d.Vehicles) != 0 {
		t.Fatalf("want NeedsConnect and no vehicles, got %+v", d)
	}
}

func TestVehiclesFor_UnauthorizedPromptsReconnect(t *testing.T) {
	h := newHandler(fakeAccount{token: "tok"}, fakeTesla{listErr: tesla.ErrUnauthorized})
	d := h.vehiclesFor(context.Background(), uuid.New())
	if !d.NeedsConnect || d.Notice == "" {
		t.Fatalf("want a reconnect notice with NeedsConnect, got %+v", d)
	}
}

func TestVehiclesFor_EmptyListShowsNotice(t *testing.T) {
	h := newHandler(fakeAccount{token: "tok"}, fakeTesla{vehicles: nil})
	d := h.vehiclesFor(context.Background(), uuid.New())
	if len(d.Vehicles) != 0 || d.NeedsConnect || d.Notice == "" {
		t.Fatalf("want a plain notice (no vehicles, no connect prompt), got %+v", d)
	}
}
