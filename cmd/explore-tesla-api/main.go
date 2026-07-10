// Command explore-tesla-api is an on-demand tool for inspecting the RAW JSON the
// Tesla Fleet API returns — including every field the typed ...Tesla DTOs omit.
//
// It is NOT part of the test suite: this is package main with no _test.go, so
// `go test ./...` never compiles or runs it. Each run makes real, paid Fleet API
// calls and wakes the car, so run it only when you want to explore live responses:
//
//	go run ./cmd/explore-tesla-api          # first vehicle
//	go run ./cmd/explore-tesla-api -i 1      # second vehicle
//
// It reads TESLA_ACCESS_TOKEN from .env via config.Load(). Access tokens live ~8h;
// when the saved one has expired the run transparently refreshes it using the
// TESLA_REFRESH_TOKEN (also in .env), persists the rotated pair back to .env, and
// retries — so a stale access token no longer forces a re-run of `go run ./cmd/setup`.
// Only if the refresh token itself is dead (~3-month lifetime, single-use) does the
// run fail with a hint to re-run setup.
//
// Progress goes to stderr; the raw JSON payloads go to stdout (pretty-printed).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/auth"
	"github.com/cristianpena/magus-tesla-api/internal/config"
	"github.com/cristianpena/magus-tesla-api/internal/tesla"
)

// pollInterval and pollTimeout bound the wake loop so a car that never wakes does
// not spin forever (each poll is a real list call).
const (
	pollInterval = 3 * time.Second
	pollTimeout  = 60 * time.Second
)

// rawVehicle is a minimal projection of the inventory payload — just enough to pick
// the target vehicle and read its state. It is local parsing glue, not a domain
// model; the full raw body is what we print.
type rawVehicle struct {
	ID          int64  `json:"id"`
	State       string `json:"state"`
	DisplayName string `json:"display_name"`
}

func main() {
	log.SetFlags(0)
	if err := run(); err != nil {
		if errors.Is(err, tesla.ErrUnauthorized) {
			log.Fatalf("access token expired or invalid — re-run `go run ./cmd/setup` to refresh it.\n(%v)", err)
		}
		log.Fatalf("error: %v", err)
	}
}

func run() error {
	index := flag.Int("i", 0, "index of the vehicle to explore (from the inventory list)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.AccessToken == "" {
		return fmt.Errorf("TESLA_ACCESS_TOKEN is empty — run `go run ./cmd/setup` first to obtain one")
	}

	ctx := context.Background()
	client := tesla.NewClient()
	creds := tesla.Credentials{AccessToken: cfg.AccessToken}

	// 1. List vehicles — refresh the saved token on the fly if it has expired, then
	//    print the full raw body and pick the target. creds is reassigned to the
	//    refreshed credentials so every later call in this run reuses the fresh token.
	log.Println("→ GET /api/1/vehicles")
	listRaw, creds, err := listVehiclesWithAutoRefresh(ctx, client, cfg, creds)
	if err != nil {
		return err
	}
	printJSON("ListVehicles", listRaw)

	vehicle, err := pickVehicle(listRaw, *index)
	if err != nil {
		return err
	}
	log.Printf("→ selected vehicle [%d]: %q (id=%d, state=%s)", *index, vehicle.DisplayName, vehicle.ID, vehicle.State)

	// 2. Wake + poll until online (VehicleData requires an online vehicle).
	if vehicle.State != "online" {
		log.Printf("→ POST /api/1/vehicles/%d/wake_up", vehicle.ID)
		wakeRaw, err := client.WakeUpRaw(ctx, creds, vehicle.ID)
		if err != nil {
			return err
		}
		printJSON("WakeUp", wakeRaw)

		if err := pollUntilOnline(ctx, client, creds, vehicle.ID, *index); err != nil {
			return err
		}
	}

	// 3. Full raw snapshot.
	log.Printf("→ GET /api/1/vehicles/%d/vehicle_data", vehicle.ID)
	dataRaw, err := client.VehicleDataRaw(ctx, creds, vehicle.ID)
	if err != nil {
		return err
	}
	printJSON("VehicleData", dataRaw)

	return nil
}

// listVehiclesWithAutoRefresh lists vehicles with the saved access token and, if that
// token has expired (HTTP 401), transparently refreshes it once via the .env refresh
// token, persists the rotated pair back to .env, and retries. It returns the raw list
// body together with the (possibly refreshed) credentials so the rest of the run reuses
// the fresh access token.
//
// Refresh mirrors what the web server's account module does with its DB-backed tokens,
// but here it is file-backed: the .env refresh-token lineage is independent of the DB
// one (each came from a separate OAuth grant), so rotating it here does not disturb the
// running web server.
func listVehiclesWithAutoRefresh(ctx context.Context, client *tesla.Client, cfg *config.Config, creds tesla.Credentials) (json.RawMessage, tesla.Credentials, error) {
	listRaw, err := client.ListVehiclesRaw(ctx, creds)
	if !errors.Is(err, tesla.ErrUnauthorized) {
		// Success, or a non-auth failure that refreshing cannot fix — surface as-is.
		return listRaw, creds, err
	}
	if cfg.RefreshToken == "" {
		return nil, creds, fmt.Errorf("access token expired and no TESLA_REFRESH_TOKEN in .env to refresh with: %w", err)
	}

	log.Println("→ access token expired; refreshing via TESLA_REFRESH_TOKEN")
	tokens, rerr := auth.RefreshTokens(cfg.ClientID, cfg.ClientSecret, cfg.RefreshToken)
	if rerr != nil {
		return nil, creds, fmt.Errorf("access token expired and refresh failed "+
			"(the refresh token may be expired or already consumed — re-run `go run ./cmd/setup`): %w", rerr)
	}
	if serr := config.SaveTokens(tokens.AccessToken, tokens.RefreshToken); serr != nil {
		return nil, creds, fmt.Errorf("saving refreshed tokens to .env: %w", serr)
	}
	log.Println("→ refreshed access token and saved the rotated pair to .env")

	creds = tesla.Credentials{AccessToken: tokens.AccessToken}
	listRaw, err = client.ListVehiclesRaw(ctx, creds)
	return listRaw, creds, err
}

// pickVehicle unmarshals the inventory's minimal projection and returns the vehicle
// at the given index.
func pickVehicle(listRaw json.RawMessage, index int) (rawVehicle, error) {
	var envelope struct {
		Response []rawVehicle `json:"response"`
	}
	if err := json.Unmarshal(listRaw, &envelope); err != nil {
		return rawVehicle{}, fmt.Errorf("parsing vehicle list: %w", err)
	}
	if len(envelope.Response) == 0 {
		return rawVehicle{}, errors.New("no vehicles on this account")
	}
	if index < 0 || index >= len(envelope.Response) {
		return rawVehicle{}, fmt.Errorf("vehicle index %d out of range (%d vehicle(s) available)", index, len(envelope.Response))
	}
	return envelope.Response[index], nil
}

// pollUntilOnline lists vehicles every pollInterval until the target reports online
// or pollTimeout elapses.
func pollUntilOnline(ctx context.Context, client *tesla.Client, creds tesla.Credentials, vehicleID int64, index int) error {
	deadline := time.Now().Add(pollTimeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("vehicle %d did not come online within %s", vehicleID, pollTimeout)
		}
		time.Sleep(pollInterval)

		listRaw, err := client.ListVehiclesRaw(ctx, creds)
		if err != nil {
			return err
		}
		vehicle, err := pickVehicle(listRaw, index)
		if err != nil {
			return err
		}
		log.Printf("… waiting for wake: state=%s", vehicle.State)
		if vehicle.State == "online" {
			return nil
		}
	}
}

// printJSON pretty-prints a raw JSON body to stdout under a labelled banner.
func printJSON(label string, raw json.RawMessage) {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		// Fall back to the unformatted body rather than losing the payload.
		fmt.Fprintf(os.Stdout, "==== %s (raw) ====\n%s\n", label, raw)
		return
	}
	fmt.Fprintf(os.Stdout, "==== %s ====\n%s\n", label, pretty.String())
}
