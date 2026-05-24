package platform

import (
	"errors"
	"fmt"
	"os"
)

// Config holds all runtime configuration for the skael server.
type Config struct {
	DatabaseURL   string
	StoragePath   string
	ListenAddr    string
	APIKey        string
	DisableSignup bool
}

// LoadConfig reads configuration from environment variables.
// DATABASE_URL is required; returns an error if absent.
// API_KEY is optional but deprecated — user accounts and personal API keys are preferred.
// STORAGE_PATH defaults to "./data/skills"; LISTEN_ADDR defaults to ":8080".
// DISABLE_SIGNUP=true prevents new user registrations.
func LoadConfig() (*Config, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}

	apiKey := os.Getenv("API_KEY")
	if apiKey != "" {
		fmt.Fprintln(os.Stderr, "WARNING: API_KEY is deprecated. Use user accounts and personal API keys instead.")
	}

	return &Config{
		DatabaseURL:   dbURL,
		APIKey:        apiKey,
		StoragePath:   envDefault("STORAGE_PATH", "./data/skills"),
		ListenAddr:    envDefault("LISTEN_ADDR", ":8080"),
		DisableSignup: os.Getenv("DISABLE_SIGNUP") == "true",
	}, nil
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
