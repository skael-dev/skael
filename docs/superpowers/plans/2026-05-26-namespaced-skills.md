# Namespaced Skills, Aliases, and Merge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Unify skill identity across agents by adding alias mapping, merge capability, import dedup with namespace prompts, and hook normalization so analytics aggregate correctly regardless of which agent fires the event.

**Architecture:** New `skill_aliases` table maps variant names to canonical skills. Analytics queries JOIN through aliases so events from any agent-specific name format aggregate under one skill. A merge endpoint combines two skill records into one. Import discovers and deduplicates plugin repo mirrors, offers namespace prefixing, and auto-creates reverse aliases. Hook scripts strip the OpenCode `skills_` prefix at ingestion.

**Tech Stack:** Go (pgx, Huma, Chi), React (existing patterns), PostgreSQL, bash (hook script)

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Create | `internal/platform/migrate/004_skill_aliases.sql` | Migration |
| Create | `internal/skill/alias.go` | Alias store: CRUD + list |
| Create | `internal/skill/alias_test.go` | Alias store tests |
| Create | `internal/skill/merge.go` | Merge logic |
| Create | `internal/skill/merge_test.go` | Merge tests |
| Modify | `internal/skill/routes.go` | Update name regex, add alias + merge routes |
| Modify | `internal/analytics/event.go` | Update all queries with alias JOIN |
| Modify | `internal/import/discover.go` | Add dedup + plugin.json detection |
| Modify | `internal/import/routes.go` | Pass plugin name in resolve response, namespace in import |
| Modify | `cli/hooks/script.go` | Strip `skills_` prefix |
| Modify | `cli/hooks/opencode_plugin.go` | Strip `skills_` prefix |
| Modify | `cli/import.go` | Namespace prompt in CLI |
| Modify | `web/src/features/skills/skill-card.tsx` | Namespace badge display |
| Modify | `web/src/features/skills/skill-detail.tsx` | Namespace display + aliases section |
| Modify | `web/src/features/skills/unregistered-tab.tsx` | Merge action + similarity hint |

---

### Task 1: Migration — `skill_aliases` table + name regex update

**Files:**
- Create: `internal/platform/migrate/004_skill_aliases.sql`
- Modify: `internal/skill/routes.go`

- [ ] **Step 1: Write the migration**

```sql
-- +goose Up
CREATE TABLE skill_aliases (
    alias      TEXT PRIMARY KEY,
    canonical  TEXT NOT NULL REFERENCES skills(name) ON UPDATE CASCADE ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_skill_aliases_canonical ON skill_aliases(canonical);

-- +goose Down
DROP TABLE IF EXISTS skill_aliases;
```

- [ ] **Step 2: Update the name validation regex**

In `internal/skill/routes.go`, change line 39:

```go
// Before:
var validSkillName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// After:
var validSkillName = regexp.MustCompile(`^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$`)
```

- [ ] **Step 3: Verify build and migration**

Run: `go build ./... && just migrate`

- [ ] **Step 4: Commit**

```bash
git add internal/platform/migrate/004_skill_aliases.sql internal/skill/routes.go
git commit -m "feat(aliases): add skill_aliases migration and allow colons in names"
```

---

### Task 2: Alias store — CRUD

**Files:**
- Create: `internal/skill/alias.go`
- Create: `internal/skill/alias_test.go`

- [ ] **Step 1: Write the alias store**

```go
// internal/skill/alias.go
package skill

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Alias maps a variant skill name to a canonical registered name.
type Alias struct {
	Alias     string    `json:"alias"`
	Canonical string    `json:"canonical"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateAlias adds an alias for a canonical skill name. Idempotent.
func (s *Store) CreateAlias(ctx context.Context, alias, canonical string) error {
	const q = `INSERT INTO skill_aliases (alias, canonical) VALUES ($1, $2) ON CONFLICT (alias) DO UPDATE SET canonical = $2`
	if _, err := s.pool.Exec(ctx, q, alias, canonical); err != nil {
		return fmt.Errorf("skill.Store.CreateAlias: %w", err)
	}
	return nil
}

// DeleteAlias removes an alias.
func (s *Store) DeleteAlias(ctx context.Context, alias string) error {
	const q = `DELETE FROM skill_aliases WHERE alias = $1`
	if _, err := s.pool.Exec(ctx, q, alias); err != nil {
		return fmt.Errorf("skill.Store.DeleteAlias: %w", err)
	}
	return nil
}

// ListAliases returns all aliases for a canonical skill name.
func (s *Store) ListAliases(ctx context.Context, canonical string) ([]Alias, error) {
	const q = `SELECT alias, canonical, created_at FROM skill_aliases WHERE canonical = $1 ORDER BY alias`
	rows, err := s.pool.Query(ctx, q, canonical)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.ListAliases query: %w", err)
	}
	defer rows.Close()

	var results []Alias
	for rows.Next() {
		var a Alias
		if err := rows.Scan(&a.Alias, &a.Canonical, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("skill.Store.ListAliases scan: %w", err)
		}
		results = append(results, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("skill.Store.ListAliases rows: %w", err)
	}
	if results == nil {
		results = []Alias{}
	}
	return results, nil
}

// ResolveAlias looks up a name in the alias table. Returns the canonical name if found, empty string if not.
func (s *Store) ResolveAlias(ctx context.Context, name string) (string, error) {
	const q = `SELECT canonical FROM skill_aliases WHERE alias = $1`
	var canonical string
	err := s.pool.QueryRow(ctx, q, name).Scan(&canonical)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("skill.Store.ResolveAlias: %w", err)
	}
	return canonical, nil
}
```

- [ ] **Step 2: Write tests**

```go
// internal/skill/alias_test.go
package skill

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/skael-dev/skael/internal/testutil"
)

func TestAlias_CreateAndList(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := NewStore(pool)

	store.Create(ctx, "brainstorming", "", "test", "", json.RawMessage(`{}`))

	if err := store.CreateAlias(ctx, "superpowers:brainstorming", "brainstorming"); err != nil {
		t.Fatalf("CreateAlias: %v", err)
	}

	aliases, err := store.ListAliases(ctx, "brainstorming")
	if err != nil {
		t.Fatalf("ListAliases: %v", err)
	}
	if len(aliases) != 1 {
		t.Fatalf("got %d aliases, want 1", len(aliases))
	}
	if aliases[0].Alias != "superpowers:brainstorming" {
		t.Errorf("alias = %q, want %q", aliases[0].Alias, "superpowers:brainstorming")
	}
}

func TestAlias_Resolve(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := NewStore(pool)

	store.Create(ctx, "brainstorming", "", "test", "", json.RawMessage(`{}`))
	store.CreateAlias(ctx, "superpowers:brainstorming", "brainstorming")

	canonical, err := store.ResolveAlias(ctx, "superpowers:brainstorming")
	if err != nil {
		t.Fatalf("ResolveAlias: %v", err)
	}
	if canonical != "brainstorming" {
		t.Errorf("canonical = %q, want %q", canonical, "brainstorming")
	}

	notFound, err := store.ResolveAlias(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("ResolveAlias not found: %v", err)
	}
	if notFound != "" {
		t.Errorf("expected empty string for nonexistent, got %q", notFound)
	}
}

func TestAlias_Delete(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := NewStore(pool)

	store.Create(ctx, "brainstorming", "", "test", "", json.RawMessage(`{}`))
	store.CreateAlias(ctx, "superpowers:brainstorming", "brainstorming")
	store.DeleteAlias(ctx, "superpowers:brainstorming")

	aliases, _ := store.ListAliases(ctx, "brainstorming")
	if len(aliases) != 0 {
		t.Errorf("got %d aliases after delete, want 0", len(aliases))
	}
}

func TestAlias_Idempotent(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := NewStore(pool)

	store.Create(ctx, "brainstorming", "", "test", "", json.RawMessage(`{}`))

	if err := store.CreateAlias(ctx, "superpowers:brainstorming", "brainstorming"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := store.CreateAlias(ctx, "superpowers:brainstorming", "brainstorming"); err != nil {
		t.Fatalf("second (should be idempotent): %v", err)
	}
}
```

- [ ] **Step 3: Run tests**

Run: `cd /Users/nathananderson-tennant/Development/skael && go test ./internal/skill/ -run TestAlias -v -count=1`

- [ ] **Step 4: Commit**

```bash
git add internal/skill/alias.go internal/skill/alias_test.go
git commit -m "feat(aliases): add alias store with CRUD and resolve"
```

---

### Task 3: Merge logic

**Files:**
- Create: `internal/skill/merge.go`
- Create: `internal/skill/merge_test.go`

- [ ] **Step 1: Write the merge function**

```go
// internal/skill/merge.go
package skill

import (
	"context"
	"fmt"
)

// Merge absorbs the source skill into the target skill within a single transaction.
// It re-parents all versions from source to target, creates an alias from source name
// to target name, and deletes the source skill.
func (s *Store) Merge(ctx context.Context, sourceName, targetName string) (*Skill, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.Merge begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Look up both skills.
	var sourceID, targetID string
	var targetLatest int
	err = tx.QueryRow(ctx, `SELECT id FROM skills WHERE name = $1`, sourceName).Scan(&sourceID)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.Merge source not found: %w", err)
	}
	err = tx.QueryRow(ctx, `SELECT id, latest_version FROM skills WHERE name = $1`, targetName).Scan(&targetID, &targetLatest)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.Merge target not found: %w", err)
	}

	// Re-sequence source versions onto target.
	// Get source versions ordered by version ASC.
	rows, err := tx.Query(ctx,
		`SELECT id, version FROM skill_versions WHERE skill_id = $1 ORDER BY version ASC`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.Merge list source versions: %w", err)
	}

	type versionRef struct {
		id      string
		version int
	}
	var sourceVersions []versionRef
	for rows.Next() {
		var v versionRef
		if err := rows.Scan(&v.id, &v.version); err != nil {
			rows.Close()
			return nil, fmt.Errorf("skill.Store.Merge scan version: %w", err)
		}
		sourceVersions = append(sourceVersions, v)
	}
	rows.Close()

	// Re-parent each version with new sequential version number.
	for i, v := range sourceVersions {
		newVersion := targetLatest + i + 1
		_, err := tx.Exec(ctx,
			`UPDATE skill_versions SET skill_id = $1, version = $2 WHERE id = $3`,
			targetID, newVersion, v.id)
		if err != nil {
			return nil, fmt.Errorf("skill.Store.Merge re-parent version %d: %w", v.version, err)
		}
	}

	// Update target latest_version.
	if len(sourceVersions) > 0 {
		newLatest := targetLatest + len(sourceVersions)
		_, err = tx.Exec(ctx,
			`UPDATE skills SET latest_version = $1, updated_at = now() WHERE id = $2`,
			newLatest, targetID)
		if err != nil {
			return nil, fmt.Errorf("skill.Store.Merge update latest_version: %w", err)
		}
	}

	// Create alias: source name -> target name.
	_, err = tx.Exec(ctx,
		`INSERT INTO skill_aliases (alias, canonical) VALUES ($1, $2) ON CONFLICT (alias) DO UPDATE SET canonical = $2`,
		sourceName, targetName)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.Merge create alias: %w", err)
	}

	// Move import_sources if target doesn't have one.
	var targetHasSource bool
	tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM import_sources WHERE skill_id = $1)`, targetID).Scan(&targetHasSource)
	if !targetHasSource {
		tx.Exec(ctx, `UPDATE import_sources SET skill_id = $1 WHERE skill_id = $2`, targetID, sourceID)
	}

	// Delete source skill (CASCADE removes remaining FKs).
	_, err = tx.Exec(ctx, `DELETE FROM skills WHERE id = $1`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.Merge delete source: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("skill.Store.Merge commit: %w", err)
	}

	// Return the updated target skill.
	return s.GetByName(ctx, targetName)
}
```

- [ ] **Step 2: Write tests**

```go
// internal/skill/merge_test.go
package skill

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/skael-dev/skael/internal/testutil"
)

func TestMerge(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := NewStore(pool)

	// Create source and target skills.
	source, _ := store.Create(ctx, "superpowers:brainstorming", "", "source", "", json.RawMessage(`{}`))
	target, _ := store.Create(ctx, "brainstorming", "", "target", "", json.RawMessage(`{}`))

	// Add a version to each.
	store.CreateVersion(ctx, source.ID, "s/archive.tar.gz", "checksum1", "", json.RawMessage(`{}`), nil, json.RawMessage(`{}`))
	store.CreateVersion(ctx, target.ID, "t/archive.tar.gz", "checksum2", "", json.RawMessage(`{}`), nil, json.RawMessage(`{}`))

	// Merge source into target.
	merged, err := store.Merge(ctx, "superpowers:brainstorming", "brainstorming")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	// Target should now have 2 versions.
	if merged.LatestVersion != 2 {
		t.Errorf("latest_version = %d, want 2", merged.LatestVersion)
	}

	// Source should be gone.
	gone, _ := store.GetByName(ctx, "superpowers:brainstorming")
	if gone != nil {
		t.Error("source skill should be deleted after merge")
	}

	// Alias should exist.
	canonical, _ := store.ResolveAlias(ctx, "superpowers:brainstorming")
	if canonical != "brainstorming" {
		t.Errorf("alias canonical = %q, want %q", canonical, "brainstorming")
	}

	// Versions should list correctly.
	versions, _ := store.ListVersions(ctx, "brainstorming")
	if len(versions) != 2 {
		t.Fatalf("got %d versions, want 2", len(versions))
	}
}
```

- [ ] **Step 3: Run tests**

Run: `cd /Users/nathananderson-tennant/Development/skael && go test ./internal/skill/ -run TestMerge -v -count=1`

- [ ] **Step 4: Commit**

```bash
git add internal/skill/merge.go internal/skill/merge_test.go
git commit -m "feat(aliases): add skill merge with version re-parenting"
```

---

### Task 4: API routes — alias CRUD + merge

**Files:**
- Modify: `internal/skill/routes.go`

- [ ] **Step 1: Add alias and merge routes**

At the end of `RegisterRoutes` in `internal/skill/routes.go`, add:

```go
	// -----------------------------------------------------------------
	// GET /api/skills/{name}/aliases
	// -----------------------------------------------------------------
	type aliasListInput struct {
		Name string `path:"name"`
	}
	type aliasListOutput struct {
		Body []Alias
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-skill-aliases",
		Method:      http.MethodGet,
		Path:        "/api/skills/{name}/aliases",
		Summary:     "List aliases for a skill",
	}, func(ctx context.Context, input *aliasListInput) (*aliasListOutput, error) {
		aliases, err := store.ListAliases(ctx, input.Name)
		if err != nil {
			return nil, fmt.Errorf("list aliases: %w", err)
		}
		return &aliasListOutput{Body: aliases}, nil
	})

	// -----------------------------------------------------------------
	// POST /api/skills/{name}/aliases
	// -----------------------------------------------------------------
	type aliasCreateBody struct {
		Alias string `json:"alias" minLength:"1"`
	}
	type aliasCreateInput struct {
		Name string `path:"name"`
		Body aliasCreateBody
	}
	huma.Register(api, huma.Operation{
		OperationID:   "create-skill-alias",
		Method:        http.MethodPost,
		Path:          "/api/skills/{name}/aliases",
		Summary:       "Add an alias for a skill",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, input *aliasCreateInput) (*struct{}, error) {
		if err := store.CreateAlias(ctx, input.Body.Alias, input.Name); err != nil {
			return nil, fmt.Errorf("create alias: %w", err)
		}
		return nil, nil
	})

	// -----------------------------------------------------------------
	// DELETE /api/skills/{name}/aliases/{alias}
	// -----------------------------------------------------------------
	type aliasDeleteInput struct {
		Name  string `path:"name"`
		Alias string `path:"alias"`
	}
	huma.Register(api, huma.Operation{
		OperationID:   "delete-skill-alias",
		Method:        http.MethodDelete,
		Path:          "/api/skills/{name}/aliases/{alias}",
		Summary:       "Remove an alias",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, input *aliasDeleteInput) (*struct{}, error) {
		if err := store.DeleteAlias(ctx, input.Alias); err != nil {
			return nil, fmt.Errorf("delete alias: %w", err)
		}
		return nil, nil
	})

	// -----------------------------------------------------------------
	// POST /api/skills/merge
	// -----------------------------------------------------------------
	type mergeBody struct {
		Source string `json:"source" minLength:"1"`
		Target string `json:"target" minLength:"1"`
	}
	type mergeInput struct {
		Body mergeBody
	}
	type mergeOutput struct {
		Body *Skill
	}
	huma.Register(api, huma.Operation{
		OperationID: "merge-skills",
		Method:      http.MethodPost,
		Path:        "/api/skills/merge",
		Summary:     "Merge source skill into target skill",
	}, func(ctx context.Context, input *mergeInput) (*mergeOutput, error) {
		merged, err := store.Merge(ctx, input.Body.Source, input.Body.Target)
		if err != nil {
			return nil, fmt.Errorf("merge skills: %w", err)
		}
		return &mergeOutput{Body: merged}, nil
	})
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./...`

- [ ] **Step 3: Commit**

```bash
git add internal/skill/routes.go
git commit -m "feat(aliases): add alias CRUD and merge API routes"
```

---

### Task 5: Update analytics queries with alias JOINs

**Files:**
- Modify: `internal/analytics/event.go`

- [ ] **Step 1: Update all analytics queries**

There are 5 queries in `event.go` that JOIN `skill_events` to `skills` on name. Each needs to also check aliases. Plus the unregistered query needs to exclude aliased names.

**`GetOverview` — active skills query (~line 93):**
```sql
-- Before:
SELECT COUNT(DISTINCT se.skill_name)
FROM skill_events se
JOIN skills s ON s.name = se.skill_name
WHERE se.created_at > now() - make_interval(days => $1)

-- After:
SELECT COUNT(DISTINCT COALESCE(a.canonical, se.skill_name))
FROM skill_events se
LEFT JOIN skill_aliases a ON a.alias = se.skill_name
WHERE se.created_at > now() - make_interval(days => $1)
  AND EXISTS (SELECT 1 FROM skills s WHERE s.name = COALESCE(a.canonical, se.skill_name))
```

**`GetOverview` — total activations query (~line 102):**
```sql
-- Before:
SELECT COUNT(*)
FROM skill_events se
JOIN skills s ON s.name = se.skill_name
WHERE se.created_at > now() - make_interval(days => $1)

-- After:
SELECT COUNT(*)
FROM skill_events se
LEFT JOIN skill_aliases a ON a.alias = se.skill_name
WHERE se.created_at > now() - make_interval(days => $1)
  AND EXISTS (SELECT 1 FROM skills s WHERE s.name = COALESCE(a.canonical, se.skill_name))
```

**`GetSkillsAnalytics` — per-skill analytics (~line 161):**
```sql
-- The LEFT JOIN subquery for events needs to resolve aliases:
LEFT JOIN (
    SELECT
        COALESCE(a.canonical, se.skill_name) AS resolved_name,
        COUNT(*)                    AS activation_count,
        COUNT(DISTINCT developer_hash) AS unique_devs,
        MAX(se.created_at)          AS last_triggered
    FROM skill_events se
    LEFT JOIN skill_aliases a ON a.alias = se.skill_name
    WHERE se.created_at > now() - make_interval(days => $1)
    GROUP BY resolved_name
) e ON e.resolved_name = s.name
```

**`GetActivations` — per-skill detail (~line 233):**
```sql
-- Both aggregate and agent queries need to match by name or alias:
-- Aggregate:
SELECT COUNT(*), COUNT(DISTINCT developer_hash), MAX(created_at)
FROM skill_events se
LEFT JOIN skill_aliases a ON a.alias = se.skill_name
WHERE COALESCE(a.canonical, se.skill_name) = $1
  AND se.created_at > now() - make_interval(days => $2)

-- Agent breakdown:
SELECT agent, COUNT(*)
FROM skill_events se
LEFT JOIN skill_aliases a ON a.alias = se.skill_name
WHERE COALESCE(a.canonical, se.skill_name) = $1
  AND se.created_at > now() - make_interval(days => $2)
GROUP BY agent
```

**`GetTimeSeries` — chart data (~line 350):**
```sql
LEFT JOIN skill_events se
    ON se.created_at::date = d.day
    AND EXISTS (
        SELECT 1 FROM skills s
        WHERE s.name = COALESCE(
            (SELECT canonical FROM skill_aliases WHERE alias = se.skill_name),
            se.skill_name
        )
    )
```

**`GetUnregisteredSkills` — add alias exclusion (~line 305):**
```sql
AND NOT EXISTS (SELECT 1 FROM skill_aliases a WHERE a.alias = se.skill_name)
```

- [ ] **Step 2: Apply all query changes**

Read the file, apply each change carefully. The pattern is consistent: LEFT JOIN `skill_aliases`, use `COALESCE(a.canonical, se.skill_name)` to resolve the effective skill name.

- [ ] **Step 3: Run all analytics tests**

Run: `cd /Users/nathananderson-tennant/Development/skael && go test ./internal/analytics/ -v -count=1`

- [ ] **Step 4: Commit**

```bash
git add internal/analytics/event.go
git commit -m "feat(aliases): update all analytics queries to resolve aliases"
```

---

### Task 6: Hook normalization — strip OpenCode prefix

**Files:**
- Modify: `cli/hooks/script.go`
- Modify: `cli/hooks/opencode_plugin.go`

- [ ] **Step 1: Add normalization to bash hook script**

In `cli/hooks/script.go`, after the skill name extraction block (after the `fi` that ends the jq/grep section, before the hash commands), add:

```bash
# Normalize agent-specific prefixes.
# OpenCode wraps skill names as "skills_<name>".
SKILL_NAME="${SKILL_NAME#skills_}"
```

- [ ] **Step 2: Fix OpenCode plugin**

In `cli/hooks/opencode_plugin.go`, change:
```typescript
skill_name: input.tool,
```
to:
```typescript
skill_name: input.tool.replace(/^skills_/, ''),
```

- [ ] **Step 3: Run hook tests**

Run: `go test ./cli/hooks/ -v -count=1`

- [ ] **Step 4: Commit**

```bash
git add cli/hooks/script.go cli/hooks/opencode_plugin.go
git commit -m "fix(hooks): strip OpenCode skills_ prefix at ingestion"
```

---

### Task 7: Import dedup + namespace prompt

**Files:**
- Modify: `internal/import/discover.go`
- Modify: `internal/import/routes.go`
- Modify: `cli/import.go`

- [ ] **Step 1: Add dedup to Discover**

In `internal/import/discover.go`, after building the `results` slice (before the sort), add deduplication:

```go
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
```

- [ ] **Step 2: Add plugin name detection to Discover**

Add a new exported function in `discover.go`:

```go
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
```

Add `"encoding/json"` to the imports if not already present.

- [ ] **Step 3: Add plugin_name to resolve response**

In `internal/import/routes.go`, in the resolve handler, after discovering skills, detect the plugin name:

```go
pluginName := DetectPluginName(result.Dir)
```

Add `PluginName string` to the resolve output body:

```go
type resolveOutput struct {
    Body struct {
        Source     Source            `json:"source"`
        Skills     []DiscoveredSkill `json:"skills"`
        PluginName string            `json:"plugin_name,omitempty"`
    }
}
```

Set it: `out.Body.PluginName = pluginName`

- [ ] **Step 4: Add namespace parameter to import endpoint**

In the import handler's request body, add an optional `Namespace` field:

```go
type importBody struct {
    Source    Source   `json:"source"`
    Skills   []string `json:"skills" minItems:"1"`
    Namespace string  `json:"namespace,omitempty"`
}
```

In the import loop, if `Namespace` is set, prefix each skill name and create a reverse alias:

```go
name := ds.Name
if input.Body.Namespace != "" {
    name = input.Body.Namespace + ":" + ds.Name
}
```

After `importSingleSkill` succeeds, if a namespace was applied, create the reverse alias:

```go
if input.Body.Namespace != "" {
    skillStore.CreateAlias(ctx, ds.Name, name) // bare name -> namespaced
}
```

Note: `importSingleSkill` uses `ds.Name` for the skill record. You'll need to pass the resolved `name` instead. Modify `importSingleSkill` to accept an optional `registryName` parameter, or override `ds.Name` before calling it.

- [ ] **Step 5: Add namespace prompt to CLI**

In `cli/import.go`, in the `presentAndImport` function, after the resolve response is received and before the interactive selector, check for `resolved.PluginName`:

First add `PluginName` to the client's `ResolveResponse`:

```go
// In cli/client/import.go:
type ResolveResponse struct {
    Source     ImportSource      `json:"source"`
    Skills     []DiscoveredSkill `json:"skills"`
    PluginName string            `json:"plugin_name,omitempty"`
}
```

Then in `presentAndImport`, before importing:

```go
namespace := ""
if resolved.PluginName != "" && !ui.JSONMode {
    fmt.Fprintf(os.Stdout, "\n  These skills come from plugin %s.\n", importNameStyle.Render(resolved.PluginName))
    fmt.Fprintf(os.Stdout, "  Use prefix \"%s:\"? [Y/n] ", resolved.PluginName)
    reader := bufio.NewReader(os.Stdin)
    answer, _ := reader.ReadString('\n')
    answer = strings.TrimSpace(strings.ToLower(answer))
    if answer != "n" && answer != "no" {
        namespace = resolved.PluginName
    }
}
```

Pass `namespace` to `ImportSkills`:

```go
// In cli/client/import.go, update ImportSkills to accept namespace:
func (c *Client) ImportSkills(source ImportSource, skillNames []string, namespace string) (*ImportResponse, error) {
    payload, err := json.Marshal(map[string]interface{}{
        "source":    source,
        "skills":    skillNames,
        "namespace": namespace,
    })
```

Update all call sites of `ImportSkills` to pass the namespace (empty string when not applicable).

Add `"bufio"` back to imports in `cli/import.go` if needed for the reader.

- [ ] **Step 6: Verify compilation**

Run: `go build ./...`

- [ ] **Step 7: Commit**

```bash
git add internal/import/discover.go internal/import/routes.go cli/import.go cli/client/import.go
git commit -m "feat(aliases): import dedup, plugin detection, namespace prompt with auto-aliases"
```

---

### Task 8: Web UI — namespace display on skill cards

**Files:**
- Modify: `web/src/features/skills/skill-card.tsx`

- [ ] **Step 1: Add namespace parsing and display**

Add a helper at the top of the file:

```tsx
function parseSkillName(name: string): { namespace: string | null; bare: string } {
  const idx = name.indexOf(":");
  if (idx === -1) return { namespace: null, bare: name };
  return { namespace: name.slice(0, idx), bare: name.slice(idx + 1) };
}
```

In the skill card's name rendering section, replace the plain name display with the parsed version:

```tsx
{(() => {
  const { namespace, bare } = parseSkillName(skill.name);
  return (
    <>
      <span className="font-mono font-medium text-[13px] text-text-primary whitespace-nowrap">
        {bare}
      </span>
      {namespace && (
        <span className="text-[10px] text-text-tertiary whitespace-nowrap">
          {namespace}
        </span>
      )}
    </>
  );
})()}
```

Remove the existing `{skill.name}` span and replace with the above.

- [ ] **Step 2: Verify TypeScript**

Run: `cd /Users/nathananderson-tennant/Development/skael/web && npx tsc --noEmit`

- [ ] **Step 3: Commit**

```bash
git add web/src/features/skills/skill-card.tsx
git commit -m "feat(aliases): namespace badge on skill cards"
```

---

### Task 9: Web UI — namespace display + aliases on skill detail

**Files:**
- Modify: `web/src/features/skills/skill-detail.tsx`

- [ ] **Step 1: Add the same `parseSkillName` helper**

Add the helper function at the top of the file (same as Task 8).

- [ ] **Step 2: Update the header to show namespace badge**

In the skill detail header where the skill name is displayed, replace it with the parsed namespace + bare name display.

- [ ] **Step 3: Add aliases section**

Add a query for aliases:

```tsx
const aliasesQuery = useQuery({
  queryKey: ["skill-aliases", name],
  queryFn: async () => {
    const res = await fetch(`/api/skills/${encodeURIComponent(name!)}/aliases`, { credentials: "include" });
    if (!res.ok) return [];
    return res.json() as Promise<{ alias: string; canonical: string; created_at: string }[]>;
  },
  enabled: !!name,
});
```

In the metadata area (after the import provenance section), add a collapsible aliases list:

```tsx
{(aliasesQuery.data ?? []).length > 0 && (
  <div className="flex items-center gap-1.5 text-[11px] text-text-tertiary flex-wrap">
    <span>Aliases:</span>
    {(aliasesQuery.data ?? []).map((a) => (
      <span key={a.alias} className="px-1.5 py-0.5 rounded bg-bg-tertiary font-mono text-[10px]">
        {a.alias}
      </span>
    ))}
  </div>
)}
```

- [ ] **Step 4: Verify TypeScript**

Run: `cd /Users/nathananderson-tennant/Development/skael/web && npx tsc --noEmit`

- [ ] **Step 5: Commit**

```bash
git add web/src/features/skills/skill-detail.tsx
git commit -m "feat(aliases): namespace display and aliases section on detail page"
```

---

### Task 10: Web UI — merge action on unregistered tab

**Files:**
- Modify: `web/src/features/skills/unregistered-tab.tsx`

- [ ] **Step 1: Add merge action**

Add a merge icon button alongside Register and Dismiss. Use `Merge` or `GitMerge` from lucide-react.

Add a merge mutation:

```tsx
async function mergeSkill(source: string, target: string): Promise<void> {
  const res = await fetch("/api/skills/merge", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "include",
    body: JSON.stringify({ source, target }),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.detail || body.title || "Failed to merge");
  }
}
```

For the merge action, the user needs to pick a target skill. Use a simple `window.prompt` for now (a proper skill picker modal can be added later):

```tsx
const handleMerge = (sourceName: string) => {
  const target = window.prompt(`Merge "${sourceName}" into which skill? Enter the target skill name:`);
  if (!target) return;
  mergeMutation.mutate({ source: sourceName, target });
};
```

Add the icon button with tooltip in each row, between Register and Dismiss:

```tsx
<div className="relative group/merge">
  <button
    onClick={() => handleMerge(sk.name)}
    className="p-1.5 rounded-md text-text-tertiary hover:text-info hover:bg-info/10 transition-colors cursor-pointer"
  >
    <GitMerge size={14} />
  </button>
  <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-1.5 px-2 py-1 text-[10px] text-text-primary bg-bg-tertiary border border-border rounded whitespace-nowrap opacity-0 pointer-events-none group-hover/merge:opacity-100 transition-opacity z-10">
    Merge into...
  </div>
</div>
```

- [ ] **Step 2: Add similarity hint**

For each unregistered skill, check if a registered skill exists with the bare name (after stripping namespace prefix). Add this hint to the row:

```tsx
{(() => {
  const bare = sk.name.includes(":") ? sk.name.split(":").pop()! : null;
  // Only show hint if the bare name differs from the full name
  if (!bare) return null;
  return (
    <span className="text-[10px] text-text-tertiary ml-2">
      Similar to: {bare}
    </span>
  );
})()}
```

This is a client-side hint. Proper similarity checking (actually querying the registry) can be added later.

- [ ] **Step 3: Verify TypeScript**

Run: `cd /Users/nathananderson-tennant/Development/skael/web && npx tsc --noEmit`

- [ ] **Step 4: Commit**

```bash
git add web/src/features/skills/unregistered-tab.tsx
git commit -m "feat(aliases): merge action and similarity hints on unregistered tab"
```

---

### Task 11: End-to-end verification

- [ ] **Step 1: Build everything**

```bash
cd /Users/nathananderson-tennant/Development/skael
go build -o bin/skael-server ./cmd/server/
go build -o bin/skael ./cmd/skael/
```

- [ ] **Step 2: Run all Go tests**

```bash
go test ./internal/skill/ -v -count=1
go test ./internal/analytics/ -v -count=1
go test ./internal/import/ -v -count=1
go test ./cli/hooks/ -v -count=1
```

- [ ] **Step 3: TypeScript check**

```bash
cd web && npx tsc --noEmit
```

- [ ] **Step 4: Regenerate OpenAPI SDK**

```bash
cd /Users/nathananderson-tennant/Development/skael
bin/skael-server --openapi > web/openapi.json
cd web && npx @hey-api/openapi-ts
```

- [ ] **Step 5: Manual test — create alias, verify analytics**

```bash
# Create a skill
bin/skael publish /tmp/my-test-skill
# Create an alias
curl -X POST http://localhost:8080/api/skills/my-test-skill/aliases \
  -H "Content-Type: application/json" -d '{"alias":"superpowers:my-test-skill"}'
# Post event under the alias name
curl -X POST http://localhost:8080/api/events \
  -H "Content-Type: application/json" \
  -d '{"skill_name":"superpowers:my-test-skill","agent":"test"}'
# Verify analytics aggregates under the canonical name
curl http://localhost:8080/api/skills/my-test-skill/activations
```

- [ ] **Step 6: Commit any fixups**

```bash
git add -A && git commit -m "fix(aliases): e2e verification fixups"
```
