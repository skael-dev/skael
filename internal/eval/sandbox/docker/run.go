package docker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/skael-dev/skael/internal/eval/sandbox"
)

// proxyHost is the alias the allowlist proxy is reachable at on a run's
// private network. It is a name rather than an address because the address is
// assigned per network.
const proxyHost = "proxy"

// RunArgv assembles the docker run command line. It is exported so that every
// version-sensitive flag is asserted by a test that needs no daemon: the flags
// are the security posture of a run, and a silently dropped --cap-drop is not
// something a passing end-to-end test would notice.
func RunArgv(rs sandbox.RunSpec, o Options, name, network string) ([]string, error) {
	if err := rs.Validate(); err != nil {
		return nil, err
	}
	workdir := rs.WorkDir
	if workdir == "" {
		workdir = sandbox.DefaultWorkDir
	}

	a := []string{
		"run", "--rm", "--name", name,
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", strconv.Itoa(o.PidsLimit),
		"--cpus", o.CPUs,
		"--memory", o.Memory,
		"--workdir", workdir,
		"-v", rs.Workspace + ":" + workdir + ":rw",
	}

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

// driverExitCodes are exit statuses `docker run` itself uses to report that
// the client or daemon could not do what was asked — a missing image, an
// unreadable bind-mount source, an entrypoint that isn't executable or
// doesn't exist. They occupy the same integer space a container's own
// command exits with, so a code in this set is never treated as the
// command's result: it is the docker CLI's own failure, reported as an error
// rather than folded into ExitCode.
//
// This is deliberately a conservative fallback rather than separating the
// two exit spaces structurally (via `docker create` + `start` + `wait`,
// where only `wait` reports the container's own status): the three-command
// split touches the timeout, allowlist-network, and cleanup logic that this
// function's tests and callers already depend on, and the risk of that
// rewrite was judged larger than the risk of this list going stale. If a
// future Docker version repurposes one of these codes for a container's own
// exit, that would show up as a run's exit code being misreported as a
// driver error — worth revisiting if this ever needs to change.
var driverExitCodes = map[int]string{
	125: "the docker CLI or daemon rejected the run (e.g. a bad flag, an unreadable bind-mount source, or a daemon error)",
	126: "the command in the image could not be invoked (not executable)",
	127: "the command in the image could not be found",
}

// Run executes one command. A non-zero exit is a result, not an error: a
// verifier that fails is the measurement. An error means the run could not be
// performed at all — including when docker's own client exits 125, 126, or
// 127 (see driverExitCodes) or when ctx is cancelled out from under it.
//
// The container is named and removed explicitly on the way out. Relying on
// --rm alone loses a container whose docker client was killed by the context,
// and sixty of those per eval is a full disk.
func (d *Driver) Run(ctx context.Context, rs sandbox.RunSpec) (sandbox.RunResult, error) {
	name, err := containerName()
	if err != nil {
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

	argv, err := RunArgv(rs, d.o, name, network)
	if err != nil {
		return sandbox.RunResult{}, err
	}

	runCtx, cancel := context.WithTimeout(ctx, rs.Timeout)
	defer cancel()

	cmd := execCommand(runCtx, d.o.Binary, argv...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = rs.Stdin, rs.Stdout, rs.Stderr

	start := time.Now()
	runErr := cmd.Run()
	res := sandbox.RunResult{Duration: time.Since(start)}

	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		res.TimedOut = true
		// The docker client is already dead; the container is not. Removing it
		// by name is the only thing that stops it.
		_, _ = d.output(context.WithoutCancel(ctx), "rm", "-f", name)
		return res, nil
	case ctx.Err() != nil:
		// The parent context was cancelled — not this run's own timeout. A
		// ctx-killed docker client leaves an exec.ExitError behind (typically
		// "signal: killed", ExitCode() == -1) that is not a measurement of
		// anything the container did; recording it as a RunResult would let a
		// worker shutdown or a caller's own deadline masquerade as a verifier
		// or agent result. Reported as an error, and Cancelled marked in case a
		// caller inspects the partial result rather than the error.
		_, _ = d.output(context.WithoutCancel(ctx), "rm", "-f", name)
		res.Cancelled = true
		return res, fmt.Errorf("docker: run cancelled: %w", ctx.Err())
	}
	// Always attempt removal: --rm has not fired if the client was interrupted.
	defer func() { _, _ = d.output(context.WithoutCancel(ctx), "rm", "-f", name) }()

	var ee *exec.ExitError
	switch {
	case runErr == nil:
		res.ExitCode = 0
	case errors.As(runErr, &ee):
		code := ee.ExitCode()
		if reason, isDriverCode := driverExitCodes[code]; isDriverCode {
			return res, fmt.Errorf("docker: run %s exited %d: %s (not a container result): %w",
				strings.Join(rs.Argv, " "), code, reason, runErr)
		}
		res.ExitCode = code
	default:
		return res, fmt.Errorf("docker: running %s: %w", strings.Join(rs.Argv, " "), runErr)
	}
	return res, nil
}

// containerName is unique per run so a timed-out container can be found and
// removed by name.
func containerName() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("docker: naming a container: %w", err)
	}
	return "whetstone-run-" + hex.EncodeToString(b[:]), nil
}
