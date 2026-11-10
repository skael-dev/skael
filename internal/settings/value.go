package settings

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Validate reports why raw is not a valid value of d. An empty value is valid:
// it means unset.
func (d *Def) Validate(raw string) error {
	if raw == "" {
		return nil
	}
	switch d.Kind {
	case KindBool:
		if raw != "true" && raw != "false" {
			return fmt.Errorf("%s must be true or false", d.Label)
		}
	case KindInt:
		n, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("%s must be a whole number", d.Label)
		}
		return d.inRange(float64(n))
	case KindFloat:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("%s must be a number", d.Label)
		}
		return d.inRange(f)
	case KindDuration:
		v, err := time.ParseDuration(raw)
		if err != nil || v <= 0 {
			return fmt.Errorf("%s must be a positive duration such as 60s or 5m", d.Label)
		}
	case KindURL:
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("%s must be an http or https URL with a host", d.Label)
		}
	case KindEnum:
		for _, o := range d.Options {
			if o.Value == raw {
				return nil
			}
		}
		return fmt.Errorf("%s must be one of %s", d.Label, strings.Join(d.optionValues(), ", "))
	}
	return nil
}

func (d *Def) inRange(f float64) error {
	if d.Min != nil && f < *d.Min {
		return fmt.Errorf("%s must be at least %s", d.Label, strconv.FormatFloat(*d.Min, 'g', -1, 64))
	}
	if d.Max != nil && f > *d.Max {
		return fmt.Errorf("%s must be at most %s", d.Label, strconv.FormatFloat(*d.Max, 'g', -1, 64))
	}
	return nil
}

func (d *Def) optionValues() []string {
	out := make([]string, len(d.Options))
	for i, o := range d.Options {
		out[i] = o.Value
	}
	return out
}

// A reference names where a secret lives instead of holding it.
const (
	refEnv  = "env:"
	refFile = "file:"
)

// ValidReference reports whether ref is env:NAME or file:/absolute/path.
func ValidReference(ref string) error {
	switch {
	case strings.HasPrefix(ref, refEnv) && len(ref) > len(refEnv):
		return nil
	case strings.HasPrefix(ref, refFile) && strings.HasPrefix(ref[len(refFile):], "/"):
		return nil
	}
	return fmt.Errorf("a reference is env:NAME or file:/absolute/path")
}

// resolveReference reads the secret ref names.
func resolveReference(ref string, env EnvFunc) (string, error) {
	if name, ok := strings.CutPrefix(ref, refEnv); ok {
		v, ok := env(name)
		if !ok || v == "" {
			return "", fmt.Errorf("%s is not set", name)
		}
		return v, nil
	}
	if path, ok := strings.CutPrefix(ref, refFile); ok {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("cannot read %s", path)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return "", fmt.Errorf("unknown reference %q", ref)
}

// ResolveReference reads the secret an env:NAME or file:/path reference names,
// from this process's environment and filesystem.
func ResolveReference(ref string) (string, error) {
	return resolveReference(ref, os.LookupEnv)
}

// IsReference reports whether v is an env: or file: reference rather than a
// secret value.
func IsReference(v string) bool { return ValidReference(v) == nil }
