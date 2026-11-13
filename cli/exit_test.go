package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/skael-dev/skael/internal/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runCLI runs one command line through the same path as main.
func runCLI(t *testing.T, args ...string) (int, string) {
	t.Helper()
	ui.JSONMode = false
	t.Cleanup(func() { ui.JSONMode = false })
	code := 0
	var stdout string
	captureStderr(t, func() {
		stdout = captureStdout(t, func() { code = run(args) })
	})
	return code, stdout
}

func noConfig(t *testing.T) {
	t.Helper()
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)
	t.Setenv("SKAEL_URL", "")
	t.Setenv("SKAEL_KEY", "")
}

func TestExitCodes(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"title":"Internal Server Error"}`, http.StatusInternalServerError)
	}))
	defer failing.Close()

	tests := []struct {
		name   string
		server bool
		args   []string
		want   int
	}{
		{"success", false, []string{"version"}, 0},
		{"unknown command", false, []string{"bogus"}, 2},
		{"unknown subcommand", false, []string{"sets", "bogus"}, 2},
		{"unknown flag", false, []string{"list", "--bogus"}, 2},
		{"missing argument", false, []string{"show"}, 2},
		{"missing required flag", false, []string{"pull"}, 2},
		{"no config", false, []string{"list"}, 2},
		{"no config for pull", false, []string{"pull", "--set", "x"}, 2},
		{"bad entry", true, []string{"sets", "create", "x", "a@nope"}, 2},
		{"api error", true, []string{"list"}, 1},
		{"api error for search", true, []string{"search", "x"}, 1},
		{"api error for sets", true, []string{"sets", "list"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			noConfig(t)
			if tt.server {
				t.Setenv("SKAEL_URL", failing.URL)
				t.Setenv("SKAEL_KEY", "k")
			}
			code, _ := runCLI(t, tt.args...)
			assert.Equal(t, tt.want, code)
		})
	}
}

func TestInvalidConfigIsUsageError(t *testing.T) {
	noConfig(t)
	dir := filepath.Join(os.Getenv("HOME"), ".skael")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not json"), 0o600))
	code, _ := runCLI(t, "list")
	assert.Equal(t, 2, code)
}

// A failure in --json mode used to print the envelope and exit 0.
func TestJSONErrorPrintsEnvelopeAndExitsNonZero(t *testing.T) {
	noConfig(t)
	for _, args := range [][]string{
		{"list", "--json"},
		{"bogus", "--json"},
		{"list", "--bogus", "--json"},
	} {
		code, stdout := runCLI(t, args...)
		assert.Equal(t, 2, code, args)
		var envelope map[string]any
		require.NoError(t, json.Unmarshal([]byte(stdout), &envelope), "stdout: %s", stdout)
		assert.NotEmpty(t, envelope["error"])
		assert.NotEmpty(t, envelope["code"])
	}
}

func TestInitSkillDoesNotOverwrite(t *testing.T) {
	t.Chdir(t.TempDir())
	path := filepath.Join(".agents", "skills", "skael", "SKILL.md")

	code, _ := runCLI(t, "init")
	require.Equal(t, 0, code)
	first, err := os.Stat(path)
	require.NoError(t, err)

	code, stdout := runCLI(t, "init", "--json")
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, `"unchanged"`)
	second, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, first.ModTime(), second.ModTime(), "a second run must not touch the file")

	require.NoError(t, os.WriteFile(path, []byte("mine\n"), 0o644))
	code, _ = runCLI(t, "init")
	assert.Equal(t, 2, code)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "mine\n", string(got), "a refusal must leave the file alone")

	code, _ = runCLI(t, "init", "--force")
	assert.Equal(t, 0, code)
	initForce = false
	got, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.NotEqual(t, "mine\n", string(got))
}

func TestInitNameStillScaffolds(t *testing.T) {
	t.Chdir(t.TempDir())
	code, _ := runCLI(t, "init", "my-skill")
	require.Equal(t, 0, code)
	assert.FileExists(t, filepath.Join("my-skill", "SKILL.md"))
}
