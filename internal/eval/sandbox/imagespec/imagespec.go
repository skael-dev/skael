// Package imagespec renders everything an image needs as text: the base
// Dockerfile, a per-skill layer over it, the proxy configuration that enforces
// a network allowlist, and the digest that makes a layer cacheable.
//
// Separate from any driver on purpose: rendering is where the security-relevant
// decisions live, and keeping it out of the driver means they are asserted
// without a daemon, in tests that gate CI.
package imagespec

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/skael-dev/skael/internal/eval/sandbox"
)

//go:embed base/*
var baseFS embed.FS

// DefaultBaseTag is the image every per-skill layer is built over. The version
// suffix is bumped whenever base/Dockerfile changes: a score is attributed to
// an environment, so a silently-changed base makes two scores incomparable.
const DefaultBaseTag = "whetstone-base:1"

// PublishedBaseImage is where the release publishes DefaultBaseTag, and the
// default for a driver that resolves an image rather than building one. Derived
// rather than written out, so bumping the tag cannot leave it pointing at the
// previous environment.
const PublishedBaseImage = "ghcr.io/skael-dev/" + DefaultBaseTag

// SlimBaseTag is the base the docker-tagged test job builds.
const SlimBaseTag = "whetstone-base-ci:1"

// ContainerHome is the home of the "runner" user every run executes as. A host
// path an adapter declares under "~" is rewritten against this, never against
// the host's own home — the container never sees that filesystem. Changing
// base/Dockerfile's USER or its useradd home requires changing this to match,
// or every auth mount silently lands in the wrong place again.
const ContainerHome = "/home/runner"

// BaseDockerfile returns the base image definition, slim for CI or full for
// real evaluation.
func BaseDockerfile(slim bool) string {
	name := "base/Dockerfile"
	if slim {
		name = "base/Dockerfile.ci"
	}
	b, err := baseFS.ReadFile(name)
	if err != nil {
		// Unreachable unless the embed directive and the filename disagree.
		panic(fmt.Sprintf("imagespec: %v", err))
	}
	return string(b)
}

// DepsDigest is the content hash of everything that determines the per-skill
// layer's contents. Sorted, so declaration order never invalidates a cache;
// base-tag-inclusive, so a rebuilt base never serves a stale layer.
func DepsDigest(e sandbox.EnvSpec) (string, error) {
	if err := ValidateDeps(e.Deps); err != nil {
		return "", err
	}

	h := sha256.New()
	base := e.BaseTag
	if base == "" {
		base = DefaultBaseTag
	}
	fmt.Fprintf(h, "base\x00%s\x00", base)
	for _, group := range []struct {
		name string
		vals []string
	}{{"apt", e.Deps.Apt}, {"pip", e.Deps.Pip}, {"npm", e.Deps.Npm}} {
		vals := append([]string(nil), group.vals...)
		sort.Strings(vals)
		fmt.Fprintf(h, "%s\x00%s\x00", group.name, strings.Join(vals, "\x1f"))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Tag is the image tag a prepared layer is stored under.
func Tag(e sandbox.EnvSpec) (string, error) {
	d, err := DepsDigest(e)
	if err != nil {
		return "", err
	}
	return "whetstone-skill:" + d[:16], nil
}

// Render emits the per-skill Dockerfile. It validates first: a dependency string
// reaches a RUN instruction, so an unchecked one is arbitrary code at build.
func Render(e sandbox.EnvSpec) (string, error) {
	if err := ValidateDeps(e.Deps); err != nil {
		return "", err
	}

	base := e.BaseTag
	if base == "" {
		base = DefaultBaseTag
	}

	var b strings.Builder
	fmt.Fprintf(&b, "FROM %s\n", base)
	if len(e.Deps.Apt) > 0 {
		// Root to install, runner afterwards: a run must not write outside its
		// workspace, but a package install must.
		fmt.Fprintf(&b, "USER root\nRUN apt-get update && apt-get install -y --no-install-recommends %s && rm -rf /var/lib/apt/lists/*\nUSER runner\n",
			strings.Join(sorted(e.Deps.Apt), " "))
	}
	if len(e.Deps.Pip) > 0 {
		fmt.Fprintf(&b, "USER root\nRUN pip install --no-cache-dir %s\nUSER runner\n", strings.Join(sorted(e.Deps.Pip), " "))
	}
	if len(e.Deps.Npm) > 0 {
		fmt.Fprintf(&b, "USER root\nRUN npm install -g %s\nUSER runner\n", strings.Join(sorted(e.Deps.Npm), " "))
	}
	return b.String(), nil
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
