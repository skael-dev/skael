# Skill Import Flow — Design Spec

## Overview

Add a unified import pipeline that brings skills from GitHub repos, local directories, and skills.sh into the Skael registry. All import sources funnel through one server-side pipeline: **Resolve → Fetch → Discover → Preview → Select → Import**. Imported skills become first-class registry entries with source provenance tracking.

Multi-file skills (references/, scripts/, assets/, agents/, etc. alongside SKILL.md) are already supported by the existing archive and publish infrastructure. This feature adds the *acquisition* layer — getting external skills into that pipeline without manual clone + tar + publish.

## Data Model

### New table: `import_sources`

```sql
CREATE TABLE import_sources (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    skill_id     UUID NOT NULL UNIQUE REFERENCES skills(id) ON DELETE CASCADE,
    source_type  TEXT NOT NULL,       -- "github", "local", "upload"
    source_url   TEXT NOT NULL DEFAULT '',  -- e.g. "https://github.com/anthropics/skills"
    source_path  TEXT NOT NULL DEFAULT '',  -- path within repo, e.g. "skills/skill-creator"
    source_ref   TEXT NOT NULL DEFAULT '',  -- branch/tag, e.g. "main"
    commit_sha   TEXT NOT NULL DEFAULT '',  -- exact commit at import time
    imported_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_checked TIMESTAMPTZ           -- for future update checking
);
```

One-to-one with `skills`. Skills without a row here are "native" (published via `skael publish`). Existing tables are unchanged.

## Server-Side Import Pipeline

New package: `internal/import/`

### Step 1 — Resolve source

`Resolver` normalizes a raw input into a structured `Source`:

```go
type Source struct {
    Type      string // "github", "local", "upload"
    Owner     string // GitHub owner
    Repo      string // GitHub repo name
    Ref       string // branch, tag, or SHA (default: repo default branch)
    Path      string // subdirectory within repo (empty = whole repo)
    CommitSHA string // resolved after fetch
}
```

GitHub URL parsing handles these formats:
- `https://github.com/owner/repo` → whole repo, default branch
- `https://github.com/owner/repo/tree/main/skills/foo` → specific directory on `main`
- `https://github.com/owner/repo/tree/v1.2.0` → specific tag
- `github.com/owner/repo` (no scheme) → add https://

### Step 2 — Fetch

`Fetcher` downloads repo content via GitHub's tarball API:

```
GET https://api.github.com/repos/{owner}/{repo}/tarball/{ref}
```

- Streams response to temp directory, unpacks tar.gz
- Extracts commit SHA from the tarball root directory name (format: `{owner}-{repo}-{shortsha}/`)
- Uses `GITHUB_TOKEN` env var if set (for private repos and rate limits)
- Timeout: 30 seconds
- Size limit: 50MB (reuses existing `maxUnpackSize`)
- Rejects symlinks, hardlinks, oversized files (reuses existing `Unpack` constraints)

### Step 3 — Discover

Walk the fetched tree for directories containing `SKILL.md`:

```go
type DiscoveredSkill struct {
    Name              string      `json:"name"`
    Description       string      `json:"description"`
    Path              string      `json:"path"`       // relative path within repo
    Files             []FileEntry `json:"files"`      // full file list
    ScanStatus        string      `json:"scan_status"`
    ScanFindingsCount int         `json:"scan_findings_count"`
}
```

- If `Source.Path` is set, only look there (skip full-repo discovery)
- Parse SKILL.md frontmatter for name and description
- Run `scan.ScanDir` on each skill directory
- Build file list via directory walk

### Step 4 — Import (per selected skill)

For each selected skill:

1. `skill.Pack(skillDir)` → tar.gz + checksum + manifest
2. Create skill record if it doesn't exist (upsert by name)
3. `store.CreateVersion(...)` → runs through existing publish pipeline (scan result already computed, store archive, create version record, update skill metadata)
4. Insert `import_sources` record with provenance

If a skill with the same name already exists, import creates a new version rather than failing.

## API Endpoints

### `POST /api/import/resolve`

Preview before importing. Server fetches, discovers, scans. Nothing is stored.

**Request:**
```json
{
    "url": "https://github.com/anthropics/skills"
}
```

**Response:**
```json
{
    "source": {
        "type": "github",
        "owner": "anthropics",
        "repo": "skills",
        "ref": "main",
        "commit_sha": "abc123def456"
    },
    "skills": [
        {
            "name": "skill-creator",
            "description": "Create and evaluate skills",
            "path": "skills/skill-creator",
            "files": [
                { "path": "SKILL.md", "size": 2048 },
                { "path": "scripts/run_loop.py", "size": 4096 },
                { "path": "references/schemas.md", "size": 1024 }
            ],
            "scan_status": "clean",
            "scan_findings_count": 0
        }
    ]
}
```

Rate limit: 10 requests per minute per API key.

The resolve response includes a `commit_sha` that pins the exact repo state. The import endpoint (below) re-fetches at that same SHA to guarantee consistency between what was previewed and what gets imported.

### `POST /api/import`

Execute the import for selected skills. Re-fetches the repo at the commit SHA from the resolve step (not HEAD — avoids TOCTOU where repo changes between preview and import).

**Request:**
```json
{
    "source": {
        "type": "github",
        "owner": "anthropics",
        "repo": "skills",
        "ref": "main"
    },
    "skills": ["skill-creator", "docx"]
}
```

**Response:**
```json
{
    "imported": [
        { "name": "skill-creator", "version": 1, "scan_status": "clean" },
        { "name": "docx", "version": 1, "scan_status": "clean" }
    ],
    "failed": [
        { "name": "bad-skill", "error": "SKILL.md frontmatter parse error: invalid YAML" }
    ]
}
```

### `POST /api/import/upload`

For local directory imports via CLI. Accepts multipart form data with a tar.gz archive.

- CLI packs the local directory and uploads it
- Server unpacks, discovers SKILL.md directories, runs the same pipeline
- Response matches `/api/import/resolve` format for preview, then CLI confirms and calls `/api/import`

### `GET /api/import/sources`

List all imported skills with their source provenance.

**Response:**
```json
[
    {
        "skill_name": "skill-creator",
        "source_type": "github",
        "source_url": "https://github.com/anthropics/skills",
        "source_path": "skills/skill-creator",
        "source_ref": "main",
        "commit_sha": "abc123def456",
        "imported_at": "2026-05-25T12:00:00Z"
    }
]
```

## CLI Command

### `skael import <url|path>`

Three modes, all hitting the same server API:

**GitHub URL:**
```bash
skael import https://github.com/anthropics/skills
```

Calls `/api/import/resolve`, shows a Lipgloss-styled interactive checklist:

```
  Import · github.com/anthropics/skills (main @ abc123)

  ┌──────────────────────────────────────────────────────────────┐
  │  [x]  skill-creator   Create and evaluate skills   7 files  │
  │  [x]  docx            Generate Word documents      2 files  │
  │  [ ]  pdf             PDF processing               3 files  │
  │  [ ]  pptx            PowerPoint generation        2 files  │
  └──────────────────────────────────────────────────────────────┘

  2 selected · all clean                      Import? [y/N]
```

Styling uses existing `internal/ui/` Lipgloss helpers:
- Skill names in accent color, mono font weight
- Scan status badges: green "clean", yellow "warn", red "critical" (matching web SecurityBadge)
- File counts in tertiary text color
- Box borders using Lipgloss border styles
- Selection indicators styled like the existing summary output
- Progress feedback during fetch/scan using `ui.Download` style spinner
- Success/failure summary using `ui.Success`/`ui.Error` helpers
- Import source info (repo, ref, SHA) styled with `ui.Subtle` or tertiary color

If the URL points to a specific directory, skip the checklist and import directly:
```bash
skael import https://github.com/anthropics/skills/tree/main/skills/skill-creator
```

**Local path:**
```bash
skael import ./my-skills/code-review     # single skill
skael import ./my-skills/                 # discover multiple
```

CLI discovers SKILL.md directories locally, shows the same styled checklist, packs and uploads each selected one via `/api/import/upload`.

**skills.sh search:**
```bash
skael import --search "react testing"
```

Queries skills.sh, shows styled results with install counts, user picks one, resolves to GitHub URL, then follows the GitHub flow.

**Flags:**
- `--all` — skip the selection prompt, import everything discovered
- `--json` — structured output for automation (suppresses Lipgloss styling via `ui.JSONMode`)
- `--dry-run` — resolve and preview without importing

## Web UI

### Import button on skill list page

Button in the header area alongside search/filters. Opens a modal with:
- URL input field (paste a GitHub URL)
- "Search skills.sh" tab with a search input
- Both resolve to the same preview flow

### Import preview modal

After resolving, shows:
- Source info header (repo name, branch, commit SHA)
- List of discovered skills, each with:
  - Name (mono font), description
  - File count, security badge
  - Checkbox for selection
- Skills that already exist in the registry show "already imported" indicator, unchecked by default
- "Import selected" button at the bottom with count

### Source provenance on skill detail page

On the detail page, if a skill has an `import_sources` row, show a metadata row:
- "Imported from github.com/anthropics/skills · main @ abc123 · 3 days ago"
- Links to the source URL

## Security and Constraints

**Fetch safety:**
- GitHub tarball API only — no arbitrary URL fetching, no `git clone`, no SSRF risk
- `GITHUB_TOKEN` env var for private repos and higher rate limits (unauthenticated: 60 req/hr)
- Fetch timeout: 30 seconds
- 50MB unpack limit. If exceeded, error message directs user to specify a subdirectory path

**Scanning:**
- Imported skills go through the same `scan.ScanDir` pipeline as published skills
- Critical/warn findings are shown in preview but do NOT auto-block import (unlike publish). Admin sees findings and decides. Rationale: false positives on third-party code are common; admin can review and approve
- Imported skills start as "unreviewed"

**Name collisions:**
- If imported skill name matches an existing skill, import creates a new version
- No rename-on-import in this scope

**Rate limiting:**
- `/api/import/resolve`: 10 req/min per API key (the expensive endpoint)
- `/api/import`: uses existing publish rate limits

## Server Configuration

New env vars:

| Variable | Required | Default | Description |
|---|---|---|---|
| `GITHUB_TOKEN` | no | — | GitHub personal access token for private repos and rate limits |

## Out of Scope

- Rename on import (use a different name than the source)
- Auto-update from upstream (manual reimport only)
- Non-GitHub sources beyond skills.sh (GitLab, Bitbucket, etc.)
- `.skill` package format support
- Deep skills.sh API integration (install counts, ratings)
- Scheduled update checking (future: `GET /api/import/check-updates/:name`)
