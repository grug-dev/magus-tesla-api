package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
)

// TestAuthorizeVehicle_OwnedVehicle covers the design's Test Contract case 1:
// a vehicle that belongs to the account returns a Ref for it, no error.
func TestAuthorizeVehicle_OwnedVehicle(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 111}, {TeslaID: 222},
	}}
	h := newHandler(acct, fakeTesla{})

	ref, err := h.authorizeVehicle(context.Background(), uuid.New(), 111)
	if err != nil {
		t.Fatalf("authorizeVehicle: unexpected error: %v", err)
	}
	if ref.TeslaID() != 111 {
		t.Fatalf("ref.TeslaID() = %d, want 111", ref.TeslaID())
	}
}

// TestAuthorizeVehicle_UnownedVehicle covers Test Contract case 2: a vehicle
// belonging to a different account returns errVehicleNotAuthorized, the same
// error a lookup failure returns (case 3) — the caller cannot tell them apart.
func TestAuthorizeVehicle_UnownedVehicle(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 111}, {TeslaID: 222},
	}}
	h := newHandler(acct, fakeTesla{})

	_, err := h.authorizeVehicle(context.Background(), uuid.New(), 999)
	if !errors.Is(err, errVehicleNotAuthorized) {
		t.Fatalf("authorizeVehicle: err = %v, want errVehicleNotAuthorized", err)
	}
}

// TestAuthorizeVehicle_RegisteredVehiclesError covers Test Contract case 3: an
// account-lookup failure renders the same errVehicleNotAuthorized as an
// ownership miss, so a transient read failure cannot be used to infer whether
// a vehicle id is real.
func TestAuthorizeVehicle_RegisteredVehiclesError(t *testing.T) {
	acct := &fakeAccount{regErr: errors.New("db unavailable")}
	h := newHandler(acct, fakeTesla{})

	_, err := h.authorizeVehicle(context.Background(), uuid.New(), 111)
	if !errors.Is(err, errVehicleNotAuthorized) {
		t.Fatalf("authorizeVehicle: err = %v, want errVehicleNotAuthorized", err)
	}
}
