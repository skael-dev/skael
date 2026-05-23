package config

import (
	"os"
	"path/filepath"
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
