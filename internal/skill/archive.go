package skill

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Pack creates a tar.gz archive of the directory at dir.
// It returns the archive bytes, a sha256 hex checksum, a file manifest, and
// any error encountered. Pack returns an error if SKILL.md is not present in dir.
func Pack(dir string) ([]byte, string, []FileEntry, error) {
	// Require SKILL.md in the directory.
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); os.IsNotExist(err) {
		return nil, "", nil, fmt.Errorf("skill.Pack: SKILL.md not found in %s", dir)
	}

	// Collect files to include in the archive.
	var entries []FileEntry
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		// Use forward slashes in archive paths regardless of OS.
		rel = filepath.ToSlash(rel)
		entries = append(entries, FileEntry{Path: rel, Size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, "", nil, fmt.Errorf("skill.Pack walk: %w", err)
	}

	// Build the in-memory tar.gz.
	var buf strings.Builder
	_ = buf // use bytes.Buffer via sha256 writer

	// We need both the archive bytes and the checksum. Write once through a
	// sha256 hasher that also feeds a bytes buffer.
	h := sha256.New()

	// Use a pipe-and-buffer approach: write to a byte slice via io.MultiWriter.
	var archiveBuf []byte
	archiveWriter := &byteWriter{}
	mw := io.MultiWriter(archiveWriter, h)

	gzw := gzip.NewWriter(mw)
	tw := tar.NewWriter(gzw)

	for _, entry := range entries {
		fullPath := filepath.Join(dir, filepath.FromSlash(entry.Path))
		info, err := os.Stat(fullPath)
		if err != nil {
			return nil, "", nil, fmt.Errorf("skill.Pack stat %s: %w", entry.Path, err)
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return nil, "", nil, fmt.Errorf("skill.Pack header %s: %w", entry.Path, err)
		}
		// Use the relative slash-separated path as the archive name.
		hdr.Name = entry.Path

		if err := tw.WriteHeader(hdr); err != nil {
			return nil, "", nil, fmt.Errorf("skill.Pack write header %s: %w", entry.Path, err)
		}

		f, err := os.Open(fullPath)
		if err != nil {
			return nil, "", nil, fmt.Errorf("skill.Pack open %s: %w", entry.Path, err)
		}
		if _, err := io.Copy(tw, f); err != nil {
			f.Close()
			return nil, "", nil, fmt.Errorf("skill.Pack copy %s: %w", entry.Path, err)
		}
		f.Close()
	}

	if err := tw.Close(); err != nil {
		return nil, "", nil, fmt.Errorf("skill.Pack close tar: %w", err)
	}
	if err := gzw.Close(); err != nil {
		return nil, "", nil, fmt.Errorf("skill.Pack close gzip: %w", err)
	}

	archiveBuf = archiveWriter.buf
	checksum := hex.EncodeToString(h.Sum(nil))

	return archiveBuf, checksum, entries, nil
}

// byteWriter is a minimal io.Writer that accumulates bytes into a slice.
type byteWriter struct {
	buf []byte
}

func (bw *byteWriter) Write(p []byte) (int, error) {
	bw.buf = append(bw.buf, p...)
	return len(p), nil
}

// maxUnpackSize is the maximum total uncompressed bytes allowed per archive.
const maxUnpackSize = 50 << 20 // 50 MB

// Unpack extracts a tar.gz archive from r into destDir.
// It rejects any archive entry whose resolved path would escape destDir
// (path traversal prevention), rejects symlinks and hardlinks, and enforces a
// 50 MB total extraction size limit.
func Unpack(r io.Reader, destDir string) error {
	// Resolve destDir to an absolute clean path to compare against.
	destDir, err := filepath.Abs(destDir)
	if err != nil {
		return fmt.Errorf("skill.Unpack abs destDir: %w", err)
	}

	gzr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("skill.Unpack gzip: %w", err)
	}
	defer gzr.Close()

	var totalSize int64

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("skill.Unpack next: %w", err)
		}

		// Sanitise the entry name and build the target path.
		target := filepath.Join(destDir, filepath.FromSlash(hdr.Name))
		// Prevent path traversal: the resolved path must be inside destDir.
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), destDir+string(os.PathSeparator)) {
			return fmt.Errorf("skill.Unpack: path traversal detected for entry %q", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return fmt.Errorf("skill.Unpack mkdir %s: %w", hdr.Name, err)
			}
		case tar.TypeReg:
			// Ensure parent directory exists.
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("skill.Unpack mkdir parent %s: %w", hdr.Name, err)
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return fmt.Errorf("skill.Unpack create %s: %w", hdr.Name, err)
			}
			lr := io.LimitReader(tr, maxUnpackSize-totalSize+1)
			n, err := io.Copy(f, lr)
			f.Close()
			if err != nil {
				return fmt.Errorf("skill.Unpack write %s: %w", hdr.Name, err)
			}
			totalSize += n
			if totalSize > maxUnpackSize {
				return fmt.Errorf("skill.Unpack: extraction size limit exceeded (%d bytes)", maxUnpackSize)
			}
		case tar.TypeSymlink, tar.TypeLink:
			return fmt.Errorf("skill.Unpack: unsupported entry type (symlink/hardlink): %s", hdr.Name)
		}
	}
	return nil
}

// ParseFrontmatter parses YAML frontmatter from content delimited by "---\n"
// blocks. It returns the frontmatter as a map, the body (content after the
// closing delimiter), and any error.
//
// If no frontmatter is found (content does not start with "---\n"), it returns
// nil, content, nil.
func ParseFrontmatter(content string) (map[string]interface{}, string, error) {
	const delim = "---\n"
	const delimNoNL = "---"
	if !strings.HasPrefix(content, delim) {
		return nil, content, nil
	}

	// Find the closing delimiter after the opening one.
	// The closing delimiter may be "---\n" (with trailing newline) or "---" at
	// the very end of the string (editors that strip trailing whitespace).
	rest := content[len(delim):]

	var yamlPart, body string
	if closeIdx := strings.Index(rest, delim); closeIdx >= 0 {
		// Standard case: closing "---\n" found.
		yamlPart = rest[:closeIdx]
		body = rest[closeIdx+len(delim):]
	} else if strings.HasSuffix(rest, "\n"+delimNoNL) {
		// Closing "---" at end of string, preceded by a newline.
		yamlPart = rest[:len(rest)-len("\n"+delimNoNL)]
		body = ""
	} else {
		// No closing delimiter; treat as no frontmatter.
		return nil, content, nil
	}

	fm := make(map[string]interface{})
	if err := yaml.Unmarshal([]byte(yamlPart), &fm); err != nil {
		return nil, "", fmt.Errorf("skill.ParseFrontmatter yaml: %w", err)
	}

	return fm, body, nil
}
