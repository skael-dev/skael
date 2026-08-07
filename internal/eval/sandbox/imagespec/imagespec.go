// Package imagespec renders everything an image needs as text: the base
// Dockerfile, a per-skill layer over it, the proxy configuration that enforces
// a network allowlist, and the digest that makes a layer cacheable.
//
// It is deliberately separate from any driver. Rendering is where the
// security-relevant decisions live — which dependency strings may become
// shell, which Dockerfile instructions a task may use — and keeping it out of
// the driver means those decisions are asserted without a daemon, in tests
// that gate CI.
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

// SlimBaseTag is the base the docker-tagged test job builds.
const SlimBaseTag = "whetstone-base-ci:1"

// ContainerHome is the home directory of the "runner" user every run executes
// as inside the container — base/Dockerfile creates that user with
// "useradd -m -u 1000 runner", and Docker derives HOME from /etc/passwd for a
// USER set by name. A host path an adapter declares under "~" (an AuthDirs
// entry, for instance) must be rewritten against this, not against the host's
// own home: the two are different filesystems with different users, and the
// container never sees the host's home directory at all.
//
// This is an image property, not a runner one: changing base/Dockerfile's
// USER or the UID/home "useradd" assigns requires changing this constant to
// match, or every auth mount silently starts landing in the wrong place
// again.
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
		// Embedded at compile time; unreachable unless the embed directive and
		// the filename disagree, which is a build-time defect.
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

// Render emits the per-skill Dockerfile. It validates before it emits: a
// dependency string reaches a RUN instruction, so an unchecked one is
// arbitrary code during an image build.
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
		// Root for the install, back to runner afterwards: a run must not be
		// able to write outside its workspace, but installing a package needs
		// to.
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
