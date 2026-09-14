package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
)

// TestOwnedVehicles_ReturnsRefsAndVehicles covers the design's Test Contract
// case 1: an account with registered vehicles gets a Ref per vehicle, in the
// same order, plus the plain vehicle list.
func TestOwnedVehicles_ReturnsRefsAndVehicles(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 111}, {TeslaID: 222},
	}}
	h := newHandler(acct, fakeTesla{})

	refs, vehicles, ok := h.ownedVehicles(context.Background(), uuid.New())
	if !ok {
		t.Fatalf("ownedVehicles: ok = false, want true")
	}
	if len(refs) != 2 {
		t.Fatalf("len(refs) = %d, want 2", len(refs))
	}
	if refs[0].TeslaID() != 111 || refs[1].TeslaID() != 222 {
		t.Fatalf("refs = %+v, want TeslaIDs 111, 222 in order", refs)
	}
	if len(vehicles) != 2 || vehicles[0].TeslaID != 111 || vehicles[1].TeslaID != 222 {
		t.Fatalf("vehicles = %+v, want the fake's registered slice in order", vehicles)
	}
}

// TestOwnedVehicles_NoRegisteredVehicles covers Test Contract case 2: an
// account with no registered vehicles (no error) is not "no filter" -- ok
// must be false with empty refs and vehicles.
func TestOwnedVehicles_NoRegisteredVehicles(t *testing.T) {
	acct := &fakeAccount{registered: nil}
	h := newHandler(acct, fakeTesla{})

	refs, vehicles, ok := h.ownedVehicles(context.Background(), uuid.New())
	if ok {
		t.Fatalf("ownedVehicles: ok = true, want false")
	}
	if len(refs) != 0 {
		t.Fatalf("len(refs) = %d, want 0", len(refs))
	}
	if len(vehicles) != 0 {
		t.Fatalf("len(vehicles) = %d, want 0", len(vehicles))
	}
}

// TestOwnedVehicles_RegisteredVehiclesError covers Test Contract case 3: a
// lookup failure renders the same result as an empty list -- ok is false, so
// a transient read failure cannot be distinguished from owning nothing.
func TestOwnedVehicles_RegisteredVehiclesError(t *testing.T) {
	acct := &fakeAccount{regErr: errors.New("db unavailable")}
	h := newHandler(acct, fakeTesla{})

	refs, vehicles, ok := h.ownedVehicles(context.Background(), uuid.New())
	if ok {
		t.Fatalf("ownedVehicles: ok = true, want false")
	}
	if len(refs) != 0 {
		t.Fatalf("len(refs) = %d, want 0", len(refs))
	}
	if len(vehicles) != 0 {
		t.Fatalf("len(vehicles) = %d, want 0", len(vehicles))
	}
}
