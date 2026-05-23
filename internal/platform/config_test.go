package platform_test

import (
	"os"
	"testing"

	"github.com/skael-dev/skael/internal/platform"
)

func TestLoadConfig_RequiresDatabaseURL(t *testing.T) {
	os.Unsetenv("DATABASE_URL")
	os.Setenv("API_KEY", "test-key")
	defer os.Unsetenv("API_KEY")

	_, err := platform.LoadConfig()
	if err == nil {
		t.Fatal("expected error when DATABASE_URL is not set, got nil")
	}
}

func TestLoadConfig_RequiresAPIKey(t *testing.T) {
	os.Setenv("DATABASE_URL", "postgres://localhost/skael")
	os.Unsetenv("API_KEY")
	defer os.Unsetenv("DATABASE_URL")

	_, err := platform.LoadConfig()
	if err == nil {
		t.Fatal("expected error when API_KEY is not set, got nil")
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	os.Setenv("DATABASE_URL", "postgres://localhost/skael")
	os.Setenv("API_KEY", "test-key")
	defer func() {
		os.Unsetenv("DATABASE_URL")
		os.Unsetenv("API_KEY")
		os.Unsetenv("STORAGE_PATH")
		os.Unsetenv("LISTEN_ADDR")
	}()
	os.Unsetenv("STORAGE_PATH")
	os.Unsetenv("LISTEN_ADDR")

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
