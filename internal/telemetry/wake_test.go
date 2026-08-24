package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/tesla"
)

// --- wake helper: online-vs-timeout outcomes with a fake tesla port ---

func TestWaitUntilOnline_OnlineImmediately(t *testing.T) {
	ft := newFakeTesla()
	ft.set(1, &vehicleScript{state: "online"})

	online, err := waitUntilOnline(context.Background(), ft, tesla.Credentials{}, 1, 2*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !online {
		t.Fatal("want online=true for an online vehicle")
	}
}

func TestWaitUntilOnline_TimesOutWhenAsleep(t *testing.T) {
	ft := newFakeTesla()
	// Never online → the short timeout must elapse and return (false, nil).
	ft.set(2, &vehicleScript{state: "asleep"})

	start := time.Now()
	online, err := waitUntilOnline(context.Background(), ft, tesla.Credentials{}, 2, 30*time.Millisecond)
	if err != nil {
		t.Fatalf("timeout must be signalled as (false,nil), got err %v", err)
	}
	if online {
		t.Fatal("want online=false on timeout")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("timeout should be bounded by the short deadline, took %v", elapsed)
	}
}

func TestWaitUntilOnline_UnauthorizedPropagates(t *testing.T) {
	ft := newFakeTesla()
	ft.set(3, &vehicleScript{state: "online", wakeErr: tesla.ErrUnauthorized})

	online, err := waitUntilOnline(context.Background(), ft, tesla.Credentials{}, 3, 2*time.Second)
	if online {
		t.Fatal("want online=false on an adapter error")
	}
	if !errors.Is(err, tesla.ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized to propagate, got %v", err)
	}
}
