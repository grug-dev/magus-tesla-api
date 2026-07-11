// Package config handles loading and saving credentials.
// Covers Step 1 of post-registration-setup.md — protect credentials via .env.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
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
	PollerScheduleHour   int
	PollerScheduleMinute int
	PollerTimezone       string
	PollerWakeTimeout    time.Duration
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

// Load reads the .env file and returns a populated Config.
func Load() (*Config, error) {
	if err := godotenv.Load(); err != nil {
		return nil, fmt.Errorf("step 1: could not load .env file: %w", err)
	}

	cfg := &Config{
		ClientID:           os.Getenv("TESLA_CLIENT_ID"),
		ClientSecret:       os.Getenv("TESLA_CLIENT_SECRET"),
		RedirectURI:        "http://localhost:8080/connect/tesla/callback",
		AccessToken:        os.Getenv("TESLA_ACCESS_TOKEN"),
		RefreshToken:       os.Getenv("TESLA_REFRESH_TOKEN"),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		Port:               os.Getenv("PORT"),
		SessionSecret:      os.Getenv("SESSION_SECRET"),
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		BaseURL:            os.Getenv("BASE_URL"),
	}

	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://localhost:8080"
	}

	cfg.PollerScheduleHour = envInt("POLLER_SCHEDULE_HOUR", 3)
	cfg.PollerScheduleMinute = envInt("POLLER_SCHEDULE_MINUTE", 30)
	cfg.PollerTimezone = os.Getenv("POLLER_TIMEZONE")
	if cfg.PollerTimezone == "" {
		cfg.PollerTimezone = "Local"
	}
	cfg.PollerWakeTimeout = envDuration("POLLER_WAKE_TIMEOUT", 90*time.Second)

	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, fmt.Errorf("step 1: TESLA_CLIENT_ID and TESLA_CLIENT_SECRET must be set in .env")
	}

	return cfg, nil
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
