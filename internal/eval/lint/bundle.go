package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SidecarDir is the eval sidecar directory at the bundle root.
const SidecarDir = "eval"

// SpecFile is the authored spec, beside the bundle rather than inside the
// sidecar.
const SpecFile = "spec.yaml"

const archiveSuffix = ".tar.gz"

// Excluded reports whether rel (slash-separated, bundle-relative) is
// authoring scaffolding rather than shipped skill content. Defined here
// because lint walks the whole directory and pack consumes the same predicate.
func Excluded(rel string) bool {
	switch {
	case rel == SidecarDir || strings.HasPrefix(rel, SidecarDir+"/"):
		return true
	case rel == SpecFile:
		return true
	case !strings.Contains(rel, "/") && strings.HasSuffix(rel, archiveSuffix):
		return true
	}
	return false
}

// checkRootArchives flags archives at the bundle root. Excluded drops them
// from other checks, so without this diagnostic they vanish silently.
func checkRootArchives(bundleDir string) ([]Finding, error) {
	entries, err := os.ReadDir(bundleDir)
	if err != nil {
		return nil, fmt.Errorf("lint: reading %s: %w", bundleDir, err)
	}

	var findings []Finding
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), archiveSuffix) {
			continue
		}
		findings = append(findings, Finding{
			Rule:     "bundle-artifact",
			Severity: SeverityWarn,
			File:     e.Name(),
			Message:  "a root-level archive is not packed as bundle content; move it outside the bundle",
		})
	}
	return findings, nil
}

// bundleRel converts path to the slash-separated, bundle-relative form
// Excluded expects. Reports !ok when path is not under bundleDir.
func bundleRel(bundleDir, path string) (string, bool) {
	rel, err := filepath.Rel(bundleDir, path)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}

// excludedWalkEntry wraps Excluded for a filepath.Walk callback. Returns
// filepath.SkipDir for excluded directories. Symlinks are never excluded:
// Walk never follows them, and excluding one would suppress the symlink check.
func excludedWalkEntry(bundleDir, path string, info os.FileInfo) (bool, error) {
	if info != nil && info.Mode()&os.ModeSymlink != 0 {
		return false, nil
	}
	rel, ok := bundleRel(bundleDir, path)
	if !ok || !Excluded(rel) {
		return false, nil
	}
	if info != nil && info.IsDir() {
		return true, filepath.SkipDir
	}
	return true, nil
}
