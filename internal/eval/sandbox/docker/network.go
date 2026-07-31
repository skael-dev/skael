package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
)

// NetworkArgv creates the private network a run and its proxy share.
//
// --internal is the enforcement mechanism, not a hardening extra: it removes
// the network's default route, so a container attached to it has no path off
// the host except through another container that is also attached to something
// else. The proxy environment variables a run receives are a convenience for a
// well-behaved client; this flag is what makes ignoring them futile.
func NetworkArgv(name string) []string {
	a := []string{"network", "create", "--internal"}
	a = append(a, ownerLabelArgs()...)
	return append(a, name)
}

// ProxyArgv starts the allowlist proxy on the internal network under the alias
// runs address it by, detached so prepareAllowlist can go on to configure it
// and give it egress. Its command blocks until the configuration described at
// prepareAllowlist has been delivered (by "docker cp", not by piping stdin
// into this command: a detached "docker run" discards anything piped to its
// own stdin, so the configuration is written into the container after it is
// already running rather than passed at startup).
//
// The proxy runs as root with every capability dropped except SETUID/SETGID:
// tinyproxy's own rendered config (imagespec.ProxyConfig) drops the process
// from root to "nobody"/"nogroup" once it has bound its port, and that drop is
// a syscall that needs those two capabilities regardless of the starting UID.
// Every other capability a container gets by default is removed, so the one
// process whose entire job is to sit on the network boundary carries the
// minimum a working proxy needs and nothing more.
func ProxyArgv(network, name, baseTag string) []string {
	a := []string{"run", "-d", "--rm", "--name", name}
	a = append(a, ownerLabelArgs()...)
	a = append(a,
		"--network", network, "--network-alias", proxyHost,
		"--user", "root",
		"--cap-drop", "ALL", "--cap-add", "SETUID", "--cap-add", "SETGID",
		"--security-opt", "no-new-privileges",
		baseTag,
		"sh", "-c", proxyEntrypoint,
	)
	return a
}

// proxyReadyFile is written last among the files prepareAllowlist copies into
// the proxy container; proxyEntrypoint polls for it so tinyproxy is only
// started once its real configuration (not the base image's package default)
// is in place.
const proxyReadyFile = "/tmp/proxy-ready"

// proxyConfPath and proxyFilterPath are where prepareAllowlist writes the
// rendered configuration inside the container. They are the base image's own
// tinyproxy paths, deliberately: imagespec.ProxyConfig's rendered config
// already says `Filter "/etc/tinyproxy/filter"`, so writing the filter there
// needs no path rewriting inside the container.
const (
	proxyConfPath   = "etc/tinyproxy/tinyproxy.conf"
	proxyFilterPath = "etc/tinyproxy/filter"
)

// proxyEntrypoint waits for prepareAllowlist to deliver the rendered
// configuration, then execs tinyproxy in the foreground so it is the
// container's PID 1.
const proxyEntrypoint = `while [ ! -e ` + proxyReadyFile + ` ]; do sleep 0.1; done
exec tinyproxy -d -c /` + proxyConfPath

// proxyPayload builds the tar stream "docker cp" writes into the proxy
// container: the rendered tinyproxy config, its filter file, and the ready
// marker proxyEntrypoint is waiting on. Going through "docker cp" rather than
// a host temp file keeps the configuration off disk on the host; going
// through it rather than the run command's own stdin is required, because a
// detached ("-d") docker run never forwards piped stdin to the container.
func proxyPayload(cfg string) ([]byte, error) {
	delim := "\n" + imagespec.FilterMarker + "\n"
	i := strings.Index(cfg, delim)
	if i < 0 {
		return nil, fmt.Errorf("docker: rendered proxy config has no %q marker", imagespec.FilterMarker)
	}
	conf, filter := cfg[:i+1], cfg[i+len(delim):]

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	now := time.Now()
	for _, f := range []struct {
		name string
		body string
	}{
		{proxyConfPath, conf},
		{proxyFilterPath, filter},
		{strings.TrimPrefix(proxyReadyFile, "/"), ""},
	} {
		hdr := &tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.body)), ModTime: now}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("docker: building proxy config archive: %w", err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			return nil, fmt.Errorf("docker: building proxy config archive: %w", err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("docker: building proxy config archive: %w", err)
	}
	return buf.Bytes(), nil
}

// prepareAllowlist creates the private network, starts the proxy on it,
// delivers its configuration, and gives the proxy a second attachment to the
// default bridge so it — and only it — can reach the internet. The returned
// cleanup removes both; a leaked network per run exhausts Docker's address
// pool inside one Deep tier.
func (d *Driver) prepareAllowlist(ctx context.Context, runName string, allow []string) (string, func(), error) {
	cfg, err := imagespec.ProxyConfig(allow)
	if err != nil {
		return "", nil, err
	}
	payload, err := proxyPayload(cfg)
	if err != nil {
		return "", nil, err
	}

	network := strings.Replace(runName, "whetstone-run-", "whetstone-net-", 1)
	proxyName := strings.Replace(runName, "whetstone-run-", "whetstone-proxy-", 1)

	if out, err := d.output(ctx, NetworkArgv(network)...); err != nil {
		return "", nil, fmt.Errorf("docker: creating network %s: %w\n%s", network, err, out)
	}
	cleanup := func() {
		clean := context.WithoutCancel(ctx)
		// The proxy must go first: a container still attached to the network
		// blocks that network's removal.
		_, _ = d.output(clean, "rm", "-f", proxyName)
		_, _ = d.output(clean, "network", "rm", network)
	}

	if out, err := d.output(ctx, ProxyArgv(network, proxyName, d.o.BaseTag)...); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("docker: starting the allowlist proxy: %w\n%s", err, out)
	}

	cmd := execCommand(ctx, d.o.Binary, "cp", "-", proxyName+":/")
	cmd.Stdin = bytes.NewReader(payload)
	if out, err := cmd.CombinedOutput(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("docker: delivering the allowlist proxy's configuration: %w\n%s", err, out)
	}

	// The second attachment is what the proxy routes through. Making it after
	// the container starts keeps the internal network's members from ever
	// seeing a bridge interface.
	if out, err := d.output(ctx, "network", "connect", "bridge", proxyName); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("docker: giving the proxy egress: %w\n%s", err, out)
	}
	return network, cleanup, nil
}
