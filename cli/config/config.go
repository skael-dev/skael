package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config holds the CLI configuration stored in config.json.
type Config struct {
	Endpoint string `json:"endpoint"`
	APIKey   string `json:"api_key"`
	Scope    string `json:"scope,omitempty"`
}

// SyncState records the last sync timestamp and each synced skill.
type SyncState struct {
	LastSync string        `json:"last_sync"`
	Skills   []SyncedSkill `json:"skills"`
}

// Placement records where a synced skill was extracted.
type Placement struct {
	Agent string `json:"agent"`
	Path  string `json:"path"`
	Scope string `json:"scope"`
}

// SyncedSkill records a skill name, version, and content checksum.
type SyncedSkill struct {
	Name       string      `json:"name"`
	Version    int         `json:"version"`
	Checksum   string      `json:"checksum"`
	Placements []Placement `json:"placements,omitempty"`
}

// DefaultDir returns the default configuration directory (~/.skael).
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".skael"
	}
	return filepath.Join(home, ".skael")
}

// WriteConfig creates dir if needed and writes cfg to config.json with mode 0600.
func WriteConfig(dir string, cfg *Config) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0600)
}

// ReadConfig reads and parses config.json from dir.
func ReadConfig(dir string) (*Config, error) {
	path := filepath.Join(dir, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config corrupt at %s (re-run skael setup): %w", path, err)
	}
	return &cfg, nil
}

// WriteState writes state.json atomically (temp file + rename) so a crash or
// concurrent writer can never leave a torn file behind.
func WriteState(dir string, state *SyncState) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "state-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		os.Remove(tmpName)
		return err
	}
	target := filepath.Join(dir, "state.json")
	if err := os.Rename(tmpName, target); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// ReadState reads state.json from dir.
// If the file is missing, an empty SyncState is returned without error.
// If the file is corrupt, it is renamed to state.json.bak and an empty SyncState is returned.
func ReadState(dir string) (*SyncState, error) {
	path := filepath.Join(dir, "state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &SyncState{}, nil
		}
		return nil, err
	}
	var state SyncState
	if err := json.Unmarshal(data, &state); err != nil {
		// Corrupt — back up and return empty.
		_ = os.Rename(path, path+".bak")
		return &SyncState{}, nil
	}
	return &state, nil
}

// LoadConfig resolves configuration with environment variables taking precedence.
// It checks SKAEL_URL and SKAEL_KEY first, then falls back to ReadConfig(DefaultDir()).
// If only one of the two env vars is set, it returns an error naming the missing one.
func LoadConfig() (*Config, error) {
	envURL := os.Getenv("SKAEL_URL")
	envKey := os.Getenv("SKAEL_KEY")

	if envURL != "" && envKey != "" {
		return &Config{Endpoint: envURL, APIKey: envKey}, nil
	}
	if envURL != "" && envKey == "" {
		return nil, fmt.Errorf("SKAEL_URL is set but SKAEL_KEY is missing")
	}
	if envURL == "" && envKey != "" {
		return nil, fmt.Errorf("SKAEL_KEY is set but SKAEL_URL is missing")
	}

	return ReadConfig(DefaultDir())
}
