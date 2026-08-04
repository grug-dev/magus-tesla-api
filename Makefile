# Makefile — magus-tesla-api
#
# One-command project/database setup. Config comes from .env (the same file the
# app reads) or the environment — everything is overridable, e.g.:
#   make db-setup DATABASE_URL=postgres://user:pass@host:5432/magus?sslmode=disable
#
# DATABASE_URL is the single source of truth. The database NAME and the admin
# (maintenance) connection used only to CREATE the database are derived from it,
# so there is nothing to keep in sync.
#
# Prereqs on the host: a running PostgreSQL, `psql`, and `goose`
# (go install github.com/pressly/goose/v3/cmd/goose@latest).

# --- Config -----------------------------------------------------------------

# Default DSN; override in .env or on the command line. The DB need not exist yet.
DATABASE_URL ?= postgres://localhost:5432/magus?sslmode=disable

# Load .env if present. Its values override the defaults above; a command-line
# `make VAR=...` still wins over both. (Expects simple KEY=value lines.)
ifneq (,$(wildcard .env))
include .env
export
endif

# Each module owns its goose migrations dir (ai/architecture.md §2). goose shares a
# single goose_db_version table across ALL dirs, so the table's "current version" is
# global while each dir's migrations are versioned independently — a module can easily
# have a pending migration older than another module's already-applied one (e.g.
# telemetry 20260802 pending while account 20260803 is applied). Ordering this list
# CANNOT fix that: it orders directories, not the migrations inside them.
#
# Both `up` loops therefore pass -allow-missing, which applies a pending migration even
# when its version sits below the global current version. That is safe here because
# modules never share tables or FKs (ai/architecture.md boundary rules), so cross-module
# version order carries no meaning; within a dir, goose still applies in version order.
# Add a module's dir here when it gains a DB — position no longer matters.
MIGRATIONS_DIRS ?= internal/account/db/migrations internal/telemetry/db/migrations internal/manualcharge/db/migrations

# goose binary: prefer one on PATH, else the `go install` location (GOPATH/bin).
GOOSE ?= $(shell command -v goose 2>/dev/null || echo $$(go env GOPATH)/bin/goose)

# The application login role. `db-setup` creates it (if missing) and makes it the
# OWNER of the database; it is the role the app connects as for all CRUD, so it is
# NOSUPERUSER (least privilege — it can do anything inside its own DB, nothing to
# the rest of the cluster). Override with `make ... APP_ROLE=name` if you must.
APP_ROLE ?= magusadmindb

# Derived — do not set directly. DB_NAME is the last path segment of the DSN.
DB_NAME := $(shell echo "$(DATABASE_URL)" | sed -E 's|.*/([^/?]+).*|\1|')

# ADMIN_DATABASE_URL is the BOOTSTRAP superuser connection, used ONLY to CREATE the
# role and the database — never by the app. It is derived from DATABASE_URL by
# dropping any user:pass (so libpq falls back to your OS superuser, e.g. the
# Homebrew role) and pointing at the maintenance `postgres` database. Override it
# on managed DBs (RDS/Cloud SQL/Supabase) where the superuser differs, e.g.
#   make db-setup ADMIN_DATABASE_URL=postgres://admin:pass@host:5432/postgres?sslmode=require
DERIVED_ADMIN := $(shell echo "$(DATABASE_URL)" | sed -E 's|^(postgres(ql)?://)([^/@]*@)?([^/?]+)/[^/?]+|\1\4/postgres|')
ADMIN_DATABASE_URL ?= $(DERIVED_ADMIN)

.PHONY: help db-url check-goose migrate-up migrate-down migrate-status \
        db-setup db-reset env-setup sqlc templ css ui-toolchain ui-bundles generate ui-guard tidy build vet test check bins \
        up cmd-setup cmd-explore-tesla cmd-poller-once

# --- Help -------------------------------------------------------------------

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

db-url: ## Print the derived DB name / admin URL (sanity check, no changes)
	@echo "DATABASE_URL       = $(DATABASE_URL)"
	@echo "DB_NAME            = $(DB_NAME)"
	@echo "APP_ROLE           = $(APP_ROLE)"
	@echo "ADMIN_DATABASE_URL = $(ADMIN_DATABASE_URL)"
	@echo "MIGRATIONS         = $(MIGRATIONS_DIRS)"
	@echo "GOOSE              = $(GOOSE)"

# --- Database ---------------------------------------------------------------

check-goose:
	@command -v $(GOOSE) >/dev/null 2>&1 || { \
		echo "ERROR: goose not found at '$(GOOSE)'."; \
		echo "Install: go install github.com/pressly/goose/v3/cmd/goose@latest"; \
		echo "Then add \"$$(go env GOPATH)/bin\" to PATH, or run: make <target> GOOSE=/path/to/goose"; \
		exit 1; }

migrate-up: check-goose ## Apply all pending migrations (every module dir; -allow-missing, see MIGRATIONS_DIRS note)
	@for dir in $(MIGRATIONS_DIRS); do \
		echo "goose up: $$dir"; \
		$(GOOSE) -dir $$dir postgres "$(DATABASE_URL)" up -allow-missing; \
	done

migrate-down: check-goose ## Roll back the newest migration in each module dir (reverse order)
	@for dir in $$(printf '%s\n' $(MIGRATIONS_DIRS) | awk '{a[NR]=$$0} END{for(i=NR;i>=1;i--)print a[i]}'); do \
		echo "goose down: $$dir"; \
		$(GOOSE) -dir $$dir postgres "$(DATABASE_URL)" down; \
	done

migrate-status: check-goose ## Show which migrations have been applied (per module dir)
	@for dir in $(MIGRATIONS_DIRS); do \
		echo "== $$dir =="; \
		$(GOOSE) -dir $$dir postgres "$(DATABASE_URL)" status; \
	done

db-setup: check-goose ## ONE COMMAND: create the app role + database (both if missing) + migrate to latest
	@set -e; \
	ADMIN="$(ADMIN_DATABASE_URL)"; ROLE="$(APP_ROLE)"; DB="$(DB_NAME)"; PW=""; \
	if psql "$$ADMIN" -tAc "SELECT 1 FROM pg_roles WHERE rolname='$$ROLE'" | grep -q 1; then \
		echo "Role '$$ROLE' already exists — leaving it unchanged."; \
	else \
		PW="$$MAGUS_DB_PASSWORD"; \
		if [ -z "$$PW" ]; then \
			if [ -t 0 ]; then \
				printf "Set a password for the new DB role '%s': " "$$ROLE" >&2; \
				stty -echo 2>/dev/null; IFS= read -r PW; stty echo 2>/dev/null; echo >&2; \
			else \
				echo "ERROR: role '$$ROLE' is missing and no password was given." >&2; \
				echo "       Run interactively, or pass MAGUS_DB_PASSWORD=... for CI/prod." >&2; \
				exit 1; \
			fi; \
		fi; \
		if [ -z "$$PW" ]; then echo "ERROR: empty password." >&2; exit 1; fi; \
		echo "Creating role '$$ROLE' (LOGIN, CREATEDB, NOSUPERUSER)..."; \
		printf '%s\n%s\n' '\getenv pw ROLE_PW' \
			"CREATE ROLE :\"role\" LOGIN CREATEDB NOSUPERUSER PASSWORD :'pw';" \
			| ROLE_PW="$$PW" psql "$$ADMIN" -X -q -v ON_ERROR_STOP=1 -v role="$$ROLE"; \
		echo "Role '$$ROLE' created."; \
	fi; \
	if psql "$$ADMIN" -tAc "SELECT 1 FROM pg_database WHERE datname='$$DB'" | grep -q 1; then \
		OWNER=$$(psql "$$ADMIN" -tAc "SELECT pg_catalog.pg_get_userbyid(datdba) FROM pg_database WHERE datname='$$DB'" | tr -d '[:space:]'); \
		if [ "$$OWNER" != "$$ROLE" ]; then \
			echo "WARNING: database '$$DB' already exists but is owned by '$$OWNER', not '$$ROLE'."; \
			echo "         The app connects as '$$ROLE' and may lack privileges on its tables."; \
			echo "         For a clean, correctly-owned DB run:  make db-reset   (DESTRUCTIVE)"; \
		else \
			echo "Database '$$DB' already exists (owned by '$$ROLE')."; \
		fi; \
	else \
		echo "Creating database $$DB owned by $$ROLE..."; \
		psql "$$ADMIN" -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"$$DB\" OWNER \"$$ROLE\""; \
	fi; \
	echo "Applying migrations as '$$ROLE'..."; \
	if [ -z "$$PW" ]; then PW="$$MAGUS_DB_PASSWORD"; fi; \
	if [ -n "$$PW" ]; then export PGUSER="$$ROLE" PGPASSWORD="$$PW"; fi; \
	for dir in $(MIGRATIONS_DIRS); do \
		echo "goose up: $$dir"; \
		$(GOOSE) -dir $$dir postgres "$(DATABASE_URL)" up -allow-missing; \
	done; \
	echo; echo "✓ Database setup complete."; \
	PRINT_DSN=$$(echo "$(DATABASE_URL)" | sed -E "s#^(postgres(ql)?://)([^/@]*@)?#\1$$ROLE:<password>@#"); \
	echo "→ Point the app at the new role. Put this in your .env (insert the password you just set;"; \
	echo "  percent-encode it if it contains any of  @ : / ? # %  ):"; \
	echo; echo "    DATABASE_URL=$$PRINT_DSN"; echo

db-reset: ## DROP the database, recreate (owned by APP_ROLE), and migrate (DESTRUCTIVE — local/dev only)
	@psql "$(ADMIN_DATABASE_URL)" -c "DROP DATABASE IF EXISTS \"$(DB_NAME)\""
	@$(MAKE) db-setup

# --- .env bootstrap (multi-tenant / cmd/web only) ---------------------------

env-setup: ## Interactive .env bootstrap — prompt only for MISSING vars, auto-generate SESSION_SECRET
	@set -e; \
	created=0; \
	if [ ! -f .env ]; then \
		echo "Creating minimal .env (multi-tenant vars only — no smoke-test entries)."; \
		: > .env; \
		created=1; \
	fi; \
	get_var()  { grep -E "^$$1=" .env 2>/dev/null | head -1 | cut -d= -f2-; }; \
	set_var()  { \
		k="$$1"; v="$$2"; \
		if grep -qE "^$$k=" .env 2>/dev/null; then \
			grep -vE "^$$k=" .env > .env.tmp || true; \
			mv .env.tmp .env; \
		fi; \
		printf '%s=%s\n' "$$k" "$$v" >> .env; \
	}; \
	prompt_default() { \
		label="$$1"; dft="$$2"; \
		if [ -t 0 ]; then \
			printf '%s [%s]: ' "$$label" "$$dft" >&2; \
			read -r ans; \
			ans=$${ans:-$$dft}; \
		else \
			echo "ERROR: no TTY — run interactively, or pre-set vars in .env." >&2; \
			exit 1; \
		fi; \
		printf '%s' "$$ans"; \
	}; \
	prompt_secret() { \
		label="$$1"; \
		if [ -t 0 ]; then \
			printf '%s: ' "$$label" >&2; \
			stty -echo 2>/dev/null; read -r val; stty echo 2>/dev/null; echo >&2; \
		else \
			echo "ERROR: no TTY — $$label is missing. Run interactively or pre-set it in .env." >&2; \
			exit 1; \
		fi; \
		if [ -z "$$val" ]; then echo "ERROR: $$label cannot be empty." >&2; exit 1; fi; \
		printf '%s' "$$val"; \
	}; \
	set_summary=""; \
	\
	echo "Checking .env (only missing/empty vars are prompted)…"; \
	\
	if [ -z "$$(get_var SESSION_SECRET)" ]; then \
		if ! command -v openssl >/dev/null 2>&1; then \
			echo "ERROR: openssl not found — need it to auto-generate SESSION_SECRET." >&2; \
			echo "       Install openssl, or set SESSION_SECRET manually in .env." >&2; \
			exit 1; \
		fi; \
		sec=$$(openssl rand -hex 32); \
		set_var SESSION_SECRET "$$sec"; \
		set_summary="$$set_summary  SESSION_SECRET (auto-generated — keep it stable)\n"; \
	fi; \
	\
	for k in TESLA_CLIENT_ID TESLA_CLIENT_SECRET GOOGLE_CLIENT_ID GOOGLE_CLIENT_SECRET; do \
		if [ -z "$$(get_var $$k)" ]; then \
			val=$$(prompt_secret "$$k"); \
			set_var $$k "$$val"; \
			set_summary="$$set_summary  $$k\n"; \
		fi; \
	done; \
	\
	if [ -z "$$(get_var DATABASE_URL)" ]; then \
		v=$$(prompt_default "DATABASE_URL" "postgres://localhost:5432/magus?sslmode=disable"); \
		set_var DATABASE_URL "$$v"; \
		set_summary="$$set_summary  DATABASE_URL\n"; \
	fi; \
	if [ -z "$$(get_var PORT)" ]; then \
		v=$$(prompt_default "PORT" "8080"); \
		set_var PORT "$$v"; \
		set_summary="$$set_summary  PORT\n"; \
	fi; \
	if [ -z "$$(get_var BASE_URL)" ]; then \
		v=$$(prompt_default "BASE_URL" "http://localhost:8080"); \
		set_var BASE_URL "$$v"; \
		set_summary="$$set_summary  BASE_URL\n"; \
	fi; \
	\
	echo; echo "✓ .env ready."; \
	if [ -n "$$set_summary" ]; then \
		echo "Set (was missing):"; \
		printf "$$set_summary"; \
	else \
		echo "All multi-tenant vars already present — nothing to set."; \
	fi; \
	echo; echo "Reminder: keep SESSION_SECRET stable — rotating it logs every user out."; \
	echo "Skipped (smoke-test only, not needed for cmd/web): TESLA_ACCESS_TOKEN, TESLA_REFRESH_TOKEN, MAGUS_DB_PASSWORD."; \
	echo "Next: make db-setup   then   go run ./cmd/web"

# --- Codegen / deps (convenience; run by you, never as part of a build) ------

sqlc: ## Regenerate type-safe DB code from SQL (sqlc generate)
	sqlc generate

templ: ## Regenerate Templ HTML code (pinned go tool — NEVER a bare `go run .../templ`, it pollutes go.mod)
	go tool templ generate ./...

# Web UI CSS (gateway only) — Node-less: one native Tailwind binary + the committed
# DaisyUI .mjs bundles. No npm, no package.json, no node_modules.
ui-toolchain: ## (Re)download the git-ignored Node-less Tailwind CLI binary for THIS host's OS/arch (macOS/Linux). Dev-only — not needed to build or deploy (app.css is committed + embedded).
	@mkdir -p internal/gateway/tools
	@os=$$(uname -s); arch=$$(uname -m); \
	case "$$os" in Darwin) os=macos;; Linux) os=linux;; *) echo "Unsupported OS '$$os' — download the right binary manually from github.com/tailwindlabs/tailwindcss/releases"; exit 1;; esac; \
	case "$$arch" in arm64|aarch64) arch=arm64;; x86_64|amd64) arch=x64;; *) echo "Unsupported arch '$$arch'"; exit 1;; esac; \
	asset="tailwindcss-$$os-$$arch"; \
	echo "Downloading $$asset ..."; \
	curl -fsSL -o internal/gateway/tools/tailwindcss \
		"https://github.com/tailwindlabs/tailwindcss/releases/latest/download/$$asset"; \
	chmod +x internal/gateway/tools/tailwindcss; \
	echo "✓ tailwindcss ($$asset) ready at internal/gateway/tools/tailwindcss"

ui-bundles: ## (Re)download the COMMITTED DaisyUI .mjs bundles (latest). Pin a version by editing the tag in the URLs; run `make css` + commit app.css after.
	curl -fsSL -o internal/gateway/static/daisyui.mjs \
		https://github.com/saadeghi/daisyui/releases/latest/download/daisyui.mjs
	curl -fsSL -o internal/gateway/static/daisyui-theme.mjs \
		https://github.com/saadeghi/daisyui/releases/latest/download/daisyui-theme.mjs
	@echo "✓ DaisyUI bundles refreshed — now run: make css   (then commit app.css + the .mjs bundles)"

# Git-ignored Tailwind binary — fetched on demand so `make css` (and `make generate` /
# `make up`) work on a fresh clone or in CI with no manual step. This runs once; make
# skips it whenever the binary already exists.
internal/gateway/tools/tailwindcss:
	@$(MAKE) ui-toolchain

css: internal/gateway/tools/tailwindcss ## Regenerate internal/gateway/static/app.css from Templ sources (Node-less Tailwind + DaisyUI). Auto-fetches the Tailwind binary if missing; safe & idempotent.
	internal/gateway/tools/tailwindcss \
		-i internal/gateway/static/input.css \
		-o internal/gateway/static/app.css --minify

generate: sqlc templ css ## Run all code generators (sqlc + templ + css)

ui-guard: ## Fail if a raw DaisyUI component class is inlined in a page/fragment (use the ui/ kit instead)
	@if grep -rnE 'class="([^"]* )?(btn|card|input|select|textarea|fieldset|alert|badge|stat|table|navbar|drawer|menu)(-[a-z0-9]+)*( [^"]*)?"' \
		internal/gateway/templates/pages internal/gateway/templates/fragments --include='*.templ'; then \
		echo ""; \
		echo "ERROR: raw DaisyUI component class found in a page/fragment above."; \
		echo "The internal/gateway/templates/ui/ kit is the anti-corruption adapter for DaisyUI:"; \
		echo "compose ui.Button/ui.Input/ui.Select/ui.Textarea/ui.Card/... instead of inlining the class."; \
		echo "Theme tokens (text-error, bg-base-100) and Tailwind layout utilities ARE allowed inline."; \
		exit 1; \
	else \
		echo "ui-guard: no inlined DaisyUI component classes in pages/fragments"; \
	fi

tidy: ## Sync go.mod / go.sum (go mod tidy)
	go mod tidy

# --- Build / verify the whole monolith (you run these; never an auto-build) --

build: ## Compile every package + command in the monolith (go build ./...)
	go build ./...

vet: ## Static analysis across all packages (go vet ./...)
	go vet ./...

test: ## Run all tests against disposable testcontainer Postgres (never the real magus DB; ignores .env DATABASE_URL)
	env -u DATABASE_URL go test ./...

test-with-db: ## Run all tests against the configured DATABASE_URL (opt-in; CI with a managed Postgres)
	go test ./...

check: build vet ui-guard test ## Full local gate: build + vet + ui-guard + test

bins: ## Compile the cmd/* entrypoints into ./bin
	@mkdir -p bin
	go build -o bin/ ./cmd/...
	@echo "Built: $$(ls bin/)"

cmd-setup: ## Build cmd/setup into ./bin and run it (one-shot Tesla OAuth flow; writes tokens to .env)
	@mkdir -p bin
	go build -o bin/setup ./cmd/setup
	./bin/setup

up: generate migrate-up ## Refresh & run: regenerate code (sqlc + templ + css), apply migrations, then build & run the web server on $PORT (default 8080)
	@mkdir -p bin
	go build -o bin/web ./cmd/web
	./bin/web

dev: ## Hot-reload the web server for UI work. Three watchers run in parallel: tailwind --watch (writes app.css to disk), templ --watch (regenerates *_templ.go), and air (rebuilds + restarts the server on any .go change). MAGUS_DEV=1 serves /static from internal/gateway/static ON DISK so CSS hot-reloads on browser refresh WITHOUT a Go rebuild. Migrations are NOT re-run — run `make migrate-up` once before. Cookie sessions survive air's restart, so you do not re-login. Ctrl-C exits all watchers.
	@command -v air >/dev/null 2>&1 || go install github.com/air-verse/air@latest
	@mkdir -p tmp
	@export MAGUS_DEV=1; \
		echo "==> tailwind: watching .templ + static/themes for class changes (writes app.css to disk; served live via MAGUS_DEV=1)"; \
		internal/gateway/tools/tailwindcss -i internal/gateway/static/input.css -o internal/gateway/static/app.css --watch & TW_PID=$$!; \
		echo "==> templ: watching .templ (regenerates *_templ.go → air rebuilds the server)"; \
		go tool templ generate --watch & TL_PID=$$!; \
		trap 'kill $$TW_PID $$TL_PID 2>/dev/null' EXIT; \
		echo "==> air: rebuilding ./cmd/web on .go changes (incl. regenerated *_templ.go); serving on :$$PORT"; \
		air; \
		exit_code=$$?; \
		kill $$TW_PID $$TL_PID 2>/dev/null; \
		exit $$exit_code

cmd-explore-tesla: ## Build cmd/explore-tesla-api into ./bin and run it (COSTS a real API call; WAKES the car). Needs a fresh TESLA_ACCESS_TOKEN in .env
	@mkdir -p bin
	go build -o bin/explore-tesla-api ./cmd/explore-tesla-api
	./bin/explore-tesla-api

cmd-poller-once: ## Build cmd/poller into ./bin and run ONE collection cycle now, then exit (--once). Needs DATABASE_URL + a connected Tesla account; MAY WAKE sleeping cars (real API calls)
	@mkdir -p bin
	go build -o bin/poller ./cmd/poller
	./bin/poller --once
