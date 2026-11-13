package hooks_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/cli/hooks"
)

// buildSkael builds the CLI into dir, a folder whose name may hold a space.
func buildSkael(t *testing.T, dir string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	out := filepath.Join(dir, "skael")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, "github.com/skael-dev/skael/cmd/skael")
	msg, err := cmd.CombinedOutput()
	require.NoError(t, err, string(msg))
	return out
}

type shellCase struct {
	name string
	argv []string
}

// The commands skael writes into each agent's config run through the shell
// that agent uses on this OS, as the agent runs them. This is the path that
// opened Windows' "How do you want to open this file" dialog.
func TestHookCommandsRunInTheAgentShells(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the CLI")
	}
	events := make(chan map[string]any, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/events" {
			body, _ := io.ReadAll(r.Body)
			var ev map[string]any
			_ = json.Unmarshal(body, &ev)
			events <- ev
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	home := t.TempDir()
	skaelDir := filepath.Join(home, ".skael")
	require.NoError(t, os.MkdirAll(skaelDir, 0o755))
	cfg, _ := json.Marshal(map[string]string{"endpoint": srv.URL, "api_key": "sk-test"})
	require.NoError(t, os.WriteFile(filepath.Join(skaelDir, "config.json"), cfg, 0o600))
	// A fresh sync time keeps `hook autosync` from starting a real sync.
	state, _ := json.Marshal(map[string]any{"last_sync": time.Now().UTC().Format(time.RFC3339)})
	require.NoError(t, os.WriteFile(filepath.Join(skaelDir, "state.json"), state, 0o600))
	env := append(os.Environ(), "HOME="+home, "USERPROFILE="+home)

	spaced := buildSkael(t, filepath.Join(t.TempDir(), "my tools"))
	skill := writeSkill(t, filepath.Join(home, ".cursor", "skills", "deploy-helper"), "deploy-helper")
	cursorPayload, _ := json.Marshal(map[string]any{"tool_name": "Read", "tool_input": map[string]any{"file_path": skill}, "workspace_roots": []string{home}})
	skillPayload := `{"tool_name":"Skill","tool_input":{"skill":"deploy-helper"}}`

	claudePath := filepath.Join(home, ".claude", "settings.json")
	require.NoError(t, hooks.InstallForAgent("claude-code", claudePath, spaced))
	require.NoError(t, hooks.InstallAutoSyncForAgent("claude-code", claudePath, spaced))
	pre := claudeHandlers(t, claudePath, "PreToolUse")[0]
	var claudeArgv []string
	claudeArgv = append(claudeArgv, pre["command"].(string))
	for _, a := range pre["args"].([]any) {
		claudeArgv = append(claudeArgv, a.(string))
	}
	auto := claudeHandlers(t, claudePath, "UserPromptSubmit")[0]
	autoArgv := []string{auto["command"].(string)}
	for _, a := range auto["args"].([]any) {
		autoArgv = append(autoArgv, a.(string))
	}

	codexCommand := func(bin string) (posix, windows string) {
		path := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, hooks.InstallForAgent("codex", path, bin))
		content := read(t, path)
		get := func(key string) string {
			m := regexp.MustCompile(`(?m)^` + key + ` = (".*")$`).FindStringSubmatch(content)
			require.Len(t, m, 2, key)
			s, err := strconv.Unquote(m[1])
			require.NoError(t, err)
			return s
		}
		return get("command"), get("command_windows")
	}
	cursorCommand := func(bin string) string {
		path := filepath.Join(t.TempDir(), "hooks.json")
		require.NoError(t, hooks.InstallForAgent("cursor", path, bin))
		return cursorEntries(t, path, "postToolUse")[0]["command"].(string)
	}

	type run struct {
		shellCase
		agent, payload string
	}
	runs := []run{
		{shellCase{"claude exec form", claudeArgv}, "claude-code", skillPayload},
		{shellCase{"claude auto-sync exec form", autoArgv}, "", skillPayload},
	}
	codexPosix, codexWindows := codexCommand(spaced)
	if runtime.GOOS == "windows" {
		bare := buildSkael(t, filepath.Join(t.TempDir(), "bin"))
		_, codexBare := codexCommand(bare)
		runs = append(runs,
			run{shellCase{"codex powershell", []string{"powershell", "-NoProfile", "-Command", codexWindows}}, "codex", skillPayload},
			run{shellCase{"codex cmd", []string{"cmd", "/C", codexBare}}, "codex", skillPayload},
			run{shellCase{"cursor powershell", []string{"powershell", "-NoProfile", "-Command", cursorCommand(spaced)}}, "cursor", string(cursorPayload)},
			run{shellCase{"cursor cmd", []string{"cmd", "/C", cursorCommand(bare)}}, "cursor", string(cursorPayload)},
		)
	} else {
		runs = append(runs,
			run{shellCase{"codex sh", []string{"sh", "-c", codexPosix}}, "codex", skillPayload},
			run{shellCase{"cursor sh", []string{"sh", "-c", cursorCommand(spaced)}}, "cursor", string(cursorPayload)},
		)
	}

	for _, r := range runs {
		t.Run(r.name, func(t *testing.T) {
			cmd := exec.Command(r.argv[0], r.argv[1:]...)
			cmd.Env = env
			cmd.Dir = home
			cmd.Stdin = strings.NewReader(r.payload)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			require.NoError(t, cmd.Run(), stderr.String())
			assert.Empty(t, stdout.String(), "an agent reads hook stdout")
			if r.agent == "" {
				return
			}
			select {
			case ev := <-events:
				assert.Equal(t, "deploy-helper", ev["skill_name"])
				assert.Equal(t, r.agent, ev["agent"])
			case <-time.After(20 * time.Second):
				t.Fatalf("no event reached the server; stderr: %s", stderr.String())
			}
		})
	}
}
