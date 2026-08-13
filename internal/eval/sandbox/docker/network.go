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

// NetworkArgv creates the private --internal network. --internal removes the
// default route, so egress is possible only through the proxy container.
func NetworkArgv(name string) []string {
	a := []string{"network", "create", "--internal"}
	a = append(a, ownerLabelArgs()...)
	return append(a, name)
}

// ProxyArgv starts the allowlist proxy, detached. Runs as root with only
// SETUID/SETGID because tinyproxy drops to nobody after binding its port.
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

const proxyEntrypoint = `while [ ! -e ` + proxyReadyFile + ` ]; do sleep 0.1; done
exec tinyproxy -d -c /` + proxyConfPath

// proxyPayload builds the tar archive "docker cp" writes into the proxy.
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

// prepareAllowlist creates the network + proxy + bridge attachment. The
// returned cleanup removes both; a leaked network per run exhausts Docker's
// address pool inside one Deep tier.
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

	if out, err := d.output(ctx, "network", "connect", "bridge", proxyName); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("docker: giving the proxy egress: %w\n%s", err, out)
	}
	return network, cleanup, nil
}
