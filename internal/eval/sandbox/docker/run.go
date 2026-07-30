package docker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/skael-dev/skael/internal/eval/sandbox"
)

// proxyHost is the alias the allowlist proxy is reachable at on a run's
// private network. It is a name rather than an address because the address is
// assigned per network.
const proxyHost = "proxy"

// runArgs builds the flags shared by "docker create" and, historically,
// "docker run" — everything except the subcommand itself. Splitting Run into
// create + start + wait (see Run's doc comment) only changes which
// subcommand these flags are attached to.
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

// RunArgv assembles the full "docker run" command line — "docker create"'s
// argv (runArgs) with "run", "--rm" in front. Run itself no longer executes
// this; it is exported (and still fully tested by argv_test.go with no
// daemon) because it is the flags-only description of a run's security
// posture that a test can assert against, and CreateArgv (what Run actually
// issues) shares every flag runArgs builds.
func RunArgv(rs sandbox.RunSpec, o Options, name, network string) ([]string, error) {
	args, err := runArgs(rs, o, name, network)
	if err != nil {
		return nil, err
	}
	a := append([]string{"run", "--rm"}, args...)
	return a, nil
}

// CreateArgv assembles the "docker create" command line Run actually issues:
// every flag RunArgv would pass to "docker run", minus "--rm" — removal is
// always explicit (see Run), never left to a flag racing "docker wait" for
// the exit code it needs to read before the container is gone.
func CreateArgv(rs sandbox.RunSpec, o Options, name, network string) ([]string, error) {
	args, err := runArgs(rs, o, name, network)
	if err != nil {
		return nil, err
	}
	return append([]string{"create"}, args...), nil
}

// Run executes one command. A non-zero exit is a result, not an error: a
// verifier that fails is the measurement. An error means the run could not be
// performed at all — including when ctx is cancelled out from under it.
//
// This is "docker create" + "docker start -a" + "docker wait", not a single
// "docker run": "docker run" (and "docker start -a") report the container's
// own exit status as their *own* CLI process's exit status, in the same
// integer space docker itself uses to report that the client or daemon
// failed to do what was asked (125 for a CLI/daemon rejection, 126 "not
// executable", 127 "not found") — there is no way to tell "the verifier
// legitimately exited 127" apart from "docker couldn't find /verifier/test.sh
// because the mount failed" by inspecting that one number. "docker create"
// can only fail for a driver-side reason (bad image, bad mount, bad flag —
// no user command has run yet), and "docker wait" reports the container's
// exit code on its own channel, populated only once the container has
// actually run and exited: wait failing is always a driver problem, wait
// succeeding always means a container produced that number. "docker start
// -a"'s own process exit code is deliberately never read for this reason —
// it mirrors the same ambiguous number "docker run" would have — only its
// blocking-until-exit behavior (so rs.Stdout/rs.Stderr see the container's
// output) and its response to context cancellation are used.
//
// The container is named and removed explicitly on the way out, from a
// single deferred cleanup registered once "docker create" has succeeded (the
// only point at which there is a container to remove). Relying on a --rm
// flag alone loses a container whose docker client was killed by the
// context — or races "docker wait" against the container's automatic
// removal — and sixty of those per eval is a full disk.
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

	createArgv, err := CreateArgv(rs, d.o, name, network)
	if err != nil {
		return sandbox.RunResult{}, err
	}

	runCtx, cancel := context.WithTimeout(ctx, rs.Timeout)
	defer cancel()

	if createOut, err := d.output(runCtx, createArgv...); err != nil {
		switch {
		case errors.Is(runCtx.Err(), context.DeadlineExceeded):
			// Extremely unlikely (create does not run the image's own
			// command and needs no image pull, since Prepare/EnsureBase
			// already guarantee the image exists) but handled the same way
			// as every other timeout: it is not a result.
			return sandbox.RunResult{TimedOut: true}, nil
		case ctx.Err() != nil:
			return sandbox.RunResult{Cancelled: true}, fmt.Errorf("docker: run cancelled: %w", ctx.Err())
		}
		// No user command has executed yet, so this can only be the docker
		// client's or daemon's own failure: a missing image, an unreadable
		// bind-mount source, an invalid flag.
		return sandbox.RunResult{}, fmt.Errorf("docker: creating a container for %s: %w\n%s", strings.Join(rs.Argv, " "), err, createOut)
	}

	// From here on a container exists and must be removed on every return
	// path, including a panic recovering elsewhere in the caller — this is
	// the one cleanup Run needs, registered exactly once.
	defer func() { _, _ = d.output(context.WithoutCancel(ctx), "rm", "-f", name) }()

	start := time.Now()
	cmd := execCommand(runCtx, d.o.Binary, "start", "-a", name)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = rs.Stdin, rs.Stdout, rs.Stderr
	// startErr is deliberately not inspected: see Run's doc comment on why
	// "docker start -a"'s own exit status is never the source of ExitCode.
	// It still needs to be run to completion (or interrupted by runCtx) so
	// rs.Stdout/rs.Stderr receive the container's output and so the
	// container has actually finished by the time "docker wait" is asked.
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

// waitExitCode blocks until name's container has exited and returns its exit
// code, read from "docker wait"'s own stdout — a bare integer, the container
// runtime's authoritative record of what the container's own command exited
// with, on a channel that shares no meaning with any docker client or daemon
// failure code. By the time Run calls this, "docker start -a" has already
// returned because the container exited, so this returns immediately rather
// than actually blocking; it is not skipped, because it is the one place
// ExitCode is allowed to come from.
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

// containerName is unique per run so a timed-out container can be found and
// removed by name.
func containerName() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("docker: naming a container: %w", err)
	}
	return "whetstone-run-" + hex.EncodeToString(b[:]), nil
}
