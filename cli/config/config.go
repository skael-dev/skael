package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SkillEntry records a skill the user has explicitly installed.
type SkillEntry struct {
	Name  string `json:"name"`
	Scope string `json:"scope,omitempty"`
}

// Config holds the CLI configuration stored in config.json.
type Config struct {
	Endpoint string       `json:"endpoint"`
	APIKey   string       `json:"api_key"`
	Scope    string       `json:"scope,omitempty"`
	Skills   []SkillEntry `json:"skills"`
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

// FindSkill returns the entry and index for the given name, or (-1) if not found.
func (c *Config) FindSkill(name string) (SkillEntry, int) {
	for i, s := range c.Skills {
		if s.Name == name {
			return s, i
		}
	}
	return SkillEntry{}, -1
}

// AddSkill adds a skill to the config, or updates its scope if already present.
func (c *Config) AddSkill(name, scope string) {
	if _, idx := c.FindSkill(name); idx >= 0 {
		c.Skills[idx].Scope = scope
		return
	}
	c.Skills = append(c.Skills, SkillEntry{Name: name, Scope: scope})
}

// RemoveSkill removes a skill from the config by name. Returns true if found.
func (c *Config) RemoveSkill(name string) bool {
	_, idx := c.FindSkill(name)
	if idx < 0 {
		return false
	}
	c.Skills = append(c.Skills[:idx], c.Skills[idx+1:]...)
	return true
}

// MigrateSkillsFromState populates cfg.Skills from the given SyncState.
// Used on first run after upgrading from a legacy config that has no "skills" key.
// Returns the names of migrated skills (for user messaging).
func MigrateSkillsFromState(cfg *Config, state *SyncState) []string {
	cfg.Skills = make([]SkillEntry, 0, len(state.Skills))
	var names []string
	for _, s := range state.Skills {
		scope := ""
		if len(s.Placements) > 0 {
			scope = s.Placements[0].Scope
		}
		cfg.Skills = append(cfg.Skills, SkillEntry{Name: s.Name, Scope: scope})
		names = append(names, s.Name)
	}
	return names
}

// DefaultDir returns the default configuration directory (~/.skael).
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".skael"
	}
	return filepath.Join(home, ".skael")
}

// WriteConfig creates dir if needed and writes cfg to config.json atomically
// (temp file + rename) with mode 0600.
func WriteConfig(dir string, cfg *Config) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, "config-*.json.tmp")
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
	if err := os.Chmod(tmpName, 0600); err != nil {
		os.Remove(tmpName)
		return err
	}
	target := filepath.Join(dir, "config.json")
	if err := os.Rename(tmpName, target); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
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

// hasSkillsKey reads the raw config file and checks if the "skills" JSON key is present.
func hasSkillsKey(dir string) (bool, error) {
	path := filepath.Join(dir, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return false, err
	}
	_, ok := raw["skills"]
	return ok, nil
}

// EnsureSkillsKey loads config from dir and ensures the skills key exists.
// If the config is legacy (no skills key), it migrates from state.json,
// writes the updated config, and returns the migrated skill names.
// If the config already has a skills key, migrated is nil.
func EnsureSkillsKey(dir string) (cfg *Config, migrated []string, err error) {
	cfg, err = ReadConfig(dir)
	if err != nil {
		return nil, nil, err
	}

	has, err := hasSkillsKey(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("check skills key: %w", err)
	}
	if has {
		return cfg, nil, nil
	}

	// Legacy config — migrate from state.
	state, err := ReadState(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("read state for migration: %w", err)
	}

	migrated = MigrateSkillsFromState(cfg, state)
	if err := WriteConfig(dir, cfg); err != nil {
		return nil, nil, fmt.Errorf("write migrated config: %w", err)
	}

	return cfg, migrated, nil
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
