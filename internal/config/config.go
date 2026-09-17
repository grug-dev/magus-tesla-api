// Package config handles loading and saving credentials.
// Covers Step 1 of post-registration-setup.md — protect credentials via .env.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/cristianpena/magus-tesla-api/internal/clock"
)

type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	AccessToken  string
	RefreshToken string
	// DatabaseURL is the Postgres DSN for the account module's store (e.g.
	// postgres://user:pass@localhost:5432/magus). Required for the DB-backed
	// modules (account, web gateway); the one-time OAuth bootstrap (cmd/setup)
	// runs without it.
	DatabaseURL string
	// Port is the TCP port the web gateway (cmd/web) listens on. Defaults to 8080.
	Port string
	// SessionSecret keys the gateway's signed+encrypted session cookie. Must be a
	// strong, stable value — rotating it invalidates all existing sessions.
	SessionSecret string
	// GoogleClientID / GoogleClientSecret are the Google OAuth 2.0 app credentials
	// used for user login (created in the Google Cloud Console).
	GoogleClientID     string
	GoogleClientSecret string
	// BaseURL is the app's public base URL (e.g. https://magus.example.com), used to
	// build the Google OAuth redirect URI. Defaults to http://localhost:8080.
	BaseURL string
	// Poller schedule — cmd/poller runs one telemetry collection cycle per day at
	// this local time (default 03:30). PollerWakeTimeout bounds how long the
	// collector waits for a sleeping vehicle to come online before recording a
	// timeout. These are read only by cmd/poller; cmd/web ignores them.
	//
	// PollerTimezone: an unset POLLER_TIMEZONE defaults to the platform's default
	// zone, internal/clock's "America/Bogota" — not the host's "Local" zone
	// (changed by RM35-config-adopt-clock; see pollerTimezoneOrDefault). A set
	// value, valid or not, passes through untouched; cmd/poller still validates it
	// via time.LoadLocation and fails fast on an invalid IANA name.
	PollerScheduleHour   int
	PollerScheduleMinute int
	PollerTimezone       string
	PollerWakeTimeout    time.Duration
	// PollerRerunToken gates cmd/poller's manual-rerun HTTP listener. An empty
	// value (unset POLLER_RERUN_TOKEN) means the listener does not start at
	// all — no port opens, no route exists (design.md D2 of
	// platform-add-manual-rerun-api, "fail-closed"). This module does not
	// validate its shape; any non-empty string is accepted.
	PollerRerunToken string
}

// GoogleRedirectURL is the exact OAuth redirect URI registered with Google.
func (c *Config) GoogleRedirectURL() string {
	return c.BaseURL + "/auth/google/callback"
}

// TeslaConnectRedirectURL is the exact redirect URI registered with the Tesla app
// for the web "connect your Tesla" flow.
func (c *Config) TeslaConnectRedirectURL() string {
	return c.BaseURL + "/connect/tesla/callback"
}

// loadDotEnv loads the .env file into the process environment, the same way
// for every caller in this package. A missing .env file is not an error: a
// container has no .env and gets its config from real environment variables
// instead. Any other error reading .env (for example, a permission error or
// a malformed file) is still fatal. godotenv.Load() never overrides a
// variable already set in the process environment, so a real environment
// variable always wins over a .env value.
func loadDotEnv() error {
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("could not load .env file: %w", err)
	}
	return nil
}

// Load reads the .env file and returns a populated Config. See loadDotEnv
// for the missing-.env / real-env-wins behavior.
func Load() (*Config, error) {
	if err := loadDotEnv(); err != nil {
		return nil, fmt.Errorf("step 1: %w", err)
	}

	cfg := &Config{
		ClientID:           envStripped("TESLA_CLIENT_ID"),
		ClientSecret:       envStripped("TESLA_CLIENT_SECRET"),
		RedirectURI:        "http://localhost:8080/connect/tesla/callback",
		AccessToken:        envStripped("TESLA_ACCESS_TOKEN"),
		RefreshToken:       envStripped("TESLA_REFRESH_TOKEN"),
		DatabaseURL:        envStripped("DATABASE_URL"),
		Port:               envStripped("PORT"),
		SessionSecret:      envStripped("SESSION_SECRET"),
		GoogleClientID:     envStripped("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: envStripped("GOOGLE_CLIENT_SECRET"),
		BaseURL:            envStripped("BASE_URL"),
	}

	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://localhost:8080"
	}

	cfg.PollerScheduleHour = envInt("POLLER_SCHEDULE_HOUR", 3)
	cfg.PollerScheduleMinute = envInt("POLLER_SCHEDULE_MINUTE", 30)
	cfg.PollerTimezone = pollerTimezoneOrDefault(envStripped("POLLER_TIMEZONE"))
	cfg.PollerWakeTimeout = envDuration("POLLER_WAKE_TIMEOUT", 90*time.Second)
	cfg.PollerRerunToken = envStripped("POLLER_RERUN_TOKEN")

	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, fmt.Errorf("step 1: TESLA_CLIENT_ID and TESLA_CLIENT_SECRET must be set in .env")
	}

	return cfg, nil
}

// defaultMigrationsRoot is where the Docker image COPYs each module's
// migrations (see cmd/migrate and
// openspec/changes/platform-add-docker-compose-deploy/design.md, decision
// D11). Overridable via MIGRATIONS_ROOT.
const defaultMigrationsRoot = "/migrations"

// defaultMigrationModules are the four migration directory names, used under
// MigrationsRoot when MIGRATIONS_DIRS is unset. This mirrors the Makefile's
// MIGRATION_MODULES variable and the image layout the Dockerfile COPYs
// (ai/go-conventions.md §Persistence).
//
// It is also the ONLY list of module names here, on purpose: it names the
// default directories AND resolves a MIGRATIONS_DIRS path back to its module
// (moduleForDir). A second list would be free to drift from this one, and the
// module name decides which version ledger a migration is recorded in — so
// drift would silently record a module's migrations in another module's ledger.
//
// The order is no longer significant. Each module records its versions in its
// own <module>.goose_db_version, and each module's migrations create only that
// module's objects and read nothing, so no module can depend on another having
// run first. The order is kept stable only to make logs comparable between runs.
var defaultMigrationModules = []string{"account", "telemetry", "charging", "analytics"}

// MigrationDir is one module's migration directory, paired with the module that
// owns it. The two always travel together because goose needs both: the
// directory to read the files from, and the module to name the version ledger
// (<module>.goose_db_version) it records them in. Deriving one from the other at
// the point of use is what allowed them to disagree.
type MigrationDir struct {
	// Module is the owning module's name, which is also its Postgres schema
	// and therefore the schema holding its version ledger.
	Module string
	// Dir is the directory holding that module's migration files.
	Dir string
}

// VersionTable is the schema-qualified goose version table for this module.
//
// goose accepts a schema-qualified table name for both the library API
// (goose.WithTableName) and the CLI (its -table flag), and its own README
// documents this exact form for a non-public schema. Putting the ledger inside
// the module's schema means `pg_dump --schema=<module>` carries the module's
// objects and its migration history together, which is what makes a module
// extractable into its own service.
func (m MigrationDir) VersionTable() string {
	return m.Module + ".goose_db_version"
}

// moduleForDir resolves a migration directory path to the module that owns it,
// by finding the path segment that names a known module. It handles both
// layouts with one rule: the image's "<root>/account" and a checkout's
// "internal/account/db/migrations" both contain exactly one segment naming a
// module.
//
// An unresolvable path is an error rather than a guess. A directory whose module
// cannot be named has no ledger to record into, and picking a fallback (the last
// path segment, say) would write "migrations.goose_db_version" or silently reuse
// another module's ledger.
func moduleForDir(dir string) (string, error) {
	var found string
	for _, seg := range strings.Split(filepath.ToSlash(dir), "/") {
		if slices.Contains(defaultMigrationModules, seg) {
			if found != "" && found != seg {
				return "", fmt.Errorf("migration dir %q names two modules (%q and %q); it must name exactly one", dir, found, seg)
			}
			found = seg
		}
	}
	if found == "" {
		return "", fmt.Errorf("migration dir %q does not name any known module (%v); a migration directory must live under the module that owns it", dir, defaultMigrationModules)
	}
	return found, nil
}

// MigrationConfig is the config for the standalone migration tool
// (cmd/migrate). It is a separate, smaller struct from Config because a
// migration-only tool must not require Tesla credentials the way Load does.
type MigrationConfig struct {
	// DatabaseURL is the Postgres DSN to migrate. Required.
	DatabaseURL string
	// MigrationsRoot is the directory holding one subfolder per module's
	// migrations (e.g. "<root>/account", "<root>/telemetry"). Defaults to
	// "/migrations", the path the Docker image COPYs them to. Kept for the
	// default-path case; MigrationsDirs is what cmd/migrate actually loops
	// over.
	MigrationsRoot string
	// MigrationsDirs is the list of migration directories to apply, each
	// paired with the module that owns it. When MIGRATIONS_DIRS is set
	// (space-separated), the directories come from there verbatim, split on
	// whitespace with empty entries dropped, and each one's module is resolved
	// by moduleForDir. When unset, it is MigrationsRoot + "/" + each of
	// defaultMigrationModules — the image-default behavior, unchanged.
	// cmd/migrate loops this slice directly; it never builds a path or a
	// version-table name itself.
	MigrationsDirs []MigrationDir
}

// LoadMigration reads config for cmd/migrate. Unlike Load, it does not
// require TESLA_CLIENT_ID/TESLA_CLIENT_SECRET — a migration-only tool has no
// reason to validate a credential it never uses. It loads .env the same way
// Load does (see loadDotEnv: a missing .env is not an error), then reads
// DATABASE_URL (required), MIGRATIONS_ROOT (defaults to defaultMigrationsRoot
// when unset), and MIGRATIONS_DIRS.
//
// MIGRATIONS_DIRS is an optional, space-separated, ORDERED list of migration
// directories (e.g. "internal/account/db/migrations
// internal/telemetry/db/migrations ..."), used to run cmd/migrate against a
// local checkout — the Docker image layout (MigrationsRoot + a fixed module
// name) only exists inside the built image, not in the repo. When
// MIGRATIONS_DIRS is unset, MigrationsDirs falls back to the four
// "<root>/<module>" paths, in the same order, so the image's compose.yaml
// and Dockerfile need no change.
func LoadMigration() (*MigrationConfig, error) {
	if err := loadDotEnv(); err != nil {
		return nil, err
	}

	dbURL := envStripped("DATABASE_URL")
	if dbURL == "" {
		return nil, fmt.Errorf("DATABASE_URL must be set")
	}

	root := envStripped("MIGRATIONS_ROOT")
	if root == "" {
		root = defaultMigrationsRoot
	}

	var dirs []MigrationDir
	if paths := splitMigrationsDirs(envStripped("MIGRATIONS_DIRS")); len(paths) > 0 {
		dirs = make([]MigrationDir, len(paths))
		for i, p := range paths {
			module, err := moduleForDir(p)
			if err != nil {
				return nil, fmt.Errorf("MIGRATIONS_DIRS: %w", err)
			}
			dirs[i] = MigrationDir{Module: module, Dir: p}
		}
	} else {
		dirs = make([]MigrationDir, len(defaultMigrationModules))
		for i, name := range defaultMigrationModules {
			dirs[i] = MigrationDir{Module: name, Dir: root + "/" + name}
		}
	}

	return &MigrationConfig{
		DatabaseURL:    dbURL,
		MigrationsRoot: root,
		MigrationsDirs: dirs,
	}, nil
}

// LoadDatabase reads config for a database-only tool (cmd/monthly-capacity). Unlike Load,
// it does not require any Tesla credential -- a tool that never calls the Fleet API has no
// reason to fail over one. It loads .env the same way Load and LoadMigration do (a missing
// .env is not an error), then reads DATABASE_URL, erroring when it is empty. Nothing else.
func LoadDatabase() (string, error) {
	if err := loadDotEnv(); err != nil {
		return "", err
	}
	dbURL := envStripped("DATABASE_URL")
	if dbURL == "" {
		return "", fmt.Errorf("DATABASE_URL must be set")
	}
	return dbURL, nil
}

// splitMigrationsDirs splits a space-separated MIGRATIONS_DIRS value into an
// ordered slice, dropping empty entries so extra whitespace (including a
// trailing space) never produces an empty directory path. Returns nil for an
// empty input, which LoadMigration reads as "unset — use the default".
func splitMigrationsDirs(v string) []string {
	fields := strings.Fields(v)
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// pollerTimezoneOrDefault returns v unchanged when it is non-empty (a set
// POLLER_TIMEZONE, valid or not — validation stays cmd/poller's job via
// time.LoadLocation, unchanged by RM35). When v is empty (POLLER_TIMEZONE unset),
// it returns the platform's default zone name, obtained from internal/clock rather
// than hard-coded here (RM35 D1/D2 — internal/clock is the sole owner of the
// default zone name, "America/Bogota").
//
// Deliberately NOT implemented as clock.LoadOrDefault(v): that stdlib-inherited
// helper treats "" as a request to resolve, and time.LoadLocation("") resolves to
// UTC with no error — so routing an unset POLLER_TIMEZONE through LoadOrDefault
// would silently produce UTC, exactly the outcome RM35 exists to prevent (tier 1
// design D10). Substituting the default before any resolution keeps that trap
// closed.
func pollerTimezoneOrDefault(v string) string {
	if v != "" {
		return v
	}
	return clock.Zone().String()
}

// envStripped reads an env var and removes a single pair of surrounding matching
// quotes (" or ') if present. The Makefile does `include .env; export`, and make's
// include does NOT strip quotes the way godotenv/bash do — so a .env line like
// TESLA_ACCESS_TOKEN="eyJ..." reaches the Go process as "eyJ..." (with literal quote
// chars) when run via `make`. godotenv.Load is non-overriding, so it cannot replace an
// already-exported (quoted) value. Stripping here makes config.Load tolerant of quoted
// exports regardless of how the var was set (make export, shell, or godotenv itself,
// which already strips — no-op in that case). A real `make VAR=x` override has no
// quotes, so override semantics are unchanged.
func envStripped(key string) string {
	v := os.Getenv(key)
	if len(v) < 2 {
		return v
	}
	first, last := v[0], v[len(v)-1]
	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}

// envInt reads an integer env var, falling back to def when unset or unparseable.
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// envDuration reads a Go duration env var (e.g. "90s"), falling back to def.
func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// SaveTokens persists the access and refresh tokens back into the .env file.
// Covers Step 6c — store tokens securely, never in git.
func SaveTokens(accessToken, refreshToken string) error {
	env, err := godotenv.Read(".env")
	if err != nil {
		return fmt.Errorf("step 6c: could not read .env: %w", err)
	}

	env["TESLA_ACCESS_TOKEN"] = accessToken
	env["TESLA_REFRESH_TOKEN"] = refreshToken

	if err := godotenv.Write(env, ".env"); err != nil {
		return fmt.Errorf("step 6c: could not write .env: %w", err)
	}

	return nil
}
