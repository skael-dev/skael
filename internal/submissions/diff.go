package submissions

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/pmezard/go-difflib/difflib"

	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/skill"
)

// maxEditableBytes bounds what a diff carries for the editor.
const maxEditableBytes = 512 << 10

// SubmissionFileDiff is one file of a submission against the served version.
type SubmissionFileDiff struct {
	Path   string `json:"path"`
	Status string `json:"status" doc:"added, removed, modified or unchanged"`
	Binary bool   `json:"binary"`
	Size   int64  `json:"size"`
	Diff   string `json:"diff,omitempty"`
	// Content is the submitted text, for edit then approve.
	Content string `json:"content,omitempty"`
}

// SubmissionDiff is what a reviewer reads. A submission may hold scripts, so
// every file is shown, not only SKILL.md. Against is 0 for a new skill.
type SubmissionDiff struct {
	Against int                  `json:"against"`
	Files   []SubmissionFileDiff `json:"files"`
}

func readAll(ctx context.Context, storage platform.Storage, p string) ([]byte, error) {
	rc, err := storage.Read(ctx, p)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func readFiles(archive []byte) (map[string][]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	files := map[string][]byte{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		files[path.Clean(hdr.Name)] = data
	}
}

func archiveManifest(archive []byte) ([]skill.FileEntry, error) {
	files, err := readFiles(archive)
	if err != nil {
		return nil, err
	}
	manifest := make([]skill.FileEntry, 0, len(files))
	for name, data := range files {
		manifest = append(manifest, skill.FileEntry{Path: name, Size: int64(len(data))})
	}
	sort.Slice(manifest, func(i, j int) bool { return manifest[i].Path < manifest[j].Path })
	return manifest, nil
}

// splitLines keeps each line ending. difflib.SplitLines appends a phantom empty
// line, which shows in a diff as a stray added line.
func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	for i := range lines {
		lines[i] += "\n"
	}
	return lines
}

func isBinary(data []byte) bool {
	head := data
	if len(head) > 8192 {
		head = head[:8192]
	}
	return bytes.IndexByte(head, 0) >= 0
}

// Diff compares a submission with the skill's served version.
func (s *Service) Diff(ctx context.Context, sub *Submission) (*SubmissionDiff, error) {
	archive, err := readAll(ctx, s.storage, sub.ArchivePath)
	if err != nil {
		return nil, fmt.Errorf("submission diff: %w", err)
	}
	to, err := readFiles(archive)
	if err != nil {
		return nil, fmt.Errorf("submission diff: %w", err)
	}

	out := &SubmissionDiff{Files: []SubmissionFileDiff{}}
	from := map[string][]byte{}
	against := sub.DiffAgainst
	if sub.State == StatePending {
		sk, err := s.skills.GetByName(ctx, sub.SkillName)
		if err != nil {
			return nil, fmt.Errorf("submission diff: %w", err)
		}
		if sk != nil {
			against = sk.LatestVersion
		}
	}
	if against > 0 {
		ver, err := s.skills.GetVersion(ctx, sub.SkillName, against)
		if err != nil || ver == nil {
			return nil, fmt.Errorf("submission diff: served version: %w", err)
		}
		base, err := readAll(ctx, s.storage, ver.ArchivePath)
		if err != nil {
			return nil, fmt.Errorf("submission diff: served archive: %w", err)
		}
		if from, err = readFiles(base); err != nil {
			return nil, fmt.Errorf("submission diff: served archive: %w", err)
		}
		out.Against = ver.Version
	}

	names := map[string]bool{}
	for n := range from {
		names[n] = true
	}
	for n := range to {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	for _, name := range sorted {
		oldData, inOld := from[name]
		newData, inNew := to[name]
		f := SubmissionFileDiff{Path: name, Size: int64(len(newData)), Binary: isBinary(oldData) || isBinary(newData)}
		switch {
		case !inOld:
			f.Status = "added"
		case !inNew:
			f.Status = "removed"
		case bytes.Equal(oldData, newData):
			f.Status = "unchanged"
		default:
			f.Status = "modified"
		}
		if !f.Binary {
			if inNew && len(newData) <= maxEditableBytes {
				f.Content = string(newData)
			}
			if f.Status != "unchanged" {
				f.Diff, err = difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
					A: splitLines(oldData), B: splitLines(newData),
					FromFile: "served/" + name, ToFile: "submitted/" + name, Context: 3,
				})
				if err != nil {
					return nil, fmt.Errorf("submission diff: %s: %w", name, err)
				}
			}
		}
		out.Files = append(out.Files, f)
	}
	return out, nil
}
