# Skael Testing Strategy — Design Spec

**Date:** 2026-05-25 · **Status:** Draft

---

## Summary

Add comprehensive frontend testing (Vitest + React Testing Library + MSW) and thin Playwright E2E tests. Reorganize test commands for a solo dev workflow: fast feedback loop (<10s) for daily work, full suite for CI.

## Goals

- 100% confidence that features work without manually running the app
- Low maintenance — test behavior, not implementation
- Fast dev loop — `just test-fast` under 10 seconds
- CI-ready — runs against local or staging environments

## Current State

- **Go backend:** 161 tests across 22 test files. Good coverage on API routes, stores, scanner, auth. Uses testcontainers for real Postgres.
- **Frontend:** 0 tests. No test framework configured. ~35 components, 5 pages.
- **E2E:** 5 backend-only Go e2e tests. No UI e2e.

---

## Frontend Testing: Vitest + RTL + MSW

### Setup

| Tool | Purpose |
|---|---|
| Vitest | Test runner (shares Vite config, zero extra bundler setup) |
| @testing-library/react | Render components, query DOM, fire events |
| @testing-library/jest-dom | Extended DOM matchers (toBeInTheDocument, etc.) |
| @testing-library/user-event | Simulate real user interactions (click, type) |
| msw | Mock Service Worker — intercepts fetch at network level |
| jsdom | DOM environment for Vitest |

### MSW Mock Handlers

`web/src/test/handlers.ts` — one file with mock API responses for all endpoints. Shared across all test files. Returns realistic fixture data matching the Go API's actual response shapes (from `types.gen.ts`).

Endpoints to mock:
- `GET /api/auth/me` → authenticated user
- `GET /api/analytics/overview` → overview data
- `GET /api/analytics/skills` → skills array with activation data
- `GET /api/skills` → skill list
- `GET /api/skills/:name` → skill detail
- `GET /api/skills/:name/activations` → activation summary
- `GET /api/skills/:name/versions` → version list
- `GET /api/skills/:name/scan` → scan report
- `PUT /api/skills/:name/review` → updated skill
- `PUT /api/skills/review` → bulk review result
- `POST /api/auth/login` → user
- `POST /api/auth/signup` → user
- `POST /api/auth/logout` → 204
- `GET /api/auth/keys` → key list
- `POST /api/auth/keys` → created key with full key
- `DELETE /api/auth/keys/:id` → 204

`web/src/test/setup.ts` — Vitest setup file. Starts MSW server before all, resets handlers after each, closes after all.

### Test Files (colocated with source)

**`web/src/features/skills/skill-list.test.tsx`** (~6 tests)
1. Renders skill list from API data — names, versions, activation counts visible
2. Search input filters skills by name
3. Tag pills filter by tag
4. Bulk review: select checkboxes → "Mark Reviewed" button appears → click → API called → list refreshes
5. Empty state: when API returns 0 skills, onboarding screen shows with CLI instructions
6. Loading state: skeleton renders before data arrives

**`web/src/features/skills/skill-detail.test.tsx`** (~6 tests)
1. Header renders skill name, description, version, meta strip
2. Content tab renders markdown (headings, code blocks)
3. Files tab shows file tree, clicking a file shows viewer
4. Versions tab shows version timeline
5. Security tab shows scan findings, "Mark Reviewed" button calls API
6. Tab switching works — clicking Usage tab shows activation data

**`web/src/features/analytics/analytics.test.tsx`** (~5 tests)
1. KPI strip renders four tiles with correct numbers
2. Table renders skill rows with activation counts
3. Column header click sorts the table
4. Dead skills (0 activations) get muted row styling
5. Time period toggle (7d/30d/90d) refetches data with new days param

**`web/src/features/settings/settings.test.tsx`** (~5 tests)
1. Workspace section shows server URL and skill count
2. API key list renders prefixes and relative times
3. "Create API Key" → dialog → submit → full key shown once with copy button
4. Delete key → confirmation dialog → API called → key removed from list
5. Danger zone regenerate button is disabled

**`web/src/features/auth/auth.test.tsx`** (~5 tests)
1. Login form submits email + password → calls API → redirects to /
2. Login with bad credentials shows error message
3. Signup form submits email + name + password → calls API → redirects to /
4. Unauthenticated user redirected to /login (RequireAuth guard)
5. Logout calls API → redirects to /login

**Total: ~27 frontend tests across 5 files.**

### What We Don't Test

- Individual shadcn components (tested upstream)
- CSS/styling/animations
- Exact DOM structure (brittle, high churn)
- react-markdown rendering fidelity (library concern)

We test user-visible behavior: "when I do X, I see Y."

---

## Playwright E2E (3 scenarios)

### Setup

| Tool | Purpose |
|---|---|
| @playwright/test | Browser automation + assertions |

Config at `web/playwright.config.ts`. Tests in `web/e2e/`.

Playwright starts the Go server automatically for local runs via `webServer` config. For CI against staging, set `PLAYWRIGHT_BASE_URL` to skip local server startup.

```ts
webServer: process.env.PLAYWRIGHT_BASE_URL ? undefined : {
  command: 'just dev',
  url: 'http://localhost:8080/api/health',
  reuseExistingServer: true,
},
use: {
  baseURL: process.env.PLAYWRIGHT_BASE_URL ?? 'http://localhost:8080',
},
```

### Scenarios

**`web/e2e/auth.spec.ts`** — Auth flow
1. Navigate to / → redirected to /login
2. Click "Sign up" link → signup form
3. Fill email + name + password → submit → lands on dashboard (empty state)
4. Logout → back on /login
5. Login with same credentials → lands on dashboard

**`web/e2e/publish-explore.spec.ts`** — Publish + explore
1. Signup + create API key in settings
2. Use key to publish a test skill via HTTP (direct API call in test, simulating CLI)
3. Navigate to / → skill appears in list
4. Click skill → detail page → Content tab shows rendered SKILL.md
5. Click Security tab → scan results visible

**`web/e2e/key-management.spec.ts`** — API key lifecycle
1. Login → Settings → "Create API Key"
2. Dialog shows full key → copy it
3. Key appears in list with prefix
4. Use key in API call (fetch /api/skills with X-API-Key header) → 200
5. Delete key → confirm → key gone from list
6. Same API call now returns 401

**Total: 3 spec files, ~15 assertions.**

### What E2E Does NOT Cover

- Every tab and sub-feature (that's what Vitest integration tests are for)
- Error states (mocked in Vitest tests)
- Edge cases (unit/integration level)

E2E covers the cross-stack "does it actually work end-to-end" question.

---

## Test Commands

Update `justfile` with clear test tiers:

```just
# All Go tests (needs Docker for testcontainers)
test-go:
    go test ./... -count=1

# Frontend unit/integration tests (fast, no server needed)
test-web:
    cd web && npx vitest run

# Full Playwright E2E (starts server, needs Docker)
test-e2e:
    cd web && npx playwright test

# Fast feedback loop (<10s) — skip testcontainers + e2e
test-fast:
    go test -short ./... -count=1 && cd web && npx vitest run

# Everything except e2e (CI gate)
test: test-go test-web

# Full CI check
check: vet fmt-check test
```

`just test-fast` is the dev command. Run after every change. Under 10 seconds.
`just test` is the CI command. Runs all Go + frontend tests.
`just test-e2e` is manual or CI-only. Runs Playwright against real server.

---

## File Structure

### New files

```
web/
  vitest.config.ts              # Vitest configuration
  playwright.config.ts          # Playwright configuration
  src/
    test/
      handlers.ts               # MSW mock API handlers (all endpoints)
      setup.ts                  # Vitest global setup (MSW server lifecycle)
      fixtures.ts               # Shared test fixture data
    features/
      skills/
        skill-list.test.tsx
        skill-detail.test.tsx
      analytics/
        analytics.test.tsx
      settings/
        settings.test.tsx
      auth/
        auth.test.tsx
  e2e/
    auth.spec.ts
    publish-explore.spec.ts
    key-management.spec.ts
```

### Modified files

```
web/package.json                # add test deps + scripts
justfile                        # add test commands
```

---

## Dependencies

### Vitest + RTL

```bash
npm install -D vitest jsdom @testing-library/react @testing-library/jest-dom @testing-library/user-event msw
```

### Playwright

```bash
npm install -D @playwright/test
npx playwright install chromium
```

---

## Conventions

- Test files colocated with source: `feature.tsx` → `feature.test.tsx`
- One `describe` per page/feature, one `it` per behavior
- Use `screen.getByRole`, `screen.getByText`, `screen.getByPlaceholderText` — not `getByTestId` (test user-visible behavior, not implementation)
- MSW handlers return realistic data matching `types.gen.ts` shapes
- No snapshot tests (high churn, low value for a solo dev)
- Playwright tests use `page.getByRole` and `page.getByText` — same philosophy as RTL
