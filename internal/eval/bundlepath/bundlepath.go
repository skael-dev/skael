// Package bundlepath holds the one bundle-containment check every package
// that writes model-authored paths into a skill bundle shares. A resource
// path (gen), or a repair proposal's file path (repair), is untrusted input
// exactly the same way in both places, and a duplicated check that drifts is
// a security defect waiting to happen — so there is exactly one
// implementation of the rule.
package bundlepath

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SafeJoin resolves rel inside dir, refusing anything that escapes: an
// absolute path, or a traversal that resolves outside dir even when buried
// after the first path segment (e.g. "scripts/../../escape.sh", which does
// not start with ".." and so would slip past a naive prefix check).
func SafeJoin(dir, rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("empty path")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute path %q", rel)
	}
	target := filepath.Join(dir, rel)
	within, err := filepath.Rel(dir, target)
	if err != nil {
		return "", fmt.Errorf("resolving %q: %w", rel, err)
	}
	if within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the bundle", rel)
	}
	return target, nil
}
