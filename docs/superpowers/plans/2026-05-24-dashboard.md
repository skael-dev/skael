# Skael Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the React SPA dashboard embedded in the Go server binary — skill explorer, detail view, analytics page, settings, and security review workflow.

**Architecture:** React 19 SPA (Vite + Tailwind v4 + shadcn/ui) consumed via hey-api generated TypeScript client. Embedded in Go binary via `embed.FS`. Backend gains review endpoints, analytics aggregation endpoints, and an `--openapi` flag for build-time spec extraction.

**Tech Stack:** Go 1.25, Huma v2, Chi, pgx v5, React 19, Vite, Tailwind CSS v4, shadcn/ui, TanStack Query, hey-api, Shiki, react-markdown, Geist font

**Design spec:** `docs/superpowers/specs/2026-05-24-dashboard-design.md`

**Design prototype:** Claude Design export at `skillctl/` (prototype components in `skillctl/project/components/`). The prototype uses "skillctl" naming — replace with "skael" everywhere. Match the visual output of the prototype's latest iteration (v2 — restrained, wide whitespace, ambient blob, minimal mono decoration). Component files to reference:
- `skillctl/project/components/skill-explorer.jsx` — explorer layout, stat tiles, filter bar, list rows
- `skillctl/project/components/skill-detail.jsx` — tabbed detail, sliding tabs, content/files/versions/usage tabs
- `skillctl/project/components/sidebar.jsx` — 56px icon rail with tooltips
- `skillctl/project/components/topbar.jsx` — breadcrumb, cmd+K trigger, sync indicator, workspace switcher
- `skillctl/project/components/command-palette.jsx` — cmd+K overlay
- `skillctl/project/components/settings.jsx` — settings page with sub-nav
- `skillctl/project/components/onboarding.jsx` — empty state
- `skillctl/project/lib/icons.jsx` — icon system
- `skillctl/project/lib/motion.jsx` — animation helpers (FadeIn, useCountUp, MOTION constants)

---

## Task 1: Schema — Add review columns to skills table

**Files:**
- Modify: `internal/platform/migrate/001_initial.sql`
- Modify: `internal/skill/skill.go`
- Modify: `internal/skill/store.go`

This adds `reviewed_at` and `reviewed_by` to the skills table and updates Go types and queries to include them. No new migration file — we haven't shipped, so we modify 001_initial.sql directly.

- [ ] **Step 1: Add columns to 001_initial.sql**

Add two columns to the `CREATE TABLE skills` block, after `updated_at`:

```sql
reviewed_at     TIMESTAMPTZ,
reviewed_by     TEXT NOT NULL DEFAULT ''
```

- [ ] **Step 2: Add fields to Skill struct**

In `internal/skill/skill.go`, add to the `Skill` struct:

```go
ReviewedAt  *time.Time `json:"reviewed_at"`
ReviewedBy  string     `json:"reviewed_by"`
```

- [ ] **Step 3: Update scanSkill in store.go**

Update the `scanSkill` function to scan the two new columns. Update every SQL query that selects from `skills` to include `reviewed_at, reviewed_by` in the SELECT list: `GetByName`, `List`, `Create`, and the search queries in `search.go`.

The column order in every SELECT must match the `Scan` call order. Add `&sk.ReviewedAt` and `&sk.ReviewedBy` at the end.

- [ ] **Step 4: Reset review on version publish**

In `store.go` `CreateVersion`, inside the transaction after incrementing `latest_version`, also reset the review columns:

```sql
UPDATE skills
SET latest_version = latest_version + 1, updated_at = now(),
    reviewed_at = NULL, reviewed_by = ''
WHERE id = $1
RETURNING latest_version
```

- [ ] **Step 5: Add review store methods**

Add to `internal/skill/store.go`:

```go
func (s *Store) SetReview(ctx context.Context, name, reviewedBy string) error {
    const q = `UPDATE skills SET reviewed_at = now(), reviewed_by = $2 WHERE name = $1`
    tag, err := s.pool.Exec(ctx, q, name, reviewedBy)
    if err != nil {
        return fmt.Errorf("skill.Store.SetReview: %w", err)
    }
    if tag.RowsAffected() == 0 {
        return fmt.Errorf("skill.Store.SetReview: skill %q not found", name)
    }
    return nil
}

func (s *Store) ClearReview(ctx context.Context, name string) error {
    const q = `UPDATE skills SET reviewed_at = NULL, reviewed_by = '' WHERE name = $1`
    tag, err := s.pool.Exec(ctx, q, name)
    if err != nil {
        return fmt.Errorf("skill.Store.ClearReview: %w", err)
    }
    if tag.RowsAffected() == 0 {
        return fmt.Errorf("skill.Store.ClearReview: skill %q not found", name)
    }
    return nil
}

func (s *Store) BulkSetReview(ctx context.Context, names []string, reviewedBy string) (int, error) {
    const q = `UPDATE skills SET reviewed_at = now(), reviewed_by = $2 WHERE name = ANY($1)`
    tag, err := s.pool.Exec(ctx, q, names, reviewedBy)
    if err != nil {
        return 0, fmt.Errorf("skill.Store.BulkSetReview: %w", err)
    }
    return int(tag.RowsAffected()), nil
}
```

- [ ] **Step 6: Drop and recreate local dev DB**

```bash
just db-stop || true
just db
sleep 2
just migrate
```

- [ ] **Step 7: Run tests, fix any scan column mismatches**

```bash
just test
```

Fix any failing tests — the most likely issue is the `scanSkill` Scan order not matching updated SELECT columns. Also update `search.go` queries if they select from `skills`.

- [ ] **Step 8: Commit**

```bash
git add internal/platform/migrate/001_initial.sql internal/skill/skill.go internal/skill/store.go internal/skill/search.go
git commit -m "feat: add reviewed_at/reviewed_by to skills schema and store"
```

---

## Task 2: Backend — Review endpoints

**Files:**
- Modify: `internal/skill/routes.go`
- Create: `internal/skill/routes_review_test.go`

- [ ] **Step 1: Write tests for review endpoints**

Create `internal/skill/routes_review_test.go`. Use the existing test patterns from `routes_test.go` — the project uses `testutil.SetupTestDB(t)` for real Postgres and `NewChiAPI()` for route setup.

Test cases:
1. `PUT /api/skills/{name}/review` — returns 200, subsequent GET shows `reviewed_at` non-null
2. `DELETE /api/skills/{name}/review` — returns 204, subsequent GET shows `reviewed_at` null
3. `PUT /api/skills/review` with body `{"names":["a","b"]}` — bulk review, both skills gain `reviewed_at`
4. `PUT /api/skills/{name}/review` on nonexistent skill — returns 404
5. Publishing a new version resets `reviewed_at` to null

- [ ] **Step 2: Run tests to verify they fail**

```bash
just test-pkg internal/skill
```

Expected: FAIL — the review route handlers don't exist yet.

- [ ] **Step 3: Implement review routes**

Add to `RegisterRoutes` in `internal/skill/routes.go`:

```go
// PUT /api/skills/{name}/review
huma.Register(api, huma.Operation{
    OperationID: "review-skill",
    Method:      http.MethodPut,
    Path:        "/api/skills/{name}/review",
    Summary:     "Mark skill as reviewed",
}, func(ctx context.Context, input *struct{ Name string `path:"name"` }) (*struct{ Body *Skill }, error) {
    if err := store.SetReview(ctx, input.Name, "admin"); err != nil {
        return nil, huma.Error404NotFound(fmt.Sprintf("skill %q not found", input.Name))
    }
    sk, err := store.GetByName(ctx, input.Name)
    if err != nil {
        return nil, fmt.Errorf("review skill: %w", err)
    }
    return &struct{ Body *Skill }{Body: sk}, nil
})

// DELETE /api/skills/{name}/review
huma.Register(api, huma.Operation{
    OperationID:   "unreview-skill",
    Method:        http.MethodDelete,
    Path:          "/api/skills/{name}/review",
    Summary:       "Unmark skill review",
    DefaultStatus: http.StatusNoContent,
}, func(ctx context.Context, input *struct{ Name string `path:"name"` }) (*struct{}, error) {
    if err := store.ClearReview(ctx, input.Name); err != nil {
        return nil, huma.Error404NotFound(fmt.Sprintf("skill %q not found", input.Name))
    }
    return nil, nil
})

// PUT /api/skills/review — bulk review
huma.Register(api, huma.Operation{
    OperationID: "bulk-review-skills",
    Method:      http.MethodPut,
    Path:        "/api/skills/review",
    Summary:     "Bulk mark skills as reviewed",
}, func(ctx context.Context, input *struct {
    Body struct {
        Names []string `json:"names" minItems:"1" maxItems:"100"`
    }
}) (*struct {
    Body struct {
        Reviewed int `json:"reviewed"`
    }
}, error) {
    n, err := store.BulkSetReview(ctx, input.Body.Names, "admin")
    if err != nil {
        return nil, fmt.Errorf("bulk review: %w", err)
    }
    return &struct {
        Body struct {
            Reviewed int `json:"reviewed"`
        }
    }{Body: struct {
        Reviewed int `json:"reviewed"`
    }{Reviewed: n}}, nil
})
```

Note: the bulk review path `/api/skills/review` must be registered **before** the parameterized `/api/skills/{name}` routes to avoid routing conflicts. Or use a different path if Chi's router doesn't allow it — test and adjust.

- [ ] **Step 4: Run tests**

```bash
just test-pkg internal/skill
```

Expected: all tests pass including the new review tests.

- [ ] **Step 5: Commit**

```bash
git add internal/skill/routes.go internal/skill/routes_review_test.go
git commit -m "feat: add review/unreview/bulk-review endpoints"
```

---

## Task 3: Backend — Analytics aggregation endpoints

**Files:**
- Modify: `internal/analytics/event.go` (add store methods)
- Modify: `internal/analytics/routes.go` (add overview + skills endpoints)
- Create: `internal/analytics/routes_analytics_test.go`

- [ ] **Step 1: Write tests for analytics endpoints**

Test cases:
1. `GET /api/analytics/overview?days=30` — returns `{ total_skills, active_skills, total_activations, security: { clean, warning, critical } }`
2. `GET /api/analytics/skills?days=30&sort=activations&order=desc` — returns sorted array with per-skill activation data
3. Both endpoints work with empty database (zero values, not errors)

- [ ] **Step 2: Run tests to verify they fail**

```bash
just test-pkg internal/analytics
```

- [ ] **Step 3: Add store methods**

In `internal/analytics/event.go`, add:

```go
type OverviewData struct {
    TotalSkills      int             `json:"total_skills"`
    ActiveSkills     int             `json:"active_skills"`
    TotalActivations int             `json:"total_activations"`
    Security         SecuritySummary `json:"security"`
}

type SecuritySummary struct {
    Clean    int `json:"clean"`
    Warning  int `json:"warning"`
    Critical int `json:"critical"`
}

type SkillAnalytics struct {
    Name           string     `json:"name"`
    Description    string     `json:"description"`
    Activations    int        `json:"activations"`
    UniqueDevs     int        `json:"unique_devs"`
    LastTriggered  *time.Time `json:"last_triggered"`
    SecurityStatus string     `json:"security_status"`
    ReviewedAt     *time.Time `json:"reviewed_at"`
    LatestVersion  int        `json:"latest_version"`
    UpdatedAt      time.Time  `json:"updated_at"`
}
```

Add `GetOverview(ctx, days int) (*OverviewData, error)` — queries skills count, joins with skill_events for activation counts, joins with skill_versions for scan_result status.

Add `GetSkillsAnalytics(ctx, days int) ([]SkillAnalytics, error)` — returns per-skill data joining skills, skill_events aggregates, and latest scan_result.

These methods need access to the skills and skill_versions tables, so the analytics `Store` needs the same `pgxpool.Pool` it already has.

- [ ] **Step 4: Implement analytics routes**

In `internal/analytics/routes.go`, add:

```go
// GET /api/analytics/overview
huma.Register(api, huma.Operation{
    OperationID: "analytics-overview",
    Method:      http.MethodGet,
    Path:        "/api/analytics/overview",
    Summary:     "Analytics overview for KPI strip",
}, handler)

// GET /api/analytics/skills
huma.Register(api, huma.Operation{
    OperationID: "analytics-skills",
    Method:      http.MethodGet,
    Path:        "/api/analytics/skills",
    Summary:     "Per-skill analytics for table view",
}, handler)
```

Both accept a `days` query param (default 30, min 1, max 365).

- [ ] **Step 5: Run tests**

```bash
just test-pkg internal/analytics
```

- [ ] **Step 6: Commit**

```bash
git add internal/analytics/
git commit -m "feat: add analytics overview and per-skill analytics endpoints"
```

---

## Task 4: Backend — OpenAPI flag for build-time spec extraction

**Files:**
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Add --openapi flag**

In `cmd/server/main.go`, check `os.Args` for `--openapi` before starting the server. If present, create the router and Huma API (registering all routes), then print the OpenAPI JSON to stdout and exit. This avoids needing a database connection for spec generation.

```go
if len(os.Args) > 1 && os.Args[1] == "--openapi" {
    router := chi.NewMux()
    config := huma.DefaultConfig("Skael API", "1.0.0")
    api := humachi.New(router, config)
    // Register all routes with nil stores (spec generation only, no DB needed)
    // ... register routes ...
    spec, _ := json.MarshalIndent(api.OpenAPI(), "", "  ")
    fmt.Println(string(spec))
    os.Exit(0)
}
```

The challenge: route registration currently requires `*Store` instances. To make this work, either:
- Pass nil stores and ensure route registration doesn't call them (it shouldn't — it only registers handlers)
- Create a separate `RegisterSpec` function that registers the Huma operations without handler implementations

The first approach works because Huma only needs the operation definitions for the spec — the handler functions are only called at request time.

- [ ] **Step 2: Test it**

```bash
go run ./cmd/server --openapi | head -20
```

Expected: JSON OpenAPI spec printed to stdout, process exits.

- [ ] **Step 3: Add auth middleware exemption for non-API paths**

The auth middleware currently only skips `/api/health` and `/api/openapi.json`. For the SPA, it needs to also skip all non-`/api/` paths (static files and the SPA catch-all). Update `internal/auth/middleware.go`:

```go
if !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/health" || r.URL.Path == "/api/openapi.json" {
    next.ServeHTTP(w, r)
    return
}
```

- [ ] **Step 4: Run tests**

```bash
just test
```

- [ ] **Step 5: Commit**

```bash
git add cmd/server/main.go internal/auth/middleware.go
git commit -m "feat: add --openapi flag for build-time spec generation, skip auth for SPA paths"
```

---

## Task 5: Frontend — Scaffold Vite + React + Tailwind v4 project

**Files:**
- Create: `web/package.json`
- Create: `web/vite.config.ts`
- Create: `web/tsconfig.json`
- Create: `web/index.html`
- Create: `web/src/main.tsx`
- Create: `web/src/app/app.tsx`
- Create: `web/src/styles/globals.css`
- Create: `web/embed.go`
- Modify: `.gitignore`

This task sets up the bare React project that renders "Hello Skael" and verifies the Vite dev server proxies to the Go API.

- [ ] **Step 1: Initialize the web project**

```bash
cd web
npm create vite@latest . -- --template react-ts
```

If the directory already has files, use `npm init -y` and install manually:

```bash
cd web
npm init -y
npm install react react-dom react-router-dom @tanstack/react-query
npm install -D vite @vitejs/plugin-react typescript @types/react @types/react-dom tailwindcss @tailwindcss/vite
```

- [ ] **Step 2: Configure vite.config.ts**

```ts
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    proxy: {
      "/api": "http://localhost:8080",
    },
  },
});
```

- [ ] **Step 3: Configure tsconfig.json**

Standard React TS config with `"paths": { "@/*": ["./src/*"] }` for clean imports. Add `"baseUrl": "."`.

- [ ] **Step 4: Create globals.css with Tailwind v4 + design tokens**

`web/src/styles/globals.css`:

```css
@import "tailwindcss";

@theme {
  --color-bg-primary: #0a0a0a;
  --color-bg-secondary: #141414;
  --color-bg-tertiary: #1e1e1e;
  --color-border: #262626;
  --color-border-active: #404040;
  --color-text-primary: #ededed;
  --color-text-secondary: #a0a0a0;
  --color-text-tertiary: #666666;
  --color-accent: #22c55e;
  --color-accent-muted: #166534;
  --color-accent-surface: #052e16;
  --color-danger: #ef4444;
  --color-warning: #f59e0b;
  --color-info: #3b82f6;
  --font-sans: "Geist", system-ui, sans-serif;
  --font-mono: "Geist Mono", "SF Mono", monospace;
}

body {
  background: var(--color-bg-primary);
  color: var(--color-text-primary);
  font-family: var(--font-sans);
}
```

- [ ] **Step 5: Create index.html**

Standard Vite entry with Geist font loaded from CDN. `<div id="root">` mount point.

- [ ] **Step 6: Create main.tsx and app.tsx**

`web/src/main.tsx` — renders `<App />` into `#root` wrapped in `QueryClientProvider` and `BrowserRouter`.

`web/src/app/app.tsx` — renders a `<Routes>` block with a single catch-all route that shows "Hello Skael" for now.

- [ ] **Step 7: Create embed.go**

`web/embed.go`:

```go
package web

import "embed"

//go:embed dist/*
var Assets embed.FS
```

Note: this file will fail to compile until `web/dist/` exists (the embed directive requires the directory). For development, create a placeholder: `mkdir -p web/dist && touch web/dist/.gitkeep`.

- [ ] **Step 8: Update .gitignore**

Append:

```
web/dist/
web/src/api/
web/openapi.json
web/node_modules/
```

- [ ] **Step 9: Test the dev server**

Start Go server in one terminal, Vite in another:

```bash
# Terminal 1
just dev

# Terminal 2
cd web && npm run dev
```

Open `http://localhost:5173` — should see "Hello Skael". Open `http://localhost:5173/api/health` — should proxy to Go and return `{"status":"ok"}`.

- [ ] **Step 10: Commit**

```bash
git add web/ .gitignore
git commit -m "feat: scaffold Vite + React + Tailwind v4 project with Go embed"
```

---

## Task 6: Frontend — shadcn/ui setup + shared primitives

**Files:**
- Create: `web/src/lib/utils.ts`
- Create: `web/components.json` (shadcn config)
- Create: `web/src/components/ui/*.tsx` (shadcn primitives)

- [ ] **Step 1: Install shadcn/ui dependencies**

```bash
cd web
npm install class-variance-authority clsx tailwind-merge lucide-react
npm install cmdk
npx shadcn@latest init
```

During init, select: TypeScript, New York style, CSS variables, `src/components/ui` for components path, `@/lib/utils` for utils.

- [ ] **Step 2: Add shadcn components**

```bash
cd web
npx shadcn@latest add button tabs table checkbox badge skeleton command dialog dropdown-menu
```

This copies component source files into `web/src/components/ui/`.

- [ ] **Step 3: Create utils.ts if shadcn didn't**

`web/src/lib/utils.ts`:

```ts
import { type ClassValue, clsx } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
```

- [ ] **Step 4: Verify components render**

Temporarily import and render a `<Button>` in app.tsx. Check it renders in the browser with correct styling. Remove after verifying.

- [ ] **Step 5: Commit**

```bash
git add web/
git commit -m "feat: add shadcn/ui components and design system primitives"
```

---

## Task 7: Frontend — hey-api client generation

**Files:**
- Create: `web/openapi-ts.config.ts`
- Modify: `web/package.json` (add generate script)
- Modify: `justfile` (add generate command)

- [ ] **Step 1: Install hey-api**

```bash
cd web
npm install -D @hey-api/openapi-ts @hey-api/client-fetch
```

- [ ] **Step 2: Create openapi-ts.config.ts**

```ts
import { defineConfig } from "@hey-api/openapi-ts";

export default defineConfig({
  client: "@hey-api/client-fetch",
  input: "openapi.json",
  output: "src/api",
});
```

- [ ] **Step 3: Add generate script to package.json**

Add to `scripts`:

```json
"generate": "openapi-ts"
```

- [ ] **Step 4: Add just commands**

Add to `justfile`:

```
# Generate OpenAPI spec + TypeScript client
generate:
    go run ./cmd/server --openapi > web/openapi.json
    cd web && npm run generate

# Run Vite dev server
web-dev:
    cd web && npm run dev

# Build the SPA
web-build:
    cd web && npm run build
```

- [ ] **Step 5: Generate the client**

```bash
just generate
```

Expected: `web/openapi.json` created, `web/src/api/` generated with typed client functions.

- [ ] **Step 6: Set up TanStack Query client**

`web/src/lib/query.ts`:

```ts
import { QueryClient } from "@tanstack/react-query";
import { client } from "@/api/client.gen";

client.setConfig({
  baseUrl: "",
  headers: {
    "X-API-Key": "dev-key", // TODO: read from settings in production
  },
});

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: 1,
    },
  },
});
```

- [ ] **Step 7: Commit**

```bash
git add web/openapi-ts.config.ts web/src/lib/query.ts web/package.json justfile
git commit -m "feat: add hey-api client generation and TanStack Query setup"
```

---

## Task 8: Frontend — Shell layout (sidebar + top bar)

**Files:**
- Create: `web/src/app/shell.tsx`
- Create: `web/src/app/sidebar.tsx`
- Create: `web/src/app/top-bar.tsx`
- Modify: `web/src/app/app.tsx`

Reference the prototype: `skillctl/project/components/sidebar.jsx` (56px icon rail) and `skillctl/project/components/topbar.jsx` (breadcrumb + cmd+K + sync indicator).

- [ ] **Step 1: Build sidebar component**

56px icon-only rail. Logo at top (green "s" square), nav icons (skills, analytics), settings at bottom. Active item gets accent icon + bg-tertiary. Tooltips on hover using a positioned div (same approach as prototype). Use `lucide-react` icons: `Layers` for skills, `BarChart3` for analytics, `Settings` for settings. Use `NavLink` from react-router-dom for active state.

- [ ] **Step 2: Build top-bar component**

48px height. Accepts `breadcrumbs` prop (array of `{label, href?, mono?}`). Renders breadcrumb segments separated by `/`. Right side: cmd+K trigger button (search icon + "Search..." + kbd hint), sync status indicator (pulsing green dot + "synced" text).

- [ ] **Step 3: Build shell component**

Composes sidebar + top-bar + `<Outlet />`. Uses flexbox: sidebar on left (fixed width), top-bar + content on right. Content area scrolls independently.

- [ ] **Step 4: Wire into app.tsx**

```tsx
<Routes>
  <Route element={<Shell />}>
    <Route path="/" element={<div>Skills page</div>} />
    <Route path="/skills/:name" element={<div>Detail page</div>} />
    <Route path="/analytics" element={<div>Analytics page</div>} />
    <Route path="/settings" element={<div>Settings page</div>} />
  </Route>
</Routes>
```

- [ ] **Step 5: Test in browser**

Navigate between routes using sidebar icons. Verify:
- Active icon highlights correctly
- Breadcrumb updates per route
- Layout doesn't scroll the sidebar/topbar (only content area scrolls)

- [ ] **Step 6: Commit**

```bash
git add web/src/app/
git commit -m "feat: add shell layout with icon rail sidebar and top bar"
```

---

## Task 9: Frontend — Skill explorer page

**Files:**
- Create: `web/src/features/skills/skill-list.tsx`
- Create: `web/src/features/skills/skill-card.tsx`
- Create: `web/src/features/security/security-badge.tsx`
- Create: `web/src/features/security/review-status.tsx`

Reference: `skillctl/project/components/skill-explorer.jsx` for layout, stat tiles, filter bar, and list row design.

- [ ] **Step 1: Create security-badge component**

Renders a colored dot based on scan status: green for clean, grey for info, yellow for warning, red for critical. Takes a `status` prop.

- [ ] **Step 2: Create review-status component**

Renders a checkmark icon (reviewed) or empty circle (unreviewed) based on `reviewedAt` prop. Small, inline — used in list rows and detail pages.

- [ ] **Step 3: Create skill-card component**

A table-like row matching the prototype's list row design. Props: skill data object. Renders:
- Checkbox (controlled, for bulk selection)
- Status dot
- Skill name (mono) + first tag (colored dot + label) + description (truncated)
- Invocations count (tabular-nums)
- Security badge + review status
- Version + relative time

Hover state: bg-secondary + 2px accent bar on left edge (use `before` pseudo-element).

- [ ] **Step 4: Create skill-list page**

Full explorer page. Sections:
1. Hero: "Workspace" label, "Skills" title + status dot, description
2. Stat tiles (3-column grid): total invocations, active devs, needs attention. Use the `StatTile` pattern from the prototype — icon, label, value, sub text.
3. Filter bar: text input with `/` keyboard hint, tag filter pills (horizontally scrollable), sort dropdown
4. Skill list: renders `SkillCard` for each skill. Header row with column labels.
5. Bulk actions bar: appears when checkboxes selected. "Select all" + "Mark Reviewed" button.

Data fetching: use TanStack Query with the generated hey-api `listSkills` function. Also fetch activation data.

Client-side filtering: filter by text query and tag. Sort by updated/name/usage.

- [ ] **Step 5: Wire into router**

Replace placeholder `<div>Skills page</div>` with `<SkillList />`.

- [ ] **Step 6: Test in browser**

Publish a few test skills via CLI, then verify:
- Skills appear in the list
- Search filtering works
- Checkbox multi-select works
- Bulk "Mark Reviewed" calls the API and updates the UI
- Empty state shows onboarding content when no skills exist

- [ ] **Step 7: Commit**

```bash
git add web/src/features/skills/skill-list.tsx web/src/features/skills/skill-card.tsx web/src/features/security/
git commit -m "feat: add skill explorer page with search, filter, and bulk review"
```

---

## Task 10: Frontend — Skill detail page (header + tabs)

**Files:**
- Create: `web/src/features/skills/skill-detail.tsx`
- Create: `web/src/features/skills/markdown-renderer.tsx`

Reference: `skillctl/project/components/skill-detail.jsx` for the header layout, meta strip, and sliding tab indicator.

- [ ] **Step 1: Create markdown-renderer component**

Uses `react-markdown` + `remark-gfm` + Shiki for syntax highlighting. Renders GFM markdown with:
- Syntax-highlighted code blocks (Shiki with a dark theme like `vitesse-dark`)
- Copy button on code blocks
- Proper heading anchors
- Styled tables with horizontal scroll
- Task list checkboxes

Install: `npm install react-markdown remark-gfm shiki`

- [ ] **Step 2: Create skill-detail page**

Fetches skill data via `getSkill(name)`, versions via `listSkillVersions(name)`, activations via `getSkillActivations(name)`, and scan results via the scan endpoint.

Layout:
1. Header: "Skill" label, skill name (mono, 32px), status dot, description, meta strip (version, author, invocations, devs, updated), tags
2. Tab bar: uses shadcn `Tabs` component. Tabs: Content, Files, Versions, Usage, Security, Changelog (disabled). Sticky positioning.

Start with only the Content tab implemented — it renders the SKILL.md content using the markdown-renderer. Other tabs will be added in subsequent tasks.

- [ ] **Step 3: Wire into router**

Replace placeholder with `<SkillDetail />`. Make skill names in `SkillCard` link to `/skills/:name`.

- [ ] **Step 4: Test in browser**

Click a skill in the explorer. Verify:
- Header renders with correct data
- SKILL.md content renders with syntax highlighting
- Tab bar is sticky on scroll
- Back navigation works (browser back or breadcrumb click)

- [ ] **Step 5: Commit**

```bash
git add web/src/features/skills/skill-detail.tsx web/src/features/skills/markdown-renderer.tsx
git commit -m "feat: add skill detail page with markdown rendering and tab bar"
```

---

## Task 11: Frontend — Skill detail tabs (Files, Versions, Usage, Security)

**Files:**
- Create: `web/src/features/skills/file-tree.tsx`
- Create: `web/src/features/skills/file-viewer.tsx`
- Create: `web/src/features/skills/version-list.tsx`
- Create: `web/src/features/security/scan-findings.tsx`
- Modify: `web/src/features/skills/skill-detail.tsx`

Reference: `skillctl/project/components/skill-detail.jsx` — specifically `TabFiles`, `TabVersions`, `TabUsage` functions.

- [ ] **Step 1: Files tab — file-tree + file-viewer**

`file-tree.tsx`: Renders the file manifest as an indented tree. Folders and files with icons. Active file gets accent left border. Clickable files update selected file state.

`file-viewer.tsx`: Read-only code viewer with line numbers. Light syntax tokenization (YAML keys colored for .md/.yml files, comments grey for .sh). Monospace font. Uses the file content endpoint: `GET /api/skills/{name}/versions/{version}/files/{path}` — note this endpoint may need to be added if it doesn't exist. Alternative: download the full archive and extract client-side, but that's wasteful. Check if the endpoint exists; if not, use the file manifest data and display a "download archive to view file contents" fallback.

- [ ] **Step 2: Versions tab**

`version-list.tsx`: Timeline layout matching the prototype. Vertical line connecting version dots. Latest version gets accent-colored dot with "current" badge. Each entry shows: version number, author, relative time, changelog text, +/- line counts. "View diff" link (placeholder for now — actual diff is Phase 2).

- [ ] **Step 3: Usage tab**

Inline within `skill-detail.tsx`. Renders:
- KPI row (4 tiles): invocations, unique devs, avg/day, last triggered
- SVG sparkline area chart with animated path reveal (port from prototype's `TabUsage`)
- Time period toggle (7d/30d/90d)
- Top users list (agent breakdown from the activations API, rendered as bar chart rows)

- [ ] **Step 4: Security tab**

`scan-findings.tsx`: Expandable list of findings from the scan result JSON. Each finding shows: severity badge, rule name, file:line, matched text, explanation message. Expand/collapse per finding.

Also includes the `ReviewStatus` component with "Mark Reviewed" / "Unmark" button. Shows `reviewed_at` and `reviewed_by` when reviewed.

- [ ] **Step 5: Wire all tabs into skill-detail.tsx**

Update the `Tabs` component to render each tab's content.

- [ ] **Step 6: Test in browser**

Navigate through all tabs on a skill that has multiple versions and activation data. Verify:
- Files tab shows tree and file viewer
- Versions tab shows timeline
- Usage tab shows sparkline and stats
- Security tab shows findings (publish a skill with info-level findings to test)
- "Mark Reviewed" button works and updates the UI

- [ ] **Step 7: Commit**

```bash
git add web/src/features/skills/ web/src/features/security/
git commit -m "feat: add Files, Versions, Usage, and Security tabs to skill detail"
```

---

## Task 12: Frontend — Analytics page

**Files:**
- Create: `web/src/features/analytics/analytics.tsx`
- Create: `web/src/features/analytics/kpi-strip.tsx`
- Create: `web/src/features/analytics/analytics-table.tsx`

- [ ] **Step 1: Create kpi-strip component**

4-tile grid. Each tile: icon (from lucide-react), label (10px uppercase), value (22px, 500 weight, tabular-nums), optional sub text. Tiles:
1. Total skills — `Layers` icon
2. Active (period) — `Activity` icon
3. Activations — `TrendingUp` icon
4. Security — `Shield` icon, sub shows "X clean, Y warning"

Uses data from `GET /api/analytics/overview`.

- [ ] **Step 2: Create analytics-table component**

Uses shadcn `Table` component. Sortable columns via client-side sort on the fetched data (click column header to toggle sort). Columns: Skill (link to detail), Activations, Devs, Last triggered, Security. Dead skills (0 activations) get `opacity-50` treatment. Monospace for numeric columns (`font-mono tabular-nums`).

- [ ] **Step 3: Create analytics page**

Composes KPI strip + time period toggle (7d/30d/90d pill buttons using shadcn Button variant) + analytics table. Time period is local state, passed as `days` query param to both API calls.

Uses data from `GET /api/analytics/overview?days=N` and `GET /api/analytics/skills?days=N`.

- [ ] **Step 4: Wire into router**

Replace placeholder with `<Analytics />`.

- [ ] **Step 5: Test in browser**

- KPI strip shows correct aggregate numbers
- Table is sortable by clicking column headers
- Time period toggle updates both KPI and table data
- Dead skills appear muted
- Skill names link to detail page

- [ ] **Step 6: Commit**

```bash
git add web/src/features/analytics/
git commit -m "feat: add analytics page with KPI strip and sortable table"
```

---

## Task 13: Frontend — Settings page

**Files:**
- Create: `web/src/features/settings/settings.tsx`

Reference: `skillctl/project/components/settings.jsx` for the sub-nav + sectioned layout.

- [ ] **Step 1: Build settings page**

Sub-nav on left (200px) with section links. Scrollable content on right (max-width 640px). Sections:

1. **Workspace** — read-only rows: workspace name, server URL (from API base), platform version (from health endpoint or hardcoded), skills count (from list API total).
2. **API & Keys** — API key display (masked with dots, reveal toggle, copy button). The key value comes from the client config — it's whatever key the user configured.
3. **Sync Targets** — placeholder list showing detected agents. Since agent detection is CLI-only, this section shows a message directing to `skael doctor` for detailed status.
4. **Danger Zone** — "Regenerate API Key" with red border card. Button shows a confirmation dialog (shadcn Dialog) before acting. Note: the regenerate endpoint doesn't exist yet — wire the button to show a "coming soon" toast or disable it.

Use scrollspy-style active section highlighting: track scroll position and highlight the corresponding nav link.

- [ ] **Step 2: Wire into router**

Replace placeholder with `<Settings />`.

- [ ] **Step 3: Test in browser**

- Sub-nav highlights correct section on scroll
- API key reveal/copy works
- Danger zone button shows confirmation dialog

- [ ] **Step 4: Commit**

```bash
git add web/src/features/settings/
git commit -m "feat: add settings page with workspace info, API key, and sync targets"
```

---

## Task 14: Frontend — Command palette

**Files:**
- Create: `web/src/components/command-palette.tsx`
- Modify: `web/src/app/shell.tsx`

Reference: `skillctl/project/components/command-palette.jsx`.

- [ ] **Step 1: Build command palette**

Uses shadcn `Command` component (based on cmdk). Opens on Cmd+K / Ctrl+K. Contains:
- Search input at top
- "Skills" group: all skills searchable by name and description. Selecting navigates to `/skills/:name`.
- "Navigation" group: Go to Skills, Go to Analytics, Go to Settings. Selecting navigates.
- Footer: "up/down navigate, enter select, N results"

Fetches skills list via TanStack Query (reuses the same query as the explorer — no duplicate fetch).

- [ ] **Step 2: Wire into shell**

Add global Cmd+K keyboard listener in shell.tsx. Toggle command palette open state. Pass `onOpenCommand` callback to top-bar's search button.

- [ ] **Step 3: Test in browser**

- Cmd+K opens palette
- Typing filters skills and actions
- Arrow keys navigate, Enter selects
- Escape closes
- Clicking outside closes

- [ ] **Step 4: Commit**

```bash
git add web/src/components/command-palette.tsx web/src/app/shell.tsx
git commit -m "feat: add command palette with skill search and navigation"
```

---

## Task 15: Frontend — Empty state / onboarding

**Files:**
- Modify: `web/src/features/skills/skill-list.tsx`

Reference: `skillctl/project/components/onboarding.jsx` — adapt for skael (not skillctl).

- [ ] **Step 1: Add empty state to skill list**

When the skills list API returns 0 skills, render an onboarding screen instead of the explorer. Content:

1. "Welcome to Skael" heading
2. "Manage and track AI agent skills across your team. Install the CLI to get started." description
3. CLI install block with install command (`curl -fsSL skael.dev/install | sh`) + copy button, tab switcher for curl/brew/go-install
4. Setup command: `skael setup <url> <api-key>` + `skael publish ./my-skill`
5. "Not sure what a skill is?" footer with brief explanation and docs link

Use the ambient blob gradient (subtle radial gradient) from the prototype for visual interest.

- [ ] **Step 2: Test in browser**

Start with a fresh database (no skills). Verify:
- Onboarding screen appears
- Copy button works
- After publishing a skill via CLI and refreshing, the explorer appears

- [ ] **Step 3: Commit**

```bash
git add web/src/features/skills/skill-list.tsx
git commit -m "feat: add onboarding empty state when no skills published"
```

---

## Task 16: Go embed — SPA serving in production

**Files:**
- Modify: `cmd/server/main.go`
- Create: `web/embed.go` (may already exist from Task 5)

- [ ] **Step 1: Add SPA serving handler to server**

In `cmd/server/main.go`, after registering all API routes, mount the SPA:

```go
import "github.com/skael-dev/skael/web"

// Serve embedded SPA for non-API routes
spaFS, _ := fs.Sub(web.Assets, "dist")
fileServer := http.FileServer(http.FS(spaFS))

// SPA catch-all: serve index.html for any path that doesn't match a static file
router.Get("/*", func(w http.ResponseWriter, r *http.Request) {
    // Try to serve the static file first
    f, err := spaFS.Open(strings.TrimPrefix(r.URL.Path, "/"))
    if err == nil {
        f.Close()
        fileServer.ServeHTTP(w, r)
        return
    }
    // Fall back to index.html for client-side routing
    r.URL.Path = "/"
    fileServer.ServeHTTP(w, r)
})
```

The catch-all must be registered **after** all `/api/*` routes so it doesn't shadow them.

- [ ] **Step 2: Build the SPA and compile**

```bash
just web-build
just build
```

- [ ] **Step 3: Test the embedded build**

```bash
./bin/skael-server
```

Open `http://localhost:8080` — should serve the SPA. Navigate to `/analytics` and refresh — should still serve the SPA (not 404). API calls from the SPA should work.

- [ ] **Step 4: Update just build command**

Modify `justfile` `build` recipe to include web build:

```
build: web-build
    CGO_ENABLED=0 go build -o bin/skael-server ./cmd/server
    CGO_ENABLED=0 go build -o bin/skael ./cmd/skael
```

- [ ] **Step 5: Commit**

```bash
git add cmd/server/main.go web/embed.go justfile
git commit -m "feat: serve embedded React SPA from Go binary with client-side routing"
```

---

## Task 17: Polish — Loading states, skeletons, animations

**Files:**
- Modify: various feature components

Reference: `skillctl/project/lib/motion.jsx` for animation patterns.

- [ ] **Step 1: Add skeleton screens**

Use shadcn `Skeleton` component. Add loading states to:
- Skill list: skeleton rows matching card dimensions
- Skill detail: skeleton header + tab content
- Analytics: skeleton KPI tiles + table rows

TanStack Query's `isLoading` state drives when to show skeletons.

- [ ] **Step 2: Add fade-in animations**

CSS animations for page content. Add to `globals.css`:

```css
@keyframes fade-up {
  from { opacity: 0; transform: translateY(8px); }
  to { opacity: 1; transform: translateY(0); }
}
```

Apply to page sections with staggered delays (prototype uses 40ms increments).

- [ ] **Step 3: Add hover transitions**

Ensure all interactive elements have 0.12-0.15s transitions on background/border color changes. Skill list rows get the accent bar animation on hover.

- [ ] **Step 4: Test in browser**

- Skeleton screens appear briefly on initial load
- Fade-in animations play on page transitions
- Hover states feel responsive and consistent

- [ ] **Step 5: Commit**

```bash
git add web/src/
git commit -m "feat: add skeleton loading states and subtle animations"
```

---

## Task 18: Integration test — Full flow verification

**Files:**
- No new files

- [ ] **Step 1: End-to-end verification**

Run through the complete flow:

```bash
# 1. Start fresh
just down-clean
just db
sleep 2

# 2. Build everything
just generate
just build

# 3. Start server
API_KEY=test-key DATABASE_URL=postgres://skael:skael@localhost:5432/skael?sslmode=disable ./bin/skael-server &

# 4. Publish test skills via CLI
./bin/skael setup http://localhost:8080 test-key
# Create and publish a few test skills

# 5. Open dashboard
open http://localhost:8080
```

Verify:
- Explorer shows published skills with correct data
- Search and filter work
- Click skill → detail page with all tabs
- Analytics page shows KPI data and sortable table
- Settings page shows workspace info
- Cmd+K palette works
- Mark Reviewed workflow works (single and bulk)
- Security tab shows scan findings

- [ ] **Step 2: Fix any issues found**

- [ ] **Step 3: Final commit**

```bash
git add -A
git commit -m "fix: integration test fixes for dashboard"
```

---

## Summary

| Task | Component | Dependencies |
|------|-----------|-------------|
| 1 | Schema + review columns | None |
| 2 | Review endpoints | Task 1 |
| 3 | Analytics endpoints | Task 1 |
| 4 | OpenAPI flag + auth fix | None |
| 5 | Vite + React scaffold | None |
| 6 | shadcn/ui setup | Task 5 |
| 7 | hey-api client gen | Tasks 4, 5 |
| 8 | Shell layout | Tasks 6 |
| 9 | Skill explorer | Tasks 7, 8 |
| 10 | Skill detail (header + content tab) | Tasks 7, 8 |
| 11 | Skill detail (remaining tabs) | Task 10 |
| 12 | Analytics page | Tasks 7, 8 |
| 13 | Settings page | Tasks 6, 8 |
| 14 | Command palette | Tasks 6, 8 |
| 15 | Empty state / onboarding | Task 9 |
| 16 | Go embed + SPA serving | Tasks 5, all frontend |
| 17 | Loading states + animations | All frontend tasks |
| 18 | Integration test | All tasks |

**Parallelizable groups:**
- Tasks 1-4 (backend) can run in parallel with Tasks 5-6 (frontend scaffold)
- Tasks 9, 10, 12, 13, 14 can be worked on in parallel once Task 8 (shell) is done
- Task 11 depends on Task 10
- Tasks 15, 16, 17 are sequential finishers
