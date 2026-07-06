# magus-tesla-api

Personal Go project to monitor **Magus** — my Tesla — via the [Tesla Fleet API](https://developer.tesla.com/docs/fleet-api).

Fetches live vehicle data (battery, range, climate, location, state) and serves as the foundation for a personal dashboard and future home automation. Built as a **modular monolith** in Go with strict package separation so new capabilities can be added cleanly over time.

---

## Project Goal

Build a personal, self-hosted tool that:
- Authenticates with the Tesla Fleet API using OAuth 2.0
- Fetches real-time data from Magus (charge level, range, climate, location, vehicle state)
- Stores and exposes that data for dashboarding and personal automation
- Grows modularly — new Tesla API capabilities slot into their own package without touching existing code

---

## Quick Start

> **Prerequisites:** Go 1.22+, a registered Tesla Fleet API app, and a completed setup (see [docs/post-registration-setup.md](docs/post-registration-setup.md)).

```bash
# Install dependencies
go mod tidy

# One-time OAuth setup — saves access + refresh tokens to .env
go run ./cmd/setup

# Fetch Magus's live data
go run ./cmd/magus
```

---

## Project Structure

```
magus-tesla-api/
│
├── cmd/
│   ├── setup/          # One-time OAuth flow (Step 6 of setup guide)
│   └── magus/          # Fetch and display Magus's live vehicle data (Step 8)
│
├── internal/
│   ├── config/         # .env loading and token persistence
│   ├── auth/           # Tesla OAuth URL, code exchange, token refresh
│   ├── server/         # Gin HTTP server for OAuth callback on localhost:8080
│   └── vehicle/        # Tesla Fleet API client + all vehicle data types
│
├── magus-public-key-netlify/   # EC public key hosted on Netlify for Tesla verification
│   └── well-known/appspecific/
│       └── com.tesla.3p.public-key.pem
│
└── docs/
    └── post-registration-setup.md   # Full setup guide — start here
```

---

## Setup Guide

All Tesla API registration steps — from getting credentials to the first API call — are documented in:

**[docs/post-registration-setup.md](docs/post-registration-setup.md)**

It covers:
1. Protecting credentials in `.env`
2. Generating the EC key pair
3. Hosting the public key on Netlify
4. Registering with the Tesla Developer Portal
5. Registering the public key via the Fleet API
6. Running the OAuth flow with `go run ./cmd/setup`
7. Virtual key pairing (skipped — only needed for commands)
8. Fetching live data with `go run ./cmd/magus`

---

## Architecture

This is a **modular monolith** — one Go module, multiple internal packages, each owning a single concern. New Tesla API capabilities (charging, telemetry, commands) slot into new packages under `internal/` without touching existing code.

| Package | Responsibility |
|---|---|
| `internal/config` | Load `.env`, typed config, token persistence |
| `internal/auth` | OAuth flow, token exchange, token refresh |
| `internal/server` | Gin callback server for OAuth redirect |
| `internal/vehicle` | Fleet API HTTP client + vehicle data types and calls |

---

## External Services

| Service | Purpose |
|---|---|
| [Tesla Fleet API](https://fleet-api.prd.na.vn.cloud.tesla.com) | Vehicle data and commands |
| [Tesla Auth](https://auth.tesla.com/oauth2/v3) | OAuth 2.0 tokens |
| [magus-monitor.netlify.app](https://magus-monitor.netlify.app) | Hosts the EC public key for Tesla domain verification |

---

## What's Next

- Automatic token refresh when the access token expires (every 8 hours)
- More vehicle endpoints: tire pressure, charge schedule, software version
- `cmd/poller` — scheduled runner to periodically fetch and persist data
- `internal/store` — local data store to log Magus's state over time
- Virtual key pairing for command support (lock, climate, charge control)

---

## Q&A

### Does the Netlify deployment need to be running always?

There's no server to "keep running" — the Netlify deploy is **static hosting** (a single PEM
file on Netlify's CDN), not a running process. Once deployed it stays live permanently at no cost
and with no compute; there is nothing to start, restart, or keep awake, and it is unaffected by
whether your local machine is on.

That said, the file must **stay published**. Tesla requires the public key to remain permanently
accessible at `https://magus-monitor.netlify.app/.well-known/appspecific/com.tesla.3p.public-key.pem`
so it can re-verify the app's domain and key over time. So:

- **Keep the `magus-monitor` Netlify site deployed** — don't delete it or unpublish the deploy.
- You only need to **redeploy** if the key changes or you move domains:
  `netlify deploy --dir=magus-public-key-netlify --prod`.
- This is Layer 1 (app-level) infrastructure — it's shared by the whole app and is independent of
  the OAuth tokens and data fetching in Layer 2. See
  [docs/layer1-app-registration.md](docs/layer1-app-registration.md) for context.
