package skill

import (
	"encoding/json"
	"time"
)

// Skill represents a skill entry in the registry.
type Skill struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	DisplayName   string          `json:"display_name,omitempty"`
	Description   string          `json:"description"`
	Content       string          `json:"content,omitempty"`
	LatestVersion int             `json:"latest_version"`
	Frontmatter   json.RawMessage `json:"frontmatter"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// Version represents a specific published version of a skill.
type Version struct {
	ID           string          `json:"id"`
	SkillID      string          `json:"skill_id"`
	Version      int             `json:"version"`
	ArchivePath  string          `json:"-"`
	Checksum     string          `json:"checksum"`
	Changelog    string          `json:"changelog"`
	Frontmatter  json.RawMessage `json:"frontmatter"`
	FileManifest []FileEntry     `json:"file_manifest"`
	ScanResult   json.RawMessage `json:"scan_result,omitempty"`
	PublishedBy  string          `json:"published_by"`
	CreatedAt    time.Time       `json:"created_at"`
}

// FileEntry describes a single file within a skill archive.
type FileEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}
