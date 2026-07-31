package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFakeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho fake\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestConfigFromEnv_RefusesWithoutAnAPIGatewayKey(t *testing.T) {
	t.Setenv("SKAEL_ENDPOINT", "http://localhost:8080")
	t.Setenv("SKAEL_API_KEY", "k")
	t.Setenv("ANTHROPIC_API_KEY", "")

	_, err := configFromEnv()
	if err == nil {
		t.Fatal("the worker started without an API gateway key")
	}
	if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("the error does not name the missing variable: %v", err)
	}
}

// A subscription gateway is whetstone's affordance, not the worker's: a
// verified score must come from a metered, reproducible backend.
func TestConfigFromEnv_DoesNotFallBackToAnAgentCLI(t *testing.T) {
	t.Setenv("SKAEL_ENDPOINT", "http://localhost:8080")
	t.Setenv("SKAEL_API_KEY", "k")
	t.Setenv("ANTHROPIC_API_KEY", "")
	dir := t.TempDir()
	writeFakeExecutable(t, filepath.Join(dir, "claude"))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if _, err := configFromEnv(); err == nil {
		t.Fatal("the worker accepted a subscription CLI in place of an API key")
	}
}

func TestConfigFromEnv_RequiresAnEndpointAndKey(t *testing.T) {
	t.Setenv("SKAEL_ENDPOINT", "")
	t.Setenv("SKAEL_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	_, err := configFromEnv()
	if err == nil {
		t.Fatal("the worker started without an endpoint or API key")
	}
	if !strings.Contains(err.Error(), "SKAEL_ENDPOINT") {
		t.Fatalf("the error does not name SKAEL_ENDPOINT: %v", err)
	}
	if !strings.Contains(err.Error(), "SKAEL_API_KEY") {
		t.Fatalf("the error does not name SKAEL_API_KEY: %v", err)
	}
}

func TestConfigFromEnv_Defaults(t *testing.T) {
	t.Setenv("SKAEL_ENDPOINT", "http://localhost:8080")
	t.Setenv("SKAEL_API_KEY", "k")
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	cfg, err := configFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Lease != 5*time.Minute || cfg.PollInterval != 15*time.Second {
		t.Fatalf("defaults = %+v", cfg)
	}
	if cfg.WorkerID == "" {
		t.Fatal("worker id defaulted to empty; two anonymous workers cannot be told apart in the lease column")
	}
}
