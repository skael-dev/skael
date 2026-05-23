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

func TestLoadConfig_RequiresAPIKey(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/skael")

	_, err := platform.LoadConfig()
	if err == nil {
		t.Fatal("expected error when API_KEY is not set, got nil")
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
