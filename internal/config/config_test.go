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
