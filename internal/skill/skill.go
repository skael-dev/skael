package skill

import (
	"encoding/json"
	"time"
)

// Skill represents a skill entry in the registry.
type Skill struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	DisplayName    string          `json:"display_name,omitempty"`
	Description    string          `json:"description"`
	Content        string          `json:"content,omitempty"`
	LatestVersion  int             `json:"latest_version"`
	Frontmatter    json.RawMessage `json:"frontmatter"`
	Author         string          `json:"author"`
	License        string          `json:"license"`
	Compatibility  string          `json:"compatibility"`
	Tags           []string        `json:"tags"`
	SpecCompliance string          `json:"spec_compliance"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	ReviewedAt     *time.Time      `json:"reviewed_at"`
	ReviewedBy     string          `json:"reviewed_by"`
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

	// Description and Content are the rendered prose this version would
	// serve. They are carried on the version, not only on the skill row,
	// because a held version writes nothing to the skill row: releasing it
	// later has to get the prose from somewhere, and re-reading the archive
	// to recover text the database already saw would be the wrong place.
	//
	// Both are json:"-" on purpose. The wire shape of a version is unchanged,
	// and a version endpoint is not a second way to read a held version's
	// body — the gate withholds exactly that.
	Description string          `json:"-"`
	Content     string          `json:"-"`
	ScanResult  json.RawMessage `json:"scan_result,omitempty"`
	PublishedBy string          `json:"published_by"`
	CreatedAt   time.Time       `json:"created_at"`

	// GateState is one of "released", "needs_review", "rejected". Only a
	// released version is pointed at by skills.latest_version.
	GateState    string          `json:"gate_state"`
	GateDecision json.RawMessage `json:"gate_decision,omitempty"`
	GatedBy      string          `json:"gated_by,omitempty"`
	GatedAt      *time.Time      `json:"gated_at,omitempty"`
	GateNote     string          `json:"gate_note,omitempty"`

	// HoldReasons are the reason kinds still standing between this version
	// and release. Empty on a released or rejected version.
	HoldReasons []string `json:"hold_reasons"`
}

// FileEntry describes a single file within a skill archive.
type FileEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}
