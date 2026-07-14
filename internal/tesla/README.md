# internal/tesla — Tesla Fleet API Adapter

## What this module does

Anti-corruption adapter over the external Tesla Fleet API. It translates Fleet API HTTP
calls into typed Go values and nothing more — no identity, no tokens, no persistence.
Every method receives `Credentials` on call; the adapter is stateless.

See [`AGENTS.md`](AGENTS.md) for the full module brief, conventions, and rules.

## Currently wrapped endpoints

| Method | HTTP | Path | Purpose |
|---|---|---|---|
| `ListVehicles` / `ListVehiclesRaw` | `GET` | `/api/1/vehicles` | Account's vehicle inventory |
| `VehicleData` / `VehicleDataRaw` | `GET` | `/api/1/vehicles/{id}/vehicle_data` | Full typed + raw data snapshot |
| `WakeUp` / `WakeUpRaw` | `POST` | `/api/1/vehicles/{id}/wake_up` | Wake the vehicle |

Each typed method in `vehicles.go` has a `Raw*` sibling in `raw.go` for the exploration
tool (`cmd/explore-tesla-api`).

## Fleet API endpoint reference

The full Tesla Fleet API documentation lives at:
<https://developer.tesla.com/docs/fleet-api/getting-started/what-is-fleet-api>

Below is a community-documented reference of known endpoints (source:
[tesla-api.timdorr.com](https://tesla-api.timdorr.com)). Paths are the same base
(`fleet-api.prd.na.vn.cloud.tesla.com`) the adapter already targets. Commands may
require additional vehicle-command signing scopes beyond the data-read scope.

### Vehicle data (state) — read-only

| HTTP | Path | Description |
|---|---|---|
| `GET` | `/api/1/vehicles` | List account's vehicles (paginated, 100/page) |
| `GET` | `/api/1/vehicles/{id}` | Single vehicle basic info |
| `GET` | `/api/1/vehicles/{id}/vehicle_data` | Full rollup snapshot (drive, climate, charge, gui, vehicle_state, vehicle_config) |
| `GET` | `/api/1/vehicles/{id}/data` | Legacy alias for `vehicle_data` |
| `GET` | `/api/1/vehicles/{id}/data_request/charge_state` | Charge state only (deprecated → use `vehicle_data`) |
| `GET` | `/api/1/vehicles/{id}/data_request/climate_state` | Climate state only (deprecated) |
| `GET` | `/api/1/vehicles/{id}/data_request/drive_state` | Drive/position state only (deprecated) |
| `GET` | `/api/1/vehicles/{id}/data_request/gui_settings` | GUI settings only (deprecated) |
| `GET` | `/api/1/vehicles/{id}/data_request/vehicle_state` | Physical vehicle state only (deprecated) |
| `GET` | `/api/1/vehicles/{id}/data_request/vehicle_config` | Vehicle configuration only (deprecated) |
| `GET` | `/api/1/vehicles/{id}/mobile_enabled` | Whether mobile access is enabled |
| `GET` | `/api/1/vehicles/{id}/nearby_charging_sites` | Nearby Tesla superchargers + destination chargers |

### Vehicle commands — control

#### Wake

| HTTP | Path | Description |
|---|---|---|
| `POST` | `/api/1/vehicles/{id}/wake_up` | Wake vehicle from sleep |

#### Alerts

| HTTP | Path | Description |
|---|---|---|
| `POST` | `/api/1/vehicles/{id}/command/honk_horn` | Honk horn twice |
| `POST` | `/api/1/vehicles/{id}/command/flash_lights` | Flash headlights once |

#### Doors

| HTTP | Path | Description |
|---|---|---|
| `POST` | `/api/1/vehicles/{id}/command/door_lock` | Lock doors |
| `POST` | `/api/1/vehicles/{id}/command/door_unlock` | Unlock doors |

#### Frunk / Trunk

| HTTP | Path | Description |
|---|---|---|
| `POST` | `/api/1/vehicles/{id}/command/actuate_trunk` | Open/close front or rear trunk (`which_trunk` param) |

#### Windows

| HTTP | Path | Description |
|---|---|---|
| `POST` | `/api/1/vehicles/{id}/command/window_control` | Vent or close windows (`command`, `lat`, `lon` params) |

#### Sunroof

| HTTP | Path | Description |
|---|---|---|
| `POST` | `/api/1/vehicles/{id}/command/sun_roof_control` | Open/close panoramic sunroof |

#### Sentry Mode

| HTTP | Path | Description |
|---|---|---|
| `POST` | `/api/1/vehicles/{id}/command/set_sentry_mode` | Enable/disable Sentry Mode (`on` param) |

#### Charging

| HTTP | Path | Description |
|---|---|---|
| `POST` | `/api/1/vehicles/{id}/command/charge_port_door_open` | Open charge port |
| `POST` | `/api/1/vehicles/{id}/command/charge_port_door_close` | Close charge port (motorized only) |
| `POST` | `/api/1/vehicles/{id}/command/charge_start` | Start charging |
| `POST` | `/api/1/vehicles/{id}/command/charge_stop` | Stop charging |
| `POST` | `/api/1/vehicles/{id}/command/charge_standard` | Set charge limit to ~90% |
| `POST` | `/api/1/vehicles/{id}/command/charge_max_range` | Set charge limit to 100% |
| `POST` | `/api/1/vehicles/{id}/command/set_charge_limit` | Set custom charge limit (`percent` param) |
| `POST` | `/api/1/vehicles/{id}/command/set_charging_amps` | Set max charging amps (`charging_amps` param) |
| `POST` | `/api/1/vehicles/{id}/command/set_scheduled_charging` | Set scheduled charge time |
| `POST` | `/api/1/vehicles/{id}/command/set_scheduled_departure` | Set scheduled departure (preconditioning + off-peak) |

#### Climate

| HTTP | Path | Description |
|---|---|---|
| `POST` | `/api/1/vehicles/{id}/command/auto_conditioning_start` | Start HVAC |
| `POST` | `/api/1/vehicles/{id}/command/auto_conditioning_stop` | Stop HVAC |
| `POST` | `/api/1/vehicles/{id}/command/set_temps` | Set target temps (`driver_temp`, `passenger_temp` in celsius) |
| `POST` | `/api/1/vehicles/{id}/command/set_preconditioning_max` | Toggle Max Defrost (`on` param) |
| `POST` | `/api/1/vehicles/{id}/command/remote_seat_heater_request` | Set seat heater level (`heater`, `level` params) |
| `POST` | `/api/1/vehicles/{id}/command/remote_seat_cooler_request` | Set seat cooler level (`seat_position`, `seat_cooler_level`) |
| `POST` | `/api/1/vehicles/{id}/command/remote_steering_wheel_heater_request` | Toggle steering wheel heater (`on` param) |
| `POST` | `/api/1/vehicles/{id}/command/set_bioweapon_mode` | Toggle Bioweapon Defense Mode |
| `POST` | `/api/1/vehicles/{id}/command/set_climate_keeper_mode` | Set Climate Keeper mode (Off/Default/Dog/Camp) |
| `POST` | `/api/1/vehicles/{id}/command/remote_auto_seat_climate_request` | Toggle automatic seat climate |
| `POST` | `/api/1/vehicles/{id}/command/set_cop_temp` | Set Cabin Overheat Protection temp |
| `POST` | `/api/1/vehicles/{id}/command/set_cabin_overheat_protection` | Toggle COP (`on`, `fan_only` params) |

#### Remote Start

| HTTP | Path | Description |
|---|---|---|
| `POST` | `/api/1/vehicles/{id}/command/remote_start` | Start car remotely |

#### Homelink

| HTTP | Path | Description |
|---|---|---|
| `POST` | `/api/1/vehicles/{id}/command/trigger_homelink` | Open/close garage door via Homelink |

#### Speed Limit

| HTTP | Path | Description |
|---|---|---|
| `POST` | `/api/1/vehicles/{id}/command/speed_limit_set_limit` | Set speed limit (`limit_mph` param) |
| `POST` | `/api/1/vehicles/{id}/command/speed_limit_activate` | Activate speed limit |
| `POST` | `/api/1/vehicles/{id}/command/speed_limit_deactivate` | Deactivate speed limit |
| `POST` | `/api/1/vehicles/{id}/command/speed_limit_clear_pin` | Clear speed limit PIN |

#### Valet Mode

| HTTP | Path | Description |
|---|---|---|
| `POST` | `/api/1/vehicles/{id}/command/set_valet_mode` | Enable/disable Valet Mode (`on`, `password` params) |
| `POST` | `/api/1/vehicles/{id}/command/reset_valet_pin` | Reset Valet Mode PIN |

#### Media

| HTTP | Path | Description |
|---|---|---|
| `POST` | `/api/1/vehicles/{id}/command/media_toggle_playback` | Toggle media playback |
| `POST` | `/api/1/vehicles/{id}/command/media_next_track` | Next track |
| `POST` | `/api/1/vehicles/{id}/command/media_prev_track` | Previous track |
| `POST` | `/api/1/vehicles/{id}/command/media_next_fav` | Next favorite |
| `POST` | `/api/1/vehicles/{id}/command/media_prev_fav` | Previous favorite |
| `POST` | `/api/1/vehicles/{id}/command/media_volume_up` | Volume up |
| `POST` | `/api/1/vehicles/{id}/command/media_volume_down` | Volume down |

#### Sharing / Navigation

| HTTP | Path | Description |
|---|---|---|
| `POST` | `/api/1/vehicles/{id}/command/share` | Share location/navigation to car |

#### Software Updates

| HTTP | Path | Description |
|---|---|---|
| `POST` | `/api/1/vehicles/{id}/command/schedule_software_update` | Schedule OTA update (`offset_sec` param) |
| `POST` | `/api/1/vehicles/{id}/command/cancel_software_update` | Cancel scheduled OTA update |

### Account / user endpoints

| HTTP | Path | Description |
|---|---|---|
| `GET` | `/api/1/user` | Current user account details |

### Other surfaces

| Surface | Description |
|---|---|
| **Streaming telemetry** | HTTP streaming of vehicle telemetry at up to 0.5s intervals |
| **Autopark / Summon** | WebSocket-based command mode for smart summon / autopark |
| **Energy products** | Powerwall / solar site state and commands (separate from vehicles) |
| **Trip Planner** | Trip planning endpoints |

> **Note:** The Fleet API may have additional endpoints not listed here.
> Always cross-reference with the official docs at
> <https://developer.tesla.com/docs/fleet-api> before adding new methods.
> Some command endpoints require vehicle-command signing (a separate OAuth scope
> + signed request body), not just the read scope the adapter currently uses.