package platform

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration for the skael server.
type Config struct {
	DatabaseURL   string
	StoragePath   string
	ListenAddr    string
	DisableSignup bool
	GitHubToken   string

	// EventRetentionDays controls how many days of skill_events to keep.
	// Events older than this are purged on startup. 0 disables cleanup.
	EventRetentionDays int

	// ExternalScanCmd, when set, is an opt-in external security scanner command
	// run over each skill on publish/import (Phase 2). The token "{dir}" is
	// replaced with the skill directory and the command must emit SARIF on
	// stdout, e.g. "gitleaks dir {dir} --report-format sarif --report-path
	// /dev/stdout". Empty disables the feature.
	ExternalScanCmd     string
	ExternalScanTimeout time.Duration

	DBMaxConns          int
	DBMinConns          int
	DBMaxConnLifetime   time.Duration
	DBMaxConnIdleTime   time.Duration
	DBHealthCheckPeriod time.Duration

	CORSOrigins   string
	LogLevel      string
	RateLimitAuth int
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
		DatabaseURL:         dbURL,
		StoragePath:         envDefault("STORAGE_PATH", "./data/skills"),
		ListenAddr:          envDefault("LISTEN_ADDR", ":8080"),
		DisableSignup:       os.Getenv("DISABLE_SIGNUP") == "true",
		GitHubToken:         os.Getenv("GITHUB_TOKEN"),
		EventRetentionDays:  envInt("EVENT_RETENTION_DAYS", 90),
		ExternalScanCmd:     os.Getenv("EXTERNAL_SCAN_CMD"),
		ExternalScanTimeout: envDuration("EXTERNAL_SCAN_TIMEOUT", 60*time.Second),
		DBMaxConns:          envInt("DB_MAX_CONNS", 25),
		DBMinConns:          envInt("DB_MIN_CONNS", 5),
		DBMaxConnLifetime:   envDuration("DB_MAX_CONN_LIFETIME", time.Hour),
		DBMaxConnIdleTime:   envDuration("DB_MAX_CONN_IDLE_TIME", 30*time.Minute),
		DBHealthCheckPeriod: envDuration("DB_HEALTH_CHECK_PERIOD", time.Minute),
		CORSOrigins:         os.Getenv("CORS_ORIGINS"),
		LogLevel:            envDefault("LOG_LEVEL", "info"),
		RateLimitAuth:       envInt("RATE_LIMIT_AUTH", 20),
	}, nil
}

// envDuration parses a Go duration (e.g. "90s", "2m") from key, or returns
// fallback when unset or invalid.
func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

// NewStorageFromConfig builds the Storage backend selected by STORAGE_PATH:
// "s3://bucket/prefix" → S3; anything else → local filesystem.
func NewStorageFromConfig(cfg *Config) (Storage, error) {
	if strings.HasPrefix(cfg.StoragePath, "s3://") {
		return newS3Storage(cfg.StoragePath)
	}
	return NewLocalStorage(cfg.StoragePath)
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

