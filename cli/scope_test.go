package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveScope_Precedence(t *testing.T) {
	assert.Equal(t, ScopeProject, resolveScope("", ""))         // default
	assert.Equal(t, ScopeUser, resolveScope("", "user"))        // config
	assert.Equal(t, ScopeProject, resolveScope("", "project"))  // config
	assert.Equal(t, ScopeUser, resolveScope("user", "project")) // flag wins
	assert.Equal(t, ScopeProject, resolveScope("project", "user"))
}

func TestGitRoot_FindsRepoFromNestedDir(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	nested := filepath.Join(root, "a", "b")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	assert.Equal(t, root, gitRoot(nested))
}

func TestGitRoot_FallsBackToStartWhenNoRepo(t *testing.T) {
	dir := t.TempDir() // no .git anywhere we create
	assert.Equal(t, dir, gitRoot(dir))
}
