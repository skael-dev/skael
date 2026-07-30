package suite

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Ref is the content hash of a written suite: every file's relative path and
// contents, in sorted order.
//
// It exists so that "the same suite" is a computed fact rather than an
// assumption. A score is only comparable to another score measured against the
// same tasks, and a trend that silently mixes suites is worse than no trend at
// all — so a report carries this value and two reports can be asked whether
// they are comparable.
func Ref(dir string) (string, error) {
	var rels []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("suite.Ref: %s is not a regular file", p)
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rels = append(rels, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(rels) == 0 {
		return "", fmt.Errorf("suite.Ref: %s holds no files", dir)
	}
	sort.Strings(rels)

	h := sha256.New()
	for _, rel := range rels {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		// Length-prefix both, so no rename can produce the same digest by
		// shifting bytes across the path/content boundary.
		fmt.Fprintf(h, "%d\x00%s\x00%d\x00", len(rel), rel, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// suiteRefPrefix is how many hex characters identify a suite in a path or a
// log line. Full digests are stored; this is for humans.
const suiteRefPrefix = 12

// ShortRef abbreviates a ref for display.
func ShortRef(ref string) string {
	if len(ref) <= suiteRefPrefix {
		return ref
	}
	return strings.ToLower(ref[:suiteRefPrefix])
}
