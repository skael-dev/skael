package platform_test

import (
	"testing"

	"github.com/skael-dev/skael/internal/platform"
)

func TestLoadConfig_RequiresDatabaseURL(t *testing.T) {
	t.Setenv("API_KEY", "test-key")

	_, err := platform.LoadConfig()
	if err == nil {
		t.Fatal("expected error when DATABASE_URL is not set, got nil")
	}
}

func TestLoadConfig_APIKeyOptional(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/skael")
	// API_KEY intentionally not set — should succeed now

	cfg, err := platform.LoadConfig()
	if err != nil {
		t.Fatalf("expected success when API_KEY is not set, got error: %v", err)
	}
	if cfg.APIKey != "" {
		t.Errorf("expected empty APIKey, got %q", cfg.APIKey)
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/skael")
	t.Setenv("API_KEY", "test-key")

	cfg, err := platform.LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("expected ListenAddr %q, got %q", ":8080", cfg.ListenAddr)
	}
	if cfg.StoragePath != "./data/skills" {
		t.Errorf("expected StoragePath %q, got %q", "./data/skills", cfg.StoragePath)
	}
}

func TestLoadConfig_DisableSignupTrue(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/skael")
	t.Setenv("DISABLE_SIGNUP", "true")

	cfg, err := platform.LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.DisableSignup {
		t.Error("expected DisableSignup to be true")
	}
}

func TestLoadConfig_DisableSignupDefault(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/skael")
	// DISABLE_SIGNUP intentionally not set

	cfg, err := platform.LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DisableSignup {
		t.Error("expected DisableSignup to be false by default")
	}
}
