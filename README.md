# magus-tesla-api

Personal Go project to monitor **Tesla Vehicles**  via the [Tesla Fleet API](https://developer.tesla.com/docs/fleet-api).

Fetches live vehicle data (battery, range, climate, location, state) and serves as the foundation for a personal dashboard and future home automation. Built as a **modular monolith** in Go with strict package separation so new capabilities can be added cleanly over time.

---

## Project Goal

Build a personal, multitenant, self-hosted tool that:
- Authenticates with the Tesla Fleet API using OAuth 2.0
- Fetches real-time data from Tesla vehicles (charge level, range, climate, location, vehicle state)
- Stores and exposes that data for dashboarding and personal automation
- Grows modularly — new Tesla API capabilities slot into their own package without touching existing code

---

## Quick Start

> **Prerequisites:** Go 1.22+, a registered Tesla Fleet API app, and a completed setup (see [docs/post-registration-setup.md](docs/post-registration-setup.md)).

```bash
# Install dependencies
go mod tidy

# One-time OAuth setup — saves access + refresh tokens to .env
# (single-user smoke path; the web gateway handles its own per-user OAuth)
go run ./cmd/setup

# Run the multi-tenant web gateway (needs DATABASE_URL + SESSION_SECRET in .env)
go run ./cmd/web
```

---

## Building the modular monolith

> **Prerequisites:** Go 1.22+. The DB-backed modules use **generated** code, so the sqlc output
> must exist *before* you compile (see below). Install `sqlc` (`brew install sqlc`) and, for
> migrations, `goose` — full tooling list in [docs/deployment.md](docs/deployment.md).

The monolith is a single Go module: `go build ./...` compiles **every** package and command at
once. The one wrinkle is code generation — `internal/account/db` is produced by sqlc, so a fresh
checkout must generate it first, or the build fails with `undefined: accountdb`.

```bash
# 1. Dependencies + generated DB code
#    (first checkout, or after changing go.mod, any query.sql, or a migration)
make tidy      # go mod tidy
make sqlc      # sqlc generate → internal/account/db/{db,models,query.sql}.go

# 2. Compile the whole monolith
make build     # go build ./...   — all internal/ packages + every cmd/

# 3. Full local gate — compile, vet, and test in one shot
make check     # build + vet + test
```

Raw Go equivalents (no Make):

```bash
go mod tidy
sqlc generate
go build ./...   # compile everything
go vet ./...     # static analysis
go test ./...    # tests (account DB tests self-skip unless DATABASE_URL is set)
```

To produce runnable **binaries** (not just compile), build the entrypoints into `./bin`:

```bash
make bins        # → bin/setup, bin/web, …  (go build -o bin/ ./cmd/...)
```

> `make sqlc` is an explicit step, never part of `make build`. Re-run it whenever you edit a
> `query.sql` or a migration, then rebuild. First-time database setup is separate — see
> **[docs/deployment.md](docs/deployment.md)** (`make db-setup`).

---

## Project Structure

```
magus-tesla-api/
│
├── cmd/               # Executable entry points — see cmd/README.md
│   ├── setup/          # One-time Tesla OAuth flow (saves tokens to .env)
│   ├── web/            # Multi-tenant HTTP gateway (vehicle dashboard)
│   └── explore-tesla-api/  # On-demand raw Tesla Fleet API JSON inspector
│
├── internal/
│   ├── account/        # Per-user Tesla tokens (persisted, refreshed) in Postgres
│   ├── tesla/          # State-less Fleet API adapter (handed creds per call)
│   ├── gateway/        # Gin + Templ HTTP layer (handlers, pages, fragments)
│   ├── googleauth/     # Google OAuth for user login
│   ├── config/         # .env loading and token persistence
│   └── auth/           # Tesla OAuth URL, code exchange, token refresh
│
├── magus-public-key-netlify/   # EC public key hosted on Netlify for Tesla verification
│   └── well-known/appspecific/
│       └── com.tesla.3p.public-key.pem
│
└── docs/
    └── post-registration-setup.md   # Full setup guide — start here
```

For details on the `cmd/` convention and each binary, see [cmd/README.md](cmd/README.md).

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
8. Fetching live data — now served by the multi-tenant web gateway (`go run ./cmd/web`)

---

## Deployment (new machine / production)

Standing the project up on a fresh machine — including PostgreSQL, `sqlc`/`goose` tooling,
and the one-command database setup (`make db-setup`) — is documented in:

**[docs/deployment.md](docs/deployment.md)**

The database is configured entirely through `DATABASE_URL` in `.env`; `make db-setup` creates
the database (if needed) and applies migrations idempotently. Persistence coding conventions
live in [ai/go-conventions.md](ai/go-conventions.md) → *Persistence (Postgres + sqlc + goose)*.

---

## Sessions & staying logged in

The web gateway (introduced in **Tier 2**) uses **cookie-based sessions**
(`gin-contrib/sessions` cookie store): session data lives in a **signed + AES-encrypted cookie
in the browser**, not in server memory, keyed by a `SESSION_SECRET` from `.env`.

**Do you stay logged in after the server restarts? — Yes.** The server holds *no* session
state; on each request it simply verifies/decrypts the cookie the browser presents. Restarting
the server (or running multiple instances) does **not** log anyone out.

**The one rule: keep `SESSION_SECRET` stable.** If you rotate/change it, existing cookies can no
longer be verified and every user is logged out on their next request. Store it in `.env`
(gitignored) and inject it as a secret in production.

> Trade-offs of the alternatives: an **in-memory** store would log everyone out on restart and
> can't scale past one instance; a **Postgres-backed** store also survives restarts and adds
> server-side revocation, at the cost of a DB read per request. We can switch to Postgres-backed
> sessions later if revocation becomes a requirement.

---

## Architecture

This is a **modular monolith** — one Go module, multiple internal packages, each owning a single concern. New Tesla API capabilities (charging, telemetry, commands) slot into new packages under `internal/` without touching existing code.

| Package | Responsibility |
|---|---|
| `internal/config` | Load `.env`, typed config, token persistence |
| `internal/auth` | OAuth flow, token exchange, token refresh |

---

## External Services

| Service | Purpose |
|---|---|
| [Tesla Fleet API](https://fleet-api.prd.na.vn.cloud.tesla.com) | Vehicle data and commands |
| [Tesla Auth](https://auth.tesla.com/oauth2/v3) | OAuth 2.0 tokens |
| [magus-monitor.netlify.app](https://magus-monitor.netlify.app) | Hosts the EC public key for Tesla domain verification |

---


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


### How artifacts work?

design.md — in the OpenSpec flow, proposal.md carries the what/why, specs/ carry behavioral requirements, tasks.md the implementation steps, and design.md is where technical decisions like table design, indexes, and migrations belong

## Update GO

`mise use go@latest` to update to the latest Go version. If you don't have `mise` installed