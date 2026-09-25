package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoad_EnvFile covers the four cases from design.md's test contract for the
// platform-add-docker-compose-deploy change. A missing .env file must not be a
// fatal error (a container has no .env — it gets real environment variables), but
// a present .env must still load, and a real environment variable must always win
// over a .env value.
//
// Each subtest saves and restores the working directory and every env var it
// touches, in defer, so the four cases here and the two pre-existing test files
// in this package (quote_test.go, timezone_test.go) never leak state between
// each other — go test runs a package's tests in one process.

// withTempDir switches into a fresh temp dir for the duration of fn, then
// restores the original working directory.
func withTempDir(t *testing.T, fn func(dir string)) {
	t.Helper()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("os.Chdir(%q): %v", dir, err)
	}
	defer func() {
		if err := os.Chdir(origDir); err != nil {
			t.Fatalf("restore os.Chdir(%q): %v", origDir, err)
		}
	}()
	fn(dir)
}

// unsetClientEnv clears TESLA_CLIENT_ID and TESLA_CLIENT_SECRET from the real
// environment and returns a restore func for defer.
func unsetClientEnv(t *testing.T) func() {
	t.Helper()
	origID, hadID := os.LookupEnv("TESLA_CLIENT_ID")
	origSecret, hadSecret := os.LookupEnv("TESLA_CLIENT_SECRET")
	os.Unsetenv("TESLA_CLIENT_ID")
	os.Unsetenv("TESLA_CLIENT_SECRET")
	return func() {
		if hadID {
			os.Setenv("TESLA_CLIENT_ID", origID)
		} else {
			os.Unsetenv("TESLA_CLIENT_ID")
		}
		if hadSecret {
			os.Setenv("TESLA_CLIENT_SECRET", origSecret)
		} else {
			os.Unsetenv("TESLA_CLIENT_SECRET")
		}
	}
}

func writeEnvFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(dir+"/.env", []byte(content), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
}

// Test 1 — .env present with values: Load() succeeds and fields come from .env.
func TestLoad_EnvFilePresentWithValues(t *testing.T) {
	restore := unsetClientEnv(t)
	defer restore()

	withTempDir(t, func(dir string) {
		writeEnvFile(t, dir, "TESLA_CLIENT_ID=fromdotenv\nTESLA_CLIENT_SECRET=fromdotenv\n")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}
		if cfg.ClientID != "fromdotenv" {
			t.Fatalf("cfg.ClientID = %q, want %q", cfg.ClientID, "fromdotenv")
		}
		if cfg.ClientSecret != "fromdotenv" {
			t.Fatalf("cfg.ClientSecret = %q, want %q", cfg.ClientSecret, "fromdotenv")
		}
	})
}

// Test 2 — .env absent, real env vars set: Load() succeeds, fields come from the
// environment. This is the exact regression this change fixes: today this case
// returns "step 1: could not load .env file".
func TestLoad_EnvFileAbsentRealEnvSet(t *testing.T) {
	restore := unsetClientEnv(t)
	defer restore()

	withTempDir(t, func(dir string) {
		// No .env file written in dir on purpose.
		os.Setenv("TESLA_CLIENT_ID", "fromenv")
		os.Setenv("TESLA_CLIENT_SECRET", "fromenv")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() returned error: %v, want no error (missing .env must not be fatal)", err)
		}
		if cfg.ClientID != "fromenv" {
			t.Fatalf("cfg.ClientID = %q, want %q", cfg.ClientID, "fromenv")
		}
	})
}

// Test 3 — .env absent, required vars also missing: Load() still fails, with the
// existing validation message. Proves the missing-file fix does not swallow a
// real validation error.
func TestLoad_EnvFileAbsentRequiredVarsMissing(t *testing.T) {
	restore := unsetClientEnv(t)
	defer restore()

	withTempDir(t, func(dir string) {
		// No .env file, and TESLA_CLIENT_ID/TESLA_CLIENT_SECRET stay unset.
		_, err := Load()
		if err == nil {
			t.Fatal("Load() returned no error, want the missing-required-vars error")
		}
		const want = "step 1: TESLA_CLIENT_ID and TESLA_CLIENT_SECRET must be set in .env"
		if err.Error() != want {
			t.Fatalf("Load() error = %q, want %q", err.Error(), want)
		}
	})
}

// Test 4 — a real env var beats a .env value: godotenv.Load() never overrides a
// variable already present in the process environment. This is existing,
// unchanged behavior — this test is a regression guard for it, since it now
// matters far more (every container env var must survive untouched).
func TestLoad_RealEnvBeatsDotEnv(t *testing.T) {
	restore := unsetClientEnv(t)
	defer restore()

	withTempDir(t, func(dir string) {
		writeEnvFile(t, dir, "TESLA_CLIENT_ID=fromdotenv\nTESLA_CLIENT_SECRET=fromdotenv\n")
		os.Setenv("TESLA_CLIENT_ID", "fromenv")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}
		if cfg.ClientID != "fromenv" {
			t.Fatalf("cfg.ClientID = %q, want %q (real env var must win over .env)", cfg.ClientID, "fromenv")
		}
	})
}

// TestLoadMigration_* cover LoadMigration, the config loader for cmd/migrate
// (platform-add-docker-compose-deploy, T7). Unlike Load, it must never
// require TESLA_CLIENT_ID/TESLA_CLIENT_SECRET — a migration-only tool has no
// reason to validate a credential it never uses.

// unsetMigrationEnv clears DATABASE_URL and MIGRATIONS_ROOT from the real
// environment and returns a restore func for defer.
func unsetMigrationEnv(t *testing.T) func() {
	t.Helper()
	origURL, hadURL := os.LookupEnv("DATABASE_URL")
	origRoot, hadRoot := os.LookupEnv("MIGRATIONS_ROOT")
	os.Unsetenv("DATABASE_URL")
	os.Unsetenv("MIGRATIONS_ROOT")
	return func() {
		if hadURL {
			os.Setenv("DATABASE_URL", origURL)
		} else {
			os.Unsetenv("DATABASE_URL")
		}
		if hadRoot {
			os.Setenv("MIGRATIONS_ROOT", origRoot)
		} else {
			os.Unsetenv("MIGRATIONS_ROOT")
		}
	}
}

// .env missing, DATABASE_URL set in the real environment (the container
// case): LoadMigration must succeed with no Tesla credential requirement,
// and MigrationsRoot must default to "/migrations".
func TestLoadMigration_EnvFileAbsentRealEnvSet(t *testing.T) {
	restore := unsetMigrationEnv(t)
	defer restore()

	withTempDir(t, func(dir string) {
		// No .env file written in dir on purpose.
		os.Setenv("DATABASE_URL", "postgres://user:pass@db:5432/magus?sslmode=disable")

		cfg, err := LoadMigration()
		if err != nil {
			t.Fatalf("LoadMigration() returned error: %v", err)
		}
		if cfg.DatabaseURL != "postgres://user:pass@db:5432/magus?sslmode=disable" {
			t.Fatalf("cfg.DatabaseURL = %q, want the set value", cfg.DatabaseURL)
		}
		if cfg.MigrationsRoot != "/migrations" {
			t.Fatalf("cfg.MigrationsRoot = %q, want default %q", cfg.MigrationsRoot, "/migrations")
		}
	})
}

// DATABASE_URL empty (and unset): LoadMigration must return a clear error.
func TestLoadMigration_DatabaseURLEmpty(t *testing.T) {
	restore := unsetMigrationEnv(t)
	defer restore()

	withTempDir(t, func(dir string) {
		// No .env file, DATABASE_URL left unset.
		_, err := LoadMigration()
		if err == nil {
			t.Fatal("LoadMigration() returned no error, want a DATABASE_URL error")
		}
	})
}

// MIGRATIONS_ROOT set: LoadMigration must use it instead of the default.
func TestLoadMigration_MigrationsRootSet(t *testing.T) {
	restore := unsetMigrationEnv(t)
	defer restore()

	withTempDir(t, func(dir string) {
		os.Setenv("DATABASE_URL", "postgres://user:pass@db:5432/magus?sslmode=disable")
		os.Setenv("MIGRATIONS_ROOT", "/custom/migrations")

		cfg, err := LoadMigration()
		if err != nil {
			t.Fatalf("LoadMigration() returned error: %v", err)
		}
		if cfg.MigrationsRoot != "/custom/migrations" {
			t.Fatalf("cfg.MigrationsRoot = %q, want %q", cfg.MigrationsRoot, "/custom/migrations")
		}
	})
}

// TestLoadMigration_MigrationsDirs* cover MigrationsDirs, the directory slice
// cmd/migrate loops directly, each entry paired with the module that owns it.
// unsetMigrationEnv already
// clears DATABASE_URL/MIGRATIONS_ROOT; these tests also clear/restore
// MIGRATIONS_DIRS themselves, since unsetMigrationEnv predates this variable.

// unsetMigrationsDirsEnv clears MIGRATIONS_DIRS from the real environment and
// returns a restore func for defer.
func unsetMigrationsDirsEnv(t *testing.T) func() {
	t.Helper()
	orig, had := os.LookupEnv("MIGRATIONS_DIRS")
	os.Unsetenv("MIGRATIONS_DIRS")
	return func() {
		if had {
			os.Setenv("MIGRATIONS_DIRS", orig)
		} else {
			os.Unsetenv("MIGRATIONS_DIRS")
		}
	}
}

// MIGRATIONS_DIRS set to two paths: MigrationsDirs must be exactly those two,
// in the given order — not the four-module default.
func TestLoadMigration_MigrationsDirsSet(t *testing.T) {
	restore := unsetMigrationEnv(t)
	defer restore()
	restoreDirs := unsetMigrationsDirsEnv(t)
	defer restoreDirs()

	withTempDir(t, func(dir string) {
		os.Setenv("DATABASE_URL", "postgres://user:pass@db:5432/magus?sslmode=disable")
		os.Setenv("MIGRATIONS_DIRS", "internal/account/db/migrations internal/telemetry/db/migrations")

		cfg, err := LoadMigration()
		if err != nil {
			t.Fatalf("LoadMigration() returned error: %v", err)
		}
		want := []MigrationDir{
			{Module: "account", Dir: "internal/account/db/migrations"},
			{Module: "telemetry", Dir: "internal/telemetry/db/migrations"},
		}
		if len(cfg.MigrationsDirs) != len(want) {
			t.Fatalf("cfg.MigrationsDirs = %v, want %v", cfg.MigrationsDirs, want)
		}
		for i := range want {
			if cfg.MigrationsDirs[i] != want[i] {
				t.Fatalf("cfg.MigrationsDirs[%d] = %+v, want %+v", i, cfg.MigrationsDirs[i], want[i])
			}
		}
	})
}

// MIGRATIONS_DIRS unset: MigrationsDirs must fall back to the four
// "<root>/<module>" default paths, in account, telemetry, charging, analytics
// order — the image-default behavior, unchanged.
func TestLoadMigration_MigrationsDirsUnsetUsesDefault(t *testing.T) {
	restore := unsetMigrationEnv(t)
	defer restore()
	restoreDirs := unsetMigrationsDirsEnv(t)
	defer restoreDirs()

	withTempDir(t, func(dir string) {
		os.Setenv("DATABASE_URL", "postgres://user:pass@db:5432/magus?sslmode=disable")
		// MIGRATIONS_DIRS and MIGRATIONS_ROOT both left unset.

		cfg, err := LoadMigration()
		if err != nil {
			t.Fatalf("LoadMigration() returned error: %v", err)
		}
		want := []MigrationDir{
			{Module: "account", Dir: "/migrations/account"},
			{Module: "telemetry", Dir: "/migrations/telemetry"},
			{Module: "charging", Dir: "/migrations/charging"},
			{Module: "analytics", Dir: "/migrations/analytics"},
			{Module: "reference", Dir: "/migrations/reference"},
		}
		if len(cfg.MigrationsDirs) != len(want) {
			t.Fatalf("cfg.MigrationsDirs = %v, want %v", cfg.MigrationsDirs, want)
		}
		for i := range want {
			if cfg.MigrationsDirs[i] != want[i] {
				t.Fatalf("cfg.MigrationsDirs[%d] = %+v, want %+v", i, cfg.MigrationsDirs[i], want[i])
			}
		}
	})
}

// TestLoadDatabase_* cover LoadDatabase, the config loader for
// cmd/monthly-capacity. Unlike Load, it must never require
// TESLA_CLIENT_ID/TESLA_CLIENT_SECRET -- a database-only tool has no reason
// to validate a credential it never uses.

// unsetDatabaseEnv clears DATABASE_URL from the real environment and returns
// a restore func for defer. Mirrors unsetMigrationEnv's exact shape.
func unsetDatabaseEnv(t *testing.T) func() {
	t.Helper()
	origURL, hadURL := os.LookupEnv("DATABASE_URL")
	os.Unsetenv("DATABASE_URL")
	return func() {
		if hadURL {
			os.Setenv("DATABASE_URL", origURL)
		} else {
			os.Unsetenv("DATABASE_URL")
		}
	}
}

// .env present with DATABASE_URL set, no Tesla credentials set anywhere:
// LoadDatabase must succeed and return the DSN, with no credential check.
func TestLoadDatabase_EnvFilePresent(t *testing.T) {
	restore := unsetDatabaseEnv(t)
	defer restore()

	withTempDir(t, func(dir string) {
		writeEnvFile(t, dir, "DATABASE_URL=postgres://user:pass@db:5432/magus?sslmode=disable\n")

		dbURL, err := LoadDatabase()
		if err != nil {
			t.Fatalf("LoadDatabase() returned error: %v", err)
		}
		want := "postgres://user:pass@db:5432/magus?sslmode=disable"
		if dbURL != want {
			t.Fatalf("LoadDatabase() = %q, want %q", dbURL, want)
		}
	})
}

// .env missing, DATABASE_URL set in the real environment (the container
// case): LoadDatabase must still succeed.
func TestLoadDatabase_EnvFileAbsentRealEnvSet(t *testing.T) {
	restore := unsetDatabaseEnv(t)
	defer restore()

	withTempDir(t, func(dir string) {
		// No .env file written in dir on purpose.
		os.Setenv("DATABASE_URL", "postgres://user:pass@db:5432/magus?sslmode=disable")

		dbURL, err := LoadDatabase()
		if err != nil {
			t.Fatalf("LoadDatabase() returned error: %v", err)
		}
		want := "postgres://user:pass@db:5432/magus?sslmode=disable"
		if dbURL != want {
			t.Fatalf("LoadDatabase() = %q, want %q", dbURL, want)
		}
	})
}

// DATABASE_URL unset everywhere: LoadDatabase must return an error.
func TestLoadDatabase_DatabaseURLEmpty(t *testing.T) {
	restore := unsetDatabaseEnv(t)
	defer restore()

	withTempDir(t, func(dir string) {
		// No .env file, DATABASE_URL left unset.
		dbURL, err := LoadDatabase()
		if err == nil {
			t.Fatal("LoadDatabase() returned no error, want a DATABASE_URL error")
		}
		if dbURL != "" {
			t.Fatalf("LoadDatabase() dbURL = %q, want empty string on error", dbURL)
		}
	})
}

// .env sets DATABASE_URL, but the real environment sets a different value:
// the real environment must win, matching godotenv.Load()'s non-overriding
// behavior.
func TestLoadDatabase_RealEnvBeatsDotEnv(t *testing.T) {
	restore := unsetDatabaseEnv(t)
	defer restore()

	withTempDir(t, func(dir string) {
		writeEnvFile(t, dir, "DATABASE_URL=postgres://from-dotenv:5432/magus\n")
		os.Setenv("DATABASE_URL", "postgres://from-real-env:5432/magus")

		dbURL, err := LoadDatabase()
		if err != nil {
			t.Fatalf("LoadDatabase() returned error: %v", err)
		}
		want := "postgres://from-real-env:5432/magus"
		if dbURL != want {
			t.Fatalf("LoadDatabase() = %q, want %q (real env var must win over .env)", dbURL, want)
		}
	})
}

// MIGRATIONS_DIRS with extra whitespace between (and around) entries must not
// produce an empty directory entry.
func TestLoadMigration_MigrationsDirsExtraWhitespace(t *testing.T) {
	restore := unsetMigrationEnv(t)
	defer restore()
	restoreDirs := unsetMigrationsDirsEnv(t)
	defer restoreDirs()

	withTempDir(t, func(dir string) {
		os.Setenv("DATABASE_URL", "postgres://user:pass@db:5432/magus?sslmode=disable")
		os.Setenv("MIGRATIONS_DIRS", "  internal/account/db/migrations    internal/telemetry/db/migrations  ")

		cfg, err := LoadMigration()
		if err != nil {
			t.Fatalf("LoadMigration() returned error: %v", err)
		}
		want := []MigrationDir{
			{Module: "account", Dir: "internal/account/db/migrations"},
			{Module: "telemetry", Dir: "internal/telemetry/db/migrations"},
		}
		if len(cfg.MigrationsDirs) != len(want) {
			t.Fatalf("cfg.MigrationsDirs = %v, want %v (no empty entries from extra whitespace)", cfg.MigrationsDirs, want)
		}
		for _, d := range cfg.MigrationsDirs {
			if d.Dir == "" {
				t.Fatalf("cfg.MigrationsDirs contains an empty entry: %v", cfg.MigrationsDirs)
			}
		}
	})
}

// TestMigrationDir_BaselineVersion* and TestMigrationDir_StampBaselineSQL* cover the
// one-time stamp that lets a pre-squash database (dev, prod) skip a baseline whose
// tables it already has. See MigrationDir.StampBaselineSQL for the full reasoning.

// A directory with a baseline and a later migration: BaselineVersion must return the
// LOWER number. Returning the higher one would record the later migration as applied
// too, and it would then never run.
func TestMigrationDir_BaselineVersionPicksLowest(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"20260917000002_drop_column.sql", "20260917000001_baseline.sql"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("-- +goose Up\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	m := MigrationDir{Module: "telemetry", Dir: dir}
	got, err := m.BaselineVersion()
	if err != nil {
		t.Fatalf("BaselineVersion() returned error: %v", err)
	}
	if want := int64(20260917000001); got != want {
		t.Fatalf("BaselineVersion() = %d, want %d", got, want)
	}
}

// Files that are not numbered .sql migrations must be ignored, not parsed. goose
// skips them too, so a README or a .go helper in the folder cannot change the answer.
func TestMigrationDir_BaselineVersionIgnoresNonMigrations(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"README.md":                   "notes",
		"helper.go":                   "package db",
		"no_number_here.sql":          "-- +goose Up",
		"20260917000001_baseline.sql": "-- +goose Up",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	m := MigrationDir{Module: "account", Dir: dir}
	got, err := m.BaselineVersion()
	if err != nil {
		t.Fatalf("BaselineVersion() returned error: %v", err)
	}
	if want := int64(20260917000001); got != want {
		t.Fatalf("BaselineVersion() = %d, want %d", got, want)
	}
}

// An empty directory has no baseline to record. That must be an error rather than
// version 0, which would stamp a ledger with a number no file corresponds to.
func TestMigrationDir_BaselineVersionEmptyDirErrors(t *testing.T) {
	m := MigrationDir{Module: "charging", Dir: t.TempDir()}
	if _, err := m.BaselineVersion(); err == nil {
		t.Fatal("BaselineVersion() returned no error for a directory with no migrations")
	}
}

// A missing directory must error rather than silently report no baseline.
func TestMigrationDir_BaselineVersionMissingDirErrors(t *testing.T) {
	m := MigrationDir{Module: "analytics", Dir: filepath.Join(t.TempDir(), "does-not-exist")}
	if _, err := m.BaselineVersion(); err == nil {
		t.Fatal("BaselineVersion() returned no error for a missing directory")
	}
}

// The real baselines on disk: every module's directory must resolve to a version.
// This is the test that fails if a module's baseline is renamed to something goose
// (and therefore BaselineVersion) cannot read.
func TestMigrationDir_BaselineVersionRealDirs(t *testing.T) {
	for _, module := range defaultMigrationModules {
		m := MigrationDir{Module: module, Dir: filepath.Join("..", module, "db", "migrations")}
		got, err := m.BaselineVersion()
		if err != nil {
			t.Fatalf("%s: BaselineVersion() returned error: %v", module, err)
		}
		if got <= 0 {
			t.Fatalf("%s: BaselineVersion() = %d, want a positive version", module, got)
		}
	}
}

// StampBaselineSQL must return exactly two statements, because the pgx driver runs
// the extended protocol and rejects several statements in one Exec.
func TestMigrationDir_StampBaselineSQLReturnsTwoStatements(t *testing.T) {
	m := MigrationDir{Module: "account", Dir: "internal/account/db/migrations"}
	got := m.StampBaselineSQL(20260917000001)
	if len(got) != 2 {
		t.Fatalf("StampBaselineSQL() returned %d statements, want 2", len(got))
	}
	for i, q := range got {
		if strings.Contains(strings.TrimSuffix(strings.TrimSpace(q), ";"), ";") {
			t.Fatalf("statement %d contains an inner semicolon, which pgx rejects:\n%s", i, q)
		}
	}
}

// The statements must name the module's own schema and the version they were given.
// A stamp written into the wrong schema would leave the real ledger empty and the
// baseline would still run.
func TestMigrationDir_StampBaselineSQLNamesSchemaAndVersion(t *testing.T) {
	m := MigrationDir{Module: "telemetry", Dir: "internal/telemetry/db/migrations"}
	got := strings.Join(m.StampBaselineSQL(20260917000001), "\n")

	for _, want := range []string{
		`"telemetry".goose_db_version`,
		`schemaname = 'telemetry'`,
		`20260917000001`,
		`tablename <> 'goose_db_version'`,
		// The guard must ignore goose's version-0 marker, or a ledger left behind by a
		// failed first run can never be stamped. See StampBaselineSQL.
		`version_id > 0`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("StampBaselineSQL() is missing %q:\n%s", want, got)
		}
	}
}

// The ledger DDL must match goose's own postgres dialect exactly (its
// internal/dialects/postgres.go CreateTable). If the two ever differ, a stamped
// database gets a ledger shaped differently from one goose created, and the
// difference shows up as a schema diff nobody expects.
func TestMigrationDir_StampBaselineSQLMatchesGooseDDL(t *testing.T) {
	m := MigrationDir{Module: "account", Dir: "internal/account/db/migrations"}
	create := m.StampBaselineSQL(1)[0]

	for _, want := range []string{
		"id         integer PRIMARY KEY GENERATED BY DEFAULT AS IDENTITY",
		"version_id bigint  NOT NULL",
		"is_applied boolean NOT NULL",
		"tstamp     timestamp NOT NULL DEFAULT now()",
	} {
		if !strings.Contains(create, want) {
			t.Fatalf("CREATE TABLE is missing %q:\n%s", want, create)
		}
	}
	// IF NOT EXISTS is what makes running this before goose safe on every database.
	if !strings.Contains(create, "CREATE TABLE IF NOT EXISTS") {
		t.Fatalf("CREATE TABLE is not guarded with IF NOT EXISTS:\n%s", create)
	}
}

// NeedsBaselineStampSQL must ask about the module's OWN tables and exclude the ledger.
// Including the ledger would make an already-stamped database look like it still needs a
// stamp; asking about the schema instead of its tables would be true on a fresh database,
// which is the bug this probe exists to prevent.
func TestMigrationDir_NeedsBaselineStampSQL(t *testing.T) {
	m := MigrationDir{Module: "charging", Dir: "internal/charging/db/migrations"}
	got := m.NeedsBaselineStampSQL()

	for _, want := range []string{
		"pg_tables",
		`schemaname = 'charging'`,
		`tablename <> 'goose_db_version'`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("NeedsBaselineStampSQL() is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "pg_namespace") {
		t.Fatalf("NeedsBaselineStampSQL() asks about the schema, not its tables:\n%s", got)
	}
}
