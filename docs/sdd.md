# Skael — System Design Document

**Version:** 0.2 · **Date:** May 2026 · **Status:** Draft

---

## Project structure

```
skael/
├── cmd/
│   ├── server/
│   │   └── main.go                     # wire dependencies, start HTTP
│   └── skael/
│       └── main.go                     # CLI entrypoint (cobra)
│
├── internal/
│   ├── skill/
│   │   ├── skill.go                    # domain types: Skill, Version, Frontmatter
│   │   ├── routes.go                   # Huma route registration + handlers
│   │   ├── store.go                    # Postgres queries (CRUD, search)
│   │   ├── archive.go                  # tar.gz pack/unpack, checksum, file manifest
│   │   ├── search.go                   # FTS query builder, ranking logic
│   │   ├── render.go                   # extract text from .md files for indexing
│   │   └── skill_test.go
│   │
│   ├── analytics/
│   │   ├── event.go                    # Event type
│   │   ├── routes.go                   # POST /events, GET /skills/:name/activations
│   │   ├── store.go                    # insert, per-skill aggregation queries
│   │   └── analytics_test.go
│   │
│   ├── sync/
│   │   ├── manifest.go                 # manifest type + diffing logic
│   │   ├── routes.go                   # GET /sync/manifest
│   │   └── sync_test.go
│   │
│   ├── scan/
│   │   ├── scanner.go                  # orchestrates all rule checks, merges external results
│   │   ├── rules.go                    # rule definitions: pattern, severity, category
│   │   ├── secrets.go                  # API key / credential regex patterns
│   │   ├── injection.go               # prompt injection pattern matching
│   │   ├── exfiltration.go            # data exfil + dangerous shell commands + env var access
│   │   ├── obfuscation.go             # base64 payloads, hex encoding, unicode tricks
│   │   ├── external.go                # shell out to snyk agent-scan or cisco skill-scanner (Phase 2)
│   │   ├── llm.go                      # optional LLM-based semantic review (Phase 3)
│   │   ├── report.go                   # ScanReport type, finding serialisation
│   │   ├── testdata/                   # cloned from snyk-labs/toxicskills-goof
│   │   └── scan_test.go
│   │
│   ├── hooks/
│   │   ├── hooks.go                    # hook lifecycle: install, uninstall, status, verify
│   │   ├── config.go                   # per-agent hook config generation
│   │   ├── claude.go                   # Claude Code hook: .claude/settings.json manipulation
│   │   ├── codex.go                    # Codex CLI hook: ~/.codex/config.toml manipulation
│   │   ├── gemini.go                   # Gemini CLI hook: .gemini/settings.json (Phase 2)
│   │   └── hooks_test.go
│   │
│   ├── auth/
│   │   ├── auth.go                     # API key types, org context
│   │   ├── middleware.go               # Huma middleware: key extraction, org ctx
│   │   ├── store.go                    # key lookup
│   │   └── auth_test.go
│   │
│   └── platform/
│       ├── config.go                   # env parsing: DB URL, storage path, listen addr
│       ├── database.go                 # pgx pool, migration runner
│       ├── storage.go                  # file storage interface (local FS, S3 later)
│       └── migrate/
│           ├── 001_initial.sql
│           └── 002_analytics.sql
│
├── cli/
│   ├── root.go                         # cobra root command + global flags
│   ├── setup.go                        # skael setup (one-command onboarding)
│   ├── sync.go                         # skael sync
│   ├── publish.go                      # skael publish
│   ├── scan.go                         # skael scan
│   ├── search.go                       # skael search
│   ├── list.go                         # skael list
│   ├── doctor.go                       # skael doctor
│   ├── hook.go                         # skael hook install/status/uninstall
│   ├── diff.go                         # skael diff (Phase 2)
│   ├── stats.go                        # skael stats (Phase 2)
│   ├── agents/
│   │   ├── detect.go                   # which agents are installed?
│   │   ├── agent.go                    # Agent interface: paths, hook format
│   │   ├── claude.go                   # Claude Code specifics
│   │   ├── codex.go                    # Codex CLI specifics
│   │   ├── gemini.go                   # Gemini CLI specifics (Phase 2)
│   │   └── opencode.go                # OpenCode specifics (Phase 3)
│   └── state/
│       └── state.go                    # ~/.skael/state.json management
│
├── hooks/
│   ├── skael-hook.sh                   # universal hook script (bash)
│   └── README.md                       # hook documentation for manual install
│
├── web/                                # React SPA
│   ├── src/
│   │   ├── api/                        # hey-api generated client (gitignored)
│   │   ├── lib/
│   │   │   └── query.ts               # TanStack Query client setup
│   │   ├── pages/
│   │   │   ├── skills/
│   │   │   │   ├── SkillList.tsx
│   │   │   │   ├── SkillDetail.tsx
│   │   │   │   └── FilePreview.tsx
│   │   │   ├── analytics/
│   │   │   │   └── Dashboard.tsx       # Phase 2
│   │   │   └── settings/
│   │   │       └── Settings.tsx
│   │   ├── components/
│   │   │   ├── layout/
│   │   │   │   ├── Sidebar.tsx
│   │   │   │   ├── Shell.tsx
│   │   │   │   └── KPIStrip.tsx        # Phase 2
│   │   │   ├── skill/
│   │   │   │   ├── SkillCard.tsx
│   │   │   │   ├── FileTree.tsx
│   │   │   │   ├── VersionList.tsx
│   │   │   │   ├── ActivationBadge.tsx
│   │   │   │   └── MarkdownRenderer.tsx
│   │   │   ├── analytics/
│   │   │   │   ├── UsageTable.tsx      # Phase 2
│   │   │   │   └── TrendIndicator.tsx  # Phase 2
│   │   │   ├── SearchBar.tsx
│   │   │   └── Skeleton.tsx
│   │   ├── App.tsx
│   │   └── main.tsx
│   ├── index.html
│   ├── package.json
│   ├── tailwind.config.ts
│   ├── vite.config.ts
│   └── openapi-ts.config.ts            # hey-api pointing at openapi.json
│
├── embed.go                            # //go:embed web/dist/*
├── Dockerfile
├── docker-compose.yml
├── Makefile
├── go.mod
└── go.sum
```

## Database schema (Phase 1)

```sql
-- 001_initial.sql

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE skills (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    display_name    TEXT,
    description     TEXT NOT NULL DEFAULT '',
    content         TEXT NOT NULL DEFAULT '',
    search_vector   TSVECTOR GENERATED ALWAYS AS (
        setweight(to_tsvector('english', coalesce(name, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(display_name, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(description, '')), 'B') ||
        setweight(to_tsvector('english', coalesce(content, '')), 'C')
    ) STORED,
    latest_version  INT NOT NULL DEFAULT 0,
    frontmatter     JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_skills_search ON skills USING gin(search_vector);
CREATE INDEX idx_skills_name_trgm ON skills USING gin(name gin_trgm_ops);

CREATE TABLE skill_versions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    skill_id        UUID NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    version         INT NOT NULL,
    archive_path    TEXT NOT NULL,
    checksum        TEXT NOT NULL,
    changelog       TEXT NOT NULL DEFAULT '',
    frontmatter     JSONB NOT NULL DEFAULT '{}',
    file_manifest   JSONB NOT NULL DEFAULT '[]',
    published_by    TEXT NOT NULL DEFAULT 'system',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE(skill_id, version)
);

CREATE TABLE skill_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    skill_name      TEXT NOT NULL,
    agent           TEXT NOT NULL,
    trigger_type    TEXT NOT NULL DEFAULT 'auto',
    project_hash    TEXT NOT NULL DEFAULT '',
    developer_hash  TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_events_skill_time ON skill_events (skill_name, created_at DESC);
CREATE INDEX idx_events_created ON skill_events (created_at DESC);

CREATE TABLE api_keys (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key_hash        TEXT NOT NULL UNIQUE,
    org_name        TEXT NOT NULL DEFAULT 'default',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

```sql
-- 002_bundles.sql (Phase 3)

CREATE TABLE bundles (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    description     TEXT NOT NULL DEFAULT '',
    skill_names     TEXT[] NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

## Hook system (activation tracking)

The hook system is skael's most architecturally significant component. It provides cross-agent skill activation telemetry — the first tool to offer visibility into which skills are being used across an entire agent fleet.

### Architecture

```
Developer machine                          Skael platform
┌─────────────────────────┐                ┌──────────────┐
│  Claude Code            │                │              │
│  ├── PreToolUse hook ───┼── POST ──────→ │  POST        │
│                         │                │  /api/events │
│  Codex CLI              │                │              │
│  ├── pre_tool_use hook ─┼── POST ──────→ │              │
│                         │                │              │
│  Gemini CLI (Phase 2)   │                │              │
│  ├── BeforeTool hook ───┼── POST ──────→ │              │
│                         │                └──────────────┘
│                         │
│  ~/.skael/hooks/        │
│  └── skael-hook.sh      │  ← single script, all agents call it
└─────────────────────────┘
```

### Design principles

1. **Fire-and-forget.** Hook failure never blocks the agent. The hook script backgrounds the HTTP request and exits immediately.
2. **Privacy-first.** Developer identities and project paths are hashed on the developer's machine before transmission. The platform never receives raw identifying data.
3. **Single script.** One bash script handles all agents. Agent-specific behaviour is selected via the `SKAEL_AGENT` environment variable.
4. **Minimal dependencies.** The hook script requires only `curl` (pre-installed on macOS and Linux) and optionally `jq` (falls back to grep-based parsing if unavailable).
5. **Idempotent installation.** Running `skael hook install` twice produces the same result. The installer merges with existing agent configs rather than overwriting them.

### Hook script

```bash
#!/usr/bin/env bash
# skael-hook.sh — reports skill activations to the skael platform
# Installed by: skael hook install
# Deps: curl, jq (optional, falls back to grep)

set -euo pipefail

SKAEL_ENDPOINT="${SKAEL_ENDPOINT:-}"
SKAEL_API_KEY="${SKAEL_API_KEY:-}"

if [ -z "$SKAEL_ENDPOINT" ] || [ -z "$SKAEL_API_KEY" ]; then
  exit 0  # silently skip if not configured
fi

INPUT=$(cat)

AGENT="${SKAEL_AGENT:-unknown}"
SKILL_NAME=""

if command -v jq &>/dev/null; then
  SKILL_NAME=$(echo "$INPUT" | jq -r '.tool_input.name // .tool_input.skill_name // empty' 2>/dev/null || true)
else
  SKILL_NAME=$(echo "$INPUT" | grep -oP '"name"\s*:\s*"[^"]*"' | head -1 | grep -oP ':\s*"\K[^"]+' || true)
fi

if [ -z "$SKILL_NAME" ]; then
  exit 0
fi

PROJECT_HASH=$(echo -n "${PWD}" | sha256sum | cut -d' ' -f1 | head -c 16)
DEV_HASH=$(echo -n "${USER:-unknown}@${HOSTNAME:-unknown}" | sha256sum | cut -d' ' -f1 | head -c 16)

curl -s -o /dev/null --max-time 2 \
  -X POST "${SKAEL_ENDPOINT}/api/events" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: ${SKAEL_API_KEY}" \
  -d "{
    \"skill_name\": \"${SKILL_NAME}\",
    \"agent\": \"${AGENT}\",
    \"trigger_type\": \"auto\",
    \"project_hash\": \"${PROJECT_HASH}\",
    \"developer_hash\": \"${DEV_HASH}\"
  }" &

exit 0
```

### Hook installation per agent

**Claude Code** — writes to `.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Skill",
        "hooks": [
          {
            "type": "command",
            "command": "SKAEL_AGENT=claude-code SKAEL_ENDPOINT=https://skills.company.com SKAEL_API_KEY=sk-xxx ~/.skael/hooks/skael-hook.sh",
            "_managed_by": "skael"
          }
        ]
      }
    ]
  }
}
```

**Codex CLI** — appends to `~/.codex/config.toml`:

```toml
[[hooks.pre_tool_use]]
matcher = "Skill"
command = "SKAEL_AGENT=codex SKAEL_ENDPOINT=https://skills.company.com SKAEL_API_KEY=sk-xxx ~/.skael/hooks/skael-hook.sh"
# managed_by = "skael"
```

**Gemini CLI (Phase 2)** — writes to `.gemini/settings.json`:

```json
{
  "hooks": {
    "BeforeTool": [
      {
        "matcher": "activate_skill",
        "hooks": [
          {
            "type": "command",
            "command": "SKAEL_AGENT=gemini SKAEL_ENDPOINT=https://skills.company.com SKAEL_API_KEY=sk-xxx ~/.skael/hooks/skael-hook.sh",
            "_managed_by": "skael"
          }
        ]
      }
    ]
  }
}
```

### Hook lifecycle

| Command | Behaviour |
|---|---|
| `skael setup` | Installs hooks for all detected agents as part of onboarding |
| `skael hook install` | Installs/updates hooks for detected agents. Merges with existing config. |
| `skael hook status` | Checks hook health: config present, script exists, can reach platform |
| `skael hook uninstall` | Removes only skael-managed hooks, leaves user's other hooks intact |
| `skael doctor` | Includes hook verification in its diagnostic output |

### Failure modes

| Failure | Behaviour |
|---|---|
| Platform unreachable | Hook silently exits (fire-and-forget). No agent disruption. |
| Malformed stdin | Hook silently exits (can't parse skill name). |
| Agent config has syntax errors | `skael hook install` refuses to modify broken config. Reports error with fix suggestion. |
| Missing curl | Hook silently exits. `skael doctor` flags this. |
| Concurrent hook writes | Each agent's config is read-modify-write with a file lock. |

### Testing strategy

- Unit tests: JSON/TOML config manipulation, skill name extraction from various stdin formats
- Integration tests: install hooks → verify config file → simulate agent invocation → check event received
- Agent compatibility: test against latest release of each supported agent's hook format
- Fixture: sample stdin payloads from each agent captured during real skill invocations

## Setup command flow

`skael setup <url> <api-key>` is the single onboarding command:

```
skael setup https://skills.company.com sk-xxxxxxxxxxxxx

  ✓ Validating connection...        → GET /api/health
  ✓ Writing config...               → ~/.skael/config.json
  ✓ Detecting agents...             → checks for claude-code, codex, gemini, opencode
    Found: claude-code, codex
  ✓ Syncing 23 skills...            → manifest diff → download → extract
  ✓ Installing hooks...             → writes hook configs for detected agents
    claude-code: ~/.claude/settings.json
    codex: ~/.codex/config.toml

  Setup complete. Skills are live across 2 agents.
```

Implementation (`cli/setup.go`):

```go
func runSetup(url, apiKey string) error {
    // 1. Validate connection
    if err := validateConnection(url, apiKey); err != nil {
        return fmt.Errorf("cannot connect to %s — is the platform running? (%w)", url, err)
    }

    // 2. Write config
    if err := writeConfig(url, apiKey); err != nil {
        return err
    }

    // 3. Detect agents
    agents := detectAgents()
    if len(agents) == 0 {
        warn("No supported agents detected. Skills will be synced but not placed in any agent directory.")
    }

    // 4. First sync
    if err := runSync(agents); err != nil {
        return err
    }

    // 5. Install hooks
    if err := installHooks(url, apiKey, agents); err != nil {
        warn("Hook installation failed: %s (activation tracking won't work, but sync is fine)", err)
    }

    return nil
}
```

Flags:
- `--json` — structured JSON output
- `--dry-run` — print what would happen without writing anything
- `--skip-sync` — configure only, don't sync skills yet
- `--skip-hooks` — configure + sync, but don't install hooks
- `--agents <list>` — override agent detection

Environment variable fallbacks:
- `SKAEL_URL` — platform URL
- `SKAEL_KEY` — API key

Error handling:
- Connection refused → "Cannot reach https://skills.company.com — is the platform running?"
- Invalid API key → "API key rejected by the platform. Check the key and try again."
- No agents found → warning (not an error), setup continues
- Sync fails → partial success: config is written, user can retry with `skael sync`
- Hook install fails → partial success: sync worked, activation tracking won't work, clear message

## CLI state management

`~/.skael/` directory:

```
~/.skael/
├── config.json         # platform URL + API key
├── state.json          # last sync state: [{name, version, checksum}]
└── hooks/
    └── skael-hook.sh   # installed hook script
```

`config.json`:
```json
{
  "endpoint": "https://skills.company.com",
  "api_key": "sk-xxxxxxxxxxxxx"
}
```

`state.json`:
```json
{
  "last_sync": "2026-05-06T14:30:00Z",
  "skills": [
    { "name": "code-review", "version": 5, "checksum": "abc123..." },
    { "name": "deployment", "version": 3, "checksum": "def456..." }
  ]
}
```

## Sync algorithm

```
1. Read ~/.skael/state.json (local state)
2. GET /api/sync/manifest (remote state)
3. Diff:
   - remote has skill not in local → download (new)
   - remote version > local version → download (updated)
   - local has skill not in remote → optionally remove (deleted upstream)
4. For each skill to download:
   a. GET /api/skills/:name/versions/:version/download
   b. Verify checksum
   c. Extract archive to temp dir
   d. For each detected agent:
      - Copy skill directory to agent's global skill path
      - e.g., ~/.claude/skills/code-review/SKILL.md
5. Update ~/.skael/state.json
6. Print summary
```

Agent detection (`cli/agents/detect.go`):
- Claude Code: check if `~/.claude/` directory exists
- Codex CLI: check if `codex` binary is in PATH or `~/.codex/` exists
- Gemini CLI: check if `gemini` binary is in PATH or `~/.gemini/` exists
- OpenCode: check if `opencode` binary is in PATH or `~/.config/opencode/` exists

## Build and development

```makefile
.PHONY: dev build generate test

generate:
	@echo "→ generating openapi spec"
	@go run ./cmd/server --openapi > web/openapi.json
	@echo "→ generating typescript client"
	@cd web && npx @hey-api/openapi-ts

dev:
	@$(MAKE) generate
	@air &
	@cd web && npm run dev

build: generate
	@cd web && npm run build
	@CGO_ENABLED=0 go build -o bin/skael-server ./cmd/server
	@CGO_ENABLED=0 go build -o bin/skael ./cmd/skael

test:
	@go test ./...
```

## Docker

```dockerfile
# Stage 1: Build React SPA
FROM node:22-slim AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
COPY cmd/server/openapi.json ./openapi.json
RUN npx @hey-api/openapi-ts && npm run build

# Stage 2: Build Go binary
FROM golang:1.24 AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
RUN CGO_ENABLED=0 go build -o /skael-server ./cmd/server

# Stage 3: Runtime
FROM gcr.io/distroless/static-debian12
COPY --from=go /skael-server /skael-server
EXPOSE 8080
ENTRYPOINT ["/skael-server"]
```

```yaml
# docker-compose.yml
services:
  server:
    image: skael-server:latest
    build: .
    ports:
      - "8080:8080"
    environment:
      DATABASE_URL: postgres://skael:skael@db:5432/skael?sslmode=disable
      STORAGE_PATH: /data/skills
      API_KEY: sk-change-me-in-production
    volumes:
      - skill-data:/data/skills
    depends_on:
      db:
        condition: service_healthy

  db:
    image: postgres:17
    environment:
      POSTGRES_USER: skael
      POSTGRES_PASSWORD: skael
      POSTGRES_DB: skael
    volumes:
      - pg-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U skael"]
      interval: 5s
      timeout: 5s
      retries: 5

volumes:
  skill-data:
  pg-data:
```

## Security considerations

- API keys are stored as bcrypt hashes, never plaintext
- Hook scripts use environment variables for credentials, never embedded in the script
- Skill archives are validated: SKILL.md must exist, frontmatter must be valid YAML, archive size capped at 10MB
- Activation events use one-way hashes for developer identity and project path
- The platform does not execute skill scripts — it only stores and serves them
- Rate limiting on event ingestion (100 events/minute per API key)
- CORS configured for the embedded SPA origin only
