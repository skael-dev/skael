package hooks_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/cli/hooks"
)

// TestWriteHookScript verifies the script is created, executable, and contains the events endpoint.
func TestWriteHookScript(t *testing.T) {
	skaalDir := t.TempDir()

	scriptPath, err := hooks.WriteHookScript(skaalDir)
	require.NoError(t, err)

	// File must exist.
	info, err := os.Stat(scriptPath)
	require.NoError(t, err, "hook script file must exist")

	// Must be executable (at least owner-execute bit).
	assert.True(t, info.Mode()&0o100 != 0, "hook script must have execute permission")

	// Must contain the events API path.
	data, err := os.ReadFile(scriptPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "/api/events", "hook script must reference /api/events")
}

// TestInstallClaudeHook_NewFile installs to a nonexistent settings.json and verifies the structure.
func TestInstallClaudeHook_NewFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")

	err := hooks.InstallClaudeHook(configPath, "https://skael.example.com", "test-api-key", "/home/user/.skael/hooks/skael-hook.sh")
	require.NoError(t, err)

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var settings map[string]any
	require.NoError(t, json.Unmarshal(data, &settings))

	// Must have hooks.PreToolUse.
	hooksSection, ok := settings["hooks"].(map[string]any)
	require.True(t, ok, "hooks key must be a JSON object")

	preToolUse, ok := hooksSection["PreToolUse"].([]any)
	require.True(t, ok, "PreToolUse must be a JSON array")
	require.NotEmpty(t, preToolUse, "PreToolUse must contain at least one entry")

	// First entry must have a hooks array with a skael-managed command.
	entry, ok := preToolUse[0].(map[string]any)
	require.True(t, ok, "PreToolUse[0] must be a JSON object")

	innerHooks, ok := entry["hooks"].([]any)
	require.True(t, ok, "entry.hooks must be a JSON array")
	require.NotEmpty(t, innerHooks)

	hookEntry, ok := innerHooks[0].(map[string]any)
	require.True(t, ok, "hooks[0] must be a JSON object")

	assert.Equal(t, "skael", hookEntry["_managed_by"], "_managed_by must be 'skael'")
	assert.Equal(t, "command", hookEntry["type"], "hook type must be 'command'")

	cmd, ok := hookEntry["command"].(string)
	require.True(t, ok, "command must be a string")
	assert.Contains(t, cmd, "SKAEL_AGENT=claude-code")
	assert.Contains(t, cmd, "skael-hook.sh")
	assert.NotContains(t, cmd, "SKAEL_ENDPOINT=")
	assert.NotContains(t, cmd, "SKAEL_API_KEY=")
}

// TestInstallClaudeHook_Idempotent verifies that installing twice results in exactly one skael entry.
func TestInstallClaudeHook_Idempotent(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")

	require.NoError(t, hooks.InstallClaudeHook(configPath, "https://skael.example.com", "key1", "/path/to/skael-hook.sh"))
	require.NoError(t, hooks.InstallClaudeHook(configPath, "https://skael.example.com", "key2", "/path/to/skael-hook.sh"))

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	content := string(data)

	// Count occurrences of "_managed_by": "skael" — must be exactly 1.
	count := strings.Count(content, `"_managed_by"`)
	assert.Equal(t, 1, count, "must have exactly one skael-managed hook after two installs")
}

// TestUninstallClaudeHook verifies that after uninstall, no skael-managed entries remain.
func TestUninstallClaudeHook(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")

	require.NoError(t, hooks.InstallClaudeHook(configPath, "https://skael.example.com", "test-key", "/path/to/skael-hook.sh"))

	// Confirm skael entry exists.
	data, _ := os.ReadFile(configPath)
	require.Contains(t, string(data), "skael", "skael entry must be present after install")

	require.NoError(t, hooks.UninstallClaudeHook(configPath))

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "skael", "no skael entries must remain after uninstall")
}

// TestInstallClaudeHook_NoPlaintextAPIKey verifies that the API key is NOT embedded in settings.json.
func TestInstallClaudeHook_NoPlaintextAPIKey(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")

	const sensitiveKey = "super-secret-api-key-12345"

	err := hooks.InstallClaudeHook(configPath, "https://skael.example.com", sensitiveKey, "/home/user/.skael/hooks/skael-hook.sh")
	require.NoError(t, err)

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	assert.NotContains(t, string(data), sensitiveKey, "API key must NOT appear in plaintext in settings.json")
}

// TestHookScript_ContainsCrossPlatformHash verifies the hook script supports both Linux and macOS hash commands.
func TestHookScript_ContainsCrossPlatformHash(t *testing.T) {
	skaalDir := t.TempDir()

	scriptPath, err := hooks.WriteHookScript(skaalDir)
	require.NoError(t, err)

	data, err := os.ReadFile(scriptPath)
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "sha256sum", "hook script must reference sha256sum (Linux)")
	assert.Contains(t, content, "shasum", "hook script must reference shasum (macOS)")
}

// TestInstallCodexHook_NewFile verifies that installing into a nonexistent
// config.toml creates the file and writes a managed block with the correct
// marker comment.
func TestInstallCodexHook_NewFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")

	err := hooks.InstallForAgent("codex", configPath, "https://skael.example.com", "test-api-key", "/home/user/.skael/hooks/skael-hook.sh")
	require.NoError(t, err)

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "# managed_by = skael", "managed block start marker must be present")
	assert.Contains(t, content, "# end managed_by = skael", "managed block end marker must be present")
	assert.Contains(t, content, "[[hooks.pre_tool_use]]", "TOML hook section must be present")
	assert.Contains(t, content, "SKAEL_AGENT=codex", "agent env var must be set to codex")
}

// TestInstallCodexHook_Idempotent verifies that installing twice results in
// exactly one managed block in the config.toml file.
func TestInstallCodexHook_Idempotent(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")

	require.NoError(t, hooks.InstallForAgent("codex", configPath, "https://skael.example.com", "key1", "/path/to/skael-hook.sh"))
	require.NoError(t, hooks.InstallForAgent("codex", configPath, "https://skael.example.com", "key2", "/path/to/skael-hook.sh"))

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	content := string(data)
	count := strings.Count(content, "# managed_by = skael")
	assert.Equal(t, 1, count, "must have exactly one managed block after two installs")
}

// TestUninstallCodexHook verifies that after uninstalling, the managed block
// is removed from the config.toml file.
func TestUninstallCodexHook(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")

	require.NoError(t, hooks.InstallForAgent("codex", configPath, "https://skael.example.com", "test-key", "/path/to/skael-hook.sh"))

	// Confirm the managed block is present after install.
	data, _ := os.ReadFile(configPath)
	require.Contains(t, string(data), "# managed_by = skael", "managed block must be present after install")

	require.NoError(t, hooks.UninstallForAgent("codex", configPath))

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "# managed_by = skael", "managed block must be removed after uninstall")
	assert.NotContains(t, string(data), "[[hooks.pre_tool_use]]", "hook section must be removed after uninstall")
}

// TestHookScript_ReadsConfigFile verifies the hook script reads credentials from config.json.
func TestHookScript_ReadsConfigFile(t *testing.T) {
	skaalDir := t.TempDir()

	scriptPath, err := hooks.WriteHookScript(skaalDir)
	require.NoError(t, err)

	data, err := os.ReadFile(scriptPath)
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "config.json", "hook script must read credentials from config.json")
}
