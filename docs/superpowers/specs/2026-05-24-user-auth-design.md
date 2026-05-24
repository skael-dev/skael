# Skael User Auth — Design Spec

**Date:** 2026-05-24 · **Status:** Draft

---

## Summary

Replace the single `API_KEY` env var with user accounts and user-scoped API keys. Session-based auth for the dashboard (cookies), API key auth for CLI/hooks. Both resolve to a user identity.

## Model

Kestra/Flagsmith open-core model:
- Open signup — anyone who can reach the server can create an account
- First user is `owner`, all subsequent users are `admin`
- `role` column exists (owner/admin/member) but is not enforced in the self-hosted version — all users have full access
- RBAC enforcement gated by license (cloud/enterprise, Phase 3)
- `DISABLE_SIGNUP=true` env var available to lock down after initial setup

## Libraries

| Library | Purpose |
|---|---|
| `alexedwards/scs` v2 | Session management (OWASP-compliant) |
| `alexedwards/scs/pgxstore` | Postgres session storage (uses existing pgx pool) |
| `golang.org/x/crypto/bcrypt` | Password hashing + API key hashing |

No external auth services. No OAuth in Phase 1 (foundation supports adding `markbates/goth` later).

---

## Data Model

### New tables

```sql
CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email           TEXT NOT NULL UNIQUE,
    name            TEXT NOT NULL,
    password_hash   TEXT NOT NULL,
    role            TEXT NOT NULL DEFAULT 'admin',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE user_api_keys (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            TEXT NOT NULL DEFAULT 'default',
    key_prefix      TEXT NOT NULL,
    key_hash        TEXT NOT NULL,
    last_used_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_api_keys_prefix ON user_api_keys (key_prefix);

CREATE TABLE sessions (
    token   TEXT PRIMARY KEY,
    data    BYTEA NOT NULL,
    expiry  TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_sessions_expiry ON sessions (expiry);
```

The `sessions` table schema is dictated by `scs/pgxstore`.

### Removed

- The `API_KEY` env var is no longer required. If present, it acts as a legacy fallback key (mapped to no user — the request gets a synthetic "system" identity). This ensures existing CLI setups don't break immediately. Deprecated for removal in a future version.
- The `api_keys` table from the original SDD is not created (it was never implemented). Replaced by `user_api_keys`.

### Modified

- `skill_versions.published_by` — stores user name (from authenticated user) instead of "system"
- `skills.reviewed_by` — stores user name instead of hardcoded "admin"

---

## Auth Middleware

Replaces the current single-key middleware. Dual-check: session cookie OR API key header.

### Request flow

```
Request
  |
  +-- /api/health, /api/openapi.json --> pass through (no auth)
  +-- non-/api/ path --> pass through (SPA static files)
  +-- /api/auth/* (login, signup, logout) --> pass through (public)
  +-- all other /api/* routes:
        |
        1. Session cookie present?
           --> scs loads session --> extract user_id --> query users table --> attach to ctx
        |
        2. X-API-Key header present?
           --> extract prefix (first 8 chars after "sk-")
           --> query user_api_keys by prefix
           --> bcrypt compare full key against key_hash
           --> look up user by user_id --> attach to ctx
           --> update last_used_at (fire-and-forget, non-blocking)
        |
        3. Legacy API_KEY env var match? (if configured)
           --> attach synthetic User{ID: "system", Name: "system", Role: "admin"}
        |
        4. None --> 401
```

### User context

```go
type User struct {
    ID    string
    Email string
    Name  string
    Role  string
}

func UserFromContext(ctx context.Context) *User
func ContextWithUser(ctx context.Context, user *User) context.Context
```

All handlers that need identity call `auth.UserFromContext(ctx)`. Returns nil for unauthenticated requests (should never happen behind the middleware).

### Session config

- Cookie name: `skael_session`
- Lifetime: 7 days
- HttpOnly: true
- SameSite: Lax
- Secure: true when `LISTEN_ADDR` is TLS or behind reverse proxy (detect via `X-Forwarded-Proto`)
- Storage: Postgres via `pgxstore`

---

## API Endpoints

### Auth endpoints (public, no auth required)

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/auth/signup` | Create account. Body: `{ email, name, password }`. Returns user + sets session cookie. Blocked when `DISABLE_SIGNUP=true`. When users table is empty, first user gets `role: "owner"`. |
| `POST` | `/api/auth/login` | Verify credentials. Body: `{ email, password }`. Returns user + sets session cookie. |
| `POST` | `/api/auth/logout` | Destroy session. Returns 204. |
| `GET` | `/api/auth/me` | Returns current user from session. 401 if not authenticated. Used by SPA on load to check auth state. |

### API key management (authenticated)

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/auth/keys` | Create API key. Body: `{ name }`. Returns `{ id, name, key, prefix, created_at }`. The `key` field contains the full key — shown ONCE, never retrievable again. |
| `GET` | `/api/auth/keys` | List user's keys. Returns `[{ id, name, prefix, last_used_at, created_at }]`. No full key. |
| `DELETE` | `/api/auth/keys/{id}` | Revoke a key. Returns 204. |

### Validation

- Email: valid format, max 255 chars
- Name: 1-100 chars
- Password: minimum 8 chars, no complexity rules (NIST 800-63B)
- API key name: 1-64 chars
- Signup returns 403 when `DISABLE_SIGNUP=true`
- Signup returns 409 when email already exists

### API key format

Keys are generated as: `sk-` + 32 random hex characters = 35 chars total.
Prefix stored: first 8 chars (`sk-a1b2c`).
Full key bcrypt hashed before storage.

---

## Frontend Changes

### New pages (outside Shell, no sidebar)

**`/login`** — email + password form. "Don't have an account? Sign up" link. Dark background, centered card, matches design system.

**`/signup`** — email + name + password form. "Already have an account? Log in" link. Same styling.

Both redirect to `/` on success.

### Auth flow in SPA

1. App loads → `GET /api/auth/me`
2. If 401 → redirect to `/login` (React Router navigate, no flash of dashboard)
3. If 200 → store user in React context (`AuthProvider`), render Shell
4. Login/signup success → set user in context, navigate to `/`
5. Logout button (in sidebar, bottom) → `POST /api/auth/logout`, clear context, redirect to `/login`

### Auth context

```tsx
interface AuthContextValue {
  user: User | null;
  isLoading: boolean;
  login: (email: string, password: string) => Promise<void>;
  signup: (email: string, name: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
}
```

### Settings page update

Replace the fake "API & Keys" section with real key management:
- List of user's API keys: name, prefix (`sk-a1b2...`), last used (relative time), created (relative time)
- "Create API Key" button → dialog: name input → on submit → show full key ONCE with copy button and "This won't be shown again" warning
- Delete button per key with confirmation dialog

### Sidebar update

Add user info at bottom of icon rail: avatar circle (first letter of name), tooltip shows name + email. Click opens a small menu with "Log out".

---

## Config Changes

| Variable | Required | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | yes | — | Postgres connection string |
| `STORAGE_PATH` | no | `./data/skills` | Archive storage directory |
| `LISTEN_ADDR` | no | `:8080` | HTTP listen address |
| `API_KEY` | no | — | Legacy API key (deprecated, for backward compat) |
| `DISABLE_SIGNUP` | no | `false` | Block new account creation when `true` |

`API_KEY` is no longer required. If set, it works as a fallback for existing CLI setups that haven't migrated to user-scoped keys. Log a deprecation warning on startup when it's present.

---

## Bug Fixes (included in this round)

These bugs were found during the adversarial review and should be fixed alongside the auth work:

### 1. TOC headings don't work

**File:** `web/src/features/skills/markdown-renderer.tsx`
**Fix:** Install `rehype-slug` and add to the react-markdown plugins. This adds `id` attributes to headings so the table of contents links and IntersectionObserver work.

### 2. Tag filtering broken

**File:** `web/src/features/skills/skill-list.tsx`, `internal/analytics/event.go`
**Fix:** Add `tags []string` field to the `SkillAnalytics` response. Extract tags from the skill's frontmatter JSON in the analytics query (`frontmatter->>'tags'`). Parse the JSON array or comma-separated string into a Go `[]string`.

### 3. XSS in onboarding

**File:** `web/src/features/skills/skill-list.tsx`
**Fix:** Replace `dangerouslySetInnerHTML` with React elements. Render the setup step text as JSX with `<code>` elements for inline code, not raw HTML.

### 4. No Error Boundary

**File:** `web/src/app/app.tsx`
**Fix:** Install `react-error-boundary`. Wrap the app in `<ErrorBoundary>` with a fallback UI that shows "Something went wrong" with a "Reload" button. Prevents white-screen crashes.

### 5. warn/warning naming

**File:** `internal/analytics/event.go`
**Fix:** Document that the scanner produces `"warn"` status and the `SecuritySummary.Warning` field aggregates both `"warn"` and `"high"` statuses. No code change needed — it already maps correctly. Add a code comment.

---

## File Structure

### New Go files

```
internal/auth/
  middleware.go      -- rewrite: dual-check (session + API key)
  middleware_test.go  -- update tests
  user.go            -- User type, context helpers, password hashing
  user_store.go      -- Postgres CRUD for users
  key.go             -- API key generation, hashing, validation
  key_store.go       -- Postgres CRUD for user_api_keys
  routes.go          -- auth endpoints (signup, login, logout, me, keys)
  routes_test.go     -- auth endpoint tests
```

### New frontend files

```
web/src/
  app/
    auth-provider.tsx  -- AuthContext + provider
  features/
    auth/
      login.tsx        -- login page
      signup.tsx        -- signup page
```

### Modified files

```
internal/platform/config.go           -- remove API_KEY requirement, add DISABLE_SIGNUP
internal/platform/migrate/001_initial.sql  -- add users, user_api_keys, sessions tables
internal/skill/routes.go              -- use auth.UserFromContext for published_by, reviewed_by
cmd/server/main.go                    -- initialize scs, wire session middleware
web/src/app/app.tsx                   -- add auth check, error boundary, login/signup routes
web/src/app/sidebar.tsx               -- add user avatar + logout
web/src/features/settings/settings.tsx -- replace fake key UI with real key management
web/src/features/skills/markdown-renderer.tsx -- add rehype-slug
web/src/features/skills/skill-list.tsx -- fix XSS, fix tag filtering
```

---

## Testing Strategy

### Backend

- Auth store tests: user CRUD, key CRUD, duplicate email, bcrypt verification
- Auth routes tests: signup (first user = owner, subsequent = admin), login (success, wrong password, nonexistent email), logout, me (authenticated, unauthenticated)
- Key routes tests: create key (returns full key once), list keys (no full key), delete key, use key for API auth
- Middleware tests: session auth, API key auth, legacy API_KEY fallback, unauthenticated rejection, public path exemption
- Integration: signup → create key → use key in API call → verify published_by

### Frontend

- Auth flow: redirect to login when unauthenticated, redirect to dashboard after login
- Settings key management: create key shows full key, list shows prefixes only, delete with confirmation
