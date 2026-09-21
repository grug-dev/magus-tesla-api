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

# Each module owns its goose migrations dir (ai/architecture.md §2) AND its own goose
# version ledger, <module>.goose_db_version, inside the Postgres schema it already owns.
# Every goose invocation below therefore passes -table $$m.goose_db_version. Leave it off
# and goose writes to public.goose_db_version instead, which silently puts that module's
# history in the wrong place and disagrees with what cmd/migrate recorded.
#
# Consequences of the per-module ledger, all of them deliberate:
#   - Two modules MAY use the same version number. All four baselines are 20260917000001.
#     There is no longer a cross-module uniqueness rule to remember or to guard.
#   - The order of this list does not affect the result. A module's migrations create only
#     that module's objects and read nothing outside its own schema, so nothing can need
#     another module to have run first. The order is kept stable only for readable logs.
#   - No loop passes -allow-missing any more. It was needed only because the shared table
#     made a module's own lower version look like a late migration. Within one module a
#     late migration is a real mistake, so goose's own check is back on. Do not add the
#     flag back to silence a complaint: read it instead.
#
# Every loop below also runs CREATE SCHEMA IF NOT EXISTS before goose. That ordering is
# required, not tidiness: goose creates its version table BEFORE running any migration, and
# that table lives in the module's schema — which on an empty database does not exist yet,
# because the baseline that creates it is the very thing goose cannot reach. Without this,
# `goose run: ERROR: relation "account.goose_db_version" does not exist ... schema "account"
# does not exist`, and no fresh database can ever be built. cmd/migrate and internal/testdb
# do the same thing for the same reason (config.MigrationDir.EnsureSchemaSQL).
#
# This is why these targets need psql on PATH as well as goose.
#
# migrate-up also runs `cmd/migrate -stamp-only` first. A database built BEFORE the
# squash already holds every table a baseline creates, so goose would run the baseline
# and fail on "relation already exists". The stamp records the baseline as applied
# instead. It is a no-op on a fresh database and on one already past the squash, so the
# step is unconditional. It lives in Go rather than in another psql -c here because
# cmd/migrate needs the same logic for the Docker deploy, and one implementation cannot
# drift from the other. One-time code: see config.MigrationDir.StampBaselineSQL for when
# to delete it.
#
# MIGRATION_MODULES is the single source of both the directory and the ledger name, which
# is why the loops below iterate modules rather than directories. Overriding
# MIGRATIONS_DIRS alone no longer changes the goose CLI loops — override MIGRATION_MODULES.
MIGRATION_MODULES ?= account telemetry charging analytics

# MIGRATIONS_DIRS is derived, and still exported to cmd/migrate by migrate-run.
MIGRATIONS_DIRS ?= $(foreach m,$(MIGRATION_MODULES),internal/$(m)/db/migrations)

# goose binary: prefer one on PATH, else the `go install` location (GOPATH/bin).
GOOSE ?= $(shell command -v goose 2>/dev/null || echo $$(go env GOPATH)/bin/goose)
# Resolution order: PATH (a mise-activated interactive shell puts it there), then
# `mise which` (a non-interactive shell — CI, a script, an agent — does NOT source
# ~/.zshrc, so mise is installed but not on PATH), then a plain `go install` copy.
GOLANGCI ?= $(shell command -v golangci-lint 2>/dev/null || mise which golangci-lint 2>/dev/null || echo $$(go env GOPATH)/bin/golangci-lint)

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

# TEST_DATABASE_URL is the opt-in target for `db-setup-test` — a hand-kept local database
# a developer can point tests at instead of the disposable testcontainer (see `test-with-db`
# above). It is NOT read by `test`/`test-with-db` themselves; a developer sets it on the
# command line when they want it. TEST_DB_NAME and TEST_ADMIN_DATABASE_URL are derived from
# it the same way DB_NAME and ADMIN_DATABASE_URL are derived from DATABASE_URL.
TEST_DATABASE_URL ?= postgres://localhost:5432/magus_test?sslmode=disable
TEST_DB_NAME := $(shell echo "$(TEST_DATABASE_URL)" | sed -E 's|.*/([^/?]+).*|\1|')
TEST_ADMIN_DATABASE_URL := $(shell echo "$(TEST_DATABASE_URL)" | sed -E 's|^(postgres(ql)?://)([^/@]*@)?([^/?]+)/[^/?]+|\1\4/postgres|')

# TEST_ADMIN_ON_DB connects to the test database ITSELF (not the `postgres` maintenance DB),
# with any user:pass stripped from TEST_DATABASE_URL. Stripping matters: a schema in
# magus_test may be owned by the developer's own OS role rather than the app role, and
# dropping the credentials makes psql fall back to that OS role, the one that can actually
# re-own the schema. Never pass this alongside `-d` — the dbname is already in the URI.
TEST_ADMIN_ON_DB := $(shell echo "$(TEST_DATABASE_URL)" | sed -E 's|^(postgres(ql)?://)([^/@]*@)?([^/?]+)/([^/?]+)|\1\4/\5|')

.PHONY: help db-url check-goose migrate-up migrate-down migrate-status migrate-run \
        db-setup db-reset db-setup-test env-setup sqlc templ css ui-toolchain ui-bundles generate ui-guard i18n-guard money-guard tz-guard logging-guard migration-boundary-guard boundary-guard theme-guard vehicleref-guard tenancy-guard naming-guard archive-guard logdir-guard delta-guard tidy build vet lint check-golangci test check bins \
        up cmd-setup cmd-explore-tesla cmd-poller-once cmd-monthly-capacity \
        docker-up docker-down docker-logs vps-logs docker-migrate backup-db

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

migrate-up: check-goose ## Apply all pending migrations, each module into its own <module>.goose_db_version
	@MIGRATIONS_DIRS="$(MIGRATIONS_DIRS)" DATABASE_URL="$(DATABASE_URL)" go run ./cmd/migrate -stamp-only
	@for m in $(MIGRATION_MODULES); do \
		echo "goose up: $$m"; \
		psql "$(DATABASE_URL)" -v ON_ERROR_STOP=1 -q -c "CREATE SCHEMA IF NOT EXISTS $$m"; \
		$(GOOSE) -dir internal/$$m/db/migrations -table $$m.goose_db_version postgres "$(DATABASE_URL)" up; \
	done

# migrate-down STOPS at a module's baseline, which refuses to roll back: reversing it
# would mean dropping the whole module schema and its data. That refusal is the point —
# an empty rollback would mark the baseline un-applied while every object still exists,
# and the next migrate-up would fail on "relation already exists". Recreate the database
# (make db-reset) instead of rolling a baseline back.
migrate-down: check-goose ## Roll back the newest migration in each module (stops at the baseline, which refuses)
	@for m in $$(printf '%s\n' $(MIGRATION_MODULES) | awk '{a[NR]=$$0} END{for(i=NR;i>=1;i--)print a[i]}'); do \
		echo "goose down: $$m"; \
		$(GOOSE) -dir internal/$$m/db/migrations -table $$m.goose_db_version postgres "$(DATABASE_URL)" down; \
	done

migrate-status: check-goose ## Show which migrations have been applied (per module ledger)
	@for m in $(MIGRATION_MODULES); do \
		echo "== $$m ($$m.goose_db_version) =="; \
		psql "$(DATABASE_URL)" -v ON_ERROR_STOP=1 -q -c "CREATE SCHEMA IF NOT EXISTS $$m"; \
		$(GOOSE) -dir internal/$$m/db/migrations -table $$m.goose_db_version postgres "$(DATABASE_URL)" status; \
	done

# migrate-run does NOT depend on check-goose: it runs cmd/migrate (the same Go
# program the Docker "migrate" service runs), not the goose CLI. It passes
# this Makefile's own MIGRATIONS_DIRS through the MIGRATIONS_DIRS env var, so
# there is one source of truth for the directory order — cmd/migrate's
# internal/config.LoadMigration reads it directly (T8). This is how you test
# the deploy path's migration program locally: same migrations, same order,
# as migrate-up above, but through the container's own binary instead of the
# goose CLI.
#
# DATABASE_URL is deliberately NOT passed on the command line here. cmd/migrate
# reads .env itself, through config.LoadMigration(). Passing it would put the
# database password in the process arguments, where any user on the machine can
# read it with `ps`.
migrate-run: ## Run cmd/migrate locally against the .env DATABASE_URL (the Docker "migrate" service's own Go program, not the goose CLI — no goose install needed)
	MIGRATIONS_DIRS="$(MIGRATIONS_DIRS)" go run ./cmd/migrate

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
	MIGRATIONS_DIRS="$(MIGRATIONS_DIRS)" DATABASE_URL="$(DATABASE_URL)" go run ./cmd/migrate -stamp-only; \
	for m in $(MIGRATION_MODULES); do \
		echo "goose up: $$m"; \
		psql "$(DATABASE_URL)" -v ON_ERROR_STOP=1 -q -c "CREATE SCHEMA IF NOT EXISTS $$m"; \
		$(GOOSE) -dir internal/$$m/db/migrations -table $$m.goose_db_version postgres "$(DATABASE_URL)" up; \
	done; \
	echo; echo "✓ Database setup complete."; \
	PRINT_DSN=$$(echo "$(DATABASE_URL)" | sed -E "s#^(postgres(ql)?://)([^/@]*@)?#\1$$ROLE:<password>@#"); \
	echo "→ Point the app at the new role. Put this in your .env (insert the password you just set;"; \
	echo "  percent-encode it if it contains any of  @ : / ? # %  ):"; \
	echo; echo "    DATABASE_URL=$$PRINT_DSN"; echo

db-reset: ## DROP the database, recreate (owned by APP_ROLE), and migrate (DESTRUCTIVE — local/dev only)
	@psql "$(ADMIN_DATABASE_URL)" -c "DROP DATABASE IF EXISTS \"$(DB_NAME)\""
	@$(MAKE) db-setup

# db-setup-test brings TEST_DATABASE_URL's database current, for the developer who wants
# to point tests at a real, always-there Postgres instead of the disposable testcontainer:
# it starts faster and needs no Docker daemon. Unlike the testcontainer, this database can
# go stale between runs, so the target re-owns every module schema and re-applies pending
# migrations each time — safe to run as often as you like, including right before `make
# test-with-db TEST_DATABASE_URL=...`.
db-setup-test: check-goose ## Bring TEST_DATABASE_URL current (create/re-own/migrate) — faster than the testcontainer, no Docker needed
	@set -e; \
	if [ "$(TEST_DB_NAME)" = "$(DB_NAME)" ]; then \
		echo "ERROR: TEST_DATABASE_URL resolves to database '$(TEST_DB_NAME)', the same name as DATABASE_URL." >&2; \
		echo "       Refusing to run — this target must never touch the real database." >&2; \
		echo "       Point TEST_DATABASE_URL at a different database, e.g. magus_test." >&2; \
		exit 1; \
	fi; \
	ADMIN="$(TEST_ADMIN_DATABASE_URL)"; ADMIN_ON_DB="$(TEST_ADMIN_ON_DB)"; DB="$(TEST_DB_NAME)"; ROLE="$(APP_ROLE)"; \
	if psql "$$ADMIN" -tAc "SELECT 1 FROM pg_database WHERE datname='$$DB'" | grep -q 1; then \
		echo "Database '$$DB' already exists."; \
	else \
		echo "Creating database $$DB owned by $$ROLE..."; \
		psql "$$ADMIN" -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"$$DB\" OWNER \"$$ROLE\""; \
	fi; \
	echo "Re-owning module schemas to '$$ROLE' (a schema that doesn't exist yet is skipped)..."; \
	for schema in $(MIGRATION_MODULES); do \
		if psql "$(TEST_DATABASE_URL)" -tAc "SELECT 1 FROM pg_namespace WHERE nspname='$$schema'" | grep -q 1; then \
			psql "$$ADMIN_ON_DB" -v ON_ERROR_STOP=1 -c "ALTER SCHEMA \"$$schema\" OWNER TO \"$$ROLE\""; \
		fi; \
	done; \
	echo "Migrating '$$DB' to latest..."; \
	MIGRATIONS_DIRS="$(MIGRATIONS_DIRS)" DATABASE_URL="$(TEST_DATABASE_URL)" go run ./cmd/migrate -stamp-only; \
	for m in $(MIGRATION_MODULES); do \
		echo "goose up: $$m"; \
		psql "$(TEST_DATABASE_URL)" -v ON_ERROR_STOP=1 -q -c "CREATE SCHEMA IF NOT EXISTS $$m"; \
		$(GOOSE) -dir internal/$$m/db/migrations -table $$m.goose_db_version postgres "$(TEST_DATABASE_URL)" up; \
	done; \
	echo; echo "✓ $$DB ready. Point tests at it with:"; \
	echo "    TEST_DATABASE_URL=$(TEST_DATABASE_URL) make test"

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

# i18n-guard's `.templ` pass mirrors ui-guard's grep-based shape (design.md D1,
# RM24-gateway-translate-all-pages), plus two wrinkles ui-guard doesn't have. First, a
# candidate line cannot simply be dropped whenever it contains 'i18n.T(' ANYWHERE on the
# line — a second, untranslated literal can sit on the same line as an already-translated
# call (e.g. a button's translated aria-label next to its own hardcoded text node), so a
# line-local `grep -v 'i18n\.T('` would mask that second literal forever. Pass 1 instead
# strips every `i18n.T(...)` call substring out of each candidate line first and only
# keeps the candidate if one of the bare-text patterns still matches what remains. Second,
# templ has no in-markup comment syntax and HTML comments are forbidden project-wide
# (CLAUDE.md "HTML templates"), so the `// i18n:allow: <reason>` escape hatch for a
# `.templ` line sits on the PRECEDING line, not the flagged line itself — a small shell
# loop checks each surviving candidate's own line AND the line above it for the marker
# before reporting it. The handler pass (`.go`) has a real comment syntax, so its marker
# is a same-line trailing `//` and a line-local `grep -v` is sufficient there.
i18n-guard: ## Fail if user-facing text bypasses i18n.T(ctx, ...) in templates or handler message sinks (escape hatch: // i18n:allow: <reason>)
	@fail=0; \
	tmpl_candidates=$$(grep -rnE \
		-e '>[[:space:]]*[A-Za-z][^<{]*<' \
		-e '>[[:space:]]*[A-Za-z][^<{}]*\{' \
		-e '\}[^<{}]*[A-Za-z][^<{}]*<' \
		internal/gateway/templates/pages internal/gateway/templates/fragments internal/gateway/templates/ui \
		--include='*.templ' \
		| grep -v 'i18n:allow' \
		|| true); \
	if [ -n "$$tmpl_candidates" ]; then \
		tmpl_flagged=$$(printf '%s\n' "$$tmpl_candidates" | while IFS= read -r m; do \
			file=$$(printf '%s\n' "$$m" | cut -d: -f1); \
			lno=$$(printf '%s\n' "$$m" | cut -d: -f2); \
			content=$$(printf '%s\n' "$$m" | cut -d: -f3-); \
			stripped=$$(printf '%s\n' "$$content" | sed -E 's/i18n\.T\([^)]*\)//g'); \
			if ! printf '%s\n' "$$stripped" | grep -qE '>[[:space:]]*[A-Za-z][^<{]*<|>[[:space:]]*[A-Za-z][^<{}]*\{|\}[^<{}]*[A-Za-z][^<{}]*<'; then continue; fi; \
			prevno=$$((lno - 1)); \
			prevline=""; \
			if [ "$$prevno" -ge 1 ]; then prevline=$$(sed -n "$${prevno}p" "$$file"); fi; \
			if ! printf '%s\n' "$$prevline" | grep -q 'i18n:allow'; then echo "$$m"; fi; \
		done); \
		if [ -n "$$tmpl_flagged" ]; then echo "$$tmpl_flagged"; fail=1; fi; \
	fi; \
	handler_matches=$$(grep -rnE \
		-e '(Notice|Error):[[:space:]]*"[A-Za-z]' \
		-e 'errs\[[^]]+\][[:space:]]*=[[:space:]]*"[A-Za-z]' \
		-e '(vm|d)\.[A-Za-z0-9_]+[[:space:]]*=[[:space:]]*"[A-Za-z]' \
		-e 'c\.String\(http\.Status[45][0-9][0-9],[[:space:]]*"[A-Za-z]' \
		-e '(Notice|Error):[[:space:]]*fmt\.(Sprintf|Errorf)\("[A-Za-z]' \
		-e 'errs\[[^]]+\][[:space:]]*=[[:space:]]*fmt\.(Sprintf|Errorf)\("[A-Za-z]' \
		-e '(vm|d)\.[A-Za-z0-9_]+[[:space:]]*=[[:space:]]*fmt\.(Sprintf|Errorf)\("[A-Za-z]' \
		-e 'c\.String\(http\.Status[45][0-9][0-9],[[:space:]]*fmt\.(Sprintf|Errorf)\("[A-Za-z]' \
		internal/gateway/handlers --include='*.go' \
		| grep -v '_test\.go:' \
		| grep -v 'i18n:allow' \
		|| true); \
	if [ -n "$$handler_matches" ]; then echo "$$handler_matches"; fail=1; fi; \
	if [ "$$fail" = "1" ]; then \
		echo ""; \
		echo "ERROR: hardcoded user-facing text found above — it bypasses i18n.T(ctx, ...)."; \
		echo "Add a Key + {ES,EN} entry to internal/gateway/i18n/catalog.go and call i18n.T(ctx, key)"; \
		echo "(or fmt.Sprintf(i18n.T(ctx, key), ...) for an interpolated string)."; \
		echo "Genuinely non-translatable literal (not app copy)? Mark it with // i18n:allow: <reason> —"; \
		echo "same line in a .go file; the line ABOVE the flagged line in a .templ file (no in-markup"; \
		echo "comment syntax, HTML comments are forbidden project-wide). Never weaken this pattern."; \
		exit 1; \
	else \
		echo "i18n-guard: no hardcoded user-facing text found in templates/{pages,fragments,ui} or handlers"; \
	fi

# money-guard mirrors ui-guard/i18n-guard's grep-based shape and escape-hatch
# convention (design.md D4, gateway-format-currency-values). It targets the
# exact bug shape MAG-9 found: a %.Nf decimal verb immediately adjacent to a
# %s currency placeholder — the signature of hand-rolling a money string
# instead of calling formatMoney (internal/gateway/handlers/format.go). It
# deliberately does NOT flag a literal unit suffix like "%.1f kWh" (D3 —
# kWh/km/degC/pct formatting is out of scope). format.go itself is excluded
# since formatMoney's own internals build the string via commaGroup +
# strconv, never via this Sprintf shape. Escape hatch: a trailing
# `// money:allow: <reason>` comment on the same line (handlers are .go
# files, which have real comment syntax, so — unlike i18n-guard's .templ
# pass — no separate above-the-line placement is needed).
money-guard: ## Fail if a handler hand-rolls a money label with fmt.Sprintf("%.Nf %s", ...) instead of calling formatMoney (escape hatch: // money:allow: <reason>)
	@if grep -rnE 'fmt\.Sprintf\("%\.[0-9]+f %s' \
		internal/gateway/handlers --include='*.go' \
		| grep -v '_test\.go:' \
		| grep -v '/format\.go:' \
		| grep -v 'money:allow'; then \
		echo ""; \
		echo "ERROR: hand-rolled money-formatting fmt.Sprintf found above."; \
		echo "Call formatMoney(amount, currency) (internal/gateway/handlers/format.go) instead of"; \
		echo "fmt.Sprintf(\"%.Nf %s\", ...) for a currency value."; \
		echo "Genuinely not a currency value (false positive)? Mark it with // money:allow: <reason>"; \
		echo "as a trailing comment on the same line. Never weaken this pattern to silence a true positive."; \
		exit 1; \
	else \
		echo "money-guard: no hand-rolled money-formatting fmt.Sprintf found in handlers/*.go"; \
	fi

# tz-guard mirrors money-guard's grep-based shape (design.md D-plat-1, RM35-platform-add-tz-guard),
# enforcing ai/go-conventions.md's time-zone rule (RM35 D2): internal/clock is the sole owner of
# the platform's default zone, "now", and calendar-day normalization. Three legs, all scanning
# `internal` (never `cmd/`, which is out of scope by roadmap D4 simply by never being scanned):
# raw time.Now(), a hand-rolled "midnight of some day" time.Date(...) construction (any trailing
# zone argument — catches both a hardcoded time.UTC truncator and a zone-parameterized one like
# startOfDayIn), and a hardcoded IANA zone string literal. Excludes _test.go wholesale (design.md
# D-plat-3 — the tier-6 test-anchor bug class this convention is warned about is a semantic
# mismatch no grep can see; every real _test.go hit is a legitimate fixture timestamp or literal
# test date, hundreds of them, with zero discriminating power) and comment-only lines (design.md
# D-plat-4 — the convention is explained in prose comments that contain the very literals being
# guarded, e.g. "time.Now()" or "America/Bogota" as example text). Escape hatch: a trailing
# `// tz:allow: <reason>` comment on the same line (all scanned files are .go, real comment
# syntax — no separate above-the-line placement needed, unlike i18n-guard's .templ pass).
tz-guard: ## Fail if code outside internal/clock hand-rolls "now", a UTC/day-midnight construction, a 24h Truncate day-rounding, or a hardcoded IANA zone name (escape hatch: // tz:allow: <reason>)
	@fail=0; \
	now_matches=$$(grep -rnE 'time\.Now\(\)' internal --include='*.go' \
		| grep -v '_test\.go:' \
		| grep -v '^internal/clock/' \
		| grep -v 'tz:allow' \
		| grep -vE '^[^:]+:[^:]+:[[:space:]]*//' \
		|| true); \
	if [ -n "$$now_matches" ]; then echo "$$now_matches"; fail=1; fi; \
	trunc_matches=$$(grep -rnE ',[[:space:]]*0,[[:space:]]*0,[[:space:]]*0,[[:space:]]*0,[[:space:]]*[A-Za-z_][A-Za-z0-9_.]*\)' internal --include='*.go' \
		| grep -v '_test\.go:' \
		| grep -v '^internal/clock/' \
		| grep -v 'tz:allow' \
		| grep -vE '^[^:]+:[^:]+:[[:space:]]*//' \
		|| true); \
	if [ -n "$$trunc_matches" ]; then echo "$$trunc_matches"; fail=1; fi; \
	daytrunc_matches=$$(grep -rnE '\.Truncate\(.*(24[[:space:]]*\*[[:space:]]*time\.Hour|time\.Hour[[:space:]]*\*[[:space:]]*24)' internal --include='*.go' \
		| grep -v '_test\.go:' \
		| grep -v '^internal/clock/' \
		| grep -v 'tz:allow' \
		| grep -vE '^[^:]+:[^:]+:[[:space:]]*//' \
		|| true); \
	if [ -n "$$daytrunc_matches" ]; then echo "$$daytrunc_matches"; fail=1; fi; \
	zone_matches=$$(grep -rnE '"[A-Z][a-zA-Z_]+/[A-Z][a-zA-Z_]+"' internal --include='*.go' \
		| grep -v '_test\.go:' \
		| grep -v '^internal/clock/' \
		| grep -v 'tz:allow' \
		| grep -vE '^[^:]+:[^:]+:[[:space:]]*//' \
		|| true); \
	if [ -n "$$zone_matches" ]; then echo "$$zone_matches"; fail=1; fi; \
	if [ "$$fail" = "1" ]; then \
		echo ""; \
		echo "ERROR: raw time.Now(), a hand-rolled UTC/day-midnight construction, a 24h Truncate"; \
		echo "day-rounding, or a hardcoded"; \
		echo "IANA zone name found above, outside internal/clock. internal/clock is the platform's"; \
		echo "sole owner of the default time zone, \"now\", and calendar-day normalization"; \
		echo "(ai/go-conventions.md, RM35-timezone-centralization D2). Call clock.Now() /"; \
		echo "clock.Zone() / clock.CalendarDay(t, loc) / clock.LoadOrDefault(name) instead."; \
		echo "Genuinely deliberate exception (a zone-parameterized helper, a different"; \
		echo "granularity, a documented non-default choice)? Mark it with // tz:allow: <reason>"; \
		echo "as a trailing comment on the same line. Never weaken this pattern to silence a true"; \
		echo "positive."; \
		exit 1; \
	else \
		echo "tz-guard: no raw time.Now(), hand-rolled UTC/day-midnight construction,"; \
		echo "24h Truncate day-rounding, or hardcoded IANA zone name found outside internal/clock"; \
	fi

# logging-guard mirrors tz-guard's grep-based shape, enforcing ai/go-conventions.md's
# Logging rule: internal/logging is the platform's single owner of the log-line format,
# [Type] [Method] message (logging.Note). Scans `internal` only — cmd/ is the exempt
# composition root (startup, shutdown, and fatal exits may use stdlib log directly,
# simply by never being scanned), excluding internal/logging itself (the owner),
# _test.go wholesale (skip notices and TestMain log.Fatalf are legitimate test-time
# output), and comment-only lines (the convention is explained in prose that contains
# the very literals being guarded, e.g. "log.Printf"). The pattern's leading boundary
# `(^|[^[:alnum:]_.])` keeps it from matching slog-style identifiers embedded in other
# words. Escape hatch: a trailing `// log:allow: <reason>` comment on the same line.
logging-guard: ## Fail if a domain module calls stdlib log directly instead of internal/logging's Note (escape hatch: // log:allow: <reason>)
	@fail=0; \
	log_matches=$$(grep -rnE '(^|[^[:alnum:]_.])log\.(Print|Fprint|Sprint|Fatal|Panic|Set|New|Default|Output|Writer|Flags|Prefix)' internal --include='*.go' \
		| grep -v '_test\.go:' \
		| grep -v '^internal/logging/' \
		| grep -v 'log:allow' \
		| grep -vE '^[^:]+:[^:]+:[[:space:]]*//' \
		|| true); \
	if [ -n "$$log_matches" ]; then echo "$$log_matches"; fail=1; fi; \
	if [ "$$fail" = "1" ]; then \
		echo ""; \
		echo "ERROR: raw stdlib log call found above, outside internal/logging. internal/logging"; \
		echo "is the platform's single owner of the log-line format, [Type] [Method] message"; \
		echo "(ai/go-conventions.md, § Logging). Call logging.Note(typ, method, format, args...)"; \
		echo "instead. cmd/ is exempt (composition root). Genuinely deliberate exception?"; \
		echo "Mark it with // log:allow: <reason> as a trailing comment on the same line."; \
		echo "Never weaken this pattern to silence a true positive."; \
		exit 1; \
	else \
		echo "logging-guard: no raw stdlib log calls found outside internal/logging"; \
	fi

tidy: ## Sync go.mod / go.sum (go mod tidy)
	go mod tidy

# --- Build / verify the whole monolith (you run these; never an auto-build) --

build: ## Compile every package + command in the monolith (go build ./...)
	go build ./...

vet: ## Static analysis across all packages (go vet ./...)
	go vet ./...

check-golangci:
	@command -v $(GOLANGCI) >/dev/null 2>&1 || { \
		echo "ERROR: golangci-lint not found at '$(GOLANGCI)'."; \
		echo "Install: mise install   (it is pinned in mise.toml)"; \
		echo "Or:      go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"; \
		echo "Then add \"$$(go env GOPATH)/bin\" to PATH, or run: make lint GOLANGCI=/path/to/golangci-lint"; \
		exit 1; }

# lint is Go-CORRECTNESS checking; the *-guard targets are PROJECT-RULE checking.
# Keep them apart: a rule goes in .golangci.yml only when an off-the-shelf linter
# already implements it, and in a guard when it is specific to this codebase.
#
# lint adds three things `go vet` does not: errcheck (an unchecked error return),
# unused (dead code — vet never reports it), and the resource-leak linters
# bodyclose / rowserrcheck / sqlclosecheck, which cover where a Fleet API call or
# a row loop would actually leak. Config + rationale: .golangci.yml.
lint: check-golangci ## Lint every package (golangci-lint run ./...; config in .golangci.yml)
	$(GOLANGCI) run ./...

test: ## Run all tests against disposable testcontainer Postgres (TEST_DATABASE_URL is never read from .env here)
	go test ./...

test-with-db: ## Run all tests against TEST_DATABASE_URL (opt-in; point it at a throwaway or CI Postgres, never the real one)
	TEST_DATABASE_URL="$(DATABASE_URL)" go test ./...

# A module's migration may name only that module's own Postgres schema. The rule is the
# same one that binds runtime code (ai/architecture.md boundary rules): a module that may
# not read another module's tables through Go must not read them in SQL either.
#
# It is guarded because breaking it is invisible until a live database is involved. Four
# migrations used to read another module's schema, and they worked purely because the
# dependency direction happened to match the order the directories were applied in. When
# telemetry later re-keyed vehicle_snapshots, an analytics migration that joined the
# dropped column failed on every fresh database and every test container — and the fix was
# to abandon the drop (MAG-65). One baseline per module removed all four, and this guard is
# what stops the fifth.
#
# COMMENT ON statements are excluded. A table or column comment is documentation: it cannot
# read a row, and the generated baselines carry comments that legitimately mention another
# module's table by name to explain where a mirrored value came from. Everything else that
# names another schema is a genuine read or write.
migration-boundary-guard: ## Fail if a module's migration names another module's Postgres schema (escape hatch: -- migration:allow: <reason>)
	@fail=0; \
	for m in $(MIGRATION_MODULES); do \
		others=$$(printf '%s\n' $(MIGRATION_MODULES) | grep -v "^$$m$$" | paste -sd'|' -); \
		hits=$$(grep -rnE "\\b($$others)\\.[a-z_]" internal/$$m/db/migrations --include='*.sql' \
			| grep -vE '^[^:]+:[0-9]+:[[:space:]]*--' \
			| grep -viE 'COMMENT ON' \
			| grep -v 'migration:allow' \
			|| true); \
		if [ -n "$$hits" ]; then echo "$$hits"; fail=1; fi; \
	done; \
	if [ "$$fail" = "1" ]; then \
		echo ""; \
		echo "ERROR: the migrations above name another module's Postgres schema."; \
		echo "A module's migrations may touch only its own schema. Reading another module's"; \
		echo "table in SQL recreates the ordering dependency that one baseline per module"; \
		echo "exists to remove: it works only while the directories happen to be applied in"; \
		echo "the right order, and it breaks silently the moment that module changes the table."; \
		echo "Fix: move the data flow to the owning module's public Go interface, or have the"; \
		echo "module that owns the table write the value itself."; \
		echo "Genuinely unavoidable (false positive)? Mark it with -- migration:allow: <reason>"; \
		echo "on the same line. Never weaken this pattern to silence a true positive."; \
		exit 1; \
	else \
		echo "migration-boundary-guard: no migration names another module's schema ($(words $(MIGRATION_MODULES)) modules)"; \
	fi

# boundary-guard mirrors money-guard's grep-based shape and escape-hatch convention.
# It enforces ONE rule: internal/gateway/ must not depend on internal/telemetry at
# all — not even through the telemetry.Reader port. The gateway reaches vehicle
# telemetry through another module's interface instead; it never names the telemetry
# package itself.
#
# This is STRICTER than the older "cross-module data flows through public interfaces"
# rule, which permitted gateway -> telemetry.Reader. See ai/architecture.md
# §"The Reader/Collector port split" for the current rule and the migration status.
#
# The guard fails on non-test files (a real compile-time dependency of the gateway
# package on telemetry) and separately WARNS on _test.go files, mirroring
# boundary-guard's warn/fail split: a fake in a test is the same dependency, but it
# is mechanical to remove and should not block the production fix.
#
# Escape hatch: a trailing `// boundary:allow: <reason>` comment on the same line as
# the import. Never widen the pattern to silence a true positive.
boundary-guard: ## Fail if internal/gateway/ imports internal/telemetry (escape hatch: // boundary:allow: <reason>)
	@testhits=$$(grep -rn '"github.com/cristianpena/magus-tesla-api/internal/telemetry"' \
		internal/gateway --include='*_test.go' \
		| grep -v 'boundary:allow' || true); \
	if [ -n "$$testhits" ]; then \
		echo "$$testhits"; \
		echo ""; \
		echo "WARNING: the gateway test files above still import internal/telemetry."; \
		echo "Not fatal yet — replace the telemetry-typed fakes when the production"; \
		echo "imports are removed. Tracked with the production migration."; \
		echo ""; \
	fi
	@if grep -rn '"github.com/cristianpena/magus-tesla-api/internal/telemetry"' \
		internal/gateway --include='*.go' \
		| grep -v '_test\.go:' \
		| grep -v 'boundary:allow'; then \
		echo ""; \
		echo "ERROR: internal/gateway/ imports internal/telemetry above."; \
		echo "The gateway must not depend on the telemetry module — not even on its"; \
		echo "Reader port. Obtain vehicle telemetry through the interface the gateway"; \
		echo "is allowed to call, and keep telemetry.* types out of gateway code."; \
		echo "See ai/architecture.md \"The Reader/Collector port split\"."; \
		echo "Genuinely unavoidable (false positive)? Mark it with // boundary:allow: <reason>"; \
		echo "as a trailing comment on the same line. Never weaken this pattern to silence"; \
		echo "a true positive."; \
		exit 1; \
	else \
		echo "boundary-guard: internal/gateway/ does not import internal/telemetry"; \
	fi

# theme-guard mirrors boundary-guard's grep-based shape and escape-hatch convention
# (design.md D5, RM42-gateway-add-theme-selector tier 2). It reconciles THREE
# independent copies of the theme vocabulary that must never drift apart:
#   1. ui.Themes (internal/gateway/templates/ui/theme.go) — the presentation-facing
#      slice the dropdown iterates and the sole source of the closed vocabulary the
#      switch endpoint validates against.
#   2. account's Theme* constants (internal/account/account.go) — the domain
#      module's own independent copy (roadmap RM42 tier 1), validated by SetTheme.
#   3. static/input.css's two theme-registration shapes — the
#      @plugin "./daisyui.mjs" { themes: ...; } block (halloween, a daisyUI builtin
#      with no CSS file of its own) and the @import "./themes/<name>.css" lines
#      (apex, graphite; _shared.css is excluded — it is shared infrastructure, not
#      a theme).
#
# Three independent, guarded copies (rather than deriving one from another at
# build/run time) keep the failure mode a clear, localized `make check` error
# naming exactly which list disagrees, instead of a silently wrong dropdown at
# runtime — see design.md D5's own rejection of a runtime-parsed alternative.
#
# Escape hatch: a trailing `// theme:allow: <reason>` comment on the same line as
# a Go-side entry (ui.Themes' slice literal, or one account Theme* constant) that
# is deliberately excluded from a comparison — e.g. a theme intentionally staged
# in ui.Themes before its CSS file lands. input.css has no comment syntax usable
# mid-line for this purpose, so the hatch is only ever placed on the Go-side lines
# being compared.
theme-guard: ## Fail if ui.Themes, account's Theme* constants, and input.css's registered themes disagree (escape hatch: // theme:allow: <reason>)
	@ui_themes=$$(grep -A1 'Themes = \[\]string{' internal/gateway/templates/ui/theme.go \
		| grep -v 'theme:allow' \
		| grep -oE '"[a-z]+"' | tr -d '"' | sort -u); \
	account_themes=$$(grep -E '^\s*Theme[A-Z][A-Za-z]*\s*=\s*"[a-z]+"' internal/account/account.go \
		| grep -v 'theme:allow' \
		| grep -oE '"[a-z]+"' | tr -d '"' | sort -u); \
	plugin_themes=$$(grep -oE 'themes:\s*[a-z, ]+;' internal/gateway/static/input.css \
		| grep -oE '[a-z]+' | grep -v themes); \
	import_themes=$$(grep -oE '@import "\./themes/[a-z]+\.css";' internal/gateway/static/input.css \
		| grep -oE '/[a-z]+\.css' | sed -E 's#/([a-z]+)\.css#\1#' | grep -v shared); \
	css_themes=$$(printf '%s\n%s\n' "$$plugin_themes" "$$import_themes" | sort -u); \
	fail=0; \
	if [ "$$ui_themes" != "$$account_themes" ]; then \
		echo "ERROR: ui.Themes and account's Theme* constants disagree:"; \
		echo ""; \
		echo "  ui.Themes (internal/gateway/templates/ui/theme.go):"; \
		echo "$$ui_themes" | sed 's/^/    /'; \
		echo "  account Theme* (internal/account/account.go):"; \
		echo "$$account_themes" | sed 's/^/    /'; \
		echo ""; \
		fail=1; \
	fi; \
	if [ "$$ui_themes" != "$$css_themes" ]; then \
		echo "ERROR: ui.Themes and static/input.css's registered themes disagree:"; \
		echo ""; \
		echo "  ui.Themes (internal/gateway/templates/ui/theme.go):"; \
		echo "$$ui_themes" | sed 's/^/    /'; \
		echo "  input.css themes (static/input.css):"; \
		echo "$$css_themes" | sed 's/^/    /'; \
		echo ""; \
		fail=1; \
	fi; \
	if [ "$$fail" = "1" ]; then \
		echo "Every theme code must appear in ALL THREE places: ui.Themes, account's"; \
		echo "Theme* constants, and static/input.css (via @plugin { themes: ...; } or an"; \
		echo "@import \"./themes/<name>.css\" line). Add or remove it in whichever list is"; \
		echo "missing it. Genuinely unavoidable (a theme staged in one list before its"; \
		echo "counterpart lands)? Mark the Go-side line with // theme:allow: <reason> as a"; \
		echo "trailing comment. Never weaken this pattern to silence a true positive."; \
		exit 1; \
	else \
		echo "theme-guard: ui.Themes, account's Theme* constants, and input.css agree ($$ui_themes)"; \
	fi

# vehicleref-guard mirrors boundary-guard's grep-based shape and escape-hatch convention.
# It enforces that vehicleref.Authorize and vehicleref.All are only ever called from
# internal/vehicleref itself, from any _test.go file, and from the gateway's
# authorizeVehicle helper in internal/gateway/handlers/handlers.go.
#
# Test files are exempt because a test has no account to check an id against. It must
# build a Ref directly to call a port that requires one. The guard protects production
# code: a Ref built in a test never reaches a running server. A Ref proves a vehicle
# id was checked against a caller's own ownership list; a second, unreviewed construction
# site would let a future handler build a "checked" Ref without the check ever running.
#
# Escape hatch: a trailing `// vehicleref:allow: <reason>` comment on the same line as the
# call. Never widen the pattern to silence a true positive.
vehicleref-guard: ## Fail if vehicleref.Authorize/.All are called outside internal/vehicleref, _test.go files and the gateway's authorizeVehicle helper (escape hatch: // vehicleref:allow: <reason>)
	@hits=$$(grep -rnE 'vehicleref\.(Authorize|All)\(' internal --include='*.go' \
		| grep -v '^internal/vehicleref/' \
		| grep -v '_test\.go:' \
		| grep -v '^internal/gateway/handlers/handlers\.go:' \
		| grep -v 'vehicleref:allow' || true); \
	if [ -n "$$hits" ]; then \
		echo "$$hits"; \
		echo ""; \
		echo "ERROR: vehicleref.Authorize/.All called outside internal/vehicleref,"; \
		echo "_test.go files, and internal/gateway/handlers/handlers.go above."; \
		echo "A Ref must only ever be built by internal/vehicleref itself or by the"; \
		echo "gateway's authorizeVehicle helper. A second construction site defeats the"; \
		echo "compile-time guarantee: a handler could build its own 'checked' Ref without"; \
		echo "the ownership check ever running."; \
		echo "Genuinely unavoidable (false positive)? Mark it with // vehicleref:allow: <reason>"; \
		echo "as a trailing comment on the same line. Never weaken this pattern to silence"; \
		echo "a true positive."; \
		exit 1; \
	else \
		echo "vehicleref-guard: vehicleref.Authorize/.All called only from internal/vehicleref, tests and authorizeVehicle"; \
	fi

# tenancy-guard mirrors boundary-guard's grep-based shape and escape-hatch convention.
# It enforces the re-keyed multi-tenant rule (ai/go-conventions.md "Read optimization
# (project-wide)", ai/architecture.md §7): a table keys on tesla_id or vin now, never
# on account_id, everywhere except internal/account, which legitimately owns
# account_id as its own primary key. A query filtering `WHERE account_id = $1`
# outside internal/account would silently reintroduce the reversed rule with
# nothing to catch it.
#
# Scope is every .sql a module keeps under db/, EXCEPT db/migrations/. A migration
# is frozen the moment it is applied — the archive-guard rule elsewhere in this
# Makefile exists for the same reason — and every migration in this repo still
# legitimately shows account_id in the table shape it altered at the time. The live
# query layer is what a future regression would touch, so that is what this guard
# reads. Today that layer is one query.sql per module, but the guard does not
# hardcode that name: a module that later splits its queries across several .sql
# files is covered without anyone remembering to widen this target.
#
# Word-boundary match only, so it never fires on created_by_account_id or
# polled_by_account_id — demoted attribute columns that record who acted, not what
# the car did — nor on a plain SQL comment (a line whose first non-blank characters
# are `--`), which explains a past decision rather than filtering a query.
#
# Escape hatch: a trailing `-- tenancy:allow: <reason>` SQL comment on the same
# line (SQL's own comment syntax — unlike the Go-file guards above, which use
# `//`). Never widen the pattern to silence a true positive.
tenancy-guard: ## Fail if a module's db/*.sql outside internal/account filters on account_id (escape hatch: -- tenancy:allow: <reason>)
	@hits=$$(find internal -path '*/db/*' -name '*.sql' ! -path '*/db/migrations/*' \
		-exec grep -nE '\baccount_id\b' {} + 2>/dev/null \
		| grep -v '^internal/account/' \
		| grep -v 'created_by_account_id' \
		| grep -v 'polled_by_account_id' \
		| grep -vE '^[^:]+:[0-9]+:[[:space:]]*--' \
		| grep -v 'tenancy:allow' || true); \
	if [ -n "$$hits" ]; then \
		echo "$$hits"; \
		echo ""; \
		echo "ERROR: account_id referenced in a db/*.sql outside internal/account above."; \
		echo "The platform re-keyed multi-tenant tables on tesla_id or vin; account_id"; \
		echo "stays only as a demoted attribute (created_by_account_id,"; \
		echo "polled_by_account_id) or inside internal/account itself. See"; \
		echo "ai/go-conventions.md \"Read optimization (project-wide)\" and"; \
		echo "ai/architecture.md §7 for the keying rules."; \
		echo "Genuinely unavoidable (false positive)? Mark it with -- tenancy:allow: <reason>"; \
		echo "as a trailing comment on the same line. Never weaken this pattern to silence"; \
		echo "a true positive."; \
		exit 1; \
	else \
		echo "tenancy-guard: no account_id filter in a db/*.sql outside internal/account"; \
	fi

# naming-guard mirrors boundary-guard's grep-based shape and warn/fail split,
# enforcing ai/go-conventions.md §Coding Rules "Type names must carry the domain
# word". A type declaration (struct or interface, exported or not) whose name ends
# in a banned generic suffix — Processor, Manager, Handler, Helper, Data, Info,
# Object, Thing — fails the guard: those names say nothing about the domain, so a
# reader cannot guess what is inside (the exact smell that produced app.Processor).
#
# Six declarations pre-date the rule and only WARN (the BASELINE filter below), so
# `make check` stays green today while any NEW banned name fails. When a baseline
# name is renamed away, delete its entry — the baseline only ever shrinks. The
# generated *_templ.go files are covered too: their type declarations live in the
# .templ source, which is the file a rename actually edits.
#
# Escape hatch: a trailing `// naming:allow: <reason>` comment on the same line.
# Never widen the pattern to silence a true positive.
naming-guard: ## Fail if a NEW type declaration ends in a banned generic suffix (Processor/Manager/Handler/Helper/Data/Info/Object/Thing); pre-rule baseline names only warn (escape hatch: // naming:allow: <reason>)
	@pattern='^type [A-Za-z0-9_]*(Processor|Manager|Handler|Helper|Data|Info|Object|Thing)(\[[^]]*\])? (struct|interface)'; \
	baseline='type (Processor|guardedProcessor|DashboardData|ExternalChargesPageData|VehiclesData|Handler) '; \
	hits=$$(grep -rnE "$$pattern" --include='*.go' internal cmd \
		| grep -v 'naming:allow' || true); \
	warn=$$(echo "$$hits" | grep -E "$$baseline" || true); \
	fail=$$(echo "$$hits" | grep -vE "$$baseline" || true); \
	if [ -n "$$warn" ]; then \
		echo "$$warn"; \
		echo ""; \
		echo "WARNING: the pre-rule type names above end in a banned generic suffix."; \
		echo "Not fatal — they pre-date ai/go-conventions.md's naming rule. Renaming"; \
		echo "one is a deliberate change; when done, shrink the baseline in the"; \
		echo "Makefile's naming-guard target."; \
		echo ""; \
	fi; \
	if [ -n "$$fail" ]; then \
		echo "$$fail"; \
		echo ""; \
		echo "ERROR: the type declarations above end in a banned generic suffix"; \
		echo "(Processor/Manager/Handler/Helper/Data/Info/Object/Thing). The name says"; \
		echo "nothing about the domain — write one sentence, \"this thing does X\","; \
		echo "and name the type after X's key word (VehicleDataCycle, not Processor)."; \
		echo "See ai/go-conventions.md §Coding Rules \"Type names must carry the domain"; \
		echo "word\". Genuinely unavoidable (false positive)? Mark the line with"; \
		echo "// naming:allow: <reason>. Never weaken this pattern to silence a true"; \
		echo "positive."; \
		exit 1; \
	else \
		echo "naming-guard: no new banned-suffix type declaration (baseline: $$(echo "$$warn" | grep -cE "$$baseline" || true) legacy name(s))"; \
	fi

# delta-guard enforces ai/go-conventions.md's delta-column naming rule: a column
# or Go field that holds today's value of a metric minus yesterday's value of the
# SAME metric must end in _delta_calc / DeltaCalc, never a bare _calc / Calc. A
# grep cannot tell a delta from a same-day rate or ratio (km_per_pct_calc,
# inferred_capacity_kwh_calc are both legitimate non-deltas) — it only checks the
# naming shape, the same honest limit every guard below accepts.
#
# Two legs, each warn/fail-split against its own baseline, mirroring
# naming-guard above:
#   - SQL leg scans column DEFINITION lines only (CREATE TABLE / ADD COLUMN, in
#     db/migrations/*.sql) — a column is named once, at definition, so query.sql
#     and COMMENT ON prose are never scanned. A guard that read prose could flag
#     its own English documentation quoting a column name.
#   - Go leg scans field-declaration-shaped lines (a tab, then a capitalized
#     ...Calc identifier) under internal/ and cmd/, excluding _test.go. A struct
#     literal field assignment has the identical shape and also matches; that is
#     accepted — it lands in the same bucket the real declaration already would.
#     It is also why the success line counts distinct names, not matched lines:
#     one baselined name matches dozens of times.
#     Files carrying a "Code generated by ... DO NOT EDIT." first line are
#     skipped: sqlc writes one struct field per column, so a column that already
#     passed the SQL leg would be flagged a second time in Go, and the escape
#     hatch is unreachable there -- the marker has to sit on the same line, and
#     the next codegen run would erase it. Skipping them checks each column once,
#     where a human can actually answer for the name.
#
# The baseline lists every _calc/Calc name that existed before this rule. It
# only ever shrinks: when a name is renamed to *_delta_calc/DeltaCalc, or proven
# to be a genuine non-delta and marked delta:allow instead, delete its entry
# here. Never add a name to the baseline — a new bare _calc/Calc name must
# either become a delta name or carry the escape hatch.
#
# Escape hatch: a trailing `-- delta:allow: <reason>` (SQL) or
# `// delta:allow: <reason>` (Go) comment on the same line. Never widen a
# pattern to silence a true positive.
delta-guard: ## Fail if a NEW day-over-day delta column/field is named _calc/Calc instead of _delta_calc/DeltaCalc; pre-rule baseline names only warn (escape hatch: -- delta:allow: / // delta:allow: <reason>)
	@sqlpattern='^[[:space:]]*(ADD COLUMN[[:space:]]+)?[a-z][a-z0-9_]*_calc\b'; \
	sqldeltapattern='(^|:)[[:space:]]*(ADD COLUMN[[:space:]]+)?[a-z][a-z0-9_]*_delta_calc\b'; \
	sqlbaseline='(distance_traveled_km_calc|battery_used_pct_calc|days_spanned_calc|km_per_pct_calc|estimated_range_km_calc|tpms_pressure_fl_psi_calc|tpms_pressure_fr_psi_calc|tpms_pressure_rl_psi_calc|tpms_pressure_rr_psi_calc|inferred_capacity_kwh_calc)\b'; \
	sqlhits=$$(grep -rnE "$$sqlpattern" --include='*.sql' internal/*/db/migrations \
		| grep -vE "$$sqldeltapattern" \
		| grep -v 'delta:allow' || true); \
	sqlwarn=$$(echo "$$sqlhits" | grep -E "$$sqlbaseline" || true); \
	sqlfail=$$(echo "$$sqlhits" | grep -vE "$$sqlbaseline" || true); \
	gopattern='^\t+[A-Z][A-Za-z0-9]*Calc\b'; \
	godeltapattern='(^|:)\t+[A-Z][A-Za-z0-9]*DeltaCalc\b'; \
	gobaseline='(DistanceTraveledKmCalc|BatteryUsedPctCalc|DaysSpannedCalc|KmPerPctCalc|InferredCapacityKWhCalc|InferredCapacityKwhCalc)\b'; \
	gohits=$$(grep -rnE "$$gopattern" --include='*.go' internal cmd \
		| grep -v '_test.go' \
		| while IFS= read -r l; do head -1 "$${l%%:*}" | grep -q 'Code generated by' || printf '%s\n' "$$l"; done \
		| grep -vE "$$godeltapattern" \
		| grep -v 'delta:allow' || true); \
	gowarn=$$(echo "$$gohits" | grep -E "$$gobaseline" || true); \
	gofail=$$(echo "$$gohits" | grep -vE "$$gobaseline" || true); \
	if [ -n "$$sqlwarn" ] || [ -n "$$gowarn" ]; then \
		[ -n "$$sqlwarn" ] && echo "$$sqlwarn"; \
		[ -n "$$gowarn" ] && echo "$$gowarn"; \
		echo ""; \
		echo "WARNING: the pre-rule _calc/Calc names above are not named as deltas, but"; \
		echo "pre-date the naming rule. Not fatal. Renaming one to *_delta_calc/DeltaCalc,"; \
		echo "or confirming it is a genuine non-delta and marking it delta:allow, is a"; \
		echo "deliberate change; when done, shrink the baseline in the Makefile's"; \
		echo "delta-guard target."; \
		echo ""; \
	fi; \
	if [ -n "$$sqlfail" ] || [ -n "$$gofail" ]; then \
		[ -n "$$sqlfail" ] && echo "$$sqlfail"; \
		[ -n "$$gofail" ] && echo "$$gofail"; \
		echo ""; \
		echo "ERROR: the names above end in a bare _calc/Calc and are not in the"; \
		echo "baseline. If this column or field holds today's value of a metric minus"; \
		echo "yesterday's value of the SAME metric, rename it to end in"; \
		echo "_delta_calc/DeltaCalc instead. See ai/go-conventions.md \"Read optimization"; \
		echo "(project-wide)\". Genuinely not a delta (a rate or ratio, like"; \
		echo "km_per_pct_calc)? Mark the line with -- delta:allow: <reason> (SQL) or"; \
		echo "// delta:allow: <reason> (Go). Never weaken this pattern to silence a true"; \
		echo "positive."; \
		exit 1; \
	else \
		echo "delta-guard: no new bare _calc/Calc name (baseline: $$(echo "$$sqlwarn" | grep -oE "$$sqlbaseline" | sort -u | wc -l | tr -d ' ') SQL / $$(echo "$$gowarn" | grep -oE "$$gobaseline" | sort -u | wc -l | tr -d ' ') Go legacy name(s))"; \
	fi

# archive-guard is the one guard that reads git history instead of the working tree,
# because the rule it enforces is about CHANGE, not about content: everything under
# openspec/changes/archive/ is an immutable snapshot of what was decided at the time.
# Nothing in there is ever edited or deleted — a wrong archived doc is corrected in
# openspec/specs/ (the live spec), never rewritten in place, or the audit trail of what
# was actually proposed is lost.
#
# The guard exists because CLAUDE.md's "Docs track structural change" rule points every
# assistant at every doc a change invalidated, and a grep for a module name hits the
# archive. Without a deterministic signal, an agent "fixes" history in good faith.
#
# Baseline is the merge-base with main, so the guard covers the whole feature branch plus
# the uncommitted working tree (git diff <commit> compares against the working tree). On
# main itself the merge-base IS HEAD, so it covers the uncommitted work only.
#
# Allowed: A (a newly archived change folder) and R100 (a pure move — this repo regroups
# archives under archive/<module>/ after the CLI drops them at the archive root; an exact
# rename keeps the content byte-identical). Fails on M / D / T, and on a move that also
# edits, which git reports as a D+A pair rather than a rename.
#
# Escape hatch: ARCHIVE_GUARD_ALLOW=1 make archive-guard, only for something that is not
# a rewrite of the record (e.g. purging a leaked secret). State the reason in the commit
# message. Never widen the pattern to silence a true positive.
logdir-guard: ## Fail if an unexpected service mounts the named-log folder, or if Caddy's log file mode is not host-readable
	@bad=$$(awk '\
		/^services:/ { in_services=1; next } \
		in_services && /^  [a-zA-Z0-9_-]+:/ { svc=$$1; sub(":", "", svc) } \
		in_services && /:\/var\/log\/magus/ { if (svc != "web" && svc != "poller" && svc != "caddy") print svc }\
	' deploy/docker/compose.yaml | sort -u); \
	if [ -n "$$bad" ]; then \
		echo "$$bad"; \
		echo ""; \
		echo "ERROR: the service(s) above mount the named-log folder (/var/log/magus)."; \
		echo "Only web, poller and caddy may. The folder is owned by ONE host user,"; \
		echo "and every extra container that writes there is another user that must be"; \
		echo "granted access by hand on every host. Send the service's log to Docker's"; \
		echo "driver instead, and read it with docker compose ... logs <service>."; \
		exit 1; \
	fi
	@if grep -q 'output file /var/log/magus/' deploy/docker/Caddyfile; then \
		mode=$$(awk '/output file \/var\/log\/magus\//,/^\t\t}/' deploy/docker/Caddyfile | awk '/^[ \t]*mode[ \t]+[0-7]+/ { print $$2 }'); \
		if [ -z "$$mode" ]; then \
			echo "ERROR: deploy/docker/Caddyfile writes a log file but sets no mode."; \
			echo "Caddy defaults to 0600, so the file would be unreadable by the host"; \
			echo "user even when the folder itself is writable - which is exactly what"; \
			echo "a named log is supposed to avoid. Add: mode 0644"; \
			exit 1; \
		fi; \
		group=$$(echo "$$mode" | sed 's/.*\(.\)\(.\)$$/\1/'); \
		case "$$group" in \
			4|5|6|7) ;; \
			*) echo "ERROR: Caddyfile log mode $$mode is not group-readable."; \
			   echo "The host user reads this file through its group. Use 0644."; \
			   exit 1 ;; \
		esac; \
		echo "logdir-guard: named-log folder mounted only by web/poller/caddy; Caddy log mode $$mode is host-readable"; \
	else \
		echo "logdir-guard: named-log folder mounted only by web/poller/caddy; Caddy writes no log file"; \
	fi

archive-guard: ## Fail if a file under openspec/changes/archive/ is edited or deleted (archives are immutable; escape hatch: ARCHIVE_GUARD_ALLOW=1)
	@if [ -n "$$ARCHIVE_GUARD_ALLOW" ]; then \
		echo "archive-guard: SKIPPED via ARCHIVE_GUARD_ALLOW — state the reason in the commit message"; \
		exit 0; \
	fi
	@base=$$(git merge-base HEAD main 2>/dev/null || git rev-parse HEAD); \
	hits=$$(git diff --name-status -M100% "$$base" -- openspec/changes/archive | grep -E '^(M|D|T)' || true); \
	if [ -n "$$hits" ]; then \
		echo "$$hits"; \
		echo ""; \
		echo "ERROR: the archived files above were edited or deleted (baseline $$base)."; \
		echo "openspec/changes/archive/ is an immutable record of what was decided at the"; \
		echo "time. Adding a newly archived change folder is fine, and so is moving one"; \
		echo "under archive/<module>/ unchanged — rewriting one is not."; \
		echo "Stale or wrong archived doc? Fix the live spec in openspec/specs/ instead."; \
		echo "Restore them with: git checkout $$base -- openspec/changes/archive"; \
		echo "Genuinely not a rewrite of the record (e.g. a leaked secret)? Run"; \
		echo "ARCHIVE_GUARD_ALLOW=1 make archive-guard and say why in the commit message."; \
		exit 1; \
	else \
		echo "archive-guard: no archived file edited or deleted since $$base"; \
	fi

check: build vet lint ui-guard i18n-guard money-guard tz-guard logging-guard migration-boundary-guard boundary-guard theme-guard vehicleref-guard tenancy-guard naming-guard archive-guard logdir-guard delta-guard test ## Full local gate: build + vet + lint + ui-guard + i18n-guard + money-guard + tz-guard + logging-guard + migration-boundary-guard + boundary-guard + theme-guard + vehicleref-guard + tenancy-guard + naming-guard + archive-guard + logdir-guard + delta-guard + test

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

cmd-monthly-capacity: ## Build cmd/monthly-capacity into ./bin and run it. Optional: PERIOD=2026-08 TESLA_ID=123 (defaults: previous month, every vehicle). Needs DATABASE_URL.
	@mkdir -p bin
	go build -o bin/monthly-capacity ./cmd/monthly-capacity
	./bin/monthly-capacity $(if $(PERIOD),-period $(PERIOD)) $(if $(TESLA_ID),-tesla-id $(TESLA_ID))

# --- Docker Compose deploy (VPS / production) --------------------------------
# See kkpa/docs/0-set-up/deployment.md §8 (first deploy) and kkpa/docs/1-deploy/docker.md
# (day-to-day commands) for the full runbook. These targets are thin wrappers
# around `docker compose` — they build/start/stop the whole stack (db, migrate,
# web, poller, caddy) defined in deploy/docker/compose.yaml.
#
# COMPOSE is the ONE place the two required flags live. The compose file is not
# at the repo root, so every command needs both:
#   -f deploy/docker/...   names the moved file.
#   --project-directory .  sets the base path Compose uses to resolve EVERY
#                          relative path in that file — env_file, build.context
#                          and bind mounts alike. Without it the base would be
#                          deploy/docker/, and all three would resolve wrong.
# So compose.yaml keeps the repo-root-relative paths it had before the move.
# deploy/docker/backup-db.sh repeats these same two flags; change both together.
COMPOSE = docker compose --project-directory . -f deploy/docker/compose.yaml

docker-up: ## Build (if needed) and start the whole Docker Compose stack (deploy/docker/compose.yaml) in the background
	$(COMPOSE) up -d --build

docker-down: ## Stop and remove the whole Docker Compose stack (deploy/docker/compose.yaml; keeps named volumes — data survives)
	$(COMPOSE) down

docker-logs: ## Follow logs from every running service in deploy/docker/compose.yaml
	$(COMPOSE) logs -f

vps-logs: ## Tail the named log files under MAGUS_LOGS_DIR (web.log, poller.log, caddy.log) — VPS only
	tail -f $(MAGUS_LOGS_DIR)/*.log

docker-migrate: ## Run the one-shot "migrate" service from deploy/docker/compose.yaml by hand (same program docker-up already runs automatically)
	$(COMPOSE) run --rm migrate

backup-db: ## Dump the compose-local Postgres database, gzip it, and prune backups older than 7 days
	./deploy/docker/backup-db.sh
