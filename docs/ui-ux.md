# Skael — UI/UX Design Document

**Version:** 0.2 · **Date:** May 2026 · **Status:** Draft

---

## Design direction

### Reference products

The dashboard draws from developer tools known for density without clutter:

- **Linear** — Sidebar navigation, keyboard-first, monochrome with a single accent colour, tight spacing, no visual waste. The gold standard for developer dashboards.
- **Dub.sh** — Clean analytics tables, subtle trend indicators, effective use of monospace type for data. Proves analytics dashboards don't need Grafana-level complexity to be useful.
- **Neon** — Dark-first developer console. Good use of code-like typography for technical content. Skill detail pages should feel this technical.
- **Raycast** — Command palette as primary navigation pattern. Search-first UX. Skael's skill search should feel this fast.
- **Coolify** — Proof that self-hosted tools can have polished UIs. The bar to clear, not the ceiling to hit.
- **Stripe Dashboard** — KPI strip at the top of analytics views. Four numbers, trend arrows, sparklines. Nothing else in the hero area.

### Aesthetic

**Industrial minimalism.** Not the glossy AI-product aesthetic (purple gradients, glassmorphism, floating orbs). Not brutalist either. Think: a well-organised workshop. Everything has a place, nothing is decorative without function. The UI should feel like a tool you reach for, not a product you admire.

Characteristics:
- **Dark-first** with a considered light mode. Dark is default because developers spend hours in terminals and dark IDEs. The dashboard should feel like a continuation of that environment, not a jarring context switch.
- **Monochrome base** with a single accent colour. The accent colour is used sparingly — active states, primary actions, and critical data points. Everything else is greyscale. This prevents the "rainbow dashboard" problem where colour loses meaning.
- **Type-driven hierarchy.** Size, weight, and case do the work that colour and borders typically do. Fewer visual elements means faster scanning.
- **Dense but breathable.** Power users want data density. But density without rhythm is just noise. Consistent spacing scale, clear section boundaries via whitespace (not borders/dividers), and aligned grids create density that reads cleanly.

### What to avoid

- Purple/blue gradients as backgrounds or accents (screams "AI product")
- Glassmorphism, frosted glass effects, or blur layers
- Rounded card borders with drop shadows as the primary layout primitive
- Illustrative empty states with cartoon characters or abstract shapes
- Excessive use of icons where text labels work fine
- "Welcome back, Nathan!" greeting headers consuming prime content space
- Loading spinners (use skeleton screens matching content layout)
- Any design choice that would look equally at home on a crypto dashboard, a note-taking app, or a social media feed

## Design system

### Colour

```
// Dark theme (default)
--bg-primary:      #0a0a0a    // page background, nearly black
--bg-secondary:    #141414    // card/panel backgrounds
--bg-tertiary:     #1e1e1e    // hover states, elevated surfaces
--border:          #262626    // subtle borders, dividers
--border-active:   #404040    // focused inputs, active elements

--text-primary:    #ededed    // headings, primary content
--text-secondary:  #a0a0a0    // descriptions, secondary labels
--text-tertiary:   #666666    // timestamps, metadata, disabled

--accent:          #22c55e    // green — primary actions, active states, live data
--accent-muted:    #166534    // hover/pressed states on accent elements
--accent-surface:  #052e16    // accent backgrounds (very subtle)

--danger:          #ef4444    // destructive actions, errors
--warning:         #f59e0b    // deprecation warnings, caution states
--info:            #3b82f6    // informational callouts (rare)

// Light theme
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

The green accent is deliberate — it reads as "operational", "live", "active". It avoids the blue-purple spectrum that every AI product defaults to. Green also works naturally for positive trend indicators and activation data.

### Typography

```
// Display / headings
font-family: 'Geist', system-ui, sans-serif
-- Page titles: 600 weight, 20px, tracking -0.02em
-- Section heads: 500 weight, 14px, uppercase, tracking 0.05em, --text-tertiary
-- Card titles: 500 weight, 14px

// Body / content  
font-family: 'Geist', system-ui, sans-serif
-- Default: 400 weight, 14px, line-height 1.5
-- Small: 400 weight, 12px (metadata, timestamps)

// Code / data
font-family: 'Geist Mono', 'SF Mono', monospace
-- Used for: skill names, file paths, version numbers, code blocks, search queries, activation counts
-- Default: 400 weight, 13px

// Rendered SKILL.md content
font-family: 'Geist', system-ui, sans-serif
-- Follows standard markdown rendering with Geist Mono for code blocks
```

Geist is Vercel's open-source font family — excellent mono and sans variants, reads well at small sizes, and isn't yet overused in the way Inter is. Fallback: IBM Plex (sans + mono) for a similar industrial quality.

### Spacing

8px base unit. All spacing is a multiple of 8:

```
--space-1:   4px    // tight internal padding
--space-2:   8px    // default gap, input padding
--space-3:   12px   // between related items in a list
--space-4:   16px   // between sections within a card
--space-5:   24px   // between cards/panels
--space-6:   32px   // page-level section gaps
--space-7:   48px   // major section breaks
```

### Components

**Sidebar navigation** — 240px fixed width, collapsible to 48px (icons only). Sections: Skills (explorer), Analytics (Phase 2), Bundles (Phase 3), Settings. Active item indicated by accent-coloured left border (2px) and `--bg-tertiary` background. 36px row height.

**Search bar** — Full-width input at the top of the skill explorer. `Cmd+K` / `Ctrl+K` shortcut to focus. Monospace placeholder text: `Search skills...`. Results appear inline below (not a modal/overlay), replacing the skill list. Debounced at 200ms.

**Skill card (list item)** — Used in the skill explorer list. Horizontal layout:
```
┌──────────────────────────────────────────────────┐
│  code-review                        v5 · 2d ago  │
│  Code review checklist with security and...      │
│  ■■■ 47 activations (30d) · 8 devs   [●] clean  │
└──────────────────────────────────────────────────┘
```
- Skill name in monospace, 500 weight
- Description truncated to one line, `--text-secondary`
- Version + relative timestamp, right-aligned, `--text-tertiary`
- Activation mini-bar + count + dev count, small text
- Security badge: coloured dot (green/grey/yellow/red) with status text

**Skill detail page** — Two-column layout:

```
┌─────────────────────────────────────────────────────────────┐
│  ← Skills    code-review                             v5    │
├─────────────────────────────────────────┬───────────────────┤
│                                         │                   │
│  [Rendered SKILL.md content]            │  ACTIVATIONS      │
│                                         │  47 (30d)         │
│  Full markdown rendering with           │  8 unique devs    │
│  syntax highlighted code blocks,        │  Last: 3h ago     │
│  tables, and headings.                  │  Agents:          │
│                                         │   claude-code: 31 │
│                                         │   codex: 16       │
│                                         │                   │
│                                         │  FILES            │
│                                         │  ├── SKILL.md     │
│                                         │  ├── scripts/     │
│                                         │  │   └── lint.sh  │
│                                         │  └── references/  │
│                                         │      └── guide.md │
│                                         │                   │
│                                         │  VERSIONS         │
│                                         │  v5 · 2d ago      │
│                                         │  v4 · 1w ago      │
│                                         │  v3 · 3w ago      │
│                                         │                   │
│                                         │  SECURITY         │
│                                         │  ● Clean          │
│                                         │                   │
├─────────────────────────────────────────┴───────────────────┤
│  Scan findings (if any, expandable)                         │
└─────────────────────────────────────────────────────────────┘
```

- Main content area: rendered SKILL.md. Syntax highlighting via Shiki (`vitesse-dark` / `vitesse-light`).
- Sidebar panel: activation summary (count, devs, per-agent breakdown), file tree (clickable), version history, security badge.
- Back navigation: `← Skills` text link.
- Activation summary shows per-agent breakdown — this is the unique cross-agent insight.

**File tree** — Indented list with folder/file icons (minimal, monochrome). Clicking a file replaces the main content area with that file's rendered content (markdown rendered, other text shown raw with syntax highlighting). Breadcrumb above: `code-review / references / guide.md`.

**Analytics table (Phase 2, cloud)** — Primary analytics view is a table, not a chart:

```
┌──────────────────┬────────────┬───────┬──────────────┬───────┐
│  Skill           │ Activations│ Devs  │ Last trigger │ Trend │
├──────────────────┼────────────┼───────┼──────────────┼───────┤
│  code-review     │        340 │    12 │ 3h ago       │  ↑ 8% │
│  deployment      │        128 │     8 │ 1d ago       │  ↑ 3% │
│  api-patterns    │         47 │     5 │ 2d ago       │    —  │
│  old-linter      │          0 │     0 │ 45d ago      │  ● ▼  │  ← highlighted
│  legacy-review   │          0 │     0 │ 62d ago      │  ● ▼  │  ← highlighted
└──────────────────┴────────────┴───────┴──────────────┴───────┘
```

- Sortable by any column. Default: activations descending.
- Dead skills (0 activations in 30d) get a muted row with `--warning` accent dot.
- Time period selector: 7d / 30d / 90d, pill-style toggle.
- Monospace for all numeric data.

**KPI strip (Phase 2, cloud)** — Above the analytics table:

```
┌─────────────┐ ┌─────────────┐ ┌─────────────┐ ┌─────────────┐
│ Total skills│ │ Active (30d)│ │ Activations │ │ Dead skills │
│          47 │ │          39 │ │       2,814 │ │        8 ▲  │
│             │ │             │ │     ↑ 12%   │ │             │
└─────────────┘ └─────────────┘ └─────────────┘ └─────────────┘
```

### Empty states

No illustrations. Text-only with a single action:

```
┌──────────────────────────────────────────────┐
│                                              │
│     No skills published yet.                 │
│     skael publish ./my-skill                 │
│                                              │
│     [Read the docs →]                        │
│                                              │
└──────────────────────────────────────────────┘
```

### Loading states

Skeleton screens matching the content layout. Pulsing shimmer animation on `--bg-tertiary` blocks. No spinners. No "Loading..." text.

## Page inventory

### Phase 1

| Page | Route | Description |
|---|---|---|
| Skill explorer | `/` | Search bar + skill list with activation counts and security badges |
| Skill detail | `/skills/:name` | Rendered SKILL.md + sidebar (activations, files, versions, security) |
| File preview | `/skills/:name/files/*path` | Rendered file from skill archive |
| Settings | `/settings` | Platform URL, API key display, version info |

### Phase 2 (cloud)

| Page | Route | Description |
|---|---|---|
| Analytics | `/analytics` | KPI strip + sortable analytics table with trends |
| Skill detail (extended) | `/skills/:name` | Adds trend indicator + changelog |

### Phase 3

| Page | Route | Description |
|---|---|---|
| Bundles | `/bundles` | List + manage skill bundles |
| Bundle detail | `/bundles/:name` | Skills in bundle, assignment |
| Team | `/team` | User management, API keys |
| Audit log | `/audit` | Publish history |

## Interaction patterns

### Keyboard navigation

- `Cmd+K` / `Ctrl+K`: Focus search bar from anywhere
- `↑` / `↓`: Navigate skill list
- `Enter`: Open selected skill
- `Escape`: Clear search, close overlays, go back
- `?`: Show keyboard shortcuts overlay

### Responsive behaviour

Desktop-first. Mobile is a secondary concern.

- `≥1280px`: Two-column skill detail (content + full sidebar)
- `≥1024px`: Two-column with narrower sidebar
- `≥768px`: Sidebar collapses to icons, content goes full-width
- `<768px`: Sidebar becomes bottom bar, single-column layout. Functional but not optimised.

### Markdown rendering

SKILL.md content is rendered with:
- GitHub-flavoured markdown (tables, task lists, strikethrough)
- Syntax highlighting via Shiki with `vitesse-dark`/`vitesse-light` themes
- Anchored headings for deep linking
- Copy button on code blocks
- Tables rendered with horizontal scroll on overflow

### URL structure

```
/                          → skill explorer
/skills/code-review        → skill detail
/skills/code-review/files/references/guide.md → file preview
/analytics                 → analytics dashboard (Phase 2, cloud)
/settings                  → platform settings
```

## Implementation notes

### React SPA tech choices

- **Vite** for build tooling
- **React Router** for client-side routing
- **TanStack Query** for API data fetching and caching
- **Tailwind CSS** for styling
- **Shiki** for syntax highlighting
- **react-markdown** + **remark-gfm** for SKILL.md rendering
- **hey-api** generated client for all API calls

No component library. The surface is small enough that a library adds more weight than value. Use shadcn/ui primitives if specific components are needed (dialog, dropdown), but don't install the full library.

### Performance targets

- Skill list page: first paint < 200ms (skeleton), data < 500ms
- Search: results appear < 300ms after typing stops
- Skill detail: SKILL.md rendering < 100ms
- No layout shift. Skeleton screens match final content dimensions.
