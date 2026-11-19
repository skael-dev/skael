package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// checksDir is where a check run's harness, scripts and results live inside
// the copy of the workspace. The agent's own workspace never holds it.
const checksDir = ".skael-checks"

// checksFileName is the checks' verdicts beside a run's other artifacts, so a
// resumed run grades the same way the first attempt did.
const checksFileName = "checks.json"

// commandTailBytes is how much of a command check's output a verdict keeps.
// The tail, because a test runner prints its summary last.
const commandTailBytes = 2000

// runChecks grades ev's checks against what the agent left in ws.
//
// It runs in the sandbox after the agent has finished, with the network off,
// against a copy of the workspace. The copy keeps a command check's side
// effects out of the outputs the judge reads. The scripts enter the copy only
// now, so the agent never saw them. Every file a check reads is copied inside
// the sandbox before any command runs, so a command cannot change what a file
// check sees.
func (r *Runner) runChecks(ctx context.Context, in ExecuteInput, ws string, ev suite.Eval) ([]suite.CheckResult, error) {
	root := r.o.WorkspaceRoot
	cws, err := os.MkdirTemp(root, "skael-checks-*")
	if err != nil {
		return nil, fmt.Errorf("runner: creating the check workspace: %w", err)
	}
	defer func() { _ = os.RemoveAll(cws) }()
	if err := copyWorkspace(ws, cws); err != nil {
		return nil, fmt.Errorf("runner: copying the workspace for checks: %w", err)
	}
	stage := filepath.Join(cws, checksDir)
	if err := os.RemoveAll(stage); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(stage, "out"), 0o777); err != nil {
		return nil, err
	}
	// MkdirAll applies the umask, and the sandbox user is not this process's
	// user on Linux, so the directory it writes results into is opened here.
	if err := os.Chmod(filepath.Join(stage, "out"), 0o777); err != nil {
		return nil, err
	}
	scripts := filepath.Join(in.SuiteDir, suite.EvalsDir, suite.ChecksDir)
	if hasCommand(ev.Checks) {
		if err := copyWorkspace(scripts, filepath.Join(stage, "scripts")); err != nil {
			return nil, fmt.Errorf("runner: staging the check scripts: %w", err)
		}
		if err := filepath.WalkDir(filepath.Join(stage, "scripts"), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.Type()&os.ModeSymlink != 0 {
				return err
			}
			return os.Chmod(p, 0o777)
		}); err != nil {
			return nil, fmt.Errorf("runner: staging the check scripts: %w", err)
		}
	}

	exec := sandbox.NewExec(r.o.Driver, sandbox.RunSpec{
		Image:     in.Image,
		Workspace: cws,
		Network:   sandbox.NetNone,
		Timeout:   r.checkTimeout(ev.Checks),
	})
	harness := checkHarness(ev.Checks, r.commandTimeout())
	if err := os.WriteFile(filepath.Join(stage, "run.sh"), []byte(harness), 0o755); err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	code, err := exec.Exec(ctx, []string{"sh", exec.WorkDir() + "/" + checksDir + "/run.sh"}, &stderr, &stderr)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("runner: the checks did not finish in %s", r.checkTimeout(ev.Checks))
		}
		return nil, fmt.Errorf("runner: running the checks: %w", err)
	}
	if code != 0 {
		return nil, fmt.Errorf("runner: the check harness exited %d: %s", code, tail(stderr.String(), commandTailBytes))
	}

	out := filepath.Join(stage, "out")
	results := make([]suite.CheckResult, 0, len(ev.Checks))
	for i, c := range ev.Checks {
		var f suite.FileState
		var cmd suite.CommandState
		if c.ReadsFile() {
			b, err := os.ReadFile(filepath.Join(out, "file-"+strconv.Itoa(i)))
			if err == nil {
				f = suite.FileState{Present: true, Content: b}
			} else if !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
		}
		if c.Type == suite.CheckCommand {
			raw, err := os.ReadFile(filepath.Join(out, "cmd-"+strconv.Itoa(i)+".code"))
			if err != nil {
				return nil, fmt.Errorf("runner: check %d left no exit code: %w", i+1, err)
			}
			cmd.ExitCode, err = strconv.Atoi(strings.TrimSpace(string(raw)))
			if err != nil {
				return nil, fmt.Errorf("runner: check %d exit code %q: %w", i+1, raw, err)
			}
			log, _ := os.ReadFile(filepath.Join(out, "cmd-"+strconv.Itoa(i)+".log"))
			cmd.Output = tail(string(log), commandTailBytes)
			if cmd.ExitCode == 124 {
				cmd.Output = "timed out after " + r.commandTimeout().String() + ". " + cmd.Output
			}
		}
		results = append(results, c.Judge(f, cmd))
	}
	return results, nil
}

// commandTimeout bounds one command check. A check that runs a test suite
// gets as long as a session; a hung test is the skill's failure, not an
// infrastructure fault, so it ends as a failed check rather than a lost run.
func (r *Runner) commandTimeout() time.Duration {
	if r.o.SessionTimeout > 0 {
		return r.o.SessionTimeout
	}
	return 10 * time.Minute
}

func (r *Runner) checkTimeout(cs []suite.Check) time.Duration {
	n := 0
	for _, c := range cs {
		if c.Type == suite.CheckCommand {
			n++
		}
	}
	return time.Duration(n)*r.commandTimeout() + 2*time.Minute
}

func hasCommand(cs []suite.Check) bool {
	for _, c := range cs {
		if c.Type == suite.CheckCommand {
			return true
		}
	}
	return false
}

// checkHarness is the shell script the check run executes. Every path in it
// is author input, validated by suite.Check.Problem and single-quoted here.
func checkHarness(cs []suite.Check, cmdTimeout time.Duration) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\ncd \"$(dirname \"$0\")/..\" || exit 2\nout=" + checksDir + "/out\n")
	for i, c := range cs {
		if c.ReadsFile() {
			p := shellQuote(filepath.ToSlash(c.Path))
			fmt.Fprintf(&b, "if [ -f %s ]; then cp %s \"$out/file-%d\"; fi\n", p, p, i)
		}
	}
	secs := int(cmdTimeout.Seconds())
	for i, c := range cs {
		if c.Type != suite.CheckCommand {
			continue
		}
		s := shellQuote(checksDir + "/scripts/" + filepath.ToSlash(c.Script))
		fmt.Fprintf(&b, "chmod +x %s 2>/dev/null\n", s)
		fmt.Fprintf(&b, "timeout %d sh -c '\"$0\"' %s > \"$out/cmd-%d.log\" 2>&1 < /dev/null\n", secs, s, i)
		fmt.Fprintf(&b, "echo $? > \"$out/cmd-%d.code\"\n", i)
	}
	b.WriteString("exit 0\n")
	return b.String()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// copyWorkspace copies src into dst: directories, regular files, and symlinks
// as symlinks, never followed. A missing src copies nothing. Everything is
// left writable by any user, because the sandbox user is not this process's
// user on Linux and a command check may write where the agent wrote.
func copyWorkspace(src, dst string) error {
	if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			if err := os.MkdirAll(target, 0o777); err != nil {
				return err
			}
			return os.Chmod(target, 0o777)
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if err := os.WriteFile(target, b, 0o666); err != nil {
				return err
			}
			return os.Chmod(target, info.Mode().Perm()|0o666)
		}
		return nil
	})
}

// writeChecks records a run's check verdicts beside its other artifacts.
func writeChecks(artifactDir string, results []suite.CheckResult) error {
	if artifactDir == "" || len(results) == 0 {
		return nil
	}
	b, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(artifactDir, checksFileName), b, wsFileMode)
}

// LoadChecks reads a run's recorded check verdicts. A run with none recorded
// returns nil.
func LoadChecks(artifactDir string) ([]suite.CheckResult, error) {
	if artifactDir == "" {
		return nil, nil
	}
	b, err := os.ReadFile(filepath.Join(artifactDir, checksFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []suite.CheckResult
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("runner: reading %s: %w", checksFileName, err)
	}
	return out, nil
}
