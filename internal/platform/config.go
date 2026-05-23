package platform

import (
	"errors"
	"os"
)

// Config holds all runtime configuration for the skael server.
type Config struct {
	DatabaseURL string
	StoragePath string
	ListenAddr  string
	APIKey      string
}

// LoadConfig reads configuration from environment variables.
// DATABASE_URL and API_KEY are required; returns an error if either is absent.
// STORAGE_PATH defaults to "./data/skills"; LISTEN_ADDR defaults to ":8080".
func LoadConfig() (*Config, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}

	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		return nil, errors.New("API_KEY is required")
	}

	return &Config{
		DatabaseURL: dbURL,
		APIKey:      apiKey,
		StoragePath: envDefault("STORAGE_PATH", "./data/skills"),
		ListenAddr:  envDefault("LISTEN_ADDR", ":8080"),
	}, nil
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
