package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
)

// TestOwnedVehicles_ReturnsRefsAndVehicles: an account with registered
// vehicles gets one Ref per vehicle, in the same order, plus the plain vehicle
// list. Both slices are two views of one read. No caller pairs them by index --
// the label lookup matches on TeslaID -- but the order is asserted so a future
// caller may rely on it.
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

// TestOwnedVehicles_NoRegisteredVehicles: an account with no registered
// vehicles is not "no filter". ok must be false, so a caller stops instead of
// reading every vehicle's rows.
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

// TestOwnedVehicles_RegisteredVehiclesError: a lookup failure gives the same
// result as an empty list. ok is false, so a read error can never widen what
// the caller sees.
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
