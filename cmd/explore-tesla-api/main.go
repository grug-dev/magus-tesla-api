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
	"path/filepath"
	"strconv"
	"strings"
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
	tz := flag.String("tz", "America/Bogota", "IANA time zone for the Wall Connector charge-history window (energy telemetry)")
	months := flag.Int("months", 6, "how many months back to query Wall Connector charge history")
	out := flag.String("out", "cmd/explore-tesla-api/output", "directory where each API call's raw JSON is written (one file per call, overwritten each run)")
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

	// Wipe the output dir before the run so its listing reflects ONLY this run. The order
	// prefix is the runtime position, so a call's number shifts when a conditional call
	// (Energy / WakeUp) is skipped — without this, a stale file under the old number would
	// linger next to the new one. dump recreates the dir on the first write.
	if err := os.RemoveAll(*out); err != nil {
		return fmt.Errorf("clearing output dir %q: %w", *out, err)
	}

	// record writes one raw API response to its own file under *out, tagging it with the
	// runtime execution order (1 = first call made this run, 2 = second, …). The counter
	// lives in the closure so order tracks the ACTUAL sequence — the conditional Energy /
	// WakeUp calls simply do not advance it when they are skipped.
	order := 0
	record := func(name, endpoint string, raw json.RawMessage) error {
		order++
		return dump(*out, order, name, endpoint, raw)
	}

	// 1. List vehicles — refresh the saved token on the fly if it has expired, then
	//    print the full raw body and pick the target. creds is reassigned to the
	//    refreshed credentials so every later call in this run reuses the fresh token.
	listEndpoint := "GET /api/1/vehicles"
	log.Println("→ " + listEndpoint)
	listRaw, creds, err := listVehiclesWithAutoRefresh(ctx, client, cfg, creds)
	if err != nil {
		return err
	}
	if err := record("ListVehicles", listEndpoint, listRaw); err != nil {
		return err
	}

	vehicle, err := pickVehicle(listRaw, *index)
	if err != nil {
		return err
	}
	log.Printf("→ selected vehicle [%d]: %q (id=%d, state=%s)", *index, vehicle.DisplayName, vehicle.ID, vehicle.State)

	// 2. Charging history — account-level, no vehicle id, no wake needed.
	chargingEndpoint := "GET /api/1/dx/charging/history"
	log.Println("→ " + chargingEndpoint)
	chargingRaw, err := client.ChargingHistoryRaw(ctx, creds)
	if err != nil {
		return err
	}
	if err := record("ChargingHistory", chargingEndpoint, chargingRaw); err != nil {
		return err
	}

	// 2b. Products — discover the account's energy products (Powerwall / Wall Connectors)
	//     and any energy_site_id. Server-side, no wake. Energy products only appear when the
	//     token carries the energy_device_data scope (else 403 / a vehicles-only list).
	productsEndpoint := "GET /api/1/products"
	log.Println("→ " + productsEndpoint)
	productsRaw, err := client.ProductsRaw(ctx, creds)
	if err != nil {
		return err
	}
	if err := record("Products", productsEndpoint, productsRaw); err != nil {
		return err
	}

	// 2c. Wall Connector charge history — ONLY if THIS account owns an energy site. A charge
	//     at someone else's Wall Connector lives on THEIR account and never appears here.
	if siteID := firstEnergySiteID(productsRaw); siteID != "" {
		// Tesla's telemetry_history endpoint rejects bare dates ("Invalid start_date");
		// it requires full ISO 8601 timestamps WITH a timezone offset, e.g.
		// 2026-01-18T00:00:00-05:00. Anchor both bounds in the requested time_zone so
		// the offset in the timestamp matches the time_zone query param.
		loc, err := time.LoadLocation(*tz)
		if err != nil {
			return fmt.Errorf("loading time zone %q: %w", *tz, err)
		}
		now := time.Now().In(loc)
		start := now.AddDate(0, -*months, 0).Format(time.RFC3339)
		end := now.Format(time.RFC3339)
		energyEndpoint := fmt.Sprintf("GET /api/1/energy_sites/%s/telemetry_history?kind=charge&start_date=%s&end_date=%s&time_zone=%s", siteID, start, end, *tz)
		log.Println("→ " + energyEndpoint)
		energyRaw, err := client.EnergyChargeHistoryRaw(ctx, creds, siteID, start, end, *tz)
		if err != nil {
			return err
		}
		if err := record("EnergyChargeHistory", energyEndpoint, energyRaw); err != nil {
			return err
		}
	} else {
		log.Println("→ /api/1/products lists no energy_site_id — this account owns no Tesla " +
			"energy product / Wall Connector, so there is no Wall Connector charge history to " +
			"fetch (a charge at someone else's Wall Connector is on THEIR account). Skipping.")
	}

	// 3. Wake + poll until online (VehicleData requires an online vehicle).
	if vehicle.State != "online" {
		wakeEndpoint := fmt.Sprintf("POST /api/1/vehicles/%d/wake_up", vehicle.ID)
		log.Println("→ " + wakeEndpoint)
		wakeRaw, err := client.WakeUpRaw(ctx, creds, vehicle.ID)
		if err != nil {
			return err
		}
		if err := record("WakeUp", wakeEndpoint, wakeRaw); err != nil {
			return err
		}

		if err := pollUntilOnline(ctx, client, creds, vehicle.ID, *index); err != nil {
			return err
		}
	}

	// 4. Full raw snapshot.
	dataEndpoint := fmt.Sprintf("GET /api/1/vehicles/%d/vehicle_data", vehicle.ID)
	log.Println("→ " + dataEndpoint)
	dataRaw, err := client.VehicleDataRaw(ctx, creds, vehicle.ID)
	if err != nil {
		return err
	}
	if err := record("VehicleData", dataEndpoint, dataRaw); err != nil {
		return err
	}

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

	// Diagnose WHY the access token 401'd before attempting refresh. The shared
	// tesla client discards Tesla's response body, so re-issue the call locally to
	// capture it. This is read-only and touches nothing the web server uses.
	log.Printf("→ access token rejected by /api/1/vehicles; Tesla response: %s", dumpListBody(cfg.AccessToken))

	if cfg.RefreshToken == "" {
		return nil, creds, fmt.Errorf("access token expired and no TESLA_REFRESH_TOKEN in .env to refresh with: %w", err)
	}

	log.Println("→ access token expired; refreshing via TESLA_REFRESH_TOKEN")
	tokens, rerr := auth.RefreshTokens(cfg.ClientID, cfg.ClientSecret, cfg.RefreshToken)
	if rerr != nil {
		// Diagnose WHY the refresh 400'd. auth.RefreshTokens discards Tesla's body,
		// so re-issue the grant locally to capture the actual error reason
		// (invalid_grant / invalid_client / invalid_scope). Safe to re-issue: a 400
		// means Tesla already rejected the refresh.
		log.Printf("→ refresh failed; Tesla response: %s", dumpRefreshBody(cfg.ClientID, cfg.RefreshToken))
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

// firstEnergySiteID scans the /products response for the first energy product and
// returns its energy_site_id as a string (json.Number preserves the large integer id
// without float precision loss). Vehicles carry no energy_site_id and are skipped, so
// "" means the account has no energy products (Powerwall / Wall Connector).
func firstEnergySiteID(productsRaw json.RawMessage) string {
	var env struct {
		Response []struct {
			EnergySiteID json.Number `json:"energy_site_id"`
		} `json:"response"`
	}
	if err := json.Unmarshal(productsRaw, &env); err != nil {
		return ""
	}
	for _, p := range env.Response {
		if s := p.EnergySiteID.String(); s != "" && s != "0" {
			return s
		}
	}
	return ""
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

// wrappedEnvelope is the FALLBACK on-disk shape, used only when the Tesla body cannot be
// merged (invalid JSON, or a non-object top-level). The normal path (mergeEnvelope)
// splices the body's own keys in instead, so there is no "response" wrapper.
type wrappedEnvelope struct {
	Order    int             `json:"order"`
	Name     string          `json:"name"`
	Endpoint string          `json:"endpoint"`
	Response json.RawMessage `json:"response"`
}

// dump writes one API call to outDir/<name>.json, overwriting any file from a previous
// run. A one-line note goes to stderr so the progress log stays readable; the payload
// itself lives in the file rather than scrolling past in the console.
func dump(outDir string, order int, name, endpoint string, raw json.RawMessage) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating output dir %q: %w", outDir, err)
	}

	pretty, err := mergeEnvelope(order, name, endpoint, raw)
	if err != nil {
		return fmt.Errorf("building %s output: %w", name, err)
	}

	// Prefix the filename with the execution order so the directory listing sorts in the
	// sequence the calls ran (1-ListVehicles.json, 2-ChargingHistory.json, …).
	path := filepath.Join(outDir, fmt.Sprintf("%d-%s.json", order, name))
	if err := os.WriteFile(path, append(pretty, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	log.Printf("→ wrote %s", path)
	return nil
}

// mergeEnvelope builds the file body: the metadata keys (order/name/endpoint) first, then
// the Tesla response body's OWN top-level keys spliced in verbatim — so the file shows the
// real Fleet API shape (response / data / count / …) with no extra wrapper key. It uses
// json.Indent (not decode+re-encode), which preserves the body's key order AND exact
// numbers, so large Tesla ids keep full precision.
func mergeEnvelope(order int, name, endpoint string, raw json.RawMessage) ([]byte, error) {
	var body bytes.Buffer
	if err := json.Indent(&body, raw, "", "  "); err != nil {
		// Body isn't valid JSON (unexpected for a 2xx). Preserve it as a JSON string under
		// "response" rather than losing it — mirrors the old printJSON fallback.
		return json.MarshalIndent(wrappedEnvelope{order, name, endpoint,
			json.RawMessage(strconv.Quote(string(raw)))}, "", "  ")
	}

	trimmed := strings.TrimSpace(body.String())
	if !strings.HasPrefix(trimmed, "{") {
		// Non-object top-level (array/scalar) — no keys to merge into. Keep it under
		// "response". The Fleet API always wraps in an object, so this is defensive only.
		return json.MarshalIndent(wrappedEnvelope{order, name, endpoint, raw}, "", "  ")
	}

	// Splice our metadata keys in as the first members of the body's object.
	meta := fmt.Sprintf("  %q: %d,\n  %q: %q,\n  %q: %q",
		"order", order, "name", name, "endpoint", endpoint)
	inner := strings.TrimLeft(trimmed[1:len(trimmed)-1], "\n")
	if strings.TrimSpace(inner) == "" {
		// Empty Tesla object — metadata only, no trailing comma.
		return []byte("{\n" + meta + "\n}"), nil
	}
	return []byte("{\n" + meta + ",\n" + inner + "}"), nil
}
