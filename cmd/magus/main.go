// Command magus fetches live data from Magus via the Tesla Fleet API.
// Step 8 of post-registration-setup.md — first API call.
// Automatically refreshes the access token when it expires.
package main

import (
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/auth"
	"github.com/cristianpena/magus-tesla-api/internal/config"
	"github.com/cristianpena/magus-tesla-api/internal/vehicle"
)

func main() {
	// Step 1 — Load credentials and tokens from .env.
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	client := vehicle.NewClient(cfg.AccessToken)

	// Step 8 — List vehicles to find Magus's ID.
	// On HTTP 401 (access token expired), refresh automatically and retry once.
	fmt.Println("Fetching vehicle list...")
	vehicles, err := client.List()
	if errors.Is(err, vehicle.ErrUnauthorized) {
		cfg, client, err = refreshTokens(cfg)
		if err != nil {
			log.Fatalf("Token refresh failed: %v\nRun `go run ./cmd/setup` to re-authenticate.", err)
		}
		vehicles, err = client.List()
	}
	if err != nil {
		log.Fatalf("Could not list vehicles: %v", err)
	}
	if len(vehicles) == 0 {
		log.Fatal("No vehicles found on this account.")
	}

	magus := vehicles[0]
	fmt.Printf("Found: %s (VIN: %s) — state: %s\n\n", magus.DisplayName, magus.VIN, magus.State)

	// Wake up the vehicle if it is not online before requesting data.
	// Both "asleep" and "offline" states must be woken before vehicle_data
	// will return — an offline car rejects the data call with HTTP 408.
	if magus.State != "online" {
		fmt.Printf("Magus is %s. Sending wake-up signal...\n", magus.State)
		if _, err := client.WakeUp(magus.ID); err != nil {
			log.Fatalf("Wake-up failed: %v", err)
		}

		fmt.Print("Waiting for Magus to come online")
		for range 15 {
			time.Sleep(4 * time.Second)
			fmt.Print(".")
			list, err := client.List()
			if err == nil && len(list) > 0 && list[0].State == "online" {
				magus = list[0]
				break
			}
		}
		fmt.Println()

		if magus.State != "online" {
			log.Fatal("Magus did not come online in time. Try again.")
		}
		fmt.Println("Magus is online.")
	}

	// Step 8 — Fetch full vehicle data.
	fmt.Println("Fetching vehicle data...")
	data, err := client.Data(magus.ID)
	if err != nil {
		log.Fatalf("Could not fetch vehicle data: %v", err)
	}

	printSummary(data)
}

// refreshTokens uses the saved refresh token to get a new access token,
// persists both new tokens to .env, and returns an updated config and client.
func refreshTokens(cfg *config.Config) (*config.Config, *vehicle.Client, error) {
	fmt.Println("[Token] Access token expired — refreshing automatically...")

	tokens, err := auth.RefreshTokens(cfg.ClientID, cfg.ClientSecret, cfg.RefreshToken)
	if err != nil {
		return nil, nil, fmt.Errorf("could not refresh tokens: %w", err)
	}

	if err := config.SaveTokens(tokens.AccessToken, tokens.RefreshToken); err != nil {
		return nil, nil, fmt.Errorf("could not save new tokens to .env: %w", err)
	}

	cfg.AccessToken = tokens.AccessToken
	cfg.RefreshToken = tokens.RefreshToken
	fmt.Println("[Token] New tokens saved to .env.")

	return cfg, vehicle.NewClient(cfg.AccessToken), nil
}

func printSummary(d *vehicle.VehicleData) {
	fmt.Printf("\n=== %s — Live Snapshot ===\n\n", d.DisplayName)

	fmt.Println("[ Battery & Charging ]")
	fmt.Printf("  Battery level   : %d%%\n", d.ChargeState.BatteryLevel)
	fmt.Printf("  Estimated range : %.1f mi (%.1f km)\n", d.ChargeState.BatteryRange, d.ChargeState.BatteryRangeKm())
	fmt.Printf("  Charging state  : %s\n", d.ChargeState.ChargingState)
	if d.ChargeState.ChargingState == "Charging" {
		fmt.Printf("  Charge rate     : %.1f mph (%.1f km/h)\n", d.ChargeState.ChargeRate, d.ChargeState.ChargeRateKmh())
		fmt.Printf("  Time to full    : %.1f hrs\n", d.ChargeState.TimeToFullCharge)
	}

	fmt.Println("\n[ Climate ]")
	fmt.Printf("  Inside temp     : %.1f°C\n", d.ClimateState.InsideTemp)
	fmt.Printf("  Outside temp    : %.1f°C\n", d.ClimateState.OutsideTemp)
	fmt.Printf("  Climate on      : %v\n", d.ClimateState.IsClimateOn)

	fmt.Println("\n[ Location & Drive ]")
	fmt.Printf("  Latitude        : %.6f\n", d.DriveState.Latitude)
	fmt.Printf("  Longitude       : %.6f\n", d.DriveState.Longitude)
	fmt.Printf("  Heading         : %d°\n", d.DriveState.Heading)
	if d.DriveState.Speed != nil {
		fmt.Printf("  Speed           : %.1f mph (%.1f km/h)\n", *d.DriveState.Speed, *d.DriveState.SpeedKmh())
	}

	fmt.Println("\n[ Vehicle State ]")
	fmt.Printf("  Locked          : %v\n", d.VehicleState.Locked)
	fmt.Printf("  Odometer        : %.1f mi (%.1f km)\n", d.VehicleState.Odometer, d.VehicleState.OdometerKm())
	fmt.Printf("  Software        : %s\n", d.VehicleState.CarVersion)
	fmt.Println()
}
