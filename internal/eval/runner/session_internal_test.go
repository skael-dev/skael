package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
	"github.com/skael-dev/skael/internal/eval/store"
)

// TestOutcomeFromRecord_EmptyArtifactDirDoesNotReadTheCwd pins a regression:
// filepath.Join("", gradingFileName) resolves to the bare relative filename,
// which os.Open resolves against the process's cwd rather than failing. A
// resumed run with no recorded ArtifactDir must fall back to the store's own
// columns, not silently pick up an unrelated grading.json sitting in
// whatever directory the test (or whetstone eval) happens to run from.
func TestOutcomeFromRecord_EmptyArtifactDirDoesNotReadTheCwd(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	strayPath := filepath.Join(wd, gradingFileName)
	if _, err := os.Stat(strayPath); err == nil {
		t.Fatalf("refusing to run: %s already exists", strayPath)
	}
	if err := os.WriteFile(strayPath, []byte(`{"reason":"wrong task's reason leaking in"}`), 0o644); err != nil {
		t.Fatalf("staging a stray grading.json in cwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(strayPath) })

	rec := store.RunRecord{
		Outcome: store.RunOutcome{
			ArtifactDir:  "",
			InputTokens:  10,
			OutputTokens: 20,
			DurationMS:   1000,
			AgentVersion: "v1",
		},
	}

	out := outcomeFromRecord(rec)

	if out.Reason != "" {
		t.Errorf("Reason = %q, want empty: the stray cwd grading.json must not have been read", out.Reason)
	}
	if !out.MetaPartial {
		t.Error("MetaPartial = false, want true: an empty ArtifactDir must fall back to the store columns")
	}
	if out.Meta.InputTokens != 10 || out.Meta.OutputTokens != 20 {
		t.Errorf("Meta = %+v, want the store columns carried through", out.Meta)
	}
}

// TestAuthMounts_RewritesHomeAndDropsMissingEntries pins the fix for the
// defect where auth directories were mounted at their host path on both
// sides: the container's HOME is imagespec.ContainerHome, not the host's, so
// a "~/..." entry must resolve differently for HostPath (the host's own
// home) and ContainerPath (the image's "runner" home) — mounting the host
// path verbatim on both sides put every credential where the container-side
// CLI never looks, so no session could ever authenticate.
func TestAuthMounts_RewritesHomeAndDropsMissingEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("staging ~/.claude: %v", err)
	}

	var logs []string
	logf := func(format string, args ...any) { logs = append(logs, format) }

	mounts, err := authMounts([]string{"~/.claude", "~/.config/claude"}, logf)
	if err != nil {
		t.Fatalf("authMounts: %v", err)
	}

	// The non-existent "~/.config/claude" entry must be dropped, not passed
	// through as a bind-mount source that would error the run or have Docker
	// silently create a root-owned directory on the host.
	if len(mounts) != 1 {
		t.Fatalf("mounts = %+v, want exactly the one existing entry", mounts)
	}
	if len(logs) != 1 {
		t.Errorf("expected the missing auth dir to be logged as skipped, got %d log lines", len(logs))
	}

	m := mounts[0]
	if m.HostPath != claudeDir {
		t.Errorf("HostPath = %q, want %q (the host's own home)", m.HostPath, claudeDir)
	}
	wantContainer := filepath.Join(imagespec.ContainerHome, ".claude")
	if m.ContainerPath != wantContainer {
		t.Errorf("ContainerPath = %q, want %q (the image's runner home)", m.ContainerPath, wantContainer)
	}
	if !m.ReadOnly {
		t.Error("auth mount must be read-only")
	}
}

// TestAuthEnv_ForwardsOnlySetNames pins the contract that authEnv only
// forwards names that are actually set and non-empty in the worker's own
// environment, in "NAME=value" form, and that an adapter declaring no
// AuthEnv names yields nothing.
func TestAuthEnv_ForwardsOnlySetNames(t *testing.T) {
	t.Setenv("SKAEL_TEST_AUTH_SET", "super-secret-value")
	t.Setenv("SKAEL_TEST_AUTH_EMPTY", "")
	// Deliberately leave SKAEL_TEST_AUTH_UNSET unset.

	env := authEnv([]string{"SKAEL_TEST_AUTH_SET", "SKAEL_TEST_AUTH_EMPTY", "SKAEL_TEST_AUTH_UNSET"})

	if len(env) != 1 {
		t.Fatalf("authEnv = %v, want exactly one forwarded var", env)
	}
	if env[0] != "SKAEL_TEST_AUTH_SET=super-secret-value" {
		t.Errorf("authEnv[0] = %q, want %q", env[0], "SKAEL_TEST_AUTH_SET=super-secret-value")
	}

	if got := authEnv(nil); got != nil {
		t.Errorf("authEnv(nil) = %v, want nil", got)
	}
	if got := authEnv([]string{}); got != nil {
		t.Errorf("authEnv([]) = %v, want nil", got)
	}
}

// stubAdapter is an agent.Adapter that declares only the auth capabilities
// resolveAuth reads; every other method panics, so a test that accidentally
// exercises more than intended fails loudly rather than silently.
type stubAdapter struct{ caps agent.Caps }

func (s stubAdapter) Name() string     { return "stub" }
func (s stubAdapter) Caps() agent.Caps { return s.caps }
func (s stubAdapter) InstallSkill(string, string) error {
	panic("stubAdapter.InstallSkill: not used by resolveAuth")
}
func (s stubAdapter) Invoke(context.Context, agent.InvokeSpec) (agent.RawStream, error) {
	panic("stubAdapter.Invoke: not used by resolveAuth")
}
func (s stubAdapter) Parse(agent.RawStream) (*agent.Result, error) {
	panic("stubAdapter.Parse: not used by resolveAuth")
}

// TestResolveAuth_EnvVarsSuppressTheHostCredentialMounts pins the precedence
// that a real run established the hard way: with both configured, the agent
// CLI inside the sandbox preferred the mounted host credentials and failed
// with "401 OAuth access token has expired" — while the environment pointed
// at a working gateway. Whatever is configured must win over whatever happens
// to be in the operator's home directory.
func TestResolveAuth_EnvVarsSuppressTheHostCredentialMounts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SKAEL_TEST_TOKEN", "from-the-environment")

	a := stubAdapter{caps: agent.Caps{
		AuthDirs: []string{"~/.claude"},
		AuthEnv:  []string{"SKAEL_TEST_TOKEN"},
	}}

	mounts, env, err := resolveAuth(a, func(string, ...any) {})
	if err != nil {
		t.Fatalf("resolveAuth: %v", err)
	}
	if len(mounts) != 0 {
		t.Fatalf("mounts = %+v, want none: a set auth env var must suppress the host mounts", mounts)
	}
	if len(env) != 1 || env[0] != "SKAEL_TEST_TOKEN=from-the-environment" {
		t.Fatalf("env = %+v, want the single configured variable", env)
	}
}

// TestResolveAuth_FallsBackToMountsWhenNoEnvVarIsSet keeps the local-development
// path working: with nothing in the environment, an existing credential
// directory is still mounted.
func TestResolveAuth_FallsBackToMountsWhenNoEnvVarIsSet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	a := stubAdapter{caps: agent.Caps{
		AuthDirs: []string{"~/.claude"},
		AuthEnv:  []string{"SKAEL_TEST_TOKEN_DEFINITELY_UNSET"},
	}}

	mounts, env, err := resolveAuth(a, func(string, ...any) {})
	if err != nil {
		t.Fatalf("resolveAuth: %v", err)
	}
	if len(env) != 0 {
		t.Fatalf("env = %+v, want none", env)
	}
	if len(mounts) != 1 {
		t.Fatalf("mounts = %+v, want the host credential dir mounted", mounts)
	}
}
