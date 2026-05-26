# v0.1.0 Polish & Ship — Design Spec

**Date:** 2026-05-26
**Author:** Nathan + Claude
**Status:** Approved

## Goal

Ship skael v0.1.0 as a publicly distributable CLI + self-hosted platform with a cohesive design, a landing page at skael.dev, and solid E2E test coverage. This is the first release available outside the development environment.

## Distribution (already set up)

- GoReleaser cross-compiles CLI + server for macOS/Linux/Windows (amd64 + arm64)
- Releases publish to `alternayte/skael-releases` (public repo)
- Homebrew formula auto-generated at `alternayte/homebrew-skael`
- Install script at `skael-releases/install.sh` with checksum verification
- Release workflow triggers on `v*` tags

## Sub-projects

v0.1.0 is decomposed into 4 sequential sub-projects. Each gets its own implementation plan.

### 1. DESIGN.md + Dashboard Polish

**Approach:** Run the impeccable skill against the current React SPA to audit the design system, extract tokens, and apply fixes.

**Source of truth:** The current dashboard SPA (`web/src/`), not the older Claude Design prototypes. The prototypes predate the current dashboard and may be outdated.

**Design system deliverable:** `docs/DESIGN.md` containing:
- Color tokens (dark theme: backgrounds, borders, text tiers, accent amber, status colors)
- Typography (Geist sans + Geist Mono, size scale)
- Spacing (base grid, component padding patterns)
- Border radius scale
- Component inventory (buttons, badges, terminal blocks, stat tiles, etc.)
- Annotated patterns (grid backgrounds, border-based separation, kbd hints)

**Dashboard fixes to address** (from Claude Design chat2 feedback):
- Type hierarchy: too much `--text-secondary` / `--text-tertiary`, promote key numbers
- Redundant action cards in skill detail (tabs already handle navigation)
- Sparkline area opacity adjustment
- `:focus-visible` parity with hover affordances
- Empty state polish

### 2. Landing Page

**Location:** `site/` directory in the main repo. Astro static site.

**Design source:** `landing.html` from the Claude Design prototype, adapted:
- Rebrand all "skillctl" references to "skael"
- Align copy to Phase 1 capabilities (remove references to bundles, SDK resolvers, promote staging->prod, SOC2 audit logs)
- Terminal demo uses real `skael` commands (`skael publish`, `skael sync`, `skael search`)
- "How it works" steps match actual flow: install CLI -> `skael setup` -> `skael publish` -> `skael sync`
- Stats strip: replace vanity metrics with capability highlights or remove
- Dashboard preview: static HTML mockup matching the actual current dashboard (same approach as the prototype's `preview-frame` section), or a real screenshot captured after sub-project 1 completes
- Update all URLs, install commands (`brew install alternayte/skael/skael`), CTAs

**Visual design:** Same dark/Geist/amber design language as the dashboard. Must use the tokens defined in DESIGN.md (sub-project 1).

**Pages for v0.1.0:** Landing page only. Docs, pricing, changelog, blog pages are future work.

**Hosting:** TBD (likely Vercel, Cloudflare Pages, or GitHub Pages). Not in scope for the spec — just needs to build to static output.

### 3. E2E Test Suite

#### Go E2E: CLI lifecycle test

A single integration test exercising the full developer flow:
1. Start test server (reuse existing `startTestServer` helper)
2. Run `setup` equivalent (configure client against test server)
3. `publish` a skill from testdata
4. `sync` skills down to a temp directory
5. Verify files land in the correct agent directory structure
6. Verify activation tracking fires during the flow

This extends the existing 5 Go E2E scenarios in `tests/e2e/e2e_test.go`.

#### Playwright: dashboard flow tests

Cover key paths not yet tested:
- **Skill list:** loads, displays skills, search filters work
- **Skill detail:** click through from list, tabs render (Overview, Files, Versions, Usage)
- **Security badge:** renders correctly for clean vs flagged skills
- **Publish flow:** upload archive, see skill appear in list

Run against Docker Compose (same as existing `auth.spec.ts`).

### 4. README + Release

**README additions:**
- Quickstart section: `cp .env.example .env` -> `docker compose up -d` -> open `http://localhost:8080`
- Install the CLI section: Homebrew (`brew install alternayte/skael/skael`), curl script, `go install`
- Brief description aligned with landing page copy

**Release:** Tag `v0.1.0`, triggering the release workflow.

## Sequencing

```
1. DESIGN.md + dashboard polish (impeccable)
   |
   v
2. Landing page (Astro, site/ directory)
   |
   v
3. E2E tests (Go + Playwright)
   |
   v
4. README + tag v0.1.0
```

Sub-projects 1 and 2 are sequential (landing page needs design tokens). Sub-projects 3 and 4 can potentially be parallelized since tests don't depend on the landing page.

## Out of scope for v0.1.0

- Docs site (separate pages beyond landing)
- Pricing page
- Light mode / theme toggle
- Blog / changelog page
- Custom domain setup for skael.dev
- Production Docker Compose (Caddy/Traefik reverse proxy, resource limits)
- Open-core feature gating (enterprise vs community)
- `go install` path (requires public repo)
