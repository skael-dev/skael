# Unregistered Skills Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface "shadow skills" (those with activation events but no registry entry) in an Unregistered tab so admins can register or dismiss them.

**Architecture:** New `dismissed_skills` table for hiding unwanted names. New analytics query diffs `skill_events` against `skills` + `dismissed_skills` to find unregistered skill names with full activation stats. Two new API endpoints (list unregistered, dismiss). One new endpoint to register without name validation. Web UI adds a tab bar to the skills page with an "Unregistered" tab showing the data with Register/Dismiss actions.

**Tech Stack:** Go (pgx, Huma), React (react-query, existing component patterns), PostgreSQL

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Create | `internal/platform/migrate/003_dismissed_skills.sql` | Migration |
| Modify | `internal/analytics/event.go` | Add `UnregisteredSkill` type + `GetUnregisteredSkills` query |
| Modify | `internal/analytics/routes.go` | Add unregistered + dismiss + register endpoints |
| Create | `internal/analytics/routes_unregistered_test.go` | Test the unregistered query |
| Create | `web/src/features/skills/unregistered-tab.tsx` | Unregistered skills table component |
| Modify | `web/src/features/skills/skill-list.tsx` | Add tab bar, wire unregistered tab |

---

### Task 1: Migration — `dismissed_skills` table

**Files:**
- Create: `internal/platform/migrate/003_dismissed_skills.sql`

- [ ] **Step 1: Write the migration file**

```sql
-- +goose Up
CREATE TABLE dismissed_skills (
    name         TEXT PRIMARY KEY,
    dismissed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS dismissed_skills;
```

- [ ] **Step 2: Verify migration applies**

Run: `just migrate`
Expected: Migration 003 applies cleanly.

- [ ] **Step 3: Commit**

```bash
git add internal/platform/migrate/003_dismissed_skills.sql
git commit -m "feat(unregistered): add dismissed_skills migration"
```

---

### Task 2: Backend — unregistered query + types

**Files:**
- Modify: `internal/analytics/event.go`
- Create: `internal/analytics/routes_unregistered_test.go`

- [ ] **Step 1: Add the UnregisteredSkill type and query to event.go**

At the end of `internal/analytics/event.go`, add:

```go
// UnregisteredSkill represents a skill name found in events but not in the registry.
type UnregisteredSkill struct {
	Name          string     `json:"name"`
	Activations   int        `json:"activations"`
	UniqueDevs    int        `json:"unique_devs"`
	LastTriggered *time.Time `json:"last_triggered"`
	FirstSeen     *time.Time `json:"first_seen"`
}

// GetUnregisteredSkills returns skill names from events that don't exist in the
// skills table or dismissed_skills table, with activation stats.
func (s *Store) GetUnregisteredSkills(ctx context.Context, days int) ([]UnregisteredSkill, error) {
	const q = `
		SELECT se.skill_name,
		       COUNT(*)::int                          AS activations,
		       COUNT(DISTINCT se.developer_hash)::int AS unique_devs,
		       MAX(se.created_at)                     AS last_triggered,
		       MIN(se.created_at)                     AS first_seen
		FROM skill_events se
		WHERE se.created_at > now() - make_interval(days => $1)
		  AND NOT EXISTS (SELECT 1 FROM skills s WHERE s.name = se.skill_name)
		  AND NOT EXISTS (SELECT 1 FROM dismissed_skills d WHERE d.name = se.skill_name)
		GROUP BY se.skill_name
		ORDER BY activations DESC
	`
	rows, err := s.pool.Query(ctx, q, days)
	if err != nil {
		return nil, fmt.Errorf("analytics.Store.GetUnregisteredSkills query: %w", err)
	}
	defer rows.Close()

	var results []UnregisteredSkill
	for rows.Next() {
		var u UnregisteredSkill
		if err := rows.Scan(&u.Name, &u.Activations, &u.UniqueDevs, &u.LastTriggered, &u.FirstSeen); err != nil {
			return nil, fmt.Errorf("analytics.Store.GetUnregisteredSkills scan: %w", err)
		}
		results = append(results, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("analytics.Store.GetUnregisteredSkills rows: %w", err)
	}
	if results == nil {
		results = []UnregisteredSkill{}
	}
	return results, nil
}

// DismissSkill inserts a name into dismissed_skills. Idempotent.
func (s *Store) DismissSkill(ctx context.Context, name string) error {
	const q = `INSERT INTO dismissed_skills (name) VALUES ($1) ON CONFLICT DO NOTHING`
	if _, err := s.pool.Exec(ctx, q, name); err != nil {
		return fmt.Errorf("analytics.Store.DismissSkill: %w", err)
	}
	return nil
}
```

- [ ] **Step 2: Write test for the unregistered query**

```go
// internal/analytics/routes_unregistered_test.go
package analytics

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
)

func TestGetUnregisteredSkills(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	store := NewStore(pool)
	skillStore := skill.NewStore(pool)

	// Create a registered skill.
	skillStore.Create(ctx, "registered-skill", "", "exists", "", json.RawMessage(`{}`))

	// Insert events for registered + unregistered + dismissed skills.
	store.Insert(ctx, Event{SkillName: "registered-skill", Agent: "claude-code", DeveloperHash: "dev1"})
	store.Insert(ctx, Event{SkillName: "shadow-skill", Agent: "claude-code", DeveloperHash: "dev1"})
	store.Insert(ctx, Event{SkillName: "shadow-skill", Agent: "opencode", DeveloperHash: "dev2"})
	store.Insert(ctx, Event{SkillName: "dismissed-one", Agent: "claude-code", DeveloperHash: "dev1"})

	// Dismiss one.
	store.DismissSkill(ctx, "dismissed-one")

	results, err := store.GetUnregisteredSkills(ctx, 30)
	if err != nil {
		t.Fatalf("GetUnregisteredSkills: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 (only shadow-skill)", len(results))
	}
	if results[0].Name != "shadow-skill" {
		t.Errorf("name = %q, want %q", results[0].Name, "shadow-skill")
	}
	if results[0].Activations != 2 {
		t.Errorf("activations = %d, want 2", results[0].Activations)
	}
	if results[0].UniqueDevs != 2 {
		t.Errorf("unique_devs = %d, want 2", results[0].UniqueDevs)
	}
}

func TestDismissSkill_Idempotent(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := NewStore(pool)

	if err := store.DismissSkill(ctx, "test-dismiss"); err != nil {
		t.Fatalf("first dismiss: %v", err)
	}
	if err := store.DismissSkill(ctx, "test-dismiss"); err != nil {
		t.Fatalf("second dismiss (should be idempotent): %v", err)
	}
}
```

- [ ] **Step 3: Run tests**

Run: `cd /Users/nathananderson-tennant/Development/skael && go test ./internal/analytics/ -run 'TestGetUnregistered|TestDismiss' -v -count=1`
Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/analytics/event.go internal/analytics/routes_unregistered_test.go
git commit -m "feat(unregistered): add query for unregistered skills and dismiss"
```

---

### Task 3: Backend — API endpoints

**Files:**
- Modify: `internal/analytics/routes.go`
- Modify: `internal/skill/routes.go`

- [ ] **Step 1: Add unregistered + dismiss endpoints to analytics routes**

At the end of the `RegisterRoutes` function in `internal/analytics/routes.go` (before the closing `}`), add:

```go
	// -----------------------------------------------------------------
	// GET /api/analytics/unregistered?days=30 — unregistered skills
	// -----------------------------------------------------------------
	type unregisteredInput struct {
		Days int `query:"days" default:"30" minimum:"1" maximum:"365"`
	}
	type unregisteredOutput struct {
		Body []UnregisteredSkill
	}
	huma.Register(api, huma.Operation{
		OperationID: "analytics-unregistered",
		Method:      http.MethodGet,
		Path:        "/api/analytics/unregistered",
		Summary:     "List unregistered skills with activation data",
	}, func(ctx context.Context, input *unregisteredInput) (*unregisteredOutput, error) {
		days := input.Days
		if days == 0 {
			days = 30
		}
		skills, err := store.GetUnregisteredSkills(ctx, days)
		if err != nil {
			return nil, fmt.Errorf("analytics unregistered: %w", err)
		}
		return &unregisteredOutput{Body: skills}, nil
	})

	// -----------------------------------------------------------------
	// POST /api/analytics/dismiss — dismiss an unregistered skill
	// -----------------------------------------------------------------
	type dismissBody struct {
		Name string `json:"name" minLength:"1"`
	}
	type dismissInput struct {
		Body dismissBody
	}
	huma.Register(api, huma.Operation{
		OperationID:   "dismiss-skill",
		Method:        http.MethodPost,
		Path:          "/api/analytics/dismiss",
		Summary:       "Dismiss an unregistered skill",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, input *dismissInput) (*struct{}, error) {
		if err := store.DismissSkill(ctx, input.Body.Name); err != nil {
			return nil, fmt.Errorf("dismiss skill: %w", err)
		}
		return nil, nil
	})
```

- [ ] **Step 2: Add register endpoint to skill routes**

In `internal/skill/routes.go`, in the `RegisterRoutes` function, after the `POST /api/skills` handler (around line 83), add:

```go
	// -----------------------------------------------------------------
	// POST /api/skills/register — register a skill stub (no name validation)
	// -----------------------------------------------------------------
	type registerBody struct {
		Name string `json:"name" minLength:"1" maxLength:"255"`
	}
	type registerInput struct {
		Body registerBody
	}
	type registerOutput struct {
		Body *Skill
	}
	huma.Register(api, huma.Operation{
		OperationID:   "register-skill",
		Method:        http.MethodPost,
		Path:          "/api/skills/register",
		Summary:       "Register a skill stub (no name format validation)",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, input *registerInput) (*registerOutput, error) {
		sk, err := store.Create(ctx, input.Body.Name, "", "", "", json.RawMessage(`{}`))
		if err != nil {
			if platform.IsDuplicateKey(err) {
				return nil, huma.Error409Conflict(
					fmt.Sprintf("skill %q already exists", input.Body.Name))
			}
			return nil, fmt.Errorf("register skill: %w", err)
		}
		return &registerOutput{Body: sk}, nil
	})
```

- [ ] **Step 3: Verify compilation**

Run: `go build ./...`
Expected: Clean build.

- [ ] **Step 4: Commit**

```bash
git add internal/analytics/routes.go internal/skill/routes.go
git commit -m "feat(unregistered): add API endpoints for unregistered list, dismiss, and register"
```

---

### Task 4: Web UI — unregistered tab component

**Files:**
- Create: `web/src/features/skills/unregistered-tab.tsx`

- [ ] **Step 1: Create the unregistered tab component**

```tsx
// web/src/features/skills/unregistered-tab.tsx
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { UserPlus, EyeOff } from "lucide-react";
import { Button } from "@/components/ui/button";
import { toast } from "sonner";

type UnregisteredSkill = {
  name: string;
  activations: number;
  unique_devs: number;
  last_triggered: string | null;
  first_seen: string | null;
};

function formatRelativeTime(dateString: string | null): string {
  if (!dateString) return "—";
  const now = Date.now();
  const then = new Date(dateString).getTime();
  const diffMs = now - then;
  const diffDay = Math.floor(diffMs / 86_400_000);
  if (diffDay < 1) {
    const diffHr = Math.floor(diffMs / 3_600_000);
    if (diffHr < 1) return "just now";
    return `${diffHr}h ago`;
  }
  if (diffDay < 7) return `${diffDay}d ago`;
  if (diffDay < 30) return `${Math.floor(diffDay / 7)}w ago`;
  return `${Math.floor(diffDay / 30)}mo ago`;
}

async function fetchUnregistered(days: number): Promise<UnregisteredSkill[]> {
  const res = await fetch(`/api/analytics/unregistered?days=${days}`, { credentials: "include" });
  if (!res.ok) return [];
  return res.json();
}

async function dismissSkill(name: string): Promise<void> {
  const res = await fetch("/api/analytics/dismiss", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "include",
    body: JSON.stringify({ name }),
  });
  if (!res.ok) throw new Error("Failed to dismiss");
}

async function registerSkill(name: string): Promise<void> {
  const res = await fetch("/api/skills/register", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "include",
    body: JSON.stringify({ name }),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.detail || body.title || "Failed to register");
  }
}

export function UnregisteredTab({ days }: { days: number }) {
  const queryClient = useQueryClient();
  const queryKey = ["analytics", "unregistered", days];

  const { data, isLoading } = useQuery({
    queryKey,
    queryFn: () => fetchUnregistered(days),
  });

  const skills = data ?? [];

  const registerMutation = useMutation({
    mutationFn: registerSkill,
    onSuccess: (_data, name) => {
      toast.success(`Registered — ${name} is now in the registry`);
      queryClient.invalidateQueries({ queryKey: ["analytics"] });
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to register");
    },
  });

  const dismissMutation = useMutation({
    mutationFn: dismissSkill,
    onSuccess: (_data, name) => {
      toast.success(`Dismissed — ${name} hidden from unregistered`);
      queryClient.invalidateQueries({ queryKey });
    },
    onError: () => {
      toast.error("Failed to dismiss");
    },
  });

  if (isLoading) {
    return (
      <div className="space-y-px">
        {Array.from({ length: 4 }).map((_, i) => (
          <div key={i} className="h-12 bg-bg-secondary animate-pulse-soft rounded mb-1" />
        ))}
      </div>
    );
  }

  if (skills.length === 0) {
    return (
      <div className="text-center py-16 text-text-secondary">
        <div className="text-sm mb-2">No unregistered skills detected</div>
        <div className="text-xs text-text-tertiary">
          All skill activations match registered skills
        </div>
      </div>
    );
  }

  return (
    <div>
      {/* Column headers */}
      <div
        className="grid gap-4 px-3.5 py-2 text-[10px] text-text-tertiary uppercase tracking-[0.08em] border-b border-border"
        style={{ gridTemplateColumns: "1fr 80px 80px 100px 100px 140px" }}
      >
        <span>Skill</span>
        <span className="text-right">Activations</span>
        <span className="text-right">Devs</span>
        <span className="text-right">Last triggered</span>
        <span className="text-right">First seen</span>
        <span className="text-right">Actions</span>
      </div>

      {/* Rows */}
      {skills.map((sk) => (
        <div
          key={sk.name}
          className="grid gap-4 items-center px-3.5 py-3 border-b border-border hover:bg-bg-secondary transition-colors"
          style={{ gridTemplateColumns: "1fr 80px 80px 100px 100px 140px" }}
        >
          <span className="font-mono text-[13px] text-text-primary font-medium truncate">
            {sk.name}
          </span>
          <span className="text-[13px] text-text-primary text-right" style={{ fontVariantNumeric: "tabular-nums" }}>
            {sk.activations.toLocaleString()}
          </span>
          <span className="text-[13px] text-text-primary text-right" style={{ fontVariantNumeric: "tabular-nums" }}>
            {sk.unique_devs}
          </span>
          <span className="text-[11px] text-text-tertiary text-right">
            {formatRelativeTime(sk.last_triggered)}
          </span>
          <span className="text-[11px] text-text-tertiary text-right">
            {formatRelativeTime(sk.first_seen)}
          </span>
          <div className="flex items-center justify-end gap-1.5">
            <Button
              size="sm"
              variant="outline"
              className="h-7 text-[11px] px-2"
              disabled={registerMutation.isPending}
              onClick={() => registerMutation.mutate(sk.name)}
            >
              <UserPlus size={12} className="mr-1" />
              Register
            </Button>
            <Button
              size="sm"
              variant="outline"
              className="h-7 text-[11px] px-2 text-text-tertiary"
              disabled={dismissMutation.isPending}
              onClick={() => dismissMutation.mutate(sk.name)}
            >
              <EyeOff size={12} className="mr-1" />
              Dismiss
            </Button>
          </div>
        </div>
      ))}
    </div>
  );
}
```

- [ ] **Step 2: Verify TypeScript compiles**

Run: `cd /Users/nathananderson-tennant/Development/skael/web && npx tsc --noEmit`
Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/features/skills/unregistered-tab.tsx
git commit -m "feat(unregistered): add unregistered skills tab component"
```

---

### Task 5: Web UI — add tab bar to skill list page

**Files:**
- Modify: `web/src/features/skills/skill-list.tsx`

- [ ] **Step 1: Add imports and state**

At the top of `skill-list.tsx`, add the imports:

```tsx
import { UnregisteredTab } from "@/features/skills/unregistered-tab";
```

Inside the `SkillList` component, add state for the active tab:

```tsx
const [activeTab, setActiveTab] = useState<"registry" | "unregistered">("registry");
```

Add a query for the unregistered count (to show in the tab badge):

```tsx
const { data: unregisteredData } = useQuery({
  queryKey: ["analytics", "unregistered", 30],
  queryFn: async () => {
    const res = await fetch("/api/analytics/unregistered?days=30", { credentials: "include" });
    if (!res.ok) return [];
    return res.json() as Promise<{ name: string }[]>;
  },
});
const unregisteredCount = unregisteredData?.length ?? 0;
```

- [ ] **Step 2: Add tab bar above the filter section**

In the JSX, right before the `{/* Filter + list */}` comment (around line 483), add:

```tsx
        {/* Tab bar */}
        <div className="px-12 flex border-b border-border max-w-screen-xl">
          <button
            onClick={() => setActiveTab("registry")}
            className={`px-4 py-2.5 text-[13px] font-sans border-b-2 transition-colors cursor-pointer bg-transparent ${
              activeTab === "registry"
                ? "text-text-primary border-accent font-medium"
                : "text-text-secondary border-transparent hover:text-text-primary"
            }`}
          >
            Registry
          </button>
          <button
            onClick={() => setActiveTab("unregistered")}
            className={`px-4 py-2.5 text-[13px] font-sans border-b-2 transition-colors cursor-pointer bg-transparent flex items-center gap-2 ${
              activeTab === "unregistered"
                ? "text-text-primary border-accent font-medium"
                : "text-text-secondary border-transparent hover:text-text-primary"
            }`}
          >
            Unregistered
            {unregisteredCount > 0 && (
              <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-warning/20 text-warning font-medium">
                {unregisteredCount}
              </span>
            )}
          </button>
        </div>
```

- [ ] **Step 3: Conditionally render the tab content**

Wrap the existing filter bar, bulk actions, column headers, and skill rows in a conditional so they only show when `activeTab === "registry"`. Replace the current `{/* Filter + list */}` section:

Change the opening of the filter+list section from:

```tsx
      {/* Filter + list */}
      <div className="px-12 pb-12 flex-1 flex flex-col min-h-0 max-w-screen-xl">
```

To:

```tsx
      {/* Tab content */}
      <div className="px-12 pb-12 flex-1 flex flex-col min-h-0 max-w-screen-xl">
        {activeTab === "unregistered" ? (
          <div className="mt-4">
            <UnregisteredTab days={30} />
          </div>
        ) : (
        <>
```

And right before the `<ImportModal .../>` at the end, close the fragment and conditional:

```tsx
        </>
        )}
```

So the structure becomes:

```tsx
<div className="px-12 pb-12 ...">
  {activeTab === "unregistered" ? (
    <div className="mt-4"><UnregisteredTab days={30} /></div>
  ) : (
    <>
      {/* Filter bar */}
      ...
      {/* Bulk actions */}
      ...
      {/* Column headers */}
      ...
      {/* Skill rows */}
      ...
    </>
  )}
</div>
<ImportModal ... />
```

- [ ] **Step 4: Verify TypeScript compiles**

Run: `cd /Users/nathananderson-tennant/Development/skael/web && npx tsc --noEmit`
Expected: No errors.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/skills/skill-list.tsx
git commit -m "feat(unregistered): add Registry/Unregistered tab bar to skills page"
```

---

### Task 6: Regenerate OpenAPI SDK + verify

**Files:**
- Regenerate: `web/openapi.json`, `web/src/api/sdk.gen.ts`, `web/src/api/types.gen.ts`

- [ ] **Step 1: Rebuild server and regenerate**

```bash
cd /Users/nathananderson-tennant/Development/skael
go build -o bin/skael-server ./cmd/server/
bin/skael-server --openapi > web/openapi.json
cd web && npx @hey-api/openapi-ts
```

Expected: New functions `analyticsUnregistered`, `dismissSkill`, `registerSkill` appear in `sdk.gen.ts`.

- [ ] **Step 2: Verify full build**

```bash
go build ./...
cd web && npx tsc --noEmit
```

- [ ] **Step 3: Run all backend tests**

```bash
cd /Users/nathananderson-tennant/Development/skael
go test ./internal/analytics/ -v -count=1
```

Expected: All tests pass including the new unregistered + dismiss tests.

- [ ] **Step 4: Commit (if SDK files are tracked)**

The SDK files are gitignored, so no commit needed for those. But commit any other changes:

```bash
git status
# If any tracked files changed, commit them
```
