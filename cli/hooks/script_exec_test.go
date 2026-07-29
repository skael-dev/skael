package hooks_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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

// groupExitCeiling bounds how long waitForGroupExit will wait for a script's
// process group — the script itself plus any background child it forked,
// e.g. the disowned curl POST — to fully exit. Reaping a local subprocess
// that just writes a file is a matter of milliseconds; this ceiling is set
// generously purely to fail loudly if something is genuinely wedged, not
// because we expect to ever wait anywhere near it.
const groupExitCeiling = 20 * time.Second

// groupPollInterval is how often waitForGroupExit rechecks whether the
// process group has emptied out.
const groupPollInterval = 5 * time.Millisecond

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

// exec runs bashBin scriptPath with payload on stdin under the given PATH
// and environment, in its own new process group, and returns its exit code
// plus that group's id. Setpgid is set with Pgid left at its zero value, so
// the started process becomes the leader of a brand new process group whose
// pgid equals its own pid — meaning the pid we already have from
// cmd.Process doubles as the pgid, no extra syscall required to look it up.
func (e *hookEnv) exec(t *testing.T, bashBin, scriptPath, payload, path string, baseEnv []string, extraEnv ...string) (exitCode, pgid int) {
	t.Helper()

	cmd := exec.Command(bashBin, scriptPath)
	cmd.Stdin = strings.NewReader(payload)
	env := append([]string{}, baseEnv...)
	env = append(env,
		"HOME="+e.home,
		"PATH="+path,
		"SKAEL_TEST_CAPTURE="+e.capture,
	)
	cmd.Env = append(env, extraEnv...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Run(); err != nil {
		exitErr, ok := err.(*exec.ExitError)
		require.Truef(t, ok, "running %s: %v", scriptPath, err)
		exitCode = exitErr.ExitCode()
	}
	return exitCode, cmd.Process.Pid
}

// waitForGroupExit blocks until process group pgid has no remaining
// members — syscall.Kill(-pgid, 0) returning ESRCH — meaning the script and
// every descendant it forked, including a backgrounded, disowned curl, have
// fully exited and been reaped.
//
// This is what makes the wait deterministic instead of a race against a
// timeout: the hook scripts run `curl ... & disown`, and disown only removes
// the job from the shell's job table — it does not call setpgid, so the
// curl child (and the subshell wrapping it) stay in the same process group
// the script itself was started in via Setpgid above. The group can only go
// empty once that child is actually done, whether it takes microseconds or
// several seconds.
//
// This guarantee rests on one assumption: no descendant of the script calls
// setsid, or its own setpgid, to leave the group. script.go, cursor_script.go,
// and opencode_plugin.go do not do this today — if one of them grew a
// detached child that escaped the group, this wait would stop seeing it, and
// this comment (and the fix) would need revisiting alongside that change.
//
// A wedged descendant would otherwise hang the suite forever, so this is
// bounded by groupExitCeiling. Hitting that ceiling fails the test loudly
// rather than falling through to read a capture file that may still be
// mid-write.
func waitForGroupExit(t *testing.T, pgid int) {
	t.Helper()

	deadline := time.Now().Add(groupExitCeiling)
	for {
		err := syscall.Kill(-pgid, 0)
		if err == syscall.ESRCH {
			return
		}
		if time.Now().After(deadline) {
			require.FailNowf(t, "process group did not exit",
				"pgid %d still had live members after %s; a background child may be wedged", pgid, groupExitCeiling)
		}
		time.Sleep(groupPollInterval)
	}
}

// readBodies parses the capture file's current contents into JSON bodies.
// Callers only reach this after waitForGroupExit has confirmed every writer
// has exited, so there is no partial-write window to tolerate here — a
// parse failure at this point is a genuine test failure.
func (e *hookEnv) readBodies(t *testing.T) []map[string]any {
	t.Helper()

	data, err := os.ReadFile(e.capture)
	if os.IsNotExist(err) {
		return nil
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
	return bodies
}

// run executes scriptPath with payload on stdin and returns its exit code
// plus every JSON body it POSTed (nil if none). The scripts fire curl in a
// backgrounded, disowned subshell, so run can't just wait on the foreground
// process the way exec.Cmd normally would — instead it waits for the
// script's entire process group to exit (see waitForGroupExit) before
// reading the capture file. That single mechanism is what both callers rely
// on: tests asserting one or more POSTs get every body that actually landed,
// and tests asserting no POST get a capture file that is genuinely settled,
// not just one that hasn't received anything yet.
func (e *hookEnv) run(t *testing.T, scriptPath, payload string, extraEnv ...string) (int, []map[string]any) {
	t.Helper()

	path := e.binDir + string(os.PathListSeparator) + os.Getenv("PATH")
	exitCode, pgid := e.exec(t, "bash", scriptPath, payload, path, os.Environ(), extraEnv...)
	waitForGroupExit(t, pgid)
	return exitCode, e.readBodies(t)
}

// jqLessFallbackBins lists the external binaries the grep/sed fallback path
// actually needs, in the order buildJQLessBinDir resolves them: bash to run
// the script, cat to read stdin, grep/sed/head/cut for field extraction. The
// hashing tool is resolved separately since it's an either/or choice.
var jqLessFallbackBins = []string{"bash", "cat", "grep", "sed", "head", "cut"}

// buildJQLessBinDir resolves jqLessFallbackBins (plus a hashing tool and a
// fake curl) into a fresh directory of symlinks with no jq anywhere on it, so
// the fallback code path actually executes instead of the jq path. It skips
// the test if any required binary (or neither sha256sum nor shasum) can't be
// resolved on the host.
func buildJQLessBinDir(t *testing.T) string {
	t.Helper()

	binDir := t.TempDir()
	for _, name := range jqLessFallbackBins {
		p, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("%s not found on PATH; skipping jq-less fallback test", name)
		}
		require.NoError(t, os.Symlink(p, filepath.Join(binDir, name)))
	}

	hashBin := ""
	for _, candidate := range []string{"sha256sum", "shasum"} {
		if p, err := exec.LookPath(candidate); err == nil {
			hashBin = candidate
			require.NoError(t, os.Symlink(p, filepath.Join(binDir, candidate)))
			break
		}
	}
	if hashBin == "" {
		t.Skip("neither sha256sum nor shasum found on PATH; skipping jq-less fallback test")
	}

	require.NoError(t, os.WriteFile(filepath.Join(binDir, "curl"), []byte(fakeCurl), 0o755))
	return binDir
}

// runWithoutJQ runs scriptPath the same way run does — including waiting for
// the whole process group to exit before reading the capture file — but with
// a PATH built from scratch out of symlinks to only the binaries the jq-less
// fallback needs, so the fallback code path actually executes instead of the
// jq path.
func (e *hookEnv) runWithoutJQ(t *testing.T, scriptPath, payload string, extraEnv ...string) (int, []map[string]any) {
	t.Helper()

	binDir := buildJQLessBinDir(t)
	bashBin := filepath.Join(binDir, "bash")

	exitCode, pgid := e.exec(t, bashBin, scriptPath, payload, binDir, nil, extraEnv...)
	waitForGroupExit(t, pgid)
	return exitCode, e.readBodies(t)
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

func TestHookScript_SilentWhenNoSkillName(t *testing.T) {
	requireJQ(t)

	scriptPath, err := hooks.WriteHookScript(t.TempDir())
	require.NoError(t, err)

	env := newHookEnv(t)
	code, bodies := env.run(t, scriptPath,
		`{"tool_name":"Bash","tool_input":{"command":"ls -la"}}`,
		"SKAEL_AGENT=codex")

	assert.Equal(t, 0, code, "hook must never fail the tool call")
	assert.Empty(t, bodies, "a tool call with no skill name must post nothing")
}

func TestHookScript_IgnoresUnrelatedNameParameter(t *testing.T) {
	requireJQ(t)

	scriptPath, err := hooks.WriteHookScript(t.TempDir())
	require.NoError(t, err)

	env := newHookEnv(t)
	code, bodies := env.run(t, scriptPath,
		`{"tool_name":"Read","tool_input":{"name":"config.json"}}`,
		"SKAEL_AGENT=codex")

	assert.Equal(t, 0, code)
	assert.Empty(t, bodies, "another tool's name parameter is not a skill activation")
}

func TestHookScript_IgnoresNonSkillTools(t *testing.T) {
	requireJQ(t)

	scriptPath, err := hooks.WriteHookScript(t.TempDir())
	require.NoError(t, err)

	env := newHookEnv(t)
	code, bodies := env.run(t, scriptPath,
		`{"tool_name":"apply_patch","tool_input":{"skill_name":"whatever"}}`,
		"SKAEL_AGENT=codex")

	assert.Equal(t, 0, code)
	assert.Empty(t, bodies, "only skill-invocation tools produce activations")
}

func TestHookScript_RejectsMalformedSkillName(t *testing.T) {
	requireJQ(t)

	scriptPath, err := hooks.WriteHookScript(t.TempDir())
	require.NoError(t, err)

	for _, name := range []string{"../../etc/passwd", "Not A Skill", "trailing-", ""} {
		env := newHookEnv(t)
		payload := `{"tool_name":"Skill","tool_input":{"skill":` + strconv.Quote(name) + `}}`
		code, bodies := env.run(t, scriptPath, payload, "SKAEL_AGENT=claude-code")

		assert.Equal(t, 0, code, "name %q", name)
		assert.Emptyf(t, bodies, "malformed skill name %q must not be posted", name)
	}
}

// TestHookScript_IgnoresToolInputNameFallback isolates the .tool_input.name
// removal specifically: tool_name "Skill" passes the tool-name gate on its
// own, so the only thing standing between this payload and a false-positive
// post is whether the skill-name jq filter still falls back to
// .tool_input.name. A regression that reintroduced that fallback would slip
// past every other test in this file but must fail this one.
func TestHookScript_IgnoresToolInputNameFallback(t *testing.T) {
	requireJQ(t)

	scriptPath, err := hooks.WriteHookScript(t.TempDir())
	require.NoError(t, err)

	env := newHookEnv(t)
	code, bodies := env.run(t, scriptPath,
		`{"tool_name":"Skill","tool_input":{"name":"whatever"}}`,
		"SKAEL_AGENT=claude-code")

	assert.Equal(t, 0, code)
	assert.Empty(t, bodies, "tool_input.name is not a skill name; the extraction filter must not fall back to it")
}

// TestHookScript_FallbackGatesOnToolKeyToo exercises the grep/sed fallback
// path used when jq is not on PATH. The fallback must gate on both "tool_name"
// and "tool" the same way the jq filter (.tool_name // .tool // "") does —
// otherwise a payload using "tool" instead of "tool_name" falls through the
// allowlist's empty-string branch on machines without jq and gets its
// tool_input.skill_name posted as if it were a real skill invocation.
func TestHookScript_FallbackGatesOnToolKeyToo(t *testing.T) {
	scriptPath, err := hooks.WriteHookScript(t.TempDir())
	require.NoError(t, err)

	env := newHookEnv(t)
	code, bodies := env.runWithoutJQ(t, scriptPath,
		`{"tool":"apply_patch","tool_input":{"skill_name":"whatever"}}`,
		"SKAEL_AGENT=codex")

	assert.Equal(t, 0, code)
	assert.Empty(t, bodies, "the jq-less fallback must reject apply_patch via the \"tool\" key, not just \"tool_name\"")
}

func TestHookScript_ReportsToolInvocationSource(t *testing.T) {
	requireJQ(t)

	scriptPath, err := hooks.WriteHookScript(t.TempDir())
	require.NoError(t, err)

	env := newHookEnv(t)
	_, bodies := env.run(t, scriptPath,
		`{"tool_name":"Skill","tool_input":{"skill":"brainstorming"}}`,
		"SKAEL_AGENT=claude-code")

	require.Len(t, bodies, 1)
	assert.Equal(t, "tool_invocation", bodies[0]["event_source"])
}

func TestCursorStopScript_ReportsTranscriptScanSource(t *testing.T) {
	requireJQ(t)

	scriptPath, err := hooks.WriteCursorStopScript(t.TempDir())
	require.NoError(t, err)

	transcript := filepath.Join(t.TempDir(), "transcript.json")
	require.NoError(t, os.WriteFile(transcript,
		[]byte(`{"messages":["read skills/brainstorming/SKILL.md for guidance"]}`), 0o644))

	env := newHookEnv(t)
	payload := `{"transcript_path":` + strconv.Quote(transcript) + `,"cwd":"/tmp/project"}`
	code, bodies := env.run(t, scriptPath, payload)

	require.Equal(t, 0, code)
	require.Len(t, bodies, 1)
	assert.Equal(t, "brainstorming", bodies[0]["skill_name"])
	assert.Equal(t, "cursor", bodies[0]["agent"])
	assert.Equal(t, "transcript_scan", bodies[0]["event_source"])
}

func TestOpenCodePlugin_ReportsToolInvocationSource(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "plugins", "skael-tracking.ts")

	require.NoError(t, hooks.InstallForAgent("opencode", configPath, "https://skael.test", "key", "/unused"))

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "tool_invocation",
		"the OpenCode plugin must label its events as tool invocations")
}
