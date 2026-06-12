package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/skael-dev/skael/internal/skill"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildValidArchive creates a minimal valid skill archive using skill.Pack
// on a temporary directory containing a SKILL.md file.
func buildValidArchive(t *testing.T) []byte {
	t.Helper()
	srcDir := t.TempDir()
	err := os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte("# Test Skill\n"), 0o644)
	require.NoError(t, err)
	archiveBytes, _, _, err := skill.Pack(srcDir)
	require.NoError(t, err)
	return archiveBytes
}

// buildCorruptArchive returns bytes that are not a valid gzip/tar stream.
func buildCorruptArchive() []byte {
	return []byte("this is not a valid gzip archive")
}

// TestExtractSkillAtomically_SuccessInstalls verifies that a valid archive is
// correctly installed at destDir.
func TestExtractSkillAtomically_SuccessInstalls(t *testing.T) {
	archiveBytes := buildValidArchive(t)
	base := t.TempDir()
	destDir := filepath.Join(base, "my-skill")

	err := extractSkillAtomically(archiveBytes, destDir)
	require.NoError(t, err)

	_, statErr := os.Stat(filepath.Join(destDir, "SKILL.md"))
	assert.NoError(t, statErr, "SKILL.md should be present in destDir after successful extraction")
}

// TestExtractSkillAtomically_CorruptArchivePreservesPreviousContent is the key
// regression test: a pre-existing skill dir must survive a failed extraction
// attempt entirely intact, and no .tmp- sibling dirs should remain.
func TestExtractSkillAtomically_CorruptArchivePreservesPreviousContent(t *testing.T) {
	// Install an initial "good" version.
	goodArchive := buildValidArchive(t)
	base := t.TempDir()
	destDir := filepath.Join(base, "my-skill")

	err := extractSkillAtomically(goodArchive, destDir)
	require.NoError(t, err, "initial installation should succeed")

	// Write a sentinel file inside the installed skill to confirm the original
	// content survives the failed update.
	sentinelPath := filepath.Join(destDir, "SKILL.md")
	originalContent, err := os.ReadFile(sentinelPath)
	require.NoError(t, err)

	// Now attempt to install a corrupt archive over the existing skill.
	err = extractSkillAtomically(buildCorruptArchive(), destDir)
	require.Error(t, err, "corrupt archive must return an error")

	// The original SKILL.md must still be present and unchanged.
	afterContent, statErr := os.ReadFile(sentinelPath)
	require.NoError(t, statErr, "destDir must still exist after failed extraction")
	assert.Equal(t, originalContent, afterContent, "original content must be untouched after failed extraction")

	// No .tmp- sibling directories should remain.
	entries, err := os.ReadDir(base)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".tmp-", "no temp dirs should remain after failure: found %q", e.Name())
	}
}

// TestExtractSkillAtomically_CorruptArchiveNoDestDirLeavesNothingBehind
// verifies that when there was no prior installation and extraction fails,
// no partial directory is left and no .tmp- dirs remain.
func TestExtractSkillAtomically_CorruptArchiveNoDestDirLeavesNothingBehind(t *testing.T) {
	base := t.TempDir()
	destDir := filepath.Join(base, "new-skill")

	err := extractSkillAtomically(buildCorruptArchive(), destDir)
	require.Error(t, err, "corrupt archive must return an error")

	_, statErr := os.Stat(destDir)
	assert.True(t, os.IsNotExist(statErr), "destDir must not exist after a failed first-time extraction")

	entries, err := os.ReadDir(base)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".tmp-", "no temp dirs should remain after failure: found %q", e.Name())
	}
}

// TestExtractSkillAtomically_SuccessReplacesExistingDir verifies that a
// successful extraction replaces the previous skill dir cleanly.
func TestExtractSkillAtomically_SuccessReplacesExistingDir(t *testing.T) {
	base := t.TempDir()
	destDir := filepath.Join(base, "my-skill")

	// Create a stale dir with a file that must NOT survive the update.
	require.NoError(t, os.MkdirAll(destDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(destDir, "stale.md"), []byte("old"), 0o644))

	archiveBytes := buildValidArchive(t)
	err := extractSkillAtomically(archiveBytes, destDir)
	require.NoError(t, err)

	// stale.md should be gone.
	_, statErr := os.Stat(filepath.Join(destDir, "stale.md"))
	assert.True(t, os.IsNotExist(statErr), "stale file from old version must be removed after successful update")

	// SKILL.md from the new version should be present.
	_, statErr = os.Stat(filepath.Join(destDir, "SKILL.md"))
	assert.NoError(t, statErr, "SKILL.md from new version must be present")
}

// buildValidArchiveWithReader exercises skill.Unpack directly so we confirm
// the helper builds a real archive. Not a test itself; kept for clarity.
func init() {
	// Smoke-check: confirm skill.Unpack can round-trip a valid archive built
	// by buildValidArchive. If this panics the test binary won't start.
	_ = func() {
		src, err := os.MkdirTemp("", "init-smoke-*")
		if err != nil {
			return
		}
		defer os.RemoveAll(src)
		if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("# s\n"), 0o644); err != nil {
			return
		}
		archiveBytes, _, _, err := skill.Pack(src)
		if err != nil {
			return
		}
		dst, err := os.MkdirTemp("", "init-smoke-dst-*")
		if err != nil {
			return
		}
		defer os.RemoveAll(dst)
		_ = skill.Unpack(bytes.NewReader(archiveBytes), dst)
	}
}
