//go:build docker

// Package whetstone_e2e drives the built binary the way a person does: from
// inside a bundle directory, with arguments a shell would produce, against a
// real daemon.
//
// Every defect this file exists to catch was invisible to a green unit suite on
// the previous phase: a default timeout that killed the longest call, a `.`
// argument that could never match a frontmatter name, and a closed stdin that
// errored instead of declining a prompt. None of them are reachable from a test
// that calls the package function directly.
package whetstone_e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// binOnce/binPath memoize the build across every test in this binary: each
// test that needs the CLI calls binary(t) independently (that is the shape
// the tests are written in), and rebuilding a Go binary per call would add
// minutes doing nothing this file is about.
var (
	binOnce sync.Once
	binPath string
)

// binary builds whetstone once per test binary, exactly as the release does.
func binary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		out := filepath.Join(os.TempDir(), fmt.Sprintf("whetstone-e2e-%d", os.Getpid()))
		cmd := exec.Command("go", "build", "-o", out, "../../cmd/whetstone")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("building whetstone: %v\n%s", err, b)
		}
		binPath = out
	})
	if binPath == "" {
		t.Fatal("whetstone binary is unavailable: an earlier build in this test binary failed")
	}
	return binPath
}

// run executes the binary in dir with stdin closed, which is what a CI job
// provides.
func run(t *testing.T, bin, dir string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Stdin = nil
	b, err := cmd.CombinedOutput()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running %v: %v\n%s", args, err, b)
	}
	return string(b), code
}

func TestWhetstone_InitDoctorAndSuiteCheckFromInsideTheBundle(t *testing.T) {
	bin := binary(t)
	root := t.TempDir()

	if out, code := run(t, bin, root, "init"); code != 0 {
		t.Fatalf("init exited %d:\n%s", code, out)
	}
	// The parked 3a defect: init nesting inside an existing workspace silently
	// shadowed the outer one.
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, code := run(t, bin, sub, "init"); code == 0 {
		t.Errorf("init nested inside an existing workspace:\n%s", out)
	}

	out, code := run(t, bin, root, "doctor")
	if code != 0 {
		t.Fatalf("doctor exited %d:\n%s", code, out)
	}
	for _, want := range []string{"docker", "agent adapters", "claude-code"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor does not report %q:\n%s", want, out)
		}
	}
}

func TestWhetstone_LintAndPackFromInsideTheBundleDirectory(t *testing.T) {
	bin := binary(t)
	root := t.TempDir()
	if _, code := run(t, bin, root, "init"); code != 0 {
		t.Fatal("init failed")
	}
	bundleDir := seedCorpusBundle(t, root, "deterministic-transform")

	// `cd bundle && lint .` is the invocation the previous phase's manual step
	// skipped, and it is the one that failed for every clean bundle.
	if out, code := run(t, bin, bundleDir, "lint", "."); code != 0 {
		t.Errorf("lint . exited %d from inside the bundle:\n%s", code, out)
	}
	out, code := run(t, bin, bundleDir, "pack", ".")
	if code != 0 {
		t.Fatalf("pack . exited %d:\n%s", code, out)
	}
	// And again, because packing must be idempotent: the archive must not become
	// bundle content the next lint chokes on.
	if out, code := run(t, bin, bundleDir, "pack", "."); code != 0 {
		t.Errorf("a second pack . exited %d:\n%s", code, out)
	}
	if out, code := run(t, bin, bundleDir, "lint", "."); code != 0 {
		t.Errorf("lint . failed after pack:\n%s", out)
	}
}

func TestWhetstone_SuiteCheckRunsRealOraclesInTheSandbox(t *testing.T) {
	bin := binary(t)
	root := t.TempDir()
	if _, code := run(t, bin, root, "init"); code != 0 {
		t.Fatal("init failed")
	}
	skill := seedSkillWithSuite(t, root) // spec + approved + a 3-task suite of real shell

	out, code := run(t, bin, root, "suite", "check", skill)
	// One of the three seeded tasks has a verifier that passes on an empty
	// workspace, so a non-zero exit is the correct outcome and the reason has to
	// be in the output.
	if code == 0 {
		t.Errorf("suite check passed a suite with a non-discriminating verifier:\n%s", out)
	}
	if !strings.Contains(out, "without") {
		t.Errorf("suite check did not explain the void task:\n%s", out)
	}

	if out, code := run(t, bin, root, "suite", "check", skill, "--allow-void"); code != 0 {
		t.Errorf("--allow-void exited %d:\n%s", code, out)
	}
}

func TestWhetstone_EvalWithNoGatewaySaysWhatToDo(t *testing.T) {
	bin := binary(t)
	root := t.TempDir()
	if _, code := run(t, bin, root, "init"); code != 0 {
		t.Fatal("init failed")
	}
	skill := seedSkillWithSuite(t, root)

	cmd := exec.Command(bin, "eval", skill, "--tier", "smoke")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH=/usr/bin:/bin", "ANTHROPIC_API_KEY=")
	b, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("eval succeeded with no gateway and no agent CLI:\n%s", b)
	}
	// A stack trace or a bare "not found" here is the difference between a user
	// fixing their setup and filing a bug.
	if !strings.Contains(string(b), "doctor") {
		t.Errorf("the failure does not point at `whetstone doctor`:\n%s", b)
	}
	if strings.Contains(string(b), "goroutine") {
		t.Errorf("eval panicked rather than reporting:\n%s", b)
	}
}

func TestWhetstone_EvalIsResumableAndItsJSONParses(t *testing.T) {
	bin := binary(t)
	root := t.TempDir()
	if _, code := run(t, bin, root, "init"); code != 0 {
		t.Fatal("init failed")
	}
	// seedSkillWithSuite bakes a stub `claude` earlier on PATH than anything
	// else inside the image: a shell script that echoes the recorded
	// stream-json fixture. Everything above the CLI is real — argv, streaming,
	// parsing, artifacts, scoring, resume — and only authentication is not.
	skill := seedSkillWithSuite(t, root)

	// The stub is baked into a base image derived from the CI slim base, not
	// into the shared whetstone-base-ci:1 tag itself, so it cannot bleed into
	// any other test in this package or in internal/eval/... that also runs
	// against the slim tag. t.Setenv restores WHETSTONE_BASE_TAG when this
	// test ends.
	t.Setenv("WHETSTONE_BASE_TAG", stubClaudeBaseTag(t))

	out, code := run(t, bin, root, "eval", skill, "--tier", "smoke", "--allow-void", "--json")
	if code != 0 {
		t.Fatalf("eval exited %d:\n%s", code, out)
	}
	var first struct {
		Headline float64 `json:"headline"`
		EvalID   int64   `json:"eval_id"`
		SuiteRef string  `json:"suite_ref"`
	}
	if err := json.Unmarshal([]byte(lastJSONLine(t, out)), &first); err != nil {
		t.Fatalf("--json output does not parse: %v\n%s", err, out)
	}
	if first.SuiteRef == "" || first.EvalID == 0 {
		t.Errorf("eval reported no suite ref or id: %+v", first)
	}

	// RunEvalWith (cli/whetstone/eval.go) writes the sidecar report files under
	// eval/reports/<eval-id>/, not directly under eval/ — the brief's own draft
	// of this test assumed the latter, which does not exist on disk.
	sidecar := filepath.Join(root, ".whetstone", "skills", skill, "eval", "reports", strconv.FormatInt(first.EvalID, 10))
	for _, name := range []string{"report.json", "report.html"} {
		if _, err := os.Stat(filepath.Join(sidecar, name)); err != nil {
			t.Errorf("%s was not written: %v", name, err)
		}
	}

	out, code = run(t, bin, root, "eval", skill, "--tier", "smoke", "--allow-void",
		"--resume", strconv.FormatInt(first.EvalID, 10), "--json")
	if code != 0 {
		t.Fatalf("resume exited %d:\n%s", code, out)
	}
	var second struct {
		Headline float64 `json:"headline"`
		EvalID   int64   `json:"eval_id"`
	}
	if err := json.Unmarshal([]byte(lastJSONLine(t, out)), &second); err != nil {
		t.Fatalf("resumed --json output does not parse: %v\n%s", err, out)
	}
	// A resume that produces a new eval id breaks every trend that referenced
	// the first one, and a resume that produces a different headline from
	// cached sessions means the cache is not keyed on what it claims to be.
	if second.EvalID != first.EvalID {
		t.Errorf("resume created eval %d, want the existing %d", second.EvalID, first.EvalID)
	}
	if second.Headline != first.Headline {
		t.Errorf("resumed headline %v, want %v", second.Headline, first.Headline)
	}
}

// lastJSONLine returns the final line of output that parses as a JSON object,
// so a progress line printed before the result does not break the parse.
// lastJSONLine returns the last complete JSON value in out, so any progress
// line printed before the result (or noise merged in from stderr) does not
// break the parse.
//
// ui.PrintJSON (internal/ui/json.go) indents its output — "--json" is never
// actually one line — so a scan for a single line that parses whole, which is
// what this function's name and doc originally promised, would fail on every
// real invocation. json.Decoder handles that for free: it consumes leading
// whitespace and, decoding repeatedly from the first "{", keeps the last
// complete top-level value it can read off the stream.
func lastJSONLine(t *testing.T, out string) string {
	t.Helper()
	trimmed := strings.TrimSpace(out)
	start := strings.IndexByte(trimmed, '{')
	if start < 0 {
		t.Fatalf("no JSON object in output:\n%s", out)
	}
	dec := json.NewDecoder(strings.NewReader(trimmed[start:]))
	var last string
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			break
		}
		last = string(raw)
	}
	if last == "" {
		t.Fatalf("no JSON object in output:\n%s", out)
	}
	return last
}

// seedCorpusBundle copies one of the golden-corpus bundles from
// internal/eval/testdata/corpus/ into <root>/.whetstone/skills/<archetype>/ —
// everything a shipped bundle carries (SKILL.md, spec.yaml, scripts/, and the
// eval/ sidecar) except expected-lint.json, which is grading metadata for the
// corpus regression suite and not bundle content; a real bundle never has it,
// and lint has no exclusion for it, so copying it in would flag as a stray
// root file the corpus author never shipped. Returns the bundle directory.
func seedCorpusBundle(t *testing.T, root, archetype string) string {
	t.Helper()
	src := filepath.Join("..", "..", "internal", "eval", "testdata", "corpus", archetype)
	dst := filepath.Join(root, ".whetstone", "skills", archetype)

	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "expected-lint.json" {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatalf("seeding corpus bundle %q: %v", archetype, err)
	}
	return dst
}

// evalSkill is the skill name seedSkillWithSuite writes. Fixed rather than
// derived per-test: every test using it gets its own workspace (t.TempDir()),
// so there is no collision to avoid.
const evalSkill = "e2e-answer-writer"

// seedSkillWithSuite writes an approved spec and a hand-written three-verdict
// suite for evalSkill, records a suite check for it (so `eval` will run at
// all — see RunEvalWith's step 2), and returns the skill name.
//
// The suite mirrors internal/eval/suite/check_docker_test.go's realSuite: one
// task kind whose oracle solves the task and whose verifier discriminates
// (repeated five times — smoke tier's budget requires five eligible dev
// tasks, so a literal three-task suite can never clear BuildPlan), one task
// whose oracle is broken, and one whose verifier passes on an untouched
// workspace. The suite check this function runs is expected to fail (two of
// seven tasks are deliberately void) — that is by design, exercised again and
// asserted on explicitly by TestWhetstone_SuiteCheckRunsRealOraclesInTheSandbox
// — but suite.Check records every task's result regardless of the command's
// own exit code, and that recorded result is all `eval` requires.
func seedSkillWithSuite(t *testing.T, root string) string {
	t.Helper()
	skill := evalSkill

	st, err := store.Open(root)
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}

	sp := &spec.SkillSpec{
		Name:        skill,
		Purpose:     "Write a fixed acknowledgement file, so an eval has something deterministic to check.",
		Description: "Use when a task asks to write an ok acknowledgement into answer.txt.",
		Triggers: []spec.TriggerPhrase{
			{Text: "Write ok to answer.txt."},
		},
		Steps: []spec.Step{
			{ID: "write-answer", Action: "Write ok to answer.txt.", Postcondition: "answer.txt exists and contains ok."},
		},
		TargetTier: spec.TierFloor,
	}
	if _, err := st.SaveSpec(sp); err != nil {
		t.Fatalf("saving spec for %s: %v", skill, err)
	}

	contractPath, err := st.ContractPath(skill)
	if err != nil {
		t.Fatalf("resolving contract path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(contractPath), 0o755); err != nil {
		t.Fatalf("creating eval sidecar: %v", err)
	}
	// No steps or semantic rules: an empty contract is vacuously satisfied by
	// drift.Score, so this exercises real scoring without needing a judge.
	if err := os.WriteFile(contractPath, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatalf("writing contract: %v", err)
	}

	suiteDir, err := st.SuiteDir(skill)
	if err != nil {
		t.Fatalf("resolving suite dir: %v", err)
	}
	s := &suite.Suite{Tasks: seedSuiteTasks()}
	if err := s.Write(suiteDir); err != nil {
		t.Fatalf("writing suite: %v", err)
	}

	if err := st.Close(); err != nil {
		t.Fatalf("closing store: %v", err)
	}

	bin := binary(t)
	if out, code := run(t, bin, root, "spec", "approve", skill); code != 0 {
		t.Fatalf("spec approve %s exited %d:\n%s", skill, code, out)
	}
	// Ignoring the exit code deliberately: two of the seven tasks below are
	// void by construction, so a clean exit is not the point here — recording
	// the check is.
	run(t, bin, root, "suite", "check", skill)

	return skill
}

// answerOracle and answerVerifier are the "sound" shape: the oracle writes
// the file its verifier checks for, and neither side is broken.
const (
	answerOracle   = "#!/bin/bash\nset -e\necho ok > answer.txt\n"
	answerVerifier = "#!/bin/bash\nset -e\ntest -f answer.txt\ngrep -q ok answer.txt\n"
)

// seedSuiteTasks is realSuite's three verdicts (internal/eval/suite/check_docker_test.go),
// with the sound verdict repeated to clear smoke tier's five-eligible-task
// floor (see BuildPlan in internal/eval/runner/plan.go).
func seedSuiteTasks() []suite.TaskPkg {
	tasks := make([]suite.TaskPkg, 0, 7)
	for i := 1; i <= 5; i++ {
		tasks = append(tasks, suite.TaskPkg{
			ID:       fmt.Sprintf("sound-%d", i),
			Kind:     "happy",
			Split:    "dev",
			PromptMD: fmt.Sprintf("write ok to answer.txt (case %d)", i),
			Oracle:   answerOracle,
			Verifier: answerVerifier,
		})
	}
	tasks = append(tasks,
		suite.TaskPkg{
			ID:       "broken-oracle",
			Kind:     "happy",
			Split:    "dev",
			PromptMD: "an oracle that cannot solve its own task",
			Oracle:   "#!/bin/bash\nexit 1\n",
			Verifier: "#!/bin/bash\nset -e\ntest -f answer.txt\n",
		},
		suite.TaskPkg{
			ID:       "toothless-verifier",
			Kind:     "happy",
			Split:    "dev",
			PromptMD: "a verifier that asserts nothing",
			Oracle:   answerOracle,
			Verifier: "#!/bin/bash\ntrue\n",
		},
	)
	return tasks
}

// stubClaudeFixture is the recorded stream-json session the stub `claude`
// replays: a real transcript the claudecode adapter already parses in
// internal/eval/agent/claudecode/parse_test.go, so the stub exercises the
// real parser rather than a hand-rolled shape that only looks like one.
const stubClaudeFixture = "../../internal/eval/agent/claudecode/testdata/basic-tools.jsonl"

// stubClaudeTag is the tag the derived image is built under. Content-fixed
// rather than digested: the fixture and the wrapper script are both
// committed, so "the tag exists" and "it has today's stub" are the same
// question — this file's own git history is what invalidates it.
const stubClaudeTag = "whetstone-e2e-stub-claude:2"

var stubClaudeOnce sync.Once

// stubClaudeBaseTag builds (once per test binary, and only if the tag is not
// already cached from a previous run) a base image derived from
// whetstone-base-ci:1 with `claude` replaced by a script that ignores its
// arguments and prints the recorded fixture to stdout. It returns the tag to
// pass as WHETSTONE_BASE_TAG.
//
// This is not the mechanism the suite's own environment/Dockerfile.frag
// fragment implies it would be: TaskPkg.EnvFrag round-trips through
// suite.Write/Load and is validated by imagespec.ValidateFragment, but
// neither cli/whetstone/eval.go nor cli/whetstone/suitecheck.go ever copies a
// task's EnvFrag into the sandbox.EnvSpec passed to Driver.Prepare — grep
// confirms the only two production call sites construct EnvSpec from Skill,
// Deps, and BaseTag alone. A task-declared environment fragment is
// consequently dead in every real `eval` and `suite check` run today; it is
// only exercised by imagespec's own unit tests and the suite package's
// write/load round trip. Building a derived base image and pointing
// WHETSTONE_BASE_TAG at it sidesteps that gap rather than fixing it — fixing
// it means deciding how a per-task fragment should combine into the one
// image a whole skill's panel shares, which is a design question outside
// this task's scope. See the report for the finding written up in full.
func stubClaudeBaseTag(t *testing.T) string {
	t.Helper()
	stubClaudeOnce.Do(func() {
		if err := exec.Command("docker", "image", "inspect", stubClaudeTag).Run(); err == nil {
			return
		}

		fixture, err := filepath.Abs(stubClaudeFixture)
		if err != nil {
			t.Fatalf("resolving stub fixture path: %v", err)
		}
		if _, err := os.Stat(fixture); err != nil {
			t.Fatalf("stub fixture missing: %v", err)
		}

		buildDir := t.TempDir()
		fixtureData, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatalf("reading stub fixture: %v", err)
		}
		// The recorded fixture carries a genuine rate_limit_event line: a real
		// transient the live session hit and recovered from mid-stream. The
		// runner treats that event as reason to discard and retry the whole
		// invocation (internal/eval/runner/session.go), which is the right
		// call for a live agent — a retried call gets a fresh stream that may
		// not repeat the transient — but is wrong for a canned replay: this
		// stub answers identically every attempt, so the event never clears
		// and the runner burns its three retries and fails every single
		// session. Stripping it here is a property of the stub, not a fix to
		// the runner: a live re-invocation would not carry the same stale
		// blip forward the way a fixed transcript does. This is independent
		// of, but adjacent to, the parser-level fix in
		// internal/eval/agent/claudecode/parse.go's rateLimitInfo, which this
		// suite's own debugging surfaced: that fix is what stops an
		// "allowed"-status rate_limit_event (this fixture's own line included)
		// from being misread as a hit in the first place.
		var filtered []byte
		for _, line := range bytes.Split(fixtureData, []byte("\n")) {
			if bytes.Contains(line, []byte(`"type":"rate_limit_event"`)) {
				continue
			}
			filtered = append(filtered, line...)
			filtered = append(filtered, '\n')
		}
		if err := os.WriteFile(filepath.Join(buildDir, "session.jsonl"), filtered, 0o644); err != nil {
			t.Fatalf("staging stub fixture: %v", err)
		}

		dockerfile := "FROM whetstone-base-ci:1\n" +
			"USER root\n" +
			"COPY session.jsonl /opt/whetstone-stub/session.jsonl\n" +
			"RUN printf '#!/bin/bash\\ncat /opt/whetstone-stub/session.jsonl\\n' > /usr/bin/claude " +
			"&& chmod +x /usr/bin/claude\n" +
			"USER runner\n"
		if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
			t.Fatalf("writing stub Dockerfile: %v", err)
		}

		cmd := exec.Command("docker", "build", "-t", stubClaudeTag, buildDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("building stub claude image: %v\n%s", err, out)
		}
	})
	return stubClaudeTag
}
