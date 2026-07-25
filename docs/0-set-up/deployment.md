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
