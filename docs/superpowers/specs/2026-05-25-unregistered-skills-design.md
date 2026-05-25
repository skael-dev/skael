# Unregistered Skills — Design Spec

## Overview

Skael's activation hooks fire for every skill invocation regardless of whether the skill is published in the registry. Events for unregistered skills (those with a `skill_name` that doesn't match any `skills.name`) are currently captured in `skill_events` but invisible in the dashboard because analytics queries JOIN on the `skills` table.

This feature surfaces those "shadow skills" — skills developers actually use but that haven't been published or imported — in an **Unregistered** tab on the skills page. Admins can triage each one: **Register** it (creates a stub in the registry so events flow into main analytics) or **Dismiss** it (hides it from the unregistered tab).

## Data Model

### New table: `dismissed_skills`

```sql
CREATE TABLE dismissed_skills (
    name         TEXT PRIMARY KEY,
    dismissed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

No changes to `skill_events` or `skills`. The unregistered skill list is derived at query time by diffing `skill_events` against `skills` and `dismissed_skills`.

### Register action

Creates a stub `skills` row via the existing `POST /api/skills` endpoint — just the name, empty description, no versions (`latest_version = 0`). Once the stub exists, the JOIN in analytics queries starts matching and events appear in the main dashboard.

### Dismiss action

Inserts the skill name into `dismissed_skills`. The unregistered query excludes dismissed names.

## Backend

### New query: `GetUnregisteredSkills(ctx, days)`

In `internal/analytics/event.go`:

```sql
SELECT se.skill_name,
       COUNT(*)                            AS activations,
       COUNT(DISTINCT se.developer_hash)   AS unique_devs,
       MAX(se.created_at)                  AS last_triggered,
       MIN(se.created_at)                  AS first_seen
FROM skill_events se
WHERE se.created_at > now() - make_interval(days => $1)
  AND NOT EXISTS (SELECT 1 FROM skills s WHERE s.name = se.skill_name)
  AND NOT EXISTS (SELECT 1 FROM dismissed_skills d WHERE d.name = se.skill_name)
GROUP BY se.skill_name
ORDER BY activations DESC
```

Returns:

```go
type UnregisteredSkill struct {
    Name          string     `json:"name"`
    Activations   int        `json:"activations"`
    UniqueDevs    int        `json:"unique_devs"`
    LastTriggered *time.Time `json:"last_triggered"`
    FirstSeen     *time.Time `json:"first_seen"`
}
```

### New endpoints

**`GET /api/analytics/unregistered?days=30`** — returns `[]UnregisteredSkill`

**`POST /api/analytics/dismiss`** — dismisses an unregistered skill

Request:
```json
{ "name": "superpowers:brainstorming" }
```

Response: 204 No Content

The dismiss store method uses `INSERT ... ON CONFLICT DO NOTHING` to be idempotent.

### Register action

No new endpoint needed — uses existing `POST /api/skills` with `{"name": "skill-name"}`. This creates a stub skill record. The only difference from normal skill creation: the name may not match the `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$` regex (e.g., `superpowers:brainstorming` has a colon). The register flow should bypass name validation since the name comes from real event data, not user input.

Add a new endpoint specifically for this: **`POST /api/skills/register`** with `{"name": "..."}` that creates a stub skill without name format validation. This keeps the normal `POST /api/skills` strict.

### Name matching

Exact match only. `superpowers:brainstorming` and `brainstorming` are treated as distinct skills. No fuzzy matching, no prefix stripping. Admins see the exact name from the hook payload and decide whether to register it.

## Web UI

### Tab bar on skills page

Add a tab bar above the filter/search area with two tabs:

- **Registry** — current skills list (default)
- **Unregistered (N)** — badge shows count of unregistered skills with events in the selected period

The tab bar uses the existing `SlidingTabs` component pattern from the skill detail page.

### Unregistered tab content

A table showing unregistered skills with columns:

| Column | Description |
|--------|-------------|
| Skill name | Mono font, the exact `skill_name` from events |
| Activations | Total count in the period |
| Unique devs | Distinct developer hashes |
| Last triggered | Relative time |
| First seen | Relative time |
| Actions | Register button + Dismiss button |

- **Register** — calls `POST /api/skills/register`, shows success toast, removes row from unregistered tab (skill moves to Registry tab)
- **Dismiss** — calls `POST /api/analytics/dismiss`, shows toast, removes row with fade
- **Empty state** — "No unregistered skills detected. All skill activations match registered skills."

Respects the same period selector (7d/30d/90d) as the main analytics view. Data fetched via `useQuery` with `["analytics", "unregistered", days]` key.

### KPI strip

No changes. KPI strip tracks registered skills only. The unregistered tab badge is the discovery signal.

## Edge Cases

### Skill deleted from registry

If a registered skill is deleted, its events become "unregistered" again and show up in the Unregistered tab. This is correct — the admin can see the events and decide to re-register or dismiss.

### Dismissed skill later registered

If an admin dismisses "foo" then later publishes/imports a skill named "foo", the skill appears in the Registry tab normally. The `dismissed_skills` row is irrelevant since the analytics queries check `NOT IN skills` first — once "foo" is in `skills`, it never hits the unregistered query.

### Hook sends garbage names

A misconfigured hook might send empty strings, very long names, or weird characters. The unregistered tab just shows whatever is in the events. The dismiss action handles these — admin sees the garbage name and dismisses it.

### High-volume unregistered events

If a popular skill generates thousands of events before being registered, the unregistered query aggregates them efficiently via GROUP BY. No performance concern.

### Register then dismiss (or vice versa)

Register creates a `skills` row — the name disappears from unregistered because the NOT EXISTS check succeeds. If the admin later deletes the skill, it reappears in unregistered (unless also dismissed). Dismiss and register are independent — dismiss only affects the unregistered query, register only affects the skills table.

## Out of Scope

- Name alias mapping (e.g., "superpowers:brainstorming" → "brainstorming")
- Auto-registration based on activation thresholds
- Unregistered skill detail page (just the table row for now)
- Undismiss action (admin can clear the dismissed_skills table directly if needed)
- Per-agent breakdown in the unregistered table (available after registration)
