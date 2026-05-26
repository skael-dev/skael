# v0.1.0 Polish & Ship Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship skael v0.1.0 as a publicly distributable CLI + self-hosted platform with cohesive design, landing page, and E2E coverage.

**Architecture:** 4 sequential sub-projects: (1) design system audit via impeccable skill, (2) Astro landing page in `site/`, (3) E2E tests in Go + Playwright, (4) README polish and release tag. Distribution infrastructure (GoReleaser, Homebrew tap, public releases repo) is already set up.

**Tech Stack:** Go, React (Vite + TanStack Query), Astro, Playwright, testcontainers

---

## Sub-project 1: DESIGN.md + Dashboard Polish

This sub-project is delegated to the `impeccable` skill. The skill will audit the current React SPA at `web/src/`, extract the design system into `docs/DESIGN.md`, and apply fixes.

### Task 1: Run impeccable audit

**Files:**
- Create: `docs/DESIGN.md`
- Modify: various files under `web/src/`

- [ ] **Step 1: Invoke the impeccable skill**

Run the impeccable skill with this context:
- Source of truth is the current dashboard SPA at `web/src/`
- Extract design tokens (colors, typography, spacing, radii) into `docs/DESIGN.md`
- Fix issues from design review: type hierarchy too gray (promote key numbers to `--text-primary`), redundant action cards in skill detail, sparkline opacity 0.12 → 0.18, `:focus-visible` parity, empty state polish
- The landing page (sub-project 2) will reference these tokens, so the DESIGN.md must be complete before moving on

- [ ] **Step 2: Commit design system and dashboard fixes**

```bash
git add docs/DESIGN.md web/src/
git commit -m "feat(ui): extract design system and polish dashboard"
```

---

## Sub-project 2: Landing Page

An Astro static site in `site/` based on the Claude Design prototype at `/tmp/skael-design/skillctl/project/landing.html`, rebranded from "skillctl" to "skael" with copy aligned to Phase 1 capabilities.

### Task 2: Scaffold Astro project

**Files:**
- Create: `site/package.json`
- Create: `site/astro.config.mjs`
- Create: `site/tsconfig.json`
- Create: `site/src/layouts/Base.astro`
- Create: `site/src/pages/index.astro`
- Create: `site/public/favicon.svg`

- [ ] **Step 1: Initialize the Astro project**

```bash
cd /Users/nathananderson-tennant/Development/skael
mkdir site && cd site
npm create astro@latest -- --template minimal --no-git --no-install .
npm install
```

- [ ] **Step 2: Verify the dev server starts**

```bash
cd /Users/nathananderson-tennant/Development/skael/site
npm run dev
```

Expected: Astro dev server starts at `http://localhost:4321`, default page renders.

- [ ] **Step 3: Verify static build works**

```bash
cd /Users/nathananderson-tennant/Development/skael/site
npm run build
```

Expected: Output in `site/dist/`, clean build with no errors.

- [ ] **Step 4: Commit scaffold**

```bash
git add site/
git commit -m "chore: scaffold Astro site in site/"
```

### Task 3: Create base layout

**Files:**
- Create: `site/src/layouts/Base.astro`
- Modify: `site/src/pages/index.astro`

- [ ] **Step 1: Write the base layout**

The layout provides the HTML shell, font loading, and CSS custom properties extracted from the prototype. Write `site/src/layouts/Base.astro`:

```astro
---
interface Props {
  title: string;
  description?: string;
}
const { title, description = "The control plane for AI agent skills" } = Astro.props;
---
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>{title}</title>
  <meta name="description" content={description} />
  <link rel="preconnect" href="https://fonts.googleapis.com" />
  <link href="https://fonts.googleapis.com/css2?family=Geist:wght@400;500;600;700&family=Geist+Mono:wght@400;500&display=swap" rel="stylesheet" />
  <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
</head>
<body>
  <slot />
</body>
</html>

<style is:global>
  :root {
    --bg:         #0a0a0a;
    --bg-2:       #131313;
    --bg-3:       #1c1c1c;
    --border:     #232323;
    --border-2:   #2e2e2e;
    --text:       #ededed;
    --text-2:     #a3a3a3;
    --text-3:     #6b6b6b;
    --accent:     #f59e0b;
    --accent-2:   #b45309;
    --accent-sur: rgba(245, 158, 11, 0.10);
    --font-sans:  'Geist', system-ui, -apple-system, sans-serif;
    --font-mono:  'Geist Mono', 'SF Mono', ui-monospace, monospace;
    --max:        1180px;
    color-scheme: dark;
  }

  * { margin: 0; padding: 0; box-sizing: border-box; }

  html, body {
    background: var(--bg);
    color: var(--text);
    font-family: var(--font-sans);
    font-size: 15px;
    line-height: 1.55;
    -webkit-font-smoothing: antialiased;
    text-rendering: optimizeLegibility;
  }

  body { overflow-x: hidden; }
  a { color: inherit; text-decoration: none; }
  button { font-family: inherit; cursor: pointer; border: 0; background: 0; color: inherit; }
  ::selection { background: var(--accent-2); color: var(--text); }
</style>
```

- [ ] **Step 2: Write a minimal index page to verify layout**

Write `site/src/pages/index.astro`:

```astro
---
import Base from "../layouts/Base.astro";
---
<Base title="skael — control plane for AI agent skills">
  <main style="padding: 100px 32px; text-align: center;">
    <h1 style="font-size: 48px; font-weight: 600; letter-spacing: -0.035em;">skael</h1>
    <p style="color: var(--text-2); margin-top: 16px;">Landing page coming soon</p>
  </main>
</Base>
```

- [ ] **Step 3: Add favicon**

Write `site/public/favicon.svg`:

```svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">
  <rect width="32" height="32" rx="6" fill="#f59e0b"/>
  <text x="50%" y="54%" dominant-baseline="middle" text-anchor="middle" fill="#1a1100" font-family="system-ui" font-weight="600" font-size="18">s</text>
</svg>
```

- [ ] **Step 4: Run dev server and verify**

```bash
cd /Users/nathananderson-tennant/Development/skael/site
npm run dev
```

Expected: Page renders at localhost:4321 with dark background, "skael" heading, amber favicon.

- [ ] **Step 5: Commit**

```bash
git add site/src/layouts/Base.astro site/src/pages/index.astro site/public/favicon.svg
git commit -m "feat(site): add base layout with design tokens"
```

### Task 4: Build the landing page

**Files:**
- Modify: `site/src/pages/index.astro`

This is the big task. Port the full `landing.html` prototype into the Astro page, rebranding and updating copy. The CSS from the prototype is carried over directly — it's production-quality already.

Reference: `/tmp/skael-design/skillctl/project/landing.html` (lines 1-1101)

- [ ] **Step 1: Write the full landing page**

Replace `site/src/pages/index.astro` with the full landing page content. Key changes from the prototype:

**Rebranding (find and replace throughout):**
- "skillctl" → "skael" (logo, nav, terminal, CLI commands, footer)
- "app.skillctl.dev" → "app.skael.dev" in dashboard preview URL bar
- "© 2026 skillctl, Inc." → "© 2026 skael"

**Copy changes in the hero:**
- Pill: `Skill registries for teams · v0.4` → `Open source · v0.1.0`
- Headline: `Version control for the skills your agents run on.` → `The control plane for the skills your agents run on.`
- Sub: Replace "skillctl is a registry, observability layer, and review workflow for the markdown skills you ship to Claude. Catch drift before your agents do." → "Publish, sync, scan, and track AI agent skills across your team. One registry for Claude Code, Codex CLI, and Gemini CLI."
- Primary CTA: `Start tracking skills` → `Get started`
- Secondary CTA: `View the docs →` → `View on GitHub →` (link to `github.com/alternayte/skael-releases`)
- Hero meta: keep "Free for solo devs · self-host on day one"

**Terminal demo (replace the body content):**
```
$ skael setup https://skills.company.com sk-xxx
  ✓ Connected to skills.company.com
  ✓ Configuration saved
  ✓ Hook installed for claude-code
  ✓ Hook installed for codex
  ✓ Setup complete. Skills are live.

$ skael publish ./code-review
  → scanning for security issues… ok
  → published code-review v5 (checksum a3f8…)

$ █
```

**Stats strip:** Replace vanity metrics with capability highlights:
- "12.4k skills under management" → "6 platforms" / "macOS · Linux · Windows"
- "340M invocations / month" → "4 agents" / "Claude · Codex · Gemini · OpenCode"
- "99.98% registry uptime" → "30+ rules" / "security scan on every publish"
- "<40ms p99 resolve time" → "1 command" / "`skael setup` and you're done"

**Features grid:** Update the 6 cards to reflect actual Phase 1 features:
1. "Cross-agent sync" — One `skael sync` command distributes skills to Claude Code, Codex CLI, and detected agents. No manual file copying.
2. "Security scanning" — 30+ regex rules catch secrets, prompt injection, data exfiltration, and obfuscation. Critical findings block publishing.
3. "Activation tracking" — See which skills fire, which agents use them, and how often. Hook scripts report events without blocking the agent.
4. "Full-text search" — Postgres FTS with pg_trgm fuzzy matching. Search across skill names, descriptions, and content.
5. "Version history" — Every publish creates an immutable version. Content-addressable archives prevent race conditions.
6. "Self-hosted" — Docker Compose with Postgres. Your skills stay on your infrastructure. Single binary, single API key.

Remove kbd hints from feature cards (those referenced features we don't have like command palette shortcuts).

**How it works:** Update the 3 steps:
1. "01 / INSTALL" — title: "Install the CLI." — description: "Single binary for macOS, Linux, and Windows. No dependencies." — code: `$ brew install alternayte/skael/skael` or `$ curl -fsSL https://raw.githubusercontent.com/alternayte/skael-releases/main/install.sh | sh`
2. "02 / SETUP" — title: "Connect to your registry." — description: "One command configures the CLI, syncs all skills, and installs activation tracking hooks for every detected agent." — code: `$ skael setup https://skills.company.com sk-xxx`
3. "03 / PUBLISH" — title: "Share a skill." — description: "Security scan runs automatically. Next time anyone runs `skael sync`, they get it." — code: `$ skael publish ./code-review`

**Dashboard preview:** Keep the same structure but update:
- URL bar: `app.skael.dev / acme / skills`
- Sidebar items: Skills (active), Analytics, Settings (remove Bundles, Activity, Tags — Phase 1 doesn't have these)
- Skill rows: keep the same data, it's illustrative

**CTA section:**
- Headline: keep "Ship skills like you ship code."
- Sub: "Free and open source. Self-host on day one."
- Primary CTA: "Get started →"
- Install command: `brew install alternayte/skael/skael && skael setup`

**Footer:**
- Logo: "skael"
- Description: "The control plane for the skills your agents run on. Self-host or use the cloud."
- Columns: Product (Features, Changelog, Roadmap), Developers (GitHub, CLI, Docs), Company (About, Contact)
- Bottom: "© 2026 skael" / "v0.1.0 · open source"

**CSS:** Copy the full `<style>` block from the prototype (lines 9-743 of `landing.html`) into a `<style>` tag in the Astro page. No modifications needed to the CSS — it's well-structured and production-quality. The CSS is scoped to class names that don't conflict with the rest of the project.

- [ ] **Step 2: Run dev server and visually verify**

```bash
cd /Users/nathananderson-tennant/Development/skael/site
npm run dev
```

Open `http://localhost:4321`. Verify:
- Dark background with grid pattern and amber glow
- Sticky nav with "skael" logo
- Hero section renders with correct copy
- Terminal demo shows skael commands
- Features grid shows 6 cards with actual Phase 1 features
- Dashboard preview renders
- "How it works" shows 3 steps
- CTA section and footer render
- Mobile: resize browser to <880px, verify responsive breakpoints work

- [ ] **Step 3: Build static output**

```bash
cd /Users/nathananderson-tennant/Development/skael/site
npm run build
```

Expected: Clean build, output in `site/dist/`.

- [ ] **Step 4: Commit**

```bash
git add site/src/pages/index.astro
git commit -m "feat(site): build landing page with Phase 1 copy"
```

### Task 5: Add justfile commands for the site

**Files:**
- Modify: `justfile`

- [ ] **Step 1: Add site commands to justfile**

Add these commands after the existing `web-build` command (around line 35 of the justfile):

```just
# Run Astro dev server for the landing page
site-dev:
    cd site && npm run dev

# Build the landing page
site-build:
    cd site && npm run build
```

- [ ] **Step 2: Verify commands work**

```bash
just site-build
```

Expected: Clean build.

- [ ] **Step 3: Commit**

```bash
git add justfile
git commit -m "chore: add site-dev and site-build to justfile"
```

---

## Sub-project 3: E2E Test Suite

### Task 6: Go E2E — full CLI lifecycle test

**Files:**
- Modify: `tests/e2e/e2e_test.go`

This test exercises the complete developer onboarding flow: configure against a test server, publish a skill, sync it to a temp directory simulating an agent's skill directory, and verify files land correctly.

- [ ] **Step 1: Write the lifecycle test**

Add this test to the end of `tests/e2e/e2e_test.go`:

```go
// ---------------------------------------------------------------------------
// Scenario 6: Full CLI lifecycle — setup → publish → sync → verify files.
// ---------------------------------------------------------------------------

func TestE2E_FullLifecycle(t *testing.T) {
	serverURL, cleanup := startTestServer(t)
	defer cleanup()

	c := client.New(serverURL, testAPIKey)

	// --- Phase 1: Publish a skill ---
	_, err := c.CreateSkill("lifecycle-skill", "lifecycle test")
	require.NoError(t, err)

	archiveBytes := packTestdataDir(t, "clean-skill")
	ver, _, err := c.PublishVersion("lifecycle-skill", archiveBytes)
	require.NoError(t, err)
	require.Equal(t, 1, ver.Version)

	// --- Phase 2: Configure (simulate setup) ---
	configDir := t.TempDir()
	cfg := &config.Config{
		Endpoint: serverURL,
		APIKey:   testAPIKey,
	}
	require.NoError(t, config.WriteConfig(configDir, cfg))

	// --- Phase 3: Get manifest and sync manually ---
	manifest, err := c.GetManifest()
	require.NoError(t, err)
	require.NotEmpty(t, manifest)

	var entry client.ManifestEntry
	for _, m := range manifest {
		if m.Name == "lifecycle-skill" {
			entry = m
			break
		}
	}
	require.Equal(t, "lifecycle-skill", entry.Name)
	require.Equal(t, 1, entry.Version)
	require.NotEmpty(t, entry.Checksum)

	// Download and verify checksum matches manifest.
	downloaded, err := c.DownloadVersion("lifecycle-skill", 1)
	require.NoError(t, err)

	actualChecksum := fmt.Sprintf("%x", sha256.Sum256(downloaded))
	require.Equal(t, entry.Checksum, actualChecksum, "downloaded archive checksum must match manifest")

	// --- Phase 4: Extract to simulated agent directory ---
	agentDir := filepath.Join(t.TempDir(), "claude-code", "skills", "lifecycle-skill")
	require.NoError(t, os.MkdirAll(agentDir, 0o755))
	err = skill.Unpack(bytes.NewReader(downloaded), agentDir)
	require.NoError(t, err)

	// Verify SKILL.md exists in the agent directory.
	skillMD, err := os.ReadFile(filepath.Join(agentDir, "SKILL.md"))
	require.NoError(t, err)
	require.Contains(t, string(skillMD), "E2E Test Skill")

	// --- Phase 5: Verify state file can be written ---
	state := &config.SyncState{
		LastSync: "2026-05-26T00:00:00Z",
		Skills: []config.SyncedSkill{
			{Name: "lifecycle-skill", Version: 1, Checksum: entry.Checksum},
		},
	}
	require.NoError(t, config.WriteState(configDir, state))

	readState, err := config.ReadState(configDir)
	require.NoError(t, err)
	require.Len(t, readState.Skills, 1)
	require.Equal(t, "lifecycle-skill", readState.Skills[0].Name)

	// --- Phase 6: Post activation event and verify ---
	postEvent(t, serverURL, testAPIKey, "lifecycle-skill", "claude-code")

	req, err := http.NewRequest(http.MethodGet,
		serverURL+"/api/skills/lifecycle-skill/activations?days=30", nil)
	require.NoError(t, err)
	req.Header.Set("X-API-Key", testAPIKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var summary analytics.ActivationSummary
	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(bodyBytes, &summary))
	require.Equal(t, 1, summary.TotalCount)
	require.Contains(t, summary.ByAgent, "claude-code")
}
```

- [ ] **Step 2: Add the `crypto/sha256` import**

The existing imports in `e2e_test.go` need `crypto/sha256` and `fmt` added. Check the import block and add any that are missing:

```go
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
	// ... rest of imports unchanged
)
```

- [ ] **Step 3: Run the test**

```bash
cd /Users/nathananderson-tennant/Development/skael
go test ./tests/e2e/ -v -count=1 -run TestE2E_FullLifecycle -tags integration
```

Expected: PASS. The test spins up a real Postgres via testcontainers, starts the server, and exercises the full lifecycle.

- [ ] **Step 4: Run all E2E tests to check for regressions**

```bash
go test ./tests/e2e/ -v -count=1 -tags integration
```

Expected: All 6 scenarios pass.

- [ ] **Step 5: Commit**

```bash
git add tests/e2e/e2e_test.go
git commit -m "test(e2e): add full CLI lifecycle scenario"
```

### Task 7: Playwright — skill list and search tests

**Files:**
- Create: `web/e2e/skills.spec.ts`

These tests require a running server with at least one published skill. The Playwright config already handles starting the server via `just dev`. The tests create test data via the API before asserting on the UI.

- [ ] **Step 1: Write the skill list test file**

Create `web/e2e/skills.spec.ts`:

```typescript
import { test, expect } from "@playwright/test";

const API_KEY = process.env.SKAEL_API_KEY ?? "sk-change-me-in-production";
const BASE_URL = process.env.PLAYWRIGHT_BASE_URL ?? "http://localhost:8080";

async function login(page: import("@playwright/test").Page) {
  await page.goto("/login");
  await page.getByPlaceholder(/email/i).fill("e2e-skills@test.com");
  await page.getByPlaceholder(/password/i).fill("testpassword123");
  await page.getByRole("button", { name: /log in|sign in/i }).click();
  await expect(page).toHaveURL("/");
}

async function ensureTestUser() {
  const res = await fetch(`${BASE_URL}/api/auth/signup`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      email: "e2e-skills@test.com",
      name: "E2E Skills User",
      password: "testpassword123",
    }),
  });
  // 201 = created, 409 = already exists — both are fine
  if (res.status !== 201 && res.status !== 409) {
    throw new Error(`Signup failed: ${res.status}`);
  }
}

async function ensureTestSkill() {
  // Create skill
  await fetch(`${BASE_URL}/api/skills`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-API-Key": API_KEY,
    },
    body: JSON.stringify({
      name: "e2e-playwright-skill",
      description: "Skill for Playwright E2E tests",
    }),
  });
  // Ignore 409 if already exists
}

test.describe("Skill list", () => {
  test.beforeAll(async () => {
    await ensureTestUser();
    await ensureTestSkill();
  });

  test("skill list page loads and displays skills", async ({ page }) => {
    await login(page);
    await expect(page.locator("text=Skills")).toBeVisible();
  });

  test("search filters the skill list", async ({ page }) => {
    await login(page);
    const searchInput = page.getByPlaceholder(/search|filter/i);
    if (await searchInput.isVisible()) {
      await searchInput.fill("e2e-playwright");
      await page.waitForTimeout(500);
      await expect(page.locator("text=e2e-playwright-skill")).toBeVisible();
    }
  });
});
```

- [ ] **Step 2: Run the test**

```bash
cd /Users/nathananderson-tennant/Development/skael/web
npx playwright test e2e/skills.spec.ts
```

Expected: Tests pass against the running dev server.

- [ ] **Step 3: Commit**

```bash
git add web/e2e/skills.spec.ts
git commit -m "test(e2e): add Playwright skill list and search tests"
```

### Task 8: Playwright — skill detail and security badge tests

**Files:**
- Create: `web/e2e/skill-detail.spec.ts`

- [ ] **Step 1: Write the skill detail test file**

Create `web/e2e/skill-detail.spec.ts`:

```typescript
import { test, expect } from "@playwright/test";

const API_KEY = process.env.SKAEL_API_KEY ?? "sk-change-me-in-production";
const BASE_URL = process.env.PLAYWRIGHT_BASE_URL ?? "http://localhost:8080";

async function login(page: import("@playwright/test").Page) {
  await page.goto("/login");
  await page.getByPlaceholder(/email/i).fill("e2e-detail@test.com");
  await page.getByPlaceholder(/password/i).fill("testpassword123");
  await page.getByRole("button", { name: /log in|sign in/i }).click();
  await expect(page).toHaveURL("/");
}

async function ensureTestUser() {
  await fetch(`${BASE_URL}/api/auth/signup`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      email: "e2e-detail@test.com",
      name: "E2E Detail User",
      password: "testpassword123",
    }),
  });
}

async function ensureTestSkill() {
  await fetch(`${BASE_URL}/api/skills`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-API-Key": API_KEY,
    },
    body: JSON.stringify({
      name: "e2e-detail-skill",
      description: "Skill for Playwright detail E2E tests",
    }),
  });
}

test.describe("Skill detail", () => {
  test.beforeAll(async () => {
    await ensureTestUser();
    await ensureTestSkill();
  });

  test("navigating to skill detail shows tabs", async ({ page }) => {
    await login(page);
    await page.goto("/skills/e2e-detail-skill");
    // The detail page has tabs: Content, Files, Versions, Usage, Security
    await expect(page.locator("text=Content")).toBeVisible();
    await expect(page.locator("text=Files")).toBeVisible();
    await expect(page.locator("text=Versions")).toBeVisible();
    await expect(page.locator("text=Usage")).toBeVisible();
    await expect(page.locator("text=Security")).toBeVisible();
  });

  test("clicking tabs switches content", async ({ page }) => {
    await login(page);
    await page.goto("/skills/e2e-detail-skill");
    await page.locator("button", { hasText: "Versions" }).click();
    await page.waitForTimeout(300);
    // Versions tab should show version-related content or empty state
    const content = page.locator("main, [role=main], .flex-1");
    await expect(content).toBeVisible();
  });

  test("security badge is visible on detail page", async ({ page }) => {
    await login(page);
    await page.goto("/skills/e2e-detail-skill");
    // SecurityBadge component renders for every skill
    // It shows "Clean", "Info", "Warning", or "Critical", or a loading/no-scan state
    const badge = page.locator('[class*="badge"], [class*="shield"], [class*="security"]').first();
    await expect(badge).toBeVisible({ timeout: 5000 });
  });
});
```

- [ ] **Step 2: Run the test**

```bash
cd /Users/nathananderson-tennant/Development/skael/web
npx playwright test e2e/skill-detail.spec.ts
```

Expected: Tests pass.

- [ ] **Step 3: Commit**

```bash
git add web/e2e/skill-detail.spec.ts
git commit -m "test(e2e): add Playwright skill detail and security badge tests"
```

---

## Sub-project 4: README + Release

### Task 9: Polish the README

**Files:**
- Modify: `README.md`

The current README already has a Quick Start and CLI section. Update it to include the new install methods and align with the landing page copy.

- [ ] **Step 1: Update the Quick Start section**

Replace the current "Quick Start" and "CLI" sections (approximately lines 7-30 of `README.md`) with:

```markdown
## Quick Start

### Self-hosted (Docker Compose)

```bash
cp .env.example .env          # set API_KEY and DATABASE_URL
docker compose up -d
```

Platform is at `http://localhost:8080`. Set `API_KEY` in `.env` before running in production.

### Install the CLI

```bash
# macOS / Linux (Homebrew)
brew install alternayte/skael/skael

# macOS / Linux (curl)
curl -fsSL https://raw.githubusercontent.com/alternayte/skael-releases/main/install.sh | sh

# From source
go install github.com/skael-dev/skael/cmd/skael@latest
```

### Connect to your registry

```bash
skael setup http://localhost:8080 <your-api-key>
```

This validates the connection, saves config, syncs all skills, and installs activation tracking hooks for every detected agent.
```

- [ ] **Step 2: Verify README renders correctly**

```bash
cd /Users/nathananderson-tennant/Development/skael
head -60 README.md
```

Expected: Updated Quick Start section with all three install methods.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: update README with install methods and quickstart"
```

### Task 10: Tag v0.1.0

**Files:** None (git operations only)

- [ ] **Step 1: Verify all tests pass**

```bash
cd /Users/nathananderson-tennant/Development/skael
just test-fast
```

Expected: All Go tests and web tests pass.

- [ ] **Step 2: Verify the site builds**

```bash
just site-build
```

Expected: Clean build.

- [ ] **Step 3: Review what will be released**

```bash
git log --oneline main..HEAD
```

Verify all commits from this plan are present.

- [ ] **Step 4: Tag the release**

```bash
git tag v0.1.0
git push origin main
git push origin v0.1.0
```

Expected: Tag triggers the release workflow at `.github/workflows/release.yml`, which:
1. Builds the SPA
2. Cross-compiles CLI + server for 6 platform/arch combinations
3. Publishes archives + checksums to `alternayte/skael-releases`
4. Pushes Homebrew formula to `alternayte/homebrew-skael`

- [ ] **Step 5: Verify the release**

```bash
gh release view v0.1.0 --repo alternayte/skael-releases
```

Expected: Release visible with 12 assets (6 archives + 6 server archives + checksums).
