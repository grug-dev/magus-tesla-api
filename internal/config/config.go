// Package config handles loading and saving credentials.
// Covers Step 1 of post-registration-setup.md — protect credentials via .env.
package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	AccessToken  string
	RefreshToken string
}

// Load reads the .env file and returns a populated Config.
func Load() (*Config, error) {
	if err := godotenv.Load(); err != nil {
		return nil, fmt.Errorf("step 1: could not load .env file: %w", err)
	}

	cfg := &Config{
		ClientID:     os.Getenv("TESLA_CLIENT_ID"),
		ClientSecret: os.Getenv("TESLA_CLIENT_SECRET"),
		RedirectURI:  "http://localhost:8080/callback",
		AccessToken:  os.Getenv("TESLA_ACCESS_TOKEN"),
		RefreshToken: os.Getenv("TESLA_REFRESH_TOKEN"),
	}

	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, fmt.Errorf("step 1: TESLA_CLIENT_ID and TESLA_CLIENT_SECRET must be set in .env")
	}

	return cfg, nil
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
