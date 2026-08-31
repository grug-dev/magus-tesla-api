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
MIGRATIONS_DIRS ?= internal/account/db/migrations internal/telemetry/db/migrations internal/charging/db/migrations internal/analytics/db/migrations

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
        db-setup db-reset env-setup sqlc templ css ui-toolchain ui-bundles generate ui-guard i18n-guard money-guard tz-guard tidy build vet test check bins \
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
		echo "tz-guard: no raw time.Now(), hand-rolled UTC/day-midnight construction, or"; \
		echo "hardcoded IANA zone name found outside internal/clock"; \
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

check: build vet ui-guard i18n-guard money-guard tz-guard test ## Full local gate: build + vet + ui-guard + i18n-guard + money-guard + tz-guard + test

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
