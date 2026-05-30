package platform_test

import (
	"testing"

	"github.com/skael-dev/skael/internal/platform"
)

func TestLoadConfig_RequiresDatabaseURL(t *testing.T) {
	_, err := platform.LoadConfig()
	if err == nil {
		t.Fatal("expected error when DATABASE_URL is not set, got nil")
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/skael")

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
