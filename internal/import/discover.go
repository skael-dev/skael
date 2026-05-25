package skillimport

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/skael-dev/skael/internal/scan"
	"github.com/skael-dev/skael/internal/skill"
)

type DiscoveredSkill struct {
	Name              string            `json:"name"`
	Description       string            `json:"description"`
	Path              string            `json:"path"`
	Files             []skill.FileEntry `json:"files"`
	ScanStatus        string            `json:"scan_status"`
	ScanFindingsCount int               `json:"scan_findings_count"`
	ExistingVersion   int               `json:"existing_version"`
}

func Discover(rootDir, subPath string) ([]DiscoveredSkill, error) {
	searchDir := rootDir
	if subPath != "" {
		searchDir = filepath.Join(rootDir, filepath.FromSlash(subPath))
	}

	var skillDirs []string

	if _, err := os.Stat(filepath.Join(searchDir, "SKILL.md")); err == nil {
		skillDirs = append(skillDirs, searchDir)
	} else {
		err := filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // skip inaccessible entries
			}
			if info.IsDir() {
				return nil
			}
			if info.Name() == "SKILL.md" {
				skillDirs = append(skillDirs, filepath.Dir(path))
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", searchDir, err)
		}
	}

	var results []DiscoveredSkill
	for _, dir := range skillDirs {
		ds, err := inspectSkillDir(rootDir, dir)
		if err != nil {
			continue
		}
		results = append(results, *ds)
	}

	// Dedup: if multiple directories produce the same skill name, keep the first.
	seen := map[string]bool{}
	var deduped []DiscoveredSkill
	for _, ds := range results {
		if !seen[ds.Name] {
			seen[ds.Name] = true
			deduped = append(deduped, ds)
		}
	}
	results = deduped

	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})
	return results, nil
}

// DetectPluginName looks for .claude-plugin/plugin.json in the root directory
// and returns the plugin name if found.
func DetectPluginName(rootDir string) string {
	data, err := os.ReadFile(filepath.Join(rootDir, ".claude-plugin", "plugin.json"))
	if err != nil {
		return ""
	}
	var manifest struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return ""
	}
	return manifest.Name
}

func inspectSkillDir(rootDir, skillDir string) (*DiscoveredSkill, error) {
	data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		return nil, err
	}

	fm, _, err := skill.ParseFrontmatter(string(data))
	if err != nil {
		return nil, err
	}

	name := ""
	description := ""
	if fm != nil {
		if n, ok := fm["name"].(string); ok {
			name = n
		}
		if d, ok := fm["description"].(string); ok {
			description = d
		}
	}
	if name == "" {
		name = filepath.Base(skillDir)
	}

	var files []skill.FileEntry
	filepath.Walk(skillDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(skillDir, path)
		if err != nil {
			return nil
		}
		files = append(files, skill.FileEntry{
			Path: filepath.ToSlash(rel),
			Size: info.Size(),
		})
		return nil
	})

	report, scanErr := scan.ScanDir(skillDir)
	scanStatus := "clean"
	scanCount := 0
	if scanErr == nil {
		scanStatus = report.Status
		scanCount = len(report.Findings)
	}

	relPath, _ := filepath.Rel(rootDir, skillDir)

	return &DiscoveredSkill{
		Name:              name,
		Description:       description,
		Path:              filepath.ToSlash(relPath),
		Files:             files,
		ScanStatus:        scanStatus,
		ScanFindingsCount: scanCount,
	}, nil
}
