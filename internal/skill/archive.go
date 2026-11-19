package skill

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

// SuiteDir is the authored eval suite at a skill folder's root. It is never
// part of a version: a suite edit must not push a new version to every
// installer, and an installer must never receive the answer key.
const SuiteDir = "evals"

// Pack creates a tar.gz archive of the directory at dir, without evals/.
// It returns the archive bytes, a sha256 hex checksum, a file manifest, and
// any error encountered. Pack returns an error if SKILL.md is not present in dir.
func Pack(dir string) ([]byte, string, []FileEntry, error) {
	// Require SKILL.md in the directory.
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); os.IsNotExist(err) {
		return nil, "", nil, fmt.Errorf("skill.Pack: SKILL.md not found in %s", dir)
	}
	entries, err := collect(dir, func(rel string) bool { return rel != SuiteDir })
	if err != nil {
		return nil, "", nil, fmt.Errorf("skill.Pack walk: %w", err)
	}
	return packEntries(dir, entries, "skill.Pack")
}

// PackUpload packs the whole folder, evals/ included, for a client that sends
// a skill to the server. The server lifts evals/ out before the version is
// made, so the version digest is still Pack's.
func PackUpload(dir string) ([]byte, []FileEntry, error) {
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("skill.PackUpload: SKILL.md not found in %s", dir)
	}
	entries, err := collect(dir, func(string) bool { return true })
	if err != nil {
		return nil, nil, fmt.Errorf("skill.PackUpload walk: %w", err)
	}
	archive, _, entries, err := packEntries(dir, entries, "skill.PackUpload")
	return archive, entries, err
}

// PackSuite packs dir/evals/ with paths that keep the evals/ prefix, which is
// the layout the suite registry stores. It returns nil when dir has no evals/.
func PackSuite(dir string) ([]byte, error) {
	info, err := os.Stat(filepath.Join(dir, SuiteDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("skill.PackSuite: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("skill.PackSuite: %s is not a directory", SuiteDir)
	}
	entries, err := collect(dir, func(rel string) bool { return rel == SuiteDir })
	if err != nil {
		return nil, fmt.Errorf("skill.PackSuite walk: %w", err)
	}
	if len(entries) == 0 {
		return nil, nil
	}
	archive, _, _, err := packEntries(dir, entries, "skill.PackSuite")
	return archive, err
}

// collect lists the regular files under dir whose top-level name keep accepts.
func collect(dir string, keep func(top string) bool) ([]FileEntry, error) {
	var entries []FileEntry
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		// Use forward slashes in archive paths regardless of OS.
		rel = filepath.ToSlash(rel)
		if rel != "." {
			top, _, _ := strings.Cut(rel, "/")
			if !keep(top) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		entries = append(entries, FileEntry{Path: rel, Size: info.Size()})
		return nil
	})
	return entries, err
}

func packEntries(dir string, entries []FileEntry, op string) ([]byte, string, []FileEntry, error) {

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
			return nil, "", nil, fmt.Errorf("%s stat %s: %w", op, entry.Path, err)
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return nil, "", nil, fmt.Errorf("%s header %s: %w", op, entry.Path, err)
		}
		// Use the relative slash-separated path as the archive name.
		hdr.Name = entry.Path
		normalizeHeader(hdr, info)

		if err := tw.WriteHeader(hdr); err != nil {
			return nil, "", nil, fmt.Errorf("%s write header %s: %w", op, entry.Path, err)
		}

		f, err := os.Open(fullPath)
		if err != nil {
			return nil, "", nil, fmt.Errorf("%s open %s: %w", op, entry.Path, err)
		}
		if _, err := io.Copy(tw, f); err != nil {
			f.Close()
			return nil, "", nil, fmt.Errorf("%s copy %s: %w", op, entry.Path, err)
		}
		f.Close()
	}

	if err := tw.Close(); err != nil {
		return nil, "", nil, fmt.Errorf("%s close tar: %w", op, err)
	}
	if err := gzw.Close(); err != nil {
		return nil, "", nil, fmt.Errorf("%s close gzip: %w", op, err)
	}

	archiveBuf = archiveWriter.buf
	checksum := hex.EncodeToString(h.Sum(nil))

	return archiveBuf, checksum, entries, nil
}

// normalizeHeader strips everything that is not the file's content or its
// path, so the same bytes always pack to the same archive and therefore the
// same checksum. Without it a header carries the file's mtime and the packing
// user's ids, and an unchanged republish one second later is a new version
// with a new checksum — which defeats the unchanged short-circuit, the
// content-addressed storage path, and any dedupe built on either.
//
// Only the executable bit survives, because Unpack masks permissions anyway
// and a skill that ships a script needs it.
func normalizeHeader(hdr *tar.Header, info os.FileInfo) {
	hdr.ModTime = time.Unix(0, 0).UTC()
	hdr.AccessTime = time.Time{}
	hdr.ChangeTime = time.Time{}
	hdr.Uid, hdr.Gid = 0, 0
	hdr.Uname, hdr.Gname = "", ""
	hdr.Mode = 0o644
	if info.Mode().Perm()&0o111 != 0 {
		hdr.Mode = 0o755
	}
	// USTAR so a stray sub-second timestamp or long name cannot switch the
	// writer into PAX and change the bytes for reasons unrelated to content.
	hdr.Format = tar.FormatUSTAR
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

// maxFileSize is the maximum size allowed for a single file within an archive.
// Files larger than this are rejected at extraction time so they cannot bypass
// security scanners that operate on a per-file size budget.
const maxFileSize = 1 << 20 // 1 MiB

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
			// Reject individual files that exceed the per-file size limit.
			// This prevents oversized files from bypassing content scanners
			// that operate within a 1 MiB per-file budget.
			if hdr.Size > maxFileSize {
				return fmt.Errorf("skill.Unpack: file size limit exceeded for %s (%d bytes > %d)", hdr.Name, hdr.Size, maxFileSize)
			}
			// Ensure parent directory exists.
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("skill.Unpack mkdir parent %s: %w", hdr.Name, err)
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
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
		default:
			return fmt.Errorf("skill.Unpack: unsupported tar entry type %d: %s", hdr.Typeflag, hdr.Name)
		}
	}
	return nil
}

// errFileNotInArchive reports that an archive has no regular file at a path.
var errFileNotInArchive = errors.New("file not in archive")

// cleanArchivePath normalises a requested archive path. It returns false for a
// path that is empty, absolute or that climbs out of the archive root.
func cleanArchivePath(p string) (string, bool) {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00") {
		return "", false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", false
		}
	}
	cleaned := path.Clean(p)
	return cleaned, cleaned != "."
}

// readArchiveFile returns the bytes of the first regular file in a tar.gz
// whose cleaned name equals want. Publish limits each file to maxFileSize, so
// the whole file fits in memory.
func readArchiveFile(r io.Reader, want string) ([]byte, error) {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("skill.readArchiveFile gzip: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, errFileNotInArchive
		}
		if err != nil {
			return nil, fmt.Errorf("skill.readArchiveFile next: %w", err)
		}
		if hdr.Typeflag == tar.TypeReg && path.Clean(hdr.Name) == want {
			content, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("skill.readArchiveFile read %s: %w", hdr.Name, err)
			}
			return content, nil
		}
	}
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

// SpecValidation holds the result of validating a skill's frontmatter against
// the Agent Skills spec format.
type SpecValidation struct {
	Compliance  string
	Author      string
	License     string
	Compat      string
	Tags        []string
	DisplayName string
	Warnings    []string
}

// specKebab matches valid spec-format names: 1-64 lowercase kebab-case.
var specKebab = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ValidateSpec extracts spec-defined metadata from parsed frontmatter and
// computes a compliance level ("full", "partial", or "none").
func ValidateSpec(fm map[string]interface{}, skillName string) *SpecValidation {
	v := &SpecValidation{
		Tags: []string{},
	}

	if fm == nil {
		v.Compliance = "none"
		v.Warnings = append(v.Warnings, "no frontmatter found")
		return v
	}

	// Extract description.
	desc, _ := fm["description"].(string)

	// Extract metadata bag fields.
	if meta, ok := fm["metadata"].(map[string]interface{}); ok {
		if a, ok := meta["author"].(string); ok {
			v.Author = a
		}
		if rawTags, ok := meta["tags"].([]interface{}); ok {
			for _, t := range rawTags {
				if s, ok := t.(string); ok {
					v.Tags = append(v.Tags, s)
				}
			}
		}
	}

	// Top-level author/tags override metadata bag if present.
	if a, ok := fm["author"].(string); ok && a != "" {
		v.Author = a
	}
	if rawTags, ok := fm["tags"].([]interface{}); ok {
		tags := make([]string, 0, len(rawTags))
		for _, t := range rawTags {
			if s, ok := t.(string); ok {
				tags = append(tags, s)
			}
		}
		if len(tags) > 0 {
			v.Tags = tags
		}
	}

	// Top-level license and compatibility.
	if l, ok := fm["license"].(string); ok {
		v.License = l
	}
	if c, ok := fm["compatibility"].(string); ok {
		v.Compat = c
	}

	// Name check — used for display_name extraction and format warnings.
	if fmName, ok := fm["name"].(string); ok && fmName != "" {
		hasUpper := false
		hasSpace := false
		for _, r := range fmName {
			if unicode.IsUpper(r) {
				hasUpper = true
			}
			if unicode.IsSpace(r) {
				hasSpace = true
			}
		}
		if hasUpper || hasSpace {
			v.DisplayName = fmName
		}

		normalized := strings.ToLower(strings.ReplaceAll(fmName, " ", "-"))
		if normalized != skillName {
			if specKebab.MatchString(normalized) && len(normalized) <= 64 {
				v.Warnings = append(v.Warnings,
					fmt.Sprintf("frontmatter name %q differs from skill name %q", fmName, skillName))
			} else {
				v.Warnings = append(v.Warnings,
					fmt.Sprintf("frontmatter name %q does not match spec format (1-64 kebab-case)", fmName))
			}
		}
	}

	// Compute compliance level.
	switch {
	case desc == "":
		v.Compliance = "partial"
		v.Warnings = append(v.Warnings, "missing description")
	case len(desc) > 1024:
		v.Compliance = "partial"
		v.Warnings = append(v.Warnings,
			fmt.Sprintf("description exceeds 1024 chars (%d)", len(desc)))
	default:
		v.Compliance = "full"
	}

	return v
}
