package config

import (
	"os"
	"testing"
)

func TestEnvStripped(t *testing.T) {
	cases := map[string]struct {
		set  string
		want string
	}{
		"double-quoted":   {`"eyJfake"`, "eyJfake"},
		"single-quoted":   {`'eyJfake'`, "eyJfake"},
		"unquoted":        {"eyJfake", "eyJfake"},
		"empty":           {"", ""},
		"one-char":        {"x", "x"},
		"two-unmatched-1": {`"x`, `"x`},
		"two-unmatched-2": {`x"`, `x"`},
		"internal-quotes": {`ey"jf`, `ey"jf`},
		"only-quotes":     {`""`, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			os.Setenv("MAGUS_TEST_ENV_STRIPPED", tc.set)
			defer os.Unsetenv("MAGUS_TEST_ENV_STRIPPED")
			got := envStripped("MAGUS_TEST_ENV_STRIPPED")
			if got != tc.want {
				t.Fatalf("envStripped(%q) = %q, want %q", tc.set, got, tc.want)
			}
		})
	}
}

// TestEnvStrippedSimulatesMakeExport reproduces the failure mode: the Makefile does
// `include .env; export`, and make keeps the literal double-quotes that godotenv/bash
// would strip. envStripped must remove them so the Go code sends a clean token.
func TestEnvStrippedSimulatesMakeExport(t *testing.T) {
	// What `make` exports for a .env line: TESLA_ACCESS_TOKEN="eyJ...gQ"
	const makeQuoted = `"eyJfake-token-gQ"`
	os.Setenv("TESLA_ACCESS_TOKEN", makeQuoted)
	defer os.Unsetenv("TESLA_ACCESS_TOKEN")

	got := envStripped("TESLA_ACCESS_TOKEN")
	if got != "eyJfake-token-gQ" {
		t.Fatalf("envStripped left quotes in place: got %q (len %d), want unquoted",
			got, len(got))
	}
	if got[0] == '"' || got[len(got)-1] == '"' {
		t.Fatalf("envStripped did not strip surrounding quotes: %q", got)
	}
}
