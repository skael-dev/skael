package platform

import (
	"errors"
	"os"
	"strings"
)

// Config holds all runtime configuration for the skael server.
type Config struct {
	DatabaseURL   string
	StoragePath   string
	ListenAddr    string
	DisableSignup bool
	GitHubToken   string
}

// LoadConfig reads configuration from environment variables.
// DATABASE_URL is required; returns an error if absent.
// STORAGE_PATH defaults to "./data/skills" (or "s3://bucket/prefix" for S3);
// LISTEN_ADDR defaults to ":8080". DISABLE_SIGNUP=true prevents new registrations.
func LoadConfig() (*Config, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}

	return &Config{
		DatabaseURL:   dbURL,
		StoragePath:   envDefault("STORAGE_PATH", "./data/skills"),
		ListenAddr:    envDefault("LISTEN_ADDR", ":8080"),
		DisableSignup: os.Getenv("DISABLE_SIGNUP") == "true",
		GitHubToken:   os.Getenv("GITHUB_TOKEN"),
	}, nil
}

// NewStorageFromConfig builds the Storage backend selected by STORAGE_PATH:
// "s3://bucket/prefix" → S3; anything else → local filesystem.
func NewStorageFromConfig(cfg *Config) (Storage, error) {
	if strings.HasPrefix(cfg.StoragePath, "s3://") {
		return newS3Storage(cfg.StoragePath)
	}
	return NewLocalStorage(cfg.StoragePath)
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
