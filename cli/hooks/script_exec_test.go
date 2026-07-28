package hooks_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/cli/hooks"
)

// fakeCurl stands in for curl on PATH. It appends the value passed to -d to
// $SKAEL_TEST_CAPTURE, one JSON body per record, and always exits 0.
//
// Records are separated with a NUL byte rather than a newline: the real
// script builds EVENT_JSON via `jq -n` without `-c`, so the captured body is
// itself pretty-printed, multi-line JSON. A newline-delimited capture format
// would misinterpret that internal formatting as multiple records.
const fakeCurl = `#!/usr/bin/env bash
body=""
while [ $# -gt 0 ]; do
  case "$1" in
    -d) body="$2"; shift 2 ;;
    *) shift ;;
  esac
done
printf '%s\0' "$body" >> "$SKAEL_TEST_CAPTURE"
exit 0
`

// requireJQ skips a test on machines without jq. The scripts have a grep
// fallback, but the jq path is the one every supported agent actually takes.
func requireJQ(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed; skipping hook script execution test")
	}
}

// hookEnv is a sandbox for running a hook script: a fake HOME holding a
// ~/.skael/config.json, a bin dir that shadows curl, and a capture file.
type hookEnv struct {
	home    string
	binDir  string
	capture string
}

func newHookEnv(t *testing.T) *hookEnv {
	t.Helper()

	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".skael"), 0o755))
	const cfg = `{"endpoint":"https://skael.test","api_key":"sk-testkey"}`
	require.NoError(t, os.WriteFile(filepath.Join(home, ".skael", "config.json"), []byte(cfg), 0o600))

	binDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "curl"), []byte(fakeCurl), 0o755))

	return &hookEnv{
		home:    home,
		binDir:  binDir,
		capture: filepath.Join(t.TempDir(), "capture.jsonl"),
	}
}

// run executes scriptPath with payload on stdin and returns its exit code plus
// every JSON body it POSTed. The scripts fire curl in the background, so run
// polls the capture file briefly before reading it.
func (e *hookEnv) run(t *testing.T, scriptPath, payload string, extraEnv ...string) (int, []map[string]any) {
	t.Helper()

	cmd := exec.Command("bash", scriptPath)
	cmd.Stdin = strings.NewReader(payload)
	cmd.Env = append(os.Environ(),
		"HOME="+e.home,
		"PATH="+e.binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SKAEL_TEST_CAPTURE="+e.capture,
	)
	cmd.Env = append(cmd.Env, extraEnv...)

	exitCode := 0
	if err := cmd.Run(); err != nil {
		exitErr, ok := err.(*exec.ExitError)
		require.Truef(t, ok, "running %s: %v", scriptPath, err)
		exitCode = exitErr.ExitCode()
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(e.capture); err == nil && strings.TrimSpace(string(data)) != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	data, err := os.ReadFile(e.capture)
	if os.IsNotExist(err) {
		return exitCode, nil
	}
	require.NoError(t, err)

	var bodies []map[string]any
	for _, record := range strings.Split(string(data), "\x00") {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		var body map[string]any
		require.NoErrorf(t, json.Unmarshal([]byte(record), &body), "captured body is not JSON: %s", record)
		bodies = append(bodies, body)
	}
	return exitCode, bodies
}

func TestHookScript_PostsSkillActivation(t *testing.T) {
	requireJQ(t)

	scriptPath, err := hooks.WriteHookScript(t.TempDir())
	require.NoError(t, err)

	env := newHookEnv(t)
	code, bodies := env.run(t, scriptPath,
		`{"tool_name":"Skill","tool_input":{"skill":"brainstorming"}}`,
		"SKAEL_AGENT=claude-code")

	require.Equal(t, 0, code)
	require.Len(t, bodies, 1)
	assert.Equal(t, "brainstorming", bodies[0]["skill_name"])
	assert.Equal(t, "claude-code", bodies[0]["agent"])
	assert.NotEmpty(t, bodies[0]["project_hash"])
	assert.NotEmpty(t, bodies[0]["developer_hash"])
}

func TestHookScript_StripsOpenCodeSkillsPrefix(t *testing.T) {
	requireJQ(t)

	scriptPath, err := hooks.WriteHookScript(t.TempDir())
	require.NoError(t, err)

	env := newHookEnv(t)
	code, bodies := env.run(t, scriptPath,
		`{"tool_name":"skills_code-review","tool_input":{"skill":"skills_code-review"}}`,
		"SKAEL_AGENT=opencode")

	require.Equal(t, 0, code)
	require.Len(t, bodies, 1)
	assert.Equal(t, "code-review", bodies[0]["skill_name"])
}
