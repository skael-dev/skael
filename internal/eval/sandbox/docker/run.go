package docker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/skael-dev/skael/internal/eval/sandbox"
)

const proxyHost = "proxy"

// openWorkspace widens the workspace to 0777 so the container's uid 1000 can
// enter it. Bind mounts carry host ownership unchanged, and the invoking user
// is not always uid 1000 (GitHub Actions is 1001). The mismatch is silent on
// macOS (Docker Desktop remaps) and a voided suite on Linux (exit 126
// misattributed to a broken oracle). 0777 rather than chown because chowning
// to another uid needs root.
func openWorkspace(ws string) error {
	if ws == "" {
		return nil // RunSpec.Validate rejects this; nothing to open.
	}
	if err := os.Chmod(ws, 0o777); err != nil {
		return fmt.Errorf("docker: opening workspace %s to the container user: %w", ws, err)
	}
	return nil
}

func runArgs(rs sandbox.RunSpec, o Options, name, network string) ([]string, error) {
	if err := rs.Validate(); err != nil {
		return nil, err
	}
	workdir := rs.WorkDir
	if workdir == "" {
		workdir = sandbox.DefaultWorkDir
	}

	a := []string{"--name", name}
	a = append(a, ownerLabelArgs()...)
	a = append(a,
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", strconv.Itoa(o.PidsLimit),
		"--cpus", o.CPUs,
		"--memory", o.Memory,
		"--workdir", workdir,
		"-v", rs.Workspace+":"+workdir+":rw",
	)

	for _, m := range rs.Mounts {
		if !m.ReadOnly {
			return nil, fmt.Errorf("docker: mount %s must be read-only; a run's only writable surface is its workspace", m.HostPath)
		}
		a = append(a, "-v", m.HostPath+":"+m.ContainerPath+":ro")
	}

	switch rs.Network {
	case sandbox.NetNone:
		a = append(a, "--network", "none")
	case sandbox.NetFull:
		a = append(a, "--network", "bridge")
	case sandbox.NetAllowlist:
		if network == "" {
			return nil, errors.New("docker: allowlist policy needs a prepared private network")
		}
		proxy := "http://" + proxyHost + ":8888"
		a = append(a, "--network", network,
			"-e", "HTTP_PROXY="+proxy, "-e", "HTTPS_PROXY="+proxy,
			"-e", "http_proxy="+proxy, "-e", "https_proxy="+proxy)
	}

	for _, e := range rs.Env {
		a = append(a, "-e", e)
	}
	a = append(a, rs.Image.Tag)
	return append(a, rs.Argv...), nil
}

// RunArgv is the "docker run" command line. Run itself uses CreateArgv; this
// is exported for argv_test.go assertions on the security posture.
func RunArgv(rs sandbox.RunSpec, o Options, name, network string) ([]string, error) {
	args, err := runArgs(rs, o, name, network)
	if err != nil {
		return nil, err
	}
	a := append([]string{"run", "--rm"}, args...)
	return a, nil
}

// CreateArgv is the "docker create" command line Run issues. No --rm: removal
// is explicit so "docker wait" can read the exit code before the container
// is gone.
func CreateArgv(rs sandbox.RunSpec, o Options, name, network string) ([]string, error) {
	args, err := runArgs(rs, o, name, network)
	if err != nil {
		return nil, err
	}
	return append([]string{"create"}, args...), nil
}

// Run executes one command. A non-zero exit is a result, not an error.
// Uses create + start -a + wait (not "docker run") so the exit code comes
// from "docker wait" on its own channel, unambiguous from docker's own 125/
// 126/127 failures.
func (d *Driver) Run(ctx context.Context, rs sandbox.RunSpec) (sandbox.RunResult, error) {
	name, err := containerName()
	if err != nil {
		return sandbox.RunResult{}, err
	}

	if err := openWorkspace(rs.Workspace); err != nil {
		return sandbox.RunResult{}, err
	}

	network := ""
	if rs.Network == sandbox.NetAllowlist {
		net, cleanup, err := d.prepareAllowlist(ctx, name, rs.Allow)
		if err != nil {
			return sandbox.RunResult{}, err
		}
		defer cleanup()
		network = net
	}

	createArgv, err := CreateArgv(rs, d.o, name, network)
	if err != nil {
		return sandbox.RunResult{}, err
	}

	runCtx, cancel := context.WithTimeout(ctx, rs.Timeout)
	defer cancel()

	if createOut, err := d.output(runCtx, createArgv...); err != nil {
		switch {
		case errors.Is(runCtx.Err(), context.DeadlineExceeded):
			return sandbox.RunResult{TimedOut: true}, nil
		case ctx.Err() != nil:
			return sandbox.RunResult{Cancelled: true}, fmt.Errorf("docker: run cancelled: %w", ctx.Err())
		}
		return sandbox.RunResult{}, fmt.Errorf("docker: creating a container for %s: %w\n%s", strings.Join(rs.Argv, " "), err, createOut)
	}
	defer func() { _, _ = d.output(context.WithoutCancel(ctx), "rm", "-f", name) }()

	start := time.Now()
	cmd := execCommand(runCtx, d.o.Binary, "start", "-a", name)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = rs.Stdin, rs.Stdout, rs.Stderr
	// start -a's own exit code is deliberately ignored — it mirrors the
	// ambiguous number "docker run" returns. Only "docker wait" is read.
	_ = cmd.Run()
	res := sandbox.RunResult{Duration: time.Since(start)}

	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		res.TimedOut = true
		return res, nil
	case ctx.Err() != nil:
		res.Cancelled = true
		return res, fmt.Errorf("docker: run cancelled: %w", ctx.Err())
	}

	code, err := d.waitExitCode(context.WithoutCancel(ctx), name)
	if err != nil {
		return res, fmt.Errorf("docker: waiting for %s: %w", strings.Join(rs.Argv, " "), err)
	}
	res.ExitCode = code
	return res, nil
}

func (d *Driver) waitExitCode(ctx context.Context, name string) (int, error) {
	out, err := d.output(ctx, "wait", name)
	if err != nil {
		return 0, fmt.Errorf("docker wait: %w: %s", err, out)
	}
	code, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("docker wait: unparseable exit code %q: %w", out, err)
	}
	return code, nil
}

func containerName() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("docker: naming a container: %w", err)
	}
	return "whetstone-run-" + hex.EncodeToString(b[:]), nil
}
