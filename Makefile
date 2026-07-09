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

# This module's goose migrations. Add more dirs here as other modules gain a DB.
MIGRATIONS_DIR ?= internal/account/db/migrations

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
        db-setup db-reset env-setup sqlc tidy build vet test check bins

# --- Help -------------------------------------------------------------------

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

db-url: ## Print the derived DB name / admin URL (sanity check, no changes)
	@echo "DATABASE_URL       = $(DATABASE_URL)"
	@echo "DB_NAME            = $(DB_NAME)"
	@echo "APP_ROLE           = $(APP_ROLE)"
	@echo "ADMIN_DATABASE_URL = $(ADMIN_DATABASE_URL)"
	@echo "MIGRATIONS         = $(MIGRATIONS_DIR)"
	@echo "GOOSE              = $(GOOSE)"

# --- Database ---------------------------------------------------------------

check-goose:
	@command -v $(GOOSE) >/dev/null 2>&1 || { \
		echo "ERROR: goose not found at '$(GOOSE)'."; \
		echo "Install: go install github.com/pressly/goose/v3/cmd/goose@latest"; \
		echo "Then add \"$$(go env GOPATH)/bin\" to PATH, or run: make <target> GOOSE=/path/to/goose"; \
		exit 1; }

migrate-up: check-goose ## Apply all pending migrations
	@$(GOOSE) -dir $(MIGRATIONS_DIR) postgres "$(DATABASE_URL)" up

migrate-down: check-goose ## Roll back the most recent migration
	@$(GOOSE) -dir $(MIGRATIONS_DIR) postgres "$(DATABASE_URL)" down

migrate-status: check-goose ## Show which migrations have been applied
	@$(GOOSE) -dir $(MIGRATIONS_DIR) postgres "$(DATABASE_URL)" status

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
	$(GOOSE) -dir $(MIGRATIONS_DIR) postgres "$(DATABASE_URL)" up; \
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

tidy: ## Sync go.mod / go.sum (go mod tidy)
	go mod tidy

# --- Build / verify the whole monolith (you run these; never an auto-build) --

build: ## Compile every package + command in the monolith (go build ./...)
	go build ./...

vet: ## Static analysis across all packages (go vet ./...)
	go vet ./...

test: ## Run all tests (DB-backed tests skip unless DATABASE_URL is set)
	go test ./...

check: build vet test ## Full local gate: build + vet + test

bins: ## Compile the cmd/* entrypoints into ./bin
	@mkdir -p bin
	go build -o bin/ ./cmd/...
	@echo "Built: $$(ls bin/)"
