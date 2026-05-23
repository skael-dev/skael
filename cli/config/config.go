package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Config holds the CLI configuration stored in config.json.
type Config struct {
	Endpoint string `json:"endpoint"`
	APIKey   string `json:"api_key"`
}

// SyncState records the last sync timestamp and each synced skill.
type SyncState struct {
	LastSync string        `json:"last_sync"`
	Skills   []SyncedSkill `json:"skills"`
}

// SyncedSkill records a skill name, version, and content checksum.
type SyncedSkill struct {
	Name     string `json:"name"`
	Version  int    `json:"version"`
	Checksum string `json:"checksum"`
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
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// WriteState writes state to state.json in dir with mode 0644.
func WriteState(dir string, state *SyncState) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "state.json"), data, 0644)
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
func LoadConfig() (*Config, error) {
	url := os.Getenv("SKAEL_URL")
	key := os.Getenv("SKAEL_KEY")
	if url != "" || key != "" {
		return &Config{Endpoint: url, APIKey: key}, nil
	}
	return ReadConfig(DefaultDir())
}
