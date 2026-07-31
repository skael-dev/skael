package whetstone

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/internal/eval/lint"
	"github.com/skael-dev/skael/internal/ui"
)

// archiveMode is the permission the finished archive carries.
const archiveMode = os.FileMode(0o644)

var packOutput string

var packCmd = &cobra.Command{
	Use:   "pack <skill|path>",
	Short: "Write a spec-valid archive with the eval sidecar stripped",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveBundle(args[0])
		if err != nil {
			return err
		}

		out, err := RunPack(dir, packOutput)
		if err != nil {
			return err
		}
		if ui.JSONMode {
			return ui.PrintJSON(map[string]string{"bundle": dir, "archive": out})
		}
		ui.Success("wrote %s", out)
		return nil
	},
}

// RunPack lints bundleDir and, if it has no errors, writes a tar.gz of it to
// outPath with the eval sidecar and the spec removed, and returns the
// archive's resolved path. When outPath is empty, the archive is written
// beside the bundle — in its parent directory, as "<bundle>.tar.gz" — rather
// than inside it: an archive inside the directory being packed is content the
// next pack has to exclude (lint.Excluded's root-tarball case) purely because
// it exists.
//
// Linting first is not a convenience: an archive built from a bundle that
// fails lint installs and then fails at use time, a long way from the cause.
func RunPack(bundleDir, outPath string) (string, error) {
	root, err := bundleRoot(bundleDir)
	if err != nil {
		return "", err
	}

	res, _, err := lintBundle(bundleDir, false)
	if err != nil {
		return "", err
	}
	if res.HasErrors() {
		return "", fmt.Errorf("%s fails lint with %d error(s); run `whetstone lint %s` and fix them before packing",
			bundleDir, res.Errors(), bundleDir)
	}

	if outPath == "" {
		outPath = filepath.Join(filepath.Dir(root), filepath.Base(root)+".tar.gz")
	}

	out, err := filepath.Abs(outPath)
	if err != nil {
		return "", fmt.Errorf("whetstone pack: resolving %q: %w", outPath, err)
	}

	// The archive is built in a temp file beside its destination and renamed
	// into place only once it is complete. Writing straight to outPath would
	// truncate the last good archive before the first byte of the new one is
	// written, so any failure during the walk replaces a valid archive with a
	// corrupt one. Beside its destination, rather than in the system temp
	// directory, so the rename is within one filesystem and therefore atomic.
	tmp, err := os.CreateTemp(filepath.Dir(out), ".whetstone-pack-*.tar.gz")
	if err != nil {
		return "", fmt.Errorf("whetstone pack: %w", err)
	}
	tmpName := tmp.Name()
	// Removing the temp file is a no-op once the rename has moved it away.
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	// Both the destination and the temp file can sit inside the directory
	// about to be walked — `whetstone pack .` is the most natural invocation
	// there is — and an archive that packs a truncated copy of itself is the
	// result if the walk is allowed to see them.
	skip := map[string]bool{out: true, tmpName: true}

	gzw := gzip.NewWriter(tmp)
	tw := tar.NewWriter(gzw)

	if err := writeBundleEntries(tw, root, skip); err != nil {
		return "", err
	}
	if err := tw.Close(); err != nil {
		return "", fmt.Errorf("whetstone pack: closing tar: %w", err)
	}
	if err := gzw.Close(); err != nil {
		return "", fmt.Errorf("whetstone pack: closing gzip: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("whetstone pack: closing %s: %w", tmpName, err)
	}

	// os.CreateTemp opens at 0600 and the rename preserves that, so without
	// this the archive is readable only by its author — a regression against
	// the 0666-and-umask an ordinary create would have produced, and an
	// archive exists to be handed to someone else.
	if err := os.Chmod(tmpName, archiveMode); err != nil {
		return "", fmt.Errorf("whetstone pack: %w", err)
	}

	if err := os.Rename(tmpName, out); err != nil {
		return "", fmt.Errorf("whetstone pack: writing %s: %w", outPath, err)
	}
	return out, nil
}

// writeBundleEntries walks bundleDir and writes every regular file that ships
// (see lint.Excluded), and is not one of the absolute paths in skip, into tw.
func writeBundleEntries(tw *tar.Writer, bundleDir string, skip map[string]bool) error {
	return filepath.Walk(bundleDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(bundleDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}

		// What does not ship is lint.Excluded's definition, not a second one
		// here: pack is gated on lint, so a bundle whose eval sidecar pack
		// strips but lint judges as shipped content can never be packed at all.
		if info.IsDir() {
			if lint.Excluded(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			// Symlinks and devices are rejected by skill.Unpack anyway, and
			// lint already reports a symlinked bundle entry as an error.
			return nil
		}
		if lint.Excluded(rel) || skip[path] {
			return nil
		}

		// The archive name is derived from a filesystem walk, so it should
		// already be a clean relative path. It is checked anyway: an archive
		// is the artifact that leaves this machine, and an absolute or
		// escaping name in one is a path-traversal bug in whatever unpacks it.
		if err := safeArchiveName(rel); err != nil {
			return err
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("whetstone pack: header for %s: %w", rel, err)
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("whetstone pack: writing header for %s: %w", rel, err)
		}

		src, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("whetstone pack: %w", err)
		}
		defer func() { _ = src.Close() }()

		if _, err := io.Copy(tw, src); err != nil {
			return fmt.Errorf("whetstone pack: copying %s: %w", rel, err)
		}
		return nil
	})
}

// safeArchiveName rejects an entry name that is absolute or contains a
// traversal segment.
func safeArchiveName(name string) error {
	if name == "" || filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return fmt.Errorf("whetstone pack: refusing absolute archive entry %q", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return fmt.Errorf("whetstone pack: refusing archive entry %q, which escapes the bundle", name)
		}
	}
	return nil
}

func init() {
	packCmd.Flags().StringVarP(&packOutput, "output", "o", "", "Archive path (default <skill>.tar.gz)")
	rootCmd.AddCommand(packCmd)
}
