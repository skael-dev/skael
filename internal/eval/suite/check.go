package suite

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/skael-dev/skael/internal/eval/sandbox"
)

// VerifierTimeout bounds one oracle or verifier script run — both the oracle
// gate (Check, below) and a session's own post-run verifier in
// runner/session.go run the identical verifier/test.sh. suite owns the
// scripts, so it owns the one bound on how long they may run: these are
// reference scripts a suite author controls, not an agent session, so a few
// minutes is generous headroom rather than a tight budget. A caller with a
// different opinion is a caller that has forgotten these are the same
// script, not a caller with a legitimate reason to run it longer.
const VerifierTimeout = 5 * time.Minute

// CheckResult is one task's verdict from the oracle gate.
type CheckResult struct {
	TaskID string
	// OracleExit is the reference solution's exit code.
	OracleExit int
	// VerifierExit is the verifier's exit code after the oracle ran.
	VerifierExit int
	// BareVerifierExit is the verifier's exit code on an untouched workspace.
	// A zero here means the verifier asserts nothing about the work.
	BareVerifierExit int
	Void             bool
	Reason           string
	Duration         time.Duration
}

// CheckOptions configures a check.
type CheckOptions struct {
	Driver      sandbox.Driver
	Image       sandbox.ImageRef
	SuiteDir    string
	Timeout     time.Duration
	Concurrency int
	Logger      func(format string, args ...any)
	// StageRoot is where per-run task workspaces are created. Empty means
	// os.TempDir(), which is correct for whetstone on an author's host. A
	// containerized caller MUST set it to a path bind-mounted identically on
	// both sides: these directories are bind-mounted into sibling containers,
	// and Docker creates a missing bind source as an empty directory rather
	// than failing — so a container-local path voids every task silently.
	StageRoot string
}

// Check runs the oracle gate over every task.
//
// Three sandbox runs per task, and each of the three answers a different
// question. Does the oracle solve the task — if not, the task is broken and a
// skill that fails it is being blamed for someone else's bug. Does the task's
// own verifier accept its own reference solution — if not, the verifier is
// wrong. And does the verifier reject an untouched workspace — if not, the task
// hands out a free pass, which inflates Reliability for both conditions and
// flattens Uplift to a tie. All three are cheaper to learn here than after
// sixty sessions have been spent.
func Check(ctx context.Context, s *Suite, o CheckOptions) ([]CheckResult, error) {
	if o.Driver == nil {
		return nil, fmt.Errorf("suite.Check: no driver")
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 4
	}
	if o.Logger == nil {
		o.Logger = func(string, ...any) {}
	}

	results := make([]CheckResult, len(s.Tasks))
	sem := make(chan struct{}, o.Concurrency)
	var wg sync.WaitGroup
	var firstErr error
	var mu sync.Mutex

	for i, task := range s.Tasks {
		wg.Add(1)
		go func(i int, task TaskPkg) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			r, err := checkOne(ctx, task, o)
			mu.Lock()
			defer mu.Unlock()
			if err != nil && firstErr == nil {
				firstErr = err
			}
			results[i] = r
		}(i, task)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	sort.Slice(results, func(i, j int) bool { return results[i].TaskID < results[j].TaskID })
	return results, nil
}

func checkOne(ctx context.Context, task TaskPkg, o CheckOptions) (CheckResult, error) {
	r := CheckResult{TaskID: task.ID}
	start := time.Now()
	defer func() { r.Duration = time.Since(start) }()

	src := filepath.Join(o.SuiteDir, "tasks", task.ID)

	// Two workspaces, not three: "solved" and "bare". A shared workspace
	// between the two would let one phase's side effects silently satisfy the
	// other — which is exactly what "bare" exists to rule out — but the
	// oracle and the post-oracle verifier are deliberately staged into the
	// SAME directory (see the comment below). Sharing that one is not a bug;
	// it is the only way to ask "does this task's verifier accept this task's
	// own reference solution."
	solved, err := stageWorkspace(src, o.StageRoot)
	if err != nil {
		return r, err
	}
	defer func() { _ = os.RemoveAll(solved) }()

	run := func(ws, script string) (int, error) {
		res, err := o.Driver.Run(ctx, sandbox.RunSpec{
			Image: o.Image, Workspace: ws, Argv: []string{"bash", script},
			Network: sandbox.NetNone, Timeout: o.Timeout,
		})
		if err != nil {
			return 0, fmt.Errorf("suite.Check %s: %w", task.ID, err)
		}
		if res.TimedOut {
			return 0, fmt.Errorf("suite.Check %s: %s exceeded %s", task.ID, script, o.Timeout)
		}
		return res.ExitCode, nil
	}

	// Setup runs in both workspaces, not just this one: the bare run's whole
	// question is whether the verifier rejects an *unsolved* workspace, and a
	// bare workspace missing the task's own inputs would be rejected for a
	// different reason, which answers nothing.
	setupExit, err := runSetup(run, task, solved)
	if err != nil {
		return r, err
	}
	if setupExit != 0 {
		r.Void = true
		r.Reason = fmt.Sprintf("the setup script failed (exit %d); the task's inputs cannot be created", setupExit)
		return r, nil
	}

	o.Logger("check %s: running oracle", task.ID)
	if r.OracleExit, err = run(solved, "oracle/solve.sh"); err != nil {
		return r, err
	}

	// Deliberately reuses "solved": this run's whole question is whether the
	// verifier accepts the oracle's own output, which it can only see by
	// running in the directory the oracle just wrote to. Do not "fix" this
	// into a fresh copy — that would just be the bare run with extra steps.
	o.Logger("check %s: running verifier against the oracle's workspace", task.ID)
	if r.VerifierExit, err = run(solved, "verifier/test.sh"); err != nil {
		return r, err
	}

	bare, err := stageWorkspace(src, o.StageRoot)
	if err != nil {
		return r, err
	}
	defer func() { _ = os.RemoveAll(bare) }()
	if setupExit, err = runSetup(run, task, bare); err != nil {
		return r, err
	}
	if setupExit != 0 {
		r.Void = true
		r.Reason = fmt.Sprintf("the setup script failed (exit %d); the task's inputs cannot be created", setupExit)
		return r, nil
	}
	o.Logger("check %s: running verifier against a bare workspace", task.ID)
	if r.BareVerifierExit, err = run(bare, "verifier/test.sh"); err != nil {
		return r, err
	}

	switch {
	case r.OracleExit != 0:
		r.Void, r.Reason = true, fmt.Sprintf("the oracle failed (exit %d); the task is unsolvable as written", r.OracleExit)
	case r.VerifierExit != 0:
		r.Void, r.Reason = true, fmt.Sprintf("the verifier rejected its own oracle (exit %d)", r.VerifierExit)
	case r.BareVerifierExit == 0:
		r.Void, r.Reason = true, "the verifier passes without the oracle having run, so it measures nothing"
	}
	return r, nil
}

// SetupScript is the task-relative path of the setup script, shared with the
// runner so the two stages cannot disagree about where it lives.
const SetupScript = "environment/setup.sh"

// StageSetup writes a task's setup script into ws at SetupScript.
//
// It writes from the loaded TaskPkg rather than relying on the staged copy of
// the task directory, which decouples the name that runs from the name on
// disk: a suite written before setup.sh existed holds the same shell under
// the old Dockerfile.frag name, loadSetup reads it, and this puts it where
// both the gate and the runner look. Without that, such a suite would stage a
// workspace with no setup.sh in it and fail on a missing file.
func StageSetup(ws string, task TaskPkg) error {
	if strings.TrimSpace(task.Setup) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(ws, "environment"), dirMode); err != nil {
		return fmt.Errorf("suite: creating environment directory for %q: %w", task.ID, err)
	}
	if err := os.WriteFile(filepath.Join(ws, "environment", "setup.sh"), []byte(task.Setup), scriptMode); err != nil {
		return fmt.Errorf("suite: staging setup script for %q: %w", task.ID, err)
	}
	return nil
}

// runSetup stages and runs a task's setup script in ws, or reports success
// without a run when the task has none — most tasks need no fixtures, and a
// container per task to run nothing is pure cost.
func runSetup(run func(ws, script string) (int, error), task TaskPkg, ws string) (int, error) {
	if strings.TrimSpace(task.Setup) == "" {
		return 0, nil
	}
	if err := StageSetup(ws, task); err != nil {
		return 0, err
	}
	return run(ws, SetupScript)
}

// stageWorkspace copies a task package (or an already-staged workspace) into
// a fresh directory, so each phase of a check runs against its own copy. This
// gate runs the oracle and the verifier itself, so their scripts are staged
// along with everything else; a later skill run must not get oracle/ or
// verifier/ in its workspace, but keeping that true is a different call
// site's responsibility, not this function's.
//
// root is CheckOptions.StageRoot; os.MkdirTemp("", …) already means
// os.TempDir(), so an empty root needs no special case.
func stageWorkspace(src, root string) (string, error) {
	dst, err := os.MkdirTemp(root, "whetstone-check-")
	if err != nil {
		return "", err
	}
	if err := copyTaskTree(src, dst); err != nil {
		_ = os.RemoveAll(dst)
		return "", err
	}
	return dst, nil
}

// copyTaskTree copies a task package (task.md, meta.yaml, environment/,
// oracle/, verifier/) from src to dst, preserving each file's mode — the
// oracle and verifier scripts are executed directly, so their 0755 bit must
// survive the copy. Non-regular files are refused for the same reason
// claudecode.copyTree refuses them: a symlink or device node copied into a
// sandbox workspace is a way out of it, not a task fixture.
func copyTaskTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("suite.Check: refusing to copy non-regular file %s", rel)
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode().Perm())
	})
}

// VoidSet is the set of task IDs an eval must exclude.
func VoidSet(rs []CheckResult) map[string]bool {
	out := map[string]bool{}
	for _, r := range rs {
		if r.Void {
			out[r.TaskID] = true
		}
	}
	return out
}
