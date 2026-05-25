# Namespaced Skills, Aliases, and Merge — Design Spec

## Overview

Different AI coding agents use different naming conventions for skills. Claude Code namespaces plugin skills as `plugin:skill-name`, Codex and Gemini use bare names, and OpenCode prefixes with `skills_`. The same logical skill can appear under multiple names in the events table, fragmenting analytics.

This feature adds:
1. **Colon-aware name validation** so namespaced names are valid registry entries
2. **Alias table** mapping variant names to a canonical skill name
3. **Merge capability** to combine two separate skill records into one
4. **Import deduplication** for plugin repos with mirrored skill directories
5. **Namespace prompt** during import from plugin repos
6. **Hook normalization** to strip the OpenCode `skills_` prefix at ingestion
7. **UI display** that parses namespace:name for clean presentation

## Agent Name Formats (reference)

| Agent | Format | Example |
|---|---|---|
| Claude Code (plugin) | `plugin:skill-name` | `superpowers:brainstorming` |
| Claude Code (user) | `skill-name` | `brainstorming` |
| Codex CLI | `skill-name` | `brainstorming` |
| Gemini CLI | `skill-name` | `brainstorming` |
| OpenCode | `skills_skill-name` | `skills_brainstorming` |

## Data Model

### Update skill name validation

Change the regex from `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$` to `^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$`. This allows colons (namespaces), dots, and hyphens. Applied in the `POST /api/skills` handler. The `POST /api/skills/register` endpoint remains unrestricted.

### New table: `skill_aliases`

```sql
CREATE TABLE skill_aliases (
    alias      TEXT PRIMARY KEY,
    canonical  TEXT NOT NULL REFERENCES skills(name) ON UPDATE CASCADE ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_skill_aliases_canonical ON skill_aliases(canonical);
```

Multiple aliases can point to one canonical name. The FK references `skills(name)` with CASCADE so renaming or deleting a skill cascades to aliases.

## Alias Resolution

Analytics queries that JOIN `skill_events` to `skills` change to also check aliases:

```sql
-- Before:
JOIN skills s ON s.name = se.skill_name

-- After:
JOIN skills s ON (
    s.name = se.skill_name
    OR EXISTS (SELECT 1 FROM skill_aliases a WHERE a.alias = se.skill_name AND a.canonical = s.name)
)
```

This applies to: `GetOverview` (active skills, total activations), `GetSkillsAnalytics` (per-skill table), `GetActivations` (detail page), `GetTimeSeries` (chart), and `GetUnregisteredSkills` (also exclude aliases from unregistered).

The unregistered query additionally filters out aliases:

```sql
AND NOT EXISTS (SELECT 1 FROM skill_aliases a WHERE a.alias = se.skill_name)
```

## Merge

`POST /api/skills/merge` with `{"source": "superpowers:brainstorming", "target": "brainstorming"}`.

Runs in a single transaction:

1. Look up both skills by name; fail if either doesn't exist
2. Re-parent all `skill_versions` from source to target, re-sequencing version numbers to continue from target's `latest_version`
3. Update target's `latest_version` to reflect new highest version
4. Create an alias: source name → target name
5. Move `import_sources` if source has one and target doesn't; otherwise delete source's
6. Delete the source skill (CASCADE removes any remaining FKs)
7. Return the merged target skill

**UI surface:**
- Skill detail page: "Actions" dropdown with "Merge into..." option, opens a search modal to pick the target
- Unregistered tab: "Merge into..." action alongside Register and Dismiss
- Confirmation dialog: "Merge 'superpowers:brainstorming' into 'brainstorming'. N versions will be re-parented. Events will aggregate under 'brainstorming'."

## Import Changes

### Deduplication

After discovering SKILL.md files in a repo, group by frontmatter `name`. If multiple directories produce the same name (e.g., impeccable mirrored across 12 platform dirs), keep only the first (alphabetical path) and show once. Log: "Found N copies of 'skill-name', showing once."

### Namespace prompt

After dedup, if the repo has `.claude-plugin/plugin.json` with a `name` field, offer to prefix:

```
These skills come from plugin "superpowers". Register with namespace?
  [superpowers:] brainstorming, debugging, tdd, ...

  Use prefix "superpowers:"? [Y/n]
```

If yes: register as `superpowers:brainstorming`, auto-create alias `brainstorming`.
If no: register as `brainstorming`, auto-create alias `superpowers:brainstorming`.

Either way, both forms resolve to the same skill in analytics.

CLI: same prompt in the interactive selector. `--all` flag defaults to yes (use prefix).

### Discover changes

The `Discover` function in `internal/import/discover.go` needs a dedup step after finding all SKILL.md files. Group by parsed frontmatter `name`, keep first path per name.

Also look for `.claude-plugin/plugin.json` in the fetched repo to extract the plugin name for the namespace prompt. Return it as part of the resolve response so both CLI and web can offer the prefix.

## Hook Normalization

### Bash hook script (`cli/hooks/script.go`)

After extracting `SKILL_NAME`, strip the OpenCode `skills_` prefix:

```bash
# Normalize OpenCode prefix.
SKILL_NAME="${SKILL_NAME#skills_}"
```

Do NOT strip the colon namespace. `superpowers:brainstorming` arrives as-is. The alias table handles mapping at query time.

### OpenCode plugin (`cli/hooks/opencode_plugin.go`)

Change `skill_name: input.tool` to strip the prefix:

```typescript
skill_name: input.tool.replace(/^skills_/, ''),
```

## UI Display

### Name parsing

For any skill name containing a colon, split on the first `:`:
- Part before = namespace (displayed as subtle badge)
- Part after = bare name (displayed as primary name)

Skills without a colon display as-is, no badge.

### Skill card (list view)

```
[checkbox] [status dot] superpowers  brainstorming    0    Clean ○    v1 · 3d ago
                        ^^^^^^^^^^   ^^^^^^^^^^^^^
                        small badge   bold mono name
```

### Skill detail page

Header shows the parsed name + namespace badge. Below metadata cells, an "Aliases" section:
- Lists all aliases pointing to this skill
- Add/remove alias controls
- Collapsible

### Unregistered tab

Add "Merge into..." action. When an unregistered skill name shares a bare name with a registered skill (e.g., unregistered `superpowers:brainstorming`, registered `brainstorming`), show a subtle hint: "Similar to: brainstorming".

## API Endpoints

### New endpoints

- `POST /api/skills/merge` — merge source into target skill
- `GET /api/skills/{name}/aliases` — list aliases for a skill
- `POST /api/skills/{name}/aliases` — add an alias `{"alias": "..."}`
- `DELETE /api/skills/{name}/aliases/{alias}` — remove an alias

### Modified endpoints

- `POST /api/skills` — updated regex allowing colons
- `POST /api/import/resolve` — returns `plugin_name` from plugin.json if found
- `POST /api/import` — accepts optional `namespace` prefix to apply to all imported skill names; auto-creates reverse aliases

## Out of Scope

- Agent-specific alias auto-detection (inferring that `skills_brainstorming` = `brainstorming`)
- Bulk merge UI (merge one at a time)
- Alias conflict resolution (two aliases pointing to different canonicals)
- Namespace management UI (admin page for viewing all namespaces)
