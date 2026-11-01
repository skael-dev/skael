package sets

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/skael-dev/skael/internal/platform"
)

// BundleRoot is where a bundle places skills, for any agent to read.
const BundleRoot = ".agents/skills"

// BundleManifest is written at BundleRoot/manifest.json.
type BundleManifest struct {
	Set          string                `json:"set"`
	ResolutionID string                `json:"resolution_id"`
	Complete     bool                  `json:"complete"`
	Skills       []BundleManifestSkill `json:"skills"`
}

// BundleManifestSkill names one skill in a bundle. SHA256 is the digest of the
// skill's archive, which the bundle carries at Archive so a consumer can check
// it: an unpacked tree cannot reproduce the digest of an uploaded archive.
type BundleManifestSkill struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
	SHA256  string `json:"sha256"`
	Archive string `json:"archive"`
}

// GoneError names the skill a stored resolution can no longer serve.
type GoneError struct{ Skill string }

func (e *GoneError) Error() string {
	return fmt.Sprintf("resolution names skill %q, which was deleted", e.Skill)
}

// CheckAvailable returns the archive path of each entry, or a GoneError before
// any byte of the response is written.
func (s *Store) CheckAvailable(ctx context.Context, res *Resolution) (map[string]string, error) {
	paths := make(map[string]string, len(res.Entries))
	for _, e := range res.Entries {
		p, err := s.ArchivePath(ctx, e.Name, e.Version)
		if err != nil {
			return nil, err
		}
		if p == "" {
			return nil, &GoneError{Skill: e.Name}
		}
		paths[e.Name] = p
	}
	return paths, nil
}

// WriteBundle writes the tar.gz of one resolution to w. It builds on demand,
// because the skill archives it copies are already content-addressed.
func WriteBundle(ctx context.Context, w io.Writer, storage platform.Storage, res *Resolution, paths map[string]string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	manifest := BundleManifest{Set: res.Set, ResolutionID: res.ID, Complete: res.Complete, Skills: []BundleManifestSkill{}}
	for _, e := range res.Entries {
		archive := fmt.Sprintf("%s/.archives/%s@%d.tar.gz", BundleRoot, e.Name, e.Version)
		manifest.Skills = append(manifest.Skills, BundleManifestSkill{Name: e.Name, Version: e.Version, SHA256: e.SHA256, Archive: archive})
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("sets.WriteBundle: %w", err)
	}
	if err := writeFile(tw, BundleRoot+"/manifest.json", raw, 0o644); err != nil {
		return err
	}

	for i, e := range res.Entries {
		data, err := readArchive(ctx, storage, paths[e.Name])
		if err != nil {
			return fmt.Errorf("sets.WriteBundle: %s: %w", e.Name, err)
		}
		if err := writeFile(tw, manifest.Skills[i].Archive, data, 0o644); err != nil {
			return err
		}
		if err := copyTree(tw, data, BundleRoot+"/"+e.Name); err != nil {
			return fmt.Errorf("sets.WriteBundle: %s: %w", e.Name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("sets.WriteBundle: %w", err)
	}
	return gz.Close()
}

func readArchive(ctx context.Context, storage platform.Storage, p string) ([]byte, error) {
	rc, err := storage.Read(ctx, p)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func writeFile(tw *tar.Writer, name string, data []byte, mode int64) error {
	hdr := &tar.Header{Name: name, Mode: mode, Size: int64(len(data)), Typeflag: tar.TypeReg, Format: tar.FormatPAX}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("sets: write %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("sets: write %s: %w", name, err)
	}
	return nil
}

// copyTree copies a skill archive's regular files under prefix. Publish already
// rejects links and traversal. This repeats the check because a bundle reaches
// machines with no agent in the loop.
func copyTree(tw *tar.Writer, archive []byte, prefix string) error {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return fmt.Errorf("entry %q has unsupported type %d", hdr.Name, hdr.Typeflag)
		}
		clean := path.Clean(hdr.Name)
		if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("entry %q leaves the skill directory", hdr.Name)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		if err := writeFile(tw, prefix+"/"+clean, data, hdr.Mode&0o777); err != nil {
			return err
		}
	}
}
