package config

import (
	"os"
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

// TestLoadMigration_MigrationsDirs* cover MigrationsDirs, the ordered
// directory slice cmd/migrate loops directly (T8). unsetMigrationEnv already
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
		want := []string{"internal/account/db/migrations", "internal/telemetry/db/migrations"}
		if len(cfg.MigrationsDirs) != len(want) {
			t.Fatalf("cfg.MigrationsDirs = %v, want %v", cfg.MigrationsDirs, want)
		}
		for i := range want {
			if cfg.MigrationsDirs[i] != want[i] {
				t.Fatalf("cfg.MigrationsDirs[%d] = %q, want %q", i, cfg.MigrationsDirs[i], want[i])
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
		want := []string{
			"/migrations/account",
			"/migrations/telemetry",
			"/migrations/charging",
			"/migrations/analytics",
		}
		if len(cfg.MigrationsDirs) != len(want) {
			t.Fatalf("cfg.MigrationsDirs = %v, want %v", cfg.MigrationsDirs, want)
		}
		for i := range want {
			if cfg.MigrationsDirs[i] != want[i] {
				t.Fatalf("cfg.MigrationsDirs[%d] = %q, want %q", i, cfg.MigrationsDirs[i], want[i])
			}
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
		want := []string{"internal/account/db/migrations", "internal/telemetry/db/migrations"}
		if len(cfg.MigrationsDirs) != len(want) {
			t.Fatalf("cfg.MigrationsDirs = %v, want %v (no empty entries from extra whitespace)", cfg.MigrationsDirs, want)
		}
		for _, d := range cfg.MigrationsDirs {
			if d == "" {
				t.Fatalf("cfg.MigrationsDirs contains an empty entry: %v", cfg.MigrationsDirs)
			}
		}
	})
}
