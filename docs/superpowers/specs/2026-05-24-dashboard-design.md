# Skael Dashboard — Design Spec

**Date:** 2026-05-24 · **Status:** Draft

---

## Summary

React SPA embedded in the Go server binary via `embed.FS`. Provides skill exploration, activation analytics, security review workflow, and platform settings. Consumes the existing Go API through a TypeScript client generated from the OpenAPI spec.

## Scope

Phase 1 dashboard with three additions pulled forward from Phase 2: dedicated analytics page, security review workflow (mark skills as reviewed), and import source attribution. Import itself remains CLI-only.

### Pages

| Route | Page | Description |
|---|---|---|
| `/` | Skill explorer | Searchable list with stat tiles, tag filters, multi-select bulk review |
| `/skills/:name` | Skill detail | Tabbed view: Content, Files, Versions, Usage, Security. File preview is inline in the Files tab. |
| `/analytics` | Analytics | KPI strip + sortable table with dead skill highlighting |
| `/settings` | Settings | Workspace info, API key, sync targets |

### Out of scope (Phase 1)

- Workspace/org switcher (multi-org is Phase 3)
- File editing + diff modal (read-only; publish via CLI)
- Notifications bell + notification settings
- "New skill" / "Create" button (publish is CLI-only)
- Trend calculations (analytics shows raw counts, not period-over-period deltas)
- Changelog tab on skill detail (shown disabled as placeholder)

---

## Tech stack

| Tool | Role |
|---|---|
| Vite | Build tooling |
| React 19 + React Router | SPA routing |
| Tailwind CSS v4 | Styling (CSS-first config, no tailwind.config.ts) |
| shadcn/ui | Component primitives (dialog, dropdown, tabs, table, checkbox) |
| TanStack Query | API data fetching + caching |
| hey-api | TypeScript client generated from OpenAPI spec |
| Shiki | Syntax highlighting for code in SKILL.md and file preview |
| react-markdown + remark-gfm | Markdown rendering |
| Geist (sans + mono) | Typography |

No full component library. shadcn/ui primitives are copied into the project — they're source code, not a dependency.

---

## Embed & serving architecture

```
Build pipeline:
  go run ./cmd/server --openapi > web/openapi.json
  cd web && npx @hey-api/openapi-ts       -> generates web/src/api/
  cd web && npm run build                  -> outputs to web/dist/

Embed:
  web/embed.go:
    //go:embed dist/*
    var Assets embed.FS

Server routing (cmd/server/main.go):
  /api/*            -> Huma/Chi handlers (existing)
  /assets/*         -> embedded static files (JS, CSS, fonts)
  everything else   -> web/dist/index.html (SPA catch-all)
```

The `embed.go` lives in the `web/` Go package. The server imports `web.Assets` and uses `fs.Sub(web.Assets, "dist")` to strip the `dist/` prefix.

**Dev mode:** Vite dev server on `:5173` proxies `/api/*` to Go server on `:8080`. No embed involved.

**Production:** Single Go binary serves the built SPA. No Node runtime needed.

---

## Frontend project structure

```
web/
  embed.go                        # //go:embed dist/*
  package.json
  vite.config.ts
  tsconfig.json
  index.html
  openapi.json                    # generated, gitignored
  openapi-ts.config.ts            # hey-api config
  src/
    main.tsx
    app/
      app.tsx                     # router + providers
      shell.tsx                   # icon rail + top bar + main content
      sidebar.tsx                 # icon rail nav, collapsible to expanded
      top-bar.tsx                 # breadcrumb + cmd+K trigger + sync indicator
    features/
      skills/
        skill-list.tsx            # explorer page: stat tiles, search, tag filter, list
        skill-detail.tsx          # tabbed detail: content, files, versions, usage, security
        skill-card.tsx            # list row with badges
        file-tree.tsx             # indented file list
        file-viewer.tsx           # read-only file viewer with line numbers + syntax highlighting (used in Files tab)
        version-list.tsx          # version timeline
        markdown-renderer.tsx     # react-markdown + remark-gfm + shiki
      analytics/
        analytics.tsx             # KPI strip + table page
        kpi-strip.tsx             # four metric cards
        analytics-table.tsx       # sortable table with dead skill highlighting
      security/
        security-badge.tsx        # clean/info/warning/critical dot
        review-status.tsx         # reviewed/unreviewed badge + mark reviewed button
        scan-findings.tsx         # expandable findings list
      settings/
        settings.tsx              # settings page with sub-nav sections
    components/                   # shared UI primitives
      ui/                         # shadcn/ui components (copied in)
        button.tsx
        tabs.tsx
        table.tsx
        checkbox.tsx
        dialog.tsx
        dropdown-menu.tsx
        badge.tsx
        skeleton.tsx
        command.tsx               # command palette (cmdk-based)
      command-palette.tsx         # cmd+K overlay wiring
      search-bar.tsx              # used in explorer + command palette
    lib/
      query.ts                    # TanStack Query client setup
      utils.ts                    # cn() helper, etc.
    api/                          # hey-api generated client (gitignored)
    styles/
      globals.css                 # tailwind v4 directives + design tokens
  dist/                           # build output (gitignored)
```

Feature-based colocation. Components are grouped by domain. `components/ui/` holds shadcn/ui primitives only. Flat within each feature folder.

### Routing

Centralized in `app.tsx`. Five routes:

```tsx
<Routes>
  <Route path="/" element={<SkillList />} />
  <Route path="/skills/:name" element={<SkillDetail />} />
  <Route path="/analytics" element={<Analytics />} />
  <Route path="/settings" element={<Settings />} />
</Routes>
```

File preview is handled within the skill detail Files tab, not as a separate route.

---

## Layout

### Shell structure

```
+--------+---------------------------------------------------+
|        | [org badge] / Skills / code-review    [cmd+K]  [*] |
|   s    +---------------------------------------------------+
|        |                                                    |
|  [sk]  |  main content area                                 |
|  [an]  |                                                    |
|  [--]  |                                                    |
|  [se]  |                                                    |
|        |                                                    |
+--------+---------------------------------------------------+
  56px                    rest of viewport
```

**Icon rail (56px):** Logo at top, nav icons (skills, analytics), settings at bottom. Tooltips on hover. Expandable to ~200px with text labels if needed. Active item gets accent-colored icon + bg-tertiary background.

**Top bar (48px):** Org badge (letter avatar + name), breadcrumb path with `/` separators, flex spacer, Cmd+K search trigger button, sync status indicator (green pulsing dot + "synced Xs ago").

### Responsive behavior

Desktop-first. Not optimized for mobile.

- >= 1280px: Full layout, all content visible
- >= 1024px: Narrower content area, same structure
- >= 768px: Icon rail collapses to hidden, hamburger menu
- < 768px: Functional but not optimized. Single column.

---

## Pages

### Skill explorer (`/`)

```
+--------+---------------------------------------------------+
|        | nathan / Skills                        [cmd+K]  [*]|
|        +---------------------------------------------------+
|        |                                                    |
|        |  Workspace                                         |
|        |  Skills                                    * active|
|        |  Manage and track AI agent skills across your team |
|        |                                                    |
|        |  [invocations 7d] [active devs]  [needs attention] |
|        |                                                    |
|        |  [filter skills... /]  [tag pills]     [sort: v]   |
|        |                                                    |
|        |  [] Select all                   [Mark Reviewed]   |
|        |     Skill          Invocations   Security Updated  |
|        |  ------------------------------------------------ |
|        |  [] * code-review       340      * clean  v5 2d   |
|        |  [] * deployment        128      * clean  v3 5d   |
|        |  [] * api-patterns       47      * warn   v1 1w   |
|        |  [] * old-linter          0      * info   v2 3mo  |
|        |                                                    |
+--------+---------------------------------------------------+
```

**Hero section:** "Workspace" label, "Skills" title with active status dot, description line. Three stat tiles below (invocations 7d, active devs, needs attention count).

**Filter bar:** Text filter input with `/` keyboard hint, horizontal tag filter pills (scrollable), sort dropdown (Updated, Name, Usage).

**Skill list:** Table-like rows with columns:
- Checkbox (appears on hover or when any checked)
- Status dot (active/stale/archived)
- Skill name (mono, 500 weight) + first tag + description (truncated, one line)
- Invocations count (tabular-nums, right-aligned)
- Security badge (colored dot + reviewed/unreviewed indicator)
- Version + relative time (right-aligned, tertiary)

**Bulk actions:** "Mark Reviewed" button appears when checkboxes are selected. "Select all" checkbox at top.

**Hover:** Row gets bg-secondary + 2px accent bar on left edge.

**Keyboard:** `/` focuses filter, arrow keys navigate list, Enter opens skill.

**Empty state:** Onboarding screen with CLI install instructions (`curl -fsSL skael.dev/install | sh` + `skael setup` + `skael publish`) and a link to documentation. Since skill creation is CLI-only, the empty state guides toward CLI setup rather than dashboard-based creation.

### Skill detail (`/skills/:name`)

```
+--------+---------------------------------------------------+
|        | nathan / Skills / code-review          [cmd+K]  [*]|
|        +---------------------------------------------------+
|        |                                                    |
|        |  Skill                                             |
|        |  code-review                          * active     |
|        |  Code review checklist with security and...        |
|        |  v5 | nathan | 340 invocations | 12 devs | 2d ago |
|        |                                                    |
|        |  Content  Files  Versions  Usage  Security  [CLog] |
|        |  ================================================ |
|        |                                                    |
|        |  [active tab content]                              |
|        |                                                    |
+--------+---------------------------------------------------+
```

**Header:** "Skill" label, skill name in mono (32px, 500 weight), status dot with glow, description, meta strip (version, author, invocations, devs, updated), tags.

**Tab bar:** Sticky at top on scroll. Sliding accent underline indicator animates between tabs. Changelog tab shown disabled with "P2" badge.

**Tabs:**

- **Content:** Rendered SKILL.md with GFM support, syntax-highlighted code blocks (Shiki), copy button on code blocks, table of contents sidebar (sticky, "On this page").
- **Files:** File tree on left (240px), read-only file viewer on right with line numbers and light syntax tokenization. File tree shows folder/file icons, file sizes. Active file gets accent left border.
- **Versions:** Timeline with version dots, changelog text per version, +/- line counts, "View diff" link. Latest version gets accent dot with "current" badge.
- **Usage:** Per-skill KPI row (invocations 30d, unique devs, avg/day, last triggered), sparkline area chart (SVG, animated path reveal), time period toggle (7d/30d/90d), top users list with percentage bars.
- **Security:** Scan findings list (expandable per finding: file, line, pattern, explanation), security badge, "Mark Reviewed" / "Unmark" button. Shows reviewed_at timestamp and reviewed_by if reviewed.

### Analytics (`/analytics`)

```
+--------+---------------------------------------------------+
|        | nathan / Analytics                     [cmd+K]  [*]|
|        +---------------------------------------------------+
|        |                                                    |
|        |  [total skills] [active 30d] [activations] [secur] |
|        |                                                    |
|        |  7d  [30d]  90d                                    |
|        |                                                    |
|        |  Skill        Activations  Devs  Last     Security |
|        |  ------------------------------------------------ |
|        |  code-review       340      12   3h ago   * clean  |
|        |  deployment        128       8   1d ago   * clean  |
|        |  api-patterns       47       5   2d ago   * warn   |
|        |  old-linter          0       0   45d ago  * info   |  <- muted row
|        |                                                    |
+--------+---------------------------------------------------+
```

**KPI strip:** Four tiles:
1. Total skills (count)
2. Active in period (skills with > 0 activations)
3. Total activations in period
4. Security summary (X clean, Y warning)

**Time period toggle:** 7d / 30d / 90d pill buttons. Default 30d.

**Table:** Sortable by any column. Columns: skill name (link to detail), activations, unique devs, last triggered (relative time), security status (badge + review indicator). Dead skills (0 activations) get muted row treatment. Monospace for numeric columns.

### Settings (`/settings`)

Sub-nav on left (200px) with scrollspy sections on right (max-width 640px).

**Sections:**
1. **Workspace** — workspace name, server URL, platform version (with "up to date" chip), skills synced count.
2. **API & Keys** — API key display (masked, reveal/copy buttons), last used timestamp.
3. **Sync Targets** — List of detected agents with status dots (synced/paused/error), paths, last sync time. Read-only display of what `skael doctor` would show.
4. **Danger Zone** — Regenerate API key button with red border treatment.

### Command palette (Cmd+K)

Modal overlay. Search input + results list. Contains:
- All skills (searchable by name and description)
- Navigation actions (Go to Skills, Go to Analytics, Go to Settings)
- Keyboard navigation (arrow keys, Enter to select, Escape to close)
- Footer showing "up/down navigate, enter select, N results"

---

## Design system

### Colors (dark theme default)

```css
--bg-primary:      #0a0a0a
--bg-secondary:    #141414
--bg-tertiary:     #1e1e1e
--border:          #262626
--border-active:   #404040

--text-primary:    #ededed
--text-secondary:  #a0a0a0
--text-tertiary:   #666666

--accent:          #22c55e      /* green */
--accent-muted:    #166534
--accent-surface:  #052e16

--danger:          #ef4444
--warning:         #f59e0b
--info:            #3b82f6
```

### Colors (light theme)

```css
--bg-primary:      #ffffff
--bg-secondary:    #f9f9f9
--bg-tertiary:     #f0f0f0
--border:          #e5e5e5
--border-active:   #d4d4d4

--text-primary:    #171717
--text-secondary:  #525252
--text-tertiary:   #a3a3a3

--accent:          #16a34a
--accent-muted:    #15803d
--accent-surface:  #f0fdf4
```

### Typography

- Display/headings: Geist, 500-600 weight, tight tracking (-0.02em to -0.03em)
- Body: Geist, 400 weight, 14px, line-height 1.5
- Code/data: Geist Mono, 400 weight, 13px. Used for skill names, file paths, version numbers, counts.
- Section labels: 10-11px, uppercase, letter-spacing 0.08em, text-tertiary

### Spacing

8px base. Tailwind's default scale applies (p-2 = 8px, p-4 = 16px, etc.)

### Tag colors

```
review:     #a78bfa (purple)
deploy:     #34d399 (green)
security:   #f87171 (red)
testing:    #60a5fa (blue)
api:        #fbbf24 (yellow)
db:         #22d3ee (cyan)
ops:        #fb923c (orange)
frontend:   #f472b6 (pink)
deprecated: #94a3b8 (slate)
```

Tags are rendered as a small colored dot + text label. Minimal footprint.

### Aesthetic principles (from prototype iterations)

- **Restrained.** Massive whitespace, one ambient effect (subtle radial gradient blob), minimal monospace decoration. The prototype went through multiple rounds of stripping back busyness.
- **Dark-first.** Continuation of terminal/IDE environment.
- **Type-driven hierarchy.** Size and weight do the work, not color and borders.
- **Subtle motion.** Fade-in + slide-up on page transitions (0.4s, ease-out). Sliding tab indicator. Count-up animation on stat tiles. Animated SVG path reveal on sparkline charts. Hover state transitions at 0.12-0.15s.

---

## Data model changes

Add to the `skills` table in `001_initial.sql` (not a new migration since we haven't shipped):

```sql
reviewed_at    TIMESTAMPTZ,
reviewed_by    TEXT NOT NULL DEFAULT ''
```

`reviewed_at` is nullable: NULL = unreviewed, non-null = reviewed. Resets to NULL when a new version is published.

---

## Backend additions

### New endpoints

| Method | Path | Description |
|---|---|---|
| `PUT` | `/api/skills/:name/review` | Mark skill as reviewed |
| `DELETE` | `/api/skills/:name/review` | Unmark review |
| `PUT` | `/api/skills/review` | Bulk review `{ names: [...] }` |
| `GET` | `/api/analytics/overview` | KPI strip data: total skills, active count, total activations, security summary |
| `GET` | `/api/analytics/skills` | Table data: per-skill name, activations, devs, last triggered, security status, review status |
| `GET` | `/api/openapi.json` | OpenAPI spec (already served by Huma; add `--openapi` flag for build-time extraction) |

### Modified responses

`GET /api/skills` and `GET /api/skills/:name` responses gain `reviewed_at` and `reviewed_by` fields. No breaking changes.

### OpenAPI spec generation

The server gets a `--openapi` flag that prints the OpenAPI JSON to stdout and exits. This feeds the hey-api build step:

```bash
go run ./cmd/server --openapi > web/openapi.json
cd web && npx @hey-api/openapi-ts
```

---

## Build integration

### Just commands (additions)

```
just generate       # openapi spec + hey-api client
just web-dev        # vite dev server (proxies /api to :8080)
just web-build      # npm run build -> web/dist/
just dev            # go server + vite dev server in parallel
just build          # generate + web-build + go build (embeds dist/)
```

### Docker

Multi-stage build remains the same pattern from the SDD:
1. Node stage: install deps, generate client, build SPA
2. Go stage: copy dist/, build binary with embed
3. Distroless runtime

### Gitignore additions

```
web/dist/
web/src/api/
web/openapi.json
web/node_modules/
```

---

## Performance targets

- Skill list: skeleton in < 200ms, data in < 500ms
- Search: results within 300ms of typing stop (200ms debounce + API round trip)
- Skill detail: SKILL.md render < 100ms
- No layout shift. Skeleton screens match final content dimensions.
- Command palette: open in < 100ms

---

## Key decisions

- **Tailwind v4** over v3: CSS-first config, no JS config file, `@theme` directive for tokens.
- **shadcn/ui** over hand-rolling: provides accessible primitives (tabs, table, dialog, checkbox, command palette) without a library dependency. Components are source code in `components/ui/`.
- **Icon-only rail** over full sidebar: matches prototype evolution, maximizes content space. Expandable to ~200px with labels if user needs it.
- **Tabbed skill detail** over two-column: explicit user choice during prototype iteration. Content gets full width. Each tab is focused.
- **Security as a tab** not a sidebar section: scan findings + review workflow deserve full-width space. "Mark Reviewed" button lives here.
- **Stat tiles on explorer** in addition to analytics page: the prototype puts summary KPIs on the home page. Analytics page goes deeper with sortable table and time period controls.
- **No file editing** in Phase 1: skills are published via CLI. Dashboard is read-only for skill content. Files tab shows read-only viewer with syntax highlighting.
- **No trends/deltas** in Phase 1: analytics shows raw counts. Period-over-period calculations add backend complexity for marginal Phase 1 value.
