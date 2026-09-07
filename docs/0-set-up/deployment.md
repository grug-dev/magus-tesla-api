# Deploying magus-tesla-api on a new machine (dev or production)

A repeatable runbook for standing up this project from scratch on a fresh machine.
Everything after the prerequisites is **idempotent** and driven by a small number of
commands — `make env-setup` (fills `.env`) and `make db-setup` (provisions the DB) are
both safe to run once or many times.

The only manual, machine-specific input is the **`.env`** file (secrets + `DATABASE_URL`),
which `make env-setup` will build interactively for you, plus a one-time **password prompt**
the first time `make db-setup` creates the app's DB role.

---

## 1. Prerequisites (install once per machine)

| Tool | Why | Install (macOS / Homebrew) |
|---|---|---|
| **Go** 1.22+ | build & run the app | `brew install go` |
| **PostgreSQL** (server + `psql`) | the account module's store | `brew install postgresql@16` then `brew services start postgresql@16` |
| **git** | clone the repo | preinstalled / `brew install git` |
| **sqlc** | generate type-safe DB code | `brew install sqlc` *(or `go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest`)* |
| **goose** | run DB migrations | `go install github.com/pressly/goose/v3/cmd/goose@latest` |

> **PATH note for Go-installed tools (goose):** `go install` drops binaries in
> `$(go env GOPATH)/bin`. That directory must be on your `PATH`, e.g. in `~/.zshrc`:
> ```sh
> export GOPATH=$HOME/go-path          # or wherever your GOPATH is
> export PATH=$PATH:$GOPATH/bin
> ```
> After editing, `source ~/.zshrc` (or open a new shell). Verify with `which goose`.
> The `Makefile` also falls back to `$(go env GOPATH)/bin/goose` if it isn't on `PATH`.

A running PostgreSQL instance is assumed. On a managed provider (RDS, Cloud SQL, Supabase)
the server — and often an empty database — already exists; see §5.

---

## 2. Clone and configure

```bash
git clone <repo-url> magus-tesla-api
cd magus-tesla-api
```

You have two ways to produce `.env`: the **interactive bootstrap** (recommended) or a
**manual copy** from the template.

### 2a. Interactive bootstrap — `make env-setup` (recommended)

```bash
make env-setup
```

`make env-setup` walks the `.env` variables the **multi-tenant web gateway**
(`cmd/web`) needs and prompts you **only for the ones that are missing or empty** —
values already present are never touched. It is safe to re-run on any machine; it
just fills the gaps.

| Variable | Behavior if missing |
|---|---|
| `SESSION_SECRET` | **Auto-generated** with `openssl rand -hex 32` — no prompt. Keep it stable afterwards (rotating it logs every user out). |
| `TESLA_CLIENT_ID` | Prompted (input hidden). |
| `TESLA_CLIENT_SECRET` | Prompted (input hidden). |
| `GOOGLE_CLIENT_ID` | Prompted (input hidden). |
| `GOOGLE_CLIENT_SECRET` | Prompted (input hidden). |
| `DATABASE_URL` | Prompted with default `postgres://localhost:5432/magus?sslmode=disable` (press Enter to accept). |
| `PORT` | Prompted with default `8080`. |
| `BASE_URL` | Prompted with default `http://localhost:8080`. |

What it **skips** (not needed by `cmd/web`): `TESLA_ACCESS_TOKEN`,
`TESLA_REFRESH_TOKEN` (smoke-test only — produced by `cmd/setup`, §6), and
`MAGUS_DB_PASSWORD` (only for non-interactive `db-setup` in CI/prod).

If `.env` does not exist, `env-setup` creates a **minimal** one with just the
multi-tenant vars above (no smoke-test entries). It requires a TTY for the
prompts — in CI/prod, pre-set the vars in the environment or write `.env` by
another means and run `env-setup` to fill anything still missing.

At the end it prints a summary of what was set vs. already present, and the
suggested next step (`make db-setup`, then `go run ./cmd/web`).

### 2b. Manual copy from the template

```bash
cp .env.example .env
# then edit .env — see the fields below
```

`.env` (never committed — it is gitignored):

```dotenv
# Tesla Fleet API app credentials (developer.tesla.com)
TESLA_CLIENT_ID=...
TESLA_CLIENT_SECRET=...

# Tesla OAuth tokens — produced by `go run ./cmd/setup` (§6)
TESLA_ACCESS_TOKEN=
TESLA_REFRESH_TOKEN=

# PostgreSQL DSN for the account store. Read by BOTH the app and the Makefile,
# which derives the DB name + bootstrap admin connection from it.
# Start with this USER-LESS "bootstrap" value — `make db-setup` uses your OS
# superuser to create the app role + database, then prints the final DSN
# (postgres://magusadmindb:<password>@…) for you to paste back here. See §4.
DATABASE_URL=postgres://localhost:5432/magus?sslmode=disable

# OPTIONAL — only for NON-INTERACTIVE setup (CI/prod). If the app role is missing
# and `make db-setup` cannot prompt, it reads the new role's password from here.
# Leave empty for local interactive setup; never commit a real value.
MAGUS_DB_PASSWORD=

# Web gateway (cmd/web)
PORT=8080
# Strong, STABLE secret for the signed+encrypted session cookie (openssl rand -hex 32).
# Rotating it logs every user out. Inject as a secret in production.
SESSION_SECRET=
# Public base URL — builds the Google OAuth redirect (BASE_URL + /auth/google/callback).
BASE_URL=http://localhost:8080
# Google OAuth (user login). Create an OAuth client ID in the Google Cloud Console and
# register <BASE_URL>/auth/google/callback as an authorized redirect URI.
GOOGLE_CLIENT_ID=
GOOGLE_CLIENT_SECRET=
```

### Google OAuth setup (one-time, for login)

1. Google Cloud Console → **APIs & Services → Credentials → Create OAuth client ID** (type: Web).
2. Add an **Authorized redirect URI**: `<BASE_URL>/auth/google/callback`
   (e.g. `http://localhost:8080/auth/google/callback` in dev, `https://<domain>/auth/google/callback` in prod).
3. Copy the client ID/secret into `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET`, and set `BASE_URL`.

See [docs/google-config.md](docs/google/google-config.md) for more details.

### Tesla connect redirect URI (for the web "Connect your Tesla" flow)

The web connect flow uses redirect `<BASE_URL>/connect/tesla/callback`. Add it to your Tesla app's
allowed redirect URIs at developer.tesla.com (alongside the existing `cmd/setup` callback). The
Tesla client id/secret are the same `TESLA_CLIENT_ID` / `TESLA_CLIENT_SECRET` already in `.env`.

---

## 3. Fetch dependencies and generate DB code

```bash
make tidy     # go mod tidy
make sqlc     # sqlc generate → internal/account/db/{db,models,query.sql}.go
```

> `go build` will fail *before* `make sqlc` runs, because the generated `accountdb`
> package doesn't exist yet. That's expected — generate first.

### Web UI CSS — nothing to install to build or deploy

The gateway's stylesheet `internal/gateway/static/app.css` is a **committed, generated
artifact** (like `htmx.min.js`) and is baked into the binary via `//go:embed static`. As a
result:

- **Building and deploying need nothing extra** — `go build ./cmd/web` embeds the existing
  `app.css`. **No Node, no npm, no `package.json`, no Tailwind binary** at build or run time.
  This is the whole point of the Node-less setup (see [`ai/htmx-conventions.md`](../../ai/htmx-conventions.md) → *Styling*).
- **Only when you edit templates or add DaisyUI/Tailwind classes** do you regenerate CSS:
  `make ui-toolchain` (once per machine — downloads the git-ignored native Tailwind binary for
  your OS/arch: macOS/Linux, arm64/x64), then `make css` (or `make generate`, which runs
  sqlc + templ + css). **Commit the updated `app.css`.**

Rule of thumb: treat `app.css` like generated code — change the classes your `.templ` files
use, run `make css`, commit the result, so the embedded stylesheet stays in sync.

---

## 4. Create the role, database, and run migrations — one command

```bash
make db-setup
```

The app connects to Postgres as a dedicated **login role, `magusadmindb`** — not your
OS superuser. `make db-setup` provisions everything and is idempotent:

1. **Creates the app role** `magusadmindb` if it doesn't exist (`LOGIN CREATEDB
   NOSUPERUSER` — it owns its own database but has no power over the rest of the
   cluster). The first time, it **prompts you for a password** (input hidden):

   ```
   Set a password for the new DB role 'magusadmindb': ␣
   ```

   The password is passed to Postgres via an environment variable, never on the
   command line (so it never lands in your shell history or `ps` output).
2. **Creates the database** `magus` **owned by `magusadmindb`** if it doesn't exist
   (guarded by a `pg_database` check against the maintenance `postgres` DB).
3. **Applies all migrations** with goose, connecting *as `magusadmindb`* so every
   table is owned by the app role (tracked in `goose_db_version`, so re-runs only
   apply what's pending).
4. **Prints the final `DATABASE_URL`** to paste back into `.env`:

   ```
   → Point the app at the new role. Put this in your .env (insert the password you just set;
     percent-encode it if it contains any of  @ : / ? # %  ):

       DATABASE_URL=postgres://magusadmindb:<password>@localhost:5432/magus?sslmode=disable
   ```

**Do this now:** replace `<password>` with the password you chose and update the
`DATABASE_URL` line in `.env`. From here on, the app (and future `make migrate-*`
runs) connect as `magusadmindb`.

> **Who creates the role?** The *bootstrap* admin connection (`ADMIN_DATABASE_URL`,
> derived by stripping the user from `DATABASE_URL` and pointing at the `postgres`
> database) must be a superuser that can `CREATE ROLE`/`CREATE DATABASE`. On a
> default Homebrew install that's your macOS user via local `trust` auth, so it
> just works. On managed DBs, override it — see §5.

> **Non-interactive** (CI/prod, no TTY): set `MAGUS_DB_PASSWORD` in the environment
> (or `.env`) and `make db-setup` uses it instead of prompting.

Useful companions:

```bash
make db-url          # print DB name / APP_ROLE / bootstrap admin URL (no changes)
make migrate-status  # which migrations are applied
make migrate-up      # apply pending migrations only (as the DATABASE_URL user)
make migrate-down    # roll back the latest migration
make db-reset        # DROP the DB + recreate (owned by the role) + migrate — DESTRUCTIVE, local/dev only
```

---

## 5. Production specifics

- **Point `DATABASE_URL` at the managed instance**, e.g.
  `postgres://magusadmindb:pass@prod-host:5432/magus?sslmode=require`.
- **Managed DBs usually pre-create the database *and* the role.** When the `magusadmindb`
  role already exists, `make db-setup` does **not** prompt and does not recreate it; when
  the database already exists it skips creation (and warns if that DB is owned by someone
  other than `magusadmindb`), then migrations do the real work. If the platform forbids
  connecting to the `postgres` maintenance DB, run **`make migrate-up`** directly instead
  of `db-setup`.
- **Provisioning the role yourself:** the bootstrap admin connection defaults to your OS
  superuser, which won't exist on a managed host. Point it at a real superuser/owner:
  ```bash
  make db-setup \
    ADMIN_DATABASE_URL=postgres://admin:adminpass@prod-host:5432/postgres?sslmode=require \
    MAGUS_DB_PASSWORD='the-app-role-password'
  ```
  `ADMIN_DATABASE_URL` is used **only** to create the role + database; `MAGUS_DB_PASSWORD`
  supplies the new role's password without a prompt. Neither is used by the app at runtime.
- **Custom role name:** override with `APP_ROLE=name` on any `db-*` target if `magusadmindb`
  is taken or your convention differs.
- **Secrets:** never commit `.env`. Inject `DATABASE_URL`, `TESLA_CLIENT_SECRET`, and the
  tokens via your platform's secret store / environment; the Makefile and app both read
  from the environment, so `make db-setup DATABASE_URL=... migrate-up` works without a file.
- **Host tools:** this CLI-based flow needs `psql` and `goose` on the deploy host. (If that's
  undesirable, the alternative is embedding migrations in the Go binary via goose's library +
  `embed.FS` — not done yet; noted here as the future hardening path.)
- **Re-runnability:** `make db-setup` is safe to run on every deploy — it converges the DB to
  the latest schema and does nothing if already current.
- **SSL:** use `sslmode=require` (or stricter) in production DSNs.
- **Web UI CSS:** no Node/Tailwind toolchain is required to build or run — `internal/gateway/static/app.css`
  is committed and embedded (`//go:embed`). Only a developer *regenerating* styles needs
  `make ui-toolchain` + `make css`. If CI should guard that the committed CSS is current, run
  `make css && git diff --exit-code internal/gateway/static/app.css` (fails if someone changed
  templates without regenerating).

---

## 6. Tesla OAuth + run

```bash
# One-time OAuth (single-user smoke path) — opens a browser, catches the
# callback, writes tokens to .env. Re-run only when the refresh token expires
# (~every 3 months). The web gateway handles its own per-user OAuth flow.
go run ./cmd/setup

# Multi-tenant web gateway — serves the per-user vehicle dashboard.
# Requires DATABASE_URL + SESSION_SECRET in .env.
go run ./cmd/web
```

Build/vet before shipping:

```bash
go build ./... && go vet ./...
go test ./...        # account integration tests run only when DATABASE_URL is set
```

---

## 7. Troubleshooting

| Symptom | Fix |
|---|---|
| `goose not found at '.../go-path/bin/goose'` | `go install github.com/pressly/goose/v3/cmd/goose@latest` and ensure `$(go env GOPATH)/bin` is on `PATH` (§1). |
| `go build` fails: undefined `accountdb` | Run `make sqlc` first (§3). |
| `psql: connection refused` | Postgres isn't running / wrong host/port in `DATABASE_URL`. `brew services start postgresql@16`. |
| `CREATE DATABASE`/`CREATE ROLE` permission denied | The bootstrap `ADMIN_DATABASE_URL` user lacks `CREATEDB`/`CREATEROLE`, or can't reach the `postgres` maintenance DB. Point `ADMIN_DATABASE_URL` at a superuser (§5), or on managed DBs skip creation and run `make migrate-up`. |
| `make db-setup` errors: *role missing and no password was given* | You ran it without a TTY (piped/CI). Pass `MAGUS_DB_PASSWORD=…` (env or `.env`). |
| `WARNING: database 'magus' … owned by '<other>', not 'magusadmindb'` | A **leftover DB from an earlier setup** exists, owned by your OS user. The app role can't touch its tables. For a clean, correctly-owned DB: **`make db-reset`** (DESTRUCTIVE — drops + recreates owned by the role + migrates). |
| App: `permission denied for table accounts` (or similar) | The DB/tables are owned by a different role than the one in `DATABASE_URL`. Easiest fix on dev: `make db-reset`. |
| `\getenv: not found` / role step fails | Needs `psql` **16+** (ships with `postgresql@16`). Check `psql --version`. |
| Integration tests skipped | Expected when `DATABASE_URL` is unset — they self-skip. |

---

For the coding-side persistence conventions (sqlc/goose layout, the module-scoped DB rule,
`DATABASE_URL` as single source of truth), see [`ai/go-conventions.md`](../ai/go-conventions.md)
→ *Persistence (Postgres + sqlc + goose)*.

---

## 8. Docker Compose deploy (VPS / production)

This section is for a **first deploy** to a small VPS, using Docker. It does not
need any of the tools from §1 (`sqlc`, `goose`, a local Postgres). Docker builds
and runs everything inside containers. Follow the steps in order.

### 8.1 Prepare the VPS

Use a small Ubuntu VPS. Hostinger's 2 GB RAM plan is enough — Postgres, the two
Go binaries, and Caddy all fit comfortably. Any Ubuntu 22.04+ VPS from any
provider works the same way.

Write down the VPS's **public IP address**. You need it for the next step.

### 8.2 Point a DNS A record at the VPS

Go to your domain's DNS provider (where you bought the domain, or its DNS
panel). Add an **A record**:

- Name: the subdomain you want, e.g. `magus`
- Type: `A`
- Value: the VPS's public IP from step 8.1

This makes `magus.example.com` point at your VPS. DNS changes can take up to a
few hours to spread. Caddy (step 8.8) cannot get an HTTPS certificate until
this resolves — check with:

```bash
# Confirm the domain resolves to the VPS IP. Run this from your own machine.
dig +short magus.example.com
```

### 8.3 Install Docker and the Compose plugin on the VPS

Log into the VPS over SSH, then run:

```bash
# Install Docker and the Docker Compose plugin in one step.
curl -fsSL https://get.docker.com | sudo sh
```

```bash
# Let your user run docker without sudo. Log out and back in after this.
sudo usermod -aG docker $USER
```

Log out and log back in (or run `newgrp docker`), then check it worked:

```bash
# Confirm Docker Compose is installed.
docker compose version
```

### 8.4 Clone the repo

```bash
# Get the code onto the VPS.
git clone <repo-url> magus-tesla-api && cd magus-tesla-api
```

Replace `<repo-url>` with this repo's real git URL.

> **Do not use `make env-setup` or `make db-setup` for this deploy.** Both
> belong to the host development path (§1–§4 above), not Docker.
>
> - `make env-setup` defaults `DATABASE_URL` to `localhost`. In Docker the
>   database host is `db`, not `localhost`. It also never asks for
>   `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`, or `BASE_DOMAIN` — the
>   four values this Docker deploy needs most.
> - `make db-setup` connects to a Postgres server on this host with `psql`, and
>   needs the `goose` CLI installed. In Docker, the `db` container creates its
>   own role and database by itself, the first time it starts. `make db-setup`
>   never touches that container.
>
> Fill `.env` by hand instead, using the table in step 8.5 below.

### 8.5 Create `.env`

```bash
# Copy the template. You will fill in the real values next.
cp .env.example .env
```

Open `.env` in an editor (e.g. `nano .env`) and fill in every value:

| Variable | Where to get it |
|---|---|
| `TESLA_CLIENT_ID`, `TESLA_CLIENT_SECRET` | Your Tesla developer app — see §2 above. |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | Your Google OAuth client — see §2 above. |
| `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB` | Pick your own values. `POSTGRES_PASSWORD` must not be empty — see the warning below. These create the database inside the `db` container — see `.env.example`'s comments. |
| `DATABASE_URL` | Build it from the three values above, using the `db` service name as host: `postgres://<POSTGRES_USER>:<POSTGRES_PASSWORD>@db:5432/<POSTGRES_DB>?sslmode=disable`. |
| `BASE_DOMAIN` | The domain from step 8.2, e.g. `magus.example.com`. Caddy uses this to get its HTTPS certificate. |
| `BASE_URL` | `https://` plus the same domain, e.g. `https://magus.example.com`. |

> **`POSTGRES_PASSWORD` must not be empty.** An empty value makes the `db`
> container fail to start. It restarts forever, and its logs repeat this error:
>
> ```
> Error: Database is uninitialized and superuser password is not specified.
>        You must specify POSTGRES_PASSWORD to a non-empty value for the
>        superuser.
> ```
>
> Set a real password in `.env` before you continue to step 8.6. Postgres never
> finished creating its database with an empty password, so setting a real one
> now and starting the stack again fixes it — see
> `docs/1-deploy/docker.md` §9 for the general rule about changing
> `POSTGRES_PASSWORD` later, once the database already holds data.

> **All three `POSTGRES_*` values must be set, not only the password.** If
> `POSTGRES_USER` or `POSTGRES_DB` is missing, Compose prints:
>
> ```
> WARN[0000] The "POSTGRES_USER" variable is not set. Defaulting to a blank string.
> WARN[0000] The "POSTGRES_DB" variable is not set. Defaulting to a blank string.
> ```
>
> This is a warning, not an error, so the stack still tries to start — and then
> stalls. Two things go wrong at once:
>
> - `db`'s healthcheck runs `pg_isready -U "" -d ""` and never passes. `migrate`
>   waits for `db` to be healthy, so nothing after `db` ever starts.
> - The container falls back to the image defaults, creating the user and
>   database as `postgres`, not the names your `DATABASE_URL` expects.
>
> Fill all three, and make `DATABASE_URL` carry the same three values.

Leave `TESLA_ACCESS_TOKEN` and `TESLA_REFRESH_TOKEN` empty — the web gateway
handles its own per-user Tesla login. Leave `MAGUS_DB_PASSWORD` empty — that
variable is for the host-only `make db-setup` path, not Docker.

### 8.6 Generate `SESSION_SECRET`

```bash
# Print a random 32-byte hex secret. Paste the output into .env as
# SESSION_SECRET=<the output>.
openssl rand -hex 32
```

Keep this value stable after you set it. Changing it later logs out every
signed-in user.

### 8.7 Add the production redirect URIs

Add these two URLs — do not remove the existing `localhost` ones, which you
still need for local development:

- **Google Cloud Console** → APIs & Services → Credentials → your OAuth
  client → Authorized redirect URIs → add `<BASE_URL>/auth/google/callback`
  (e.g. `https://magus.example.com/auth/google/callback`).
- **Tesla developer app** (developer.tesla.com) → add
  `<BASE_URL>/connect/tesla/callback` to the allowed redirect URIs.

### 8.8 First deploy

```bash
# Build the images and start every service in the background.
# Run this from the repo root. The Docker files live under deploy/docker/,
# so every compose command needs the two flags below.
docker compose --project-directory . -f deploy/docker/compose.yaml up -d --build
```

What happens, in order:

1. `db` (Postgres) starts and waits until it reports healthy.
2. `migrate` runs all pending database migrations, then exits with code 0.
3. `web` and `poller` start (they wait for `migrate` to finish first).
4. `caddy` starts and requests an HTTPS certificate for `BASE_DOMAIN`.

The first build can take a few minutes. Later deploys are faster.

### 8.9 Verify it worked

```bash
# Expect "HTTP/2 200". Replace <domain> with your real domain.
curl -I https://<domain>/healthz
```

```bash
# Expect every long-running service "Up" (or "healthy"), and
# migrate "Exited (0)". Run from the repo root.
docker compose --project-directory . -f deploy/docker/compose.yaml ps
```

If something looks wrong, see the troubleshooting table in
`docs/1-deploy/docker.md`.

### 8.10 Set up the daily backup cron

```bash
# Open your crontab editor.
crontab -e
```

Add this line (edit the path to match where you cloned the repo in step 8.4):

```
0 2 * * * cd /path/to/magus-tesla-api && make backup-db >> /var/log/magus-backup.log 2>&1
```

This runs `deploy/docker/backup-db.sh` every night at 2 AM. It writes a
gzipped `.sql.gz` dump to `backups/` and deletes any backup older than 7
days. The script builds its `docker compose` command relative to the
current folder, so the cron line above must `cd` to the repo root first —
it already does.

### 8.11 Deploying an update, from now on

```bash
# Pull the latest code, then rebuild and restart what changed. Run from
# the repo root.
git pull
docker compose --project-directory . -f deploy/docker/compose.yaml up -d --build
```

`--build` is always safe to include, even when nothing changed — Docker's
build cache makes a no-op rebuild fast.

For every other day-to-day command — logs, restarting one service, database
access, backups, and troubleshooting — see
**[`docs/1-deploy/docker.md`](../1-deploy/docker.md)**.
