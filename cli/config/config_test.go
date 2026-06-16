package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteAndReadConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		Endpoint: "https://api.skael.dev",
		APIKey:   "sk-test-abc123",
	}

	err := WriteConfig(dir, cfg)
	require.NoError(t, err)

	got, err := ReadConfig(dir)
	require.NoError(t, err)
	assert.Equal(t, cfg.Endpoint, got.Endpoint)
	assert.Equal(t, cfg.APIKey, got.APIKey)
}

func TestReadConfig_NotFound(t *testing.T) {
	dir := t.TempDir()

	_, err := ReadConfig(dir)
	assert.Error(t, err)
}

func TestWriteAndReadState(t *testing.T) {
	dir := t.TempDir()
	state := &SyncState{
		LastSync: "2026-05-23T10:00:00Z",
		Skills: []SyncedSkill{
			{Name: "my-skill", Version: 3, Checksum: "abc123def456"},
		},
	}

	err := WriteState(dir, state)
	require.NoError(t, err)

	got, err := ReadState(dir)
	require.NoError(t, err)
	assert.Equal(t, state.LastSync, got.LastSync)
	require.Len(t, got.Skills, 1)
	assert.Equal(t, state.Skills[0].Name, got.Skills[0].Name)
	assert.Equal(t, state.Skills[0].Version, got.Skills[0].Version)
	assert.Equal(t, state.Skills[0].Checksum, got.Skills[0].Checksum)
}

func TestReadState_Missing_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()

	got, err := ReadState(dir)
	require.NoError(t, err)
	assert.Equal(t, "", got.LastSync)
	assert.Empty(t, got.Skills)
}

func TestLoadConfig_PartialEnvVars_URLOnly(t *testing.T) {
	t.Setenv("SKAEL_URL", "https://example.com")
	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "SKAEL_KEY") {
		t.Error("should mention SKAEL_KEY")
	}
}

func TestLoadConfig_PartialEnvVars_KeyOnly(t *testing.T) {
	t.Setenv("SKAEL_KEY", "sk-test")
	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "SKAEL_URL") {
		t.Error("should mention SKAEL_URL")
	}
}

func TestReadState_Corrupt_BacksUp(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")

	err := os.WriteFile(stateFile, []byte("not valid json {{{"), 0644)
	require.NoError(t, err)

	got, err := ReadState(dir)
	require.NoError(t, err)
	assert.Equal(t, "", got.LastSync)
	assert.Empty(t, got.Skills)

	_, statErr := os.Stat(stateFile + ".bak")
	assert.NoError(t, statErr, "state.json.bak should exist after corrupt state recovery")
}

func TestSyncedSkill_PlacementRoundTrip(t *testing.T) {
	dir := t.TempDir()
	state := &SyncState{
		LastSync: "2026-06-16T10:00:00Z",
		Skills: []SyncedSkill{
			{
				Name:     "my-skill",
				Version:  2,
				Checksum: "abc123",
				Placements: []Placement{
					{Agent: "claude", Path: "/home/user/.claude/skills/my-skill", Scope: "user"},
					{Agent: "cursor", Path: "/projects/foo/.cursor/skills/my-skill", Scope: "project"},
				},
			},
			{
				Name:     "old-skill",
				Version:  1,
				Checksum: "def456",
			},
		},
	}

	err := WriteState(dir, state)
	require.NoError(t, err)

	got, err := ReadState(dir)
	require.NoError(t, err)
	require.Len(t, got.Skills, 2)

	assert.Equal(t, "my-skill", got.Skills[0].Name)
	require.Len(t, got.Skills[0].Placements, 2)
	assert.Equal(t, "claude", got.Skills[0].Placements[0].Agent)
	assert.Equal(t, "/home/user/.claude/skills/my-skill", got.Skills[0].Placements[0].Path)
	assert.Equal(t, "user", got.Skills[0].Placements[0].Scope)
	assert.Equal(t, "cursor", got.Skills[0].Placements[1].Agent)
	assert.Equal(t, "project", got.Skills[0].Placements[1].Scope)

	assert.Equal(t, "old-skill", got.Skills[1].Name)
	assert.Empty(t, got.Skills[1].Placements)
}

func TestSyncedSkill_PlacementsOmittedWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	state := &SyncState{
		LastSync: "2026-06-16T10:00:00Z",
		Skills: []SyncedSkill{
			{Name: "bare-skill", Version: 1, Checksum: "abc"},
		},
	}

	require.NoError(t, WriteState(dir, state))

	raw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "placements", "placements key should be omitted when empty")
}

func TestWriteState_AtomicRoundTrip(t *testing.T) {
	dir := t.TempDir()

	st := &SyncState{}
	require.NoError(t, WriteState(dir, st))

	got, err := ReadState(dir)
	require.NoError(t, err)
	require.NotNil(t, got)

	// Only state.json may remain — no temp leftovers.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "state.json", entries[0].Name())
}
