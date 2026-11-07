package platform

import (
	"strings"
	"time"
)

// Config holds the settings the server needs before it reads its database.
// internal/settings builds it from the environment; every other setting is in
// the settings snapshot.
type Config struct {
	DatabaseURL string
	StoragePath string
	ListenAddr  string

	DBMaxConns          int
	DBMinConns          int
	DBMaxConnLifetime   time.Duration
	DBMaxConnIdleTime   time.Duration
	DBHealthCheckPeriod time.Duration

	CORSOrigins string
	// TrustedProxies is the comma-separated list of proxy addresses and CIDR
	// blocks whose X-Forwarded-For / X-Real-IP headers may be believed. Empty
	// means trust none of them — see ParseTrustedProxies.
	TrustedProxies string
	CookieSecure   bool
	MetricsEnabled bool

	// MasterKeys seals secrets stored from the UI, such as the GitHub App's
	// private key. Empty is valid: features that store a secret stay off.
	MasterKeys *Keyring
}

// NewStorageFromConfig builds the Storage backend selected by STORAGE_PATH:
// "s3://bucket/prefix" → S3; anything else → local filesystem.
func NewStorageFromConfig(cfg *Config) (Storage, error) {
	if strings.HasPrefix(cfg.StoragePath, "s3://") {
		return newS3Storage(cfg.StoragePath)
	}
	return NewLocalStorage(cfg.StoragePath)
}
