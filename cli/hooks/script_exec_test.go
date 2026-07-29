package hooks_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// postWaitCeiling bounds how long we poll the capture file for an expected
// POST. Landing a body is just a local file write plus process scheduling —
// milliseconds in practice — so this is set generously (tens of seconds)
// purely to absorb scheduler noise under a loaded, parallel `go test ./...`.
// It costs nothing in the common case: every wait loop below returns the
// instant its condition is satisfied rather than sleeping the full ceiling.
const postWaitCeiling = 20 * time.Second

// pollInterval is how often we re-check the capture file while waiting.
const pollInterval = 10 * time.Millisecond

// sentinelSkillName is a reserved skill name used only by the sentinel
// barrier invocation (see (*hookEnv).run). It must not collide with any
// skill name used elsewhere in this file's test payloads.
const sentinelSkillName = "skael-test-sentinel-barrier"

// sentinelPayload is a Skill invocation guaranteed to pass every gate in the
// hook scripts and produce exactly one POST.
func sentinelPayload() string {
	return `{"tool_name":"Skill","tool_input":{"skill":"` + sentinelSkillName + `"}}`
}

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
// and environment, and returns its exit code. It does not wait for any
// backgrounded child the script may have forked — callers decide how (or
// whether) to wait for the capture file.
func (e *hookEnv) exec(t *testing.T, bashBin, scriptPath, payload, path string, baseEnv []string, extraEnv ...string) int {
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

	if err := cmd.Run(); err != nil {
		exitErr, ok := err.(*exec.ExitError)
		require.Truef(t, ok, "running %s: %v", scriptPath, err)
		return exitErr.ExitCode()
	}
	return 0
}

// tryReadBodies attempts to parse the capture file's current contents. It is
// used while polling, where a partial write (the fake curl script's printf
// landing mid-append) is expected transiently — a parse failure here just
// means "not ready yet", not a test failure. ok is false if the file can't
// be read yet or a record fails to parse.
func (e *hookEnv) tryReadBodies() (bodies []map[string]any, ok bool) {
	data, err := os.ReadFile(e.capture)
	if err != nil {
		return nil, false
	}
	for _, record := range strings.Split(string(data), "\x00") {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(record), &body); err != nil {
			return nil, false
		}
		bodies = append(bodies, body)
	}
	return bodies, true
}

// readBodies is the authoritative, non-lenient read used once a wait loop
// has finished: any parse failure at this point is a real test failure, not
// a transient partial write.
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

// waitUntil polls the capture file until satisfied returns true for its
// current contents, or postWaitCeiling elapses. Either way it finishes with
// an authoritative (non-lenient) read.
func (e *hookEnv) waitUntil(t *testing.T, satisfied func([]map[string]any) bool) []map[string]any {
	t.Helper()

	deadline := time.Now().Add(postWaitCeiling)
	for {
		if bodies, ok := e.tryReadBodies(); ok && satisfied(bodies) {
			return bodies
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(pollInterval)
	}
	return e.readBodies(t)
}

// run executes scriptPath with payload and asserts (by construction) that it
// produced no POST. Rather than waiting out a fixed timeout — which either
// wastes time when nothing was ever going to arrive, or is dangerously prone
// to a false pass when a slow POST lands after a short one expires — it
// proves the absence with a happens-after barrier:
//
// After the payload's script exits, run runs a second, independent
// invocation of the same script with a payload guaranteed to POST (the
// "sentinel"), then waits (bounded by postWaitCeiling, but typically
// resolving in milliseconds) for the sentinel's specific body to land.
//
// This is sound because every no-POST branch in the hook scripts under test
// (script.go, cursor_script.go) exits before the backgrounded curl is ever
// forked — there is no leftover in-flight background job from the payload
// run that could still land afterward. So once the sentinel's body is seen
// and nothing else has appeared, the payload demonstrably posted nothing.
// If a future change to the scripts introduced a POST-then-maybe-abort path,
// this assumption would need revisiting, but no such path exists today.
func (e *hookEnv) run(t *testing.T, scriptPath, payload string, extraEnv ...string) (int, []map[string]any) {
	t.Helper()

	path := e.binDir + string(os.PathListSeparator) + os.Getenv("PATH")
	baseEnv := os.Environ()

	exitCode := e.exec(t, "bash", scriptPath, payload, path, baseEnv, extraEnv...)

	sentinelExit := e.exec(t, "bash", scriptPath, sentinelPayload(), path, baseEnv, "SKAEL_AGENT=skael-test-sentinel")
	require.Equal(t, 0, sentinelExit, "sentinel barrier invocation must exit 0")

	hasSentinel := func(bodies []map[string]any) bool {
		for _, b := range bodies {
			if b["skill_name"] == sentinelSkillName {
				return true
			}
		}
		return false
	}
	bodies := e.waitUntil(t, hasSentinel)
	require.Truef(t, hasSentinel(bodies),
		"sentinel barrier body never landed within %s; cannot prove the payload run posted nothing", postWaitCeiling)

	var payloadBodies []map[string]any
	for _, b := range bodies {
		if b["skill_name"] == sentinelSkillName {
			continue
		}
		payloadBodies = append(payloadBodies, b)
	}
	return exitCode, payloadBodies
}

// runExpectingBodies executes scriptPath with payload and waits for at least
// want POST bodies to land in the capture file, polling rather than sleeping
// a fixed duration: the common case (milliseconds) isn't taxed, and a slow
// scheduler under parallel `go test ./...` still succeeds within
// postWaitCeiling instead of racing a short fixed deadline.
func (e *hookEnv) runExpectingBodies(t *testing.T, scriptPath, payload string, want int, extraEnv ...string) (int, []map[string]any) {
	t.Helper()

	path := e.binDir + string(os.PathListSeparator) + os.Getenv("PATH")
	exitCode := e.exec(t, "bash", scriptPath, payload, path, os.Environ(), extraEnv...)

	bodies := e.waitUntil(t, func(b []map[string]any) bool { return len(b) >= want })
	return exitCode, bodies
}

// jqLessFallbackBins lists the external binaries the grep/sed fallback path
// actually needs, in the order runWithoutJQ resolves them: bash to run the
// script, cat to read stdin, grep/sed/head/cut for field extraction. The
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

// runWithoutJQ runs scriptPath the same way run does — including the
// sentinel happens-after barrier proving no POST occurred — but with a PATH
// built from scratch out of symlinks to only the binaries the jq-less
// fallback needs, so the fallback code path actually executes instead of the
// jq path.
func (e *hookEnv) runWithoutJQ(t *testing.T, scriptPath, payload string, extraEnv ...string) (int, []map[string]any) {
	t.Helper()

	binDir := buildJQLessBinDir(t)
	bashBin := filepath.Join(binDir, "bash")

	exitCode := e.exec(t, bashBin, scriptPath, payload, binDir, nil, extraEnv...)

	sentinelExit := e.exec(t, bashBin, scriptPath, sentinelPayload(), binDir, nil, "SKAEL_AGENT=skael-test-sentinel")
	require.Equal(t, 0, sentinelExit, "sentinel barrier invocation must exit 0")

	hasSentinel := func(bodies []map[string]any) bool {
		for _, b := range bodies {
			if b["skill_name"] == sentinelSkillName {
				return true
			}
		}
		return false
	}
	bodies := e.waitUntil(t, hasSentinel)
	require.Truef(t, hasSentinel(bodies),
		"sentinel barrier body never landed within %s; cannot prove the payload run posted nothing", postWaitCeiling)

	var payloadBodies []map[string]any
	for _, b := range bodies {
		if b["skill_name"] == sentinelSkillName {
			continue
		}
		payloadBodies = append(payloadBodies, b)
	}
	return exitCode, payloadBodies
}

func TestHookScript_PostsSkillActivation(t *testing.T) {
	requireJQ(t)

	scriptPath, err := hooks.WriteHookScript(t.TempDir())
	require.NoError(t, err)

	env := newHookEnv(t)
	code, bodies := env.runExpectingBodies(t, scriptPath,
		`{"tool_name":"Skill","tool_input":{"skill":"brainstorming"}}`, 1,
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
	code, bodies := env.runExpectingBodies(t, scriptPath,
		`{"tool_name":"skills_code-review","tool_input":{"skill":"skills_code-review"}}`, 1,
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
	_, bodies := env.runExpectingBodies(t, scriptPath,
		`{"tool_name":"Skill","tool_input":{"skill":"brainstorming"}}`, 1,
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
	code, bodies := env.runExpectingBodies(t, scriptPath, payload, 1)

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
