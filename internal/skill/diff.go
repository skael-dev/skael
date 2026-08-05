package skill

import (
	"context"
	"fmt"

	"github.com/pmezard/go-difflib/difflib"
)

// FileChange describes how a single file differs between the version being
// diffed and the currently-served baseline.
type FileChange struct {
	Path   string `json:"path"`
	Status string `json:"status"` // "added", "removed", or "modified"
}

// VersionDiff is what a reviewer sees when deciding whether to approve a
// version held for review: what changed in SKILL.md, and which files were
// added, removed, or modified relative to the version currently being
// served.
type VersionDiff struct {
	// Against is the served version number this diff was computed against.
	// 0 means there is no baseline — this is the skill's first version, so
	// there is nothing to diff against rather than an empty phantom one.
	Against int          `json:"against"`
	SkillMD string       `json:"skill_md"`
	Files   []FileChange `json:"files"`
}

// DiffAgainstServed compares the given version of a skill against the
// version currently served (skills.latest_version). Returns nil, nil if the
// skill or the target version does not exist, matching GetByName/GetVersion.
//
// When the skill has no served version yet (LatestVersion == 0 — this is
// the first version ever created for it), Against is 0, SkillMD is empty,
// and every file in the target version's manifest is reported as "added":
// there is no baseline to render a text diff against, and rendering one
// against an empty phantom version would be misleading, not just unhelpful.
func (s *Store) DiffAgainstServed(ctx context.Context, name string, version int) (*VersionDiff, error) {
	sk, err := s.GetByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.DiffAgainstServed: get skill: %w", err)
	}
	if sk == nil {
		return nil, nil
	}

	target, err := s.GetVersion(ctx, name, version)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.DiffAgainstServed: get target version: %w", err)
	}
	if target == nil {
		return nil, nil
	}

	if sk.LatestVersion == 0 {
		files := make([]FileChange, 0, len(target.FileManifest))
		for _, f := range target.FileManifest {
			files = append(files, FileChange{Path: f.Path, Status: "added"})
		}
		return &VersionDiff{Against: 0, Files: files}, nil
	}

	baseline, err := s.GetVersion(ctx, name, sk.LatestVersion)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.DiffAgainstServed: get baseline version: %w", err)
	}
	if baseline == nil {
		return nil, fmt.Errorf("skill.Store.DiffAgainstServed: served version %d not found for skill %q", sk.LatestVersion, name)
	}

	skillMD, err := unifiedSkillMDDiff(baseline.Content, target.Content, sk.LatestVersion, version)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.DiffAgainstServed: diff SKILL.md: %w", err)
	}

	return &VersionDiff{
		Against: sk.LatestVersion,
		SkillMD: skillMD,
		Files:   diffManifests(baseline.FileManifest, target.FileManifest),
	}, nil
}

// unifiedSkillMDDiff renders a standard unified diff (---/+++/@@ hunks) of
// two SKILL.md bodies. Identical content produces an empty string rather
// than a no-op diff header, so a reviewer's "did the prose change" check is
// a plain emptiness check.
func unifiedSkillMDDiff(from, to string, fromVersion, toVersion int) (string, error) {
	if from == to {
		return "", nil
	}
	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(from),
		B:        difflib.SplitLines(to),
		FromFile: fmt.Sprintf("v%d/SKILL.md", fromVersion),
		ToFile:   fmt.Sprintf("v%d/SKILL.md", toVersion),
		Context:  3,
	}
	return difflib.GetUnifiedDiffString(diff)
}

// diffManifests compares two file manifests by path and size, reporting a
// FileChange for every path that was added, removed, or changed size.
// Unchanged paths (same path, same size) are omitted — Files is a list of
// what changed, not the full manifest. Order follows the target manifest
// first (what a reviewer sees in the new archive), then anything left over
// that only the baseline had.
func diffManifests(from, to []FileEntry) []FileChange {
	fromSize := make(map[string]int64, len(from))
	for _, f := range from {
		fromSize[f.Path] = f.Size
	}
	toSize := make(map[string]int64, len(to))
	for _, f := range to {
		toSize[f.Path] = f.Size
	}

	var changes []FileChange
	seen := make(map[string]bool, len(from)+len(to))

	for _, f := range to {
		if seen[f.Path] {
			continue
		}
		seen[f.Path] = true
		if size, ok := fromSize[f.Path]; !ok {
			changes = append(changes, FileChange{Path: f.Path, Status: "added"})
		} else if size != f.Size {
			changes = append(changes, FileChange{Path: f.Path, Status: "modified"})
		}
	}
	for _, f := range from {
		if seen[f.Path] {
			continue
		}
		seen[f.Path] = true
		if _, ok := toSize[f.Path]; !ok {
			changes = append(changes, FileChange{Path: f.Path, Status: "removed"})
		}
	}
	return changes
}
