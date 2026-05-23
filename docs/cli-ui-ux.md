# Skael — CLI UI/UX Design Document

**Version:** 0.2 · **Date:** May 2026 · **Status:** Draft

---

## Design philosophy

Skael is a CLI tool, not a TUI application. It does not take over the terminal. It runs a command, produces styled output, and exits. Think `gh`, `pnpm`, `bun` — not `lazygit` or `k9s`.

The styling serves two purposes: make output scannable at a glance, and make the tool feel premium enough that developers trust it with their agent configuration. Every command should feel like it was built by someone who ships developer tools for a living.

## Toolkit

**Lipgloss v2** (`charm.land/lipgloss/v2`) for all output styling. Declarative, immutable styles. Handles automatic color downsampling for terminals that don't support true colour.

**No Bubbletea.** No interactive TUI. Every command is run → styled output → exit. If we ever need interactive prompts (e.g., `skael setup` with missing args), use `charmbracelet/huh` for forms, not a full bubbletea program.

**No Bubbles.** No tables, spinners, or progress bars from the Bubbles component library. Our output is simple enough that lipgloss styles on plain `fmt.Println` calls are sufficient.

## Colour palette

Matches the dashboard's green accent on dark background. Uses ANSI 256 colours as the baseline.

```go
package ui

import "charm.land/lipgloss/v2"

var (
    Green   = lipgloss.Color("#22c55e")  // accent: success, active, primary actions
    Red     = lipgloss.Color("#ef4444")  // errors, destructive
    Yellow  = lipgloss.Color("#f59e0b")  // warnings, deprecation
    Dim     = lipgloss.Color("#666666")  // metadata, timestamps, secondary info
    Muted   = lipgloss.Color("#a0a0a0")  // descriptions, help text
    White   = lipgloss.Color("#ededed")  // primary text, skill names
)
```

When colour is disabled (`NO_COLOR` env var, piped output, dumb terminals), lipgloss automatically strips ANSI codes. Output must be perfectly readable without colour — colour adds emphasis, it never carries meaning alone.

## Styles

```go
var (
    // Prefixes
    SuccessPrefix = lipgloss.NewStyle().Foreground(Green).Bold(true).Render("✓")
    ErrorPrefix   = lipgloss.NewStyle().Foreground(Red).Bold(true).Render("✗")
    WarnPrefix    = lipgloss.NewStyle().Foreground(Yellow).Bold(true).Render("!")
    InfoPrefix    = lipgloss.NewStyle().Foreground(Dim).Render("·")
    DownArrow     = lipgloss.NewStyle().Foreground(Green).Render("↓")
    PlusSign      = lipgloss.NewStyle().Foreground(Green).Bold(true).Render("+")

    // Text styles
    Bold      = lipgloss.NewStyle().Bold(true).Foreground(White)
    Faint     = lipgloss.NewStyle().Foreground(Dim)
    Desc      = lipgloss.NewStyle().Foreground(Muted)
    Code      = lipgloss.NewStyle().Foreground(White)
    Highlight = lipgloss.NewStyle().Foreground(Green).Bold(true)

    // Error box (for multi-line errors with context)
    ErrorBox = lipgloss.NewStyle().
        BorderStyle(lipgloss.RoundedBorder()).
        BorderForeground(Red).
        Padding(0, 1).
        MarginTop(1)
)
```

## Output patterns

Every command follows the same structural pattern: **action lines → summary**. No banners, no ASCII art, no logo.

### Step output (setup, sync, hook install)

Each step prints as it completes. Not all at once at the end.

```
  ✓ Connected to skills.company.com
  ✓ Detected agents: claude-code, codex
  ↓ code-review                            v4 → v5
  ↓ deployment-checklist                   v2 → v3
  + api-patterns                           v1 (new)
  ✓ Synced 3 skills to claude-code, codex
  ✓ Installed hooks for claude-code, codex

  3 updated · 0 removed · 23 total
```

Rules:
- One line per action. Never wrap.
- Prefix character is always column 2 (2-space left indent).
- Skill names are left-aligned. Version info is right-aligned (pad with spaces to terminal width or a max of 60 chars).
- Summary line at the bottom is separated by one blank line. Uses `·` (middle dot) as separator. Key numbers in bold/highlight.

### Search results

```
  code-review              v5   Code review checklist with security checks
  api-patterns             v1   REST API design patterns and conventions
  deployment-checklist     v3   Pre-deployment verification steps

  3 results for "review"
```

### Publish output

```
  ✓ Validated SKILL.md
  ✓ Packed code-review (4 files, 3.2 KB)
  ✓ Security scan: clean
  ✓ Published v6

  https://skills.company.com/skills/code-review
```

### Publish with scan findings

```
  ✓ Validated SKILL.md
  ✓ Packed code-review (4 files, 3.2 KB)
  ! Scan found 1 medium finding:
    SKILL.md:42 — references .env file (SENSITIVE_FILE_ACCESS)
  ✓ Published v6 (1 warning)

  https://skills.company.com/skills/code-review
```

### Scan output

```
  ✓ Scanning deployment-checklist...

  SKILL.md:12     MEDIUM   References .env file (SENSITIVE_FILE_ACCESS)
  scripts/deploy.sh:8  HIGH   curl piping to bash (DANGEROUS_SHELL)

  1 high · 1 medium · 0 info
```

### Doctor output

```
  ✓ Config                  ~/.skael/config.json
  ✓ Platform                skills.company.com (healthy, 23 skills)
  ✓ State                   23 skills synced, last sync 2h ago
  ✓ claude-code             ~/.claude/skills/ (23 skills, hook installed)
  ✓ codex                   ~/.codex/skills/ (23 skills, hook installed)
  ! gemini                  ~/.gemini/skills/ (23 skills, no hook — not yet supported)
  ✗ opencode                not detected

  5 checks passed · 1 warning · 1 not applicable
```

### Hook status output

```
  ✓ claude-code   hook installed   last event: 3h ago
  ✓ codex         hook installed   last event: 1d ago
  ! gemini        no hook          not yet supported
  ✗ opencode      not detected

  2 active · 1 unsupported · 1 not detected
```

### Stats output (Phase 2)

```
  code-review              340 activations   12 devs   3h ago
  deployment               128 activations    8 devs   1d ago
  api-patterns              47 activations    5 devs   2d ago

  515 total activations across 3 skills (30d)
```

### Diff output (Phase 2)

```
  code-review  v4 → v5

  + Added: security review section for auth endpoints
  ~ Changed: TypeScript example updated for v5.3 syntax
  - Removed: deprecated ESLint rule references

  3 changes
```

## Error handling

### Error display

Errors are never raw Go error strings. Every error has three components:

1. **What failed** — one-line summary with `✗` prefix
2. **Why** — context the user needs
3. **What to do** — actionable next step

```
  ✗ Cannot connect to skills.company.com

    Connection refused. The platform might not be running,
    or the URL might be wrong.

    Try: curl -s https://skills.company.com/api/health
```

```
  ✗ API key rejected

    The platform returned 401 Unauthorized. The key might be
    expired or copied incorrectly.

    Try: skael setup https://skills.company.com <new-key>
```

```
  ✗ Hook installation failed for codex

    ~/.codex/config.toml has invalid TOML syntax (line 42).
    Skael won't modify a broken config file.

    Fix the syntax error, then: skael hook install
```

```
  ✗ Publish blocked — critical security finding

    SKILL.md:15 — possible API key detected (SECRET_EXPOSURE)
    
    Remove the secret and try again, or use --force to override.
    Run: skael scan ./my-skill for full report.
```

Multi-line errors use the `ErrorBox` style (rounded red border) only when there are 3+ lines of context.

### Warning display

Warnings use `!` prefix in yellow. They don't stop execution.

```
  ✓ Connected to skills.company.com
  ✓ Synced 23 skills
  ! Hook installation skipped for gemini (not yet supported)
  ✓ Installed hooks for claude-code, codex

  23 synced · 1 warning
```

### Error categories and recovery

| Category | Example | Behaviour |
|---|---|---|
| **Network** | Platform unreachable, timeout, DNS failure | Fail fast. Print URL tried. Suggest `curl` test. |
| **Auth** | Invalid/expired API key, 401/403 | Fail fast. Suggest re-running `setup`. |
| **Conflict** | Config file exists but has unexpected format | Don't overwrite. Print what's wrong. Suggest manual fix. |
| **Partial** | 20 of 23 skills synced, 3 failed to download | Complete what you can. Report failures individually. Exit code 1. |
| **Agent** | Agent config file has syntax errors | Skip that agent. Warn. Continue with other agents. |
| **Disk** | Permission denied, disk full | Fail fast. Print the path that failed. Suggest fix. |
| **Security** | Critical scan finding on publish | Block publish. Print findings. Suggest fix or `--force`. |

## Idempotency and state safety

**Every command must be safely re-runnable.**

### `skael setup`

- If `~/.skael/config.json` exists, overwrite it (re-running setup = fresh config).
- If skills are already synced, the sync step diffs and only downloads changes.
- If hooks are already installed, detect existing hooks and update in place (or skip if identical).
- If setup fails mid-way, next `skael setup` picks up cleanly.

### `skael sync`

- Uses `~/.skael/state.json` to track what's synced. If missing or corrupt, treats as fresh sync.
- Never deletes locally-created skills. Only manages platform-sourced skills (tracked by state file).
- If a download fails, old version stays. Failure reported, old skill continues working.
- If state file is out of sync with reality, next sync re-downloads as needed.

### `skael hook install`

- Reads existing agent config and merges. Never clobbers user's own hooks.
- Each hook is tagged with `"_managed_by": "skael"` or `# managed_by = "skael"`.
- Running twice produces the same result as running once.
- `skael hook uninstall` removes only skael-managed hooks.

### `skael publish`

- Publishing same directory twice creates new version only if content changed (checksum comparison).
- Upload is atomic (write to temp, rename on success). No partial versions.

### State file corruption

If `~/.skael/state.json` is corrupt:
1. Renames corrupt file to `state.json.bak`
2. Warns: `! State file was corrupt, backed up to state.json.bak. Starting fresh sync.`
3. Proceeds as first-time sync

Never crashes on corrupt state. Never requires manual file deletion.

## `--json` flag

Every command supports `--json`. When set:
- All styling is suppressed
- Output is a single JSON object on stdout
- Errors are JSON: `{"error": "message", "code": "AUTH_FAILED", "suggestion": "..."}`
- Exit codes still reflect success/failure

```bash
skael sync --json
```
```json
{
  "synced": [
    {"name": "code-review", "from_version": 4, "to_version": 5},
    {"name": "api-patterns", "from_version": null, "to_version": 1}
  ],
  "failed": [],
  "agents": ["claude-code", "codex"],
  "total": 23
}
```

Build this from the start so the output structure is never an afterthought.

## `--no-color` and `NO_COLOR`

Respect the `NO_COLOR` environment variable (https://no-color.org/). Lipgloss handles this automatically. Also support `--no-color` flag.

Prefix characters (`✓`, `✗`, `!`, `↓`, `+`) still render without colour — they carry meaning through shape.

## Terminal width handling

Use `lipgloss.Width()` for measuring rendered strings. Detect terminal width via `os.Stdout`. Default to 80 columns if width can't be detected.

Truncate descriptions and paths that would wrap. Never produce output wider than the terminal. For narrow terminals (< 60 cols), drop right-aligned columns and show only the skill name.

## Go package structure

```
internal/ui/
├── styles.go      # all lipgloss styles, colours, prefix constants
├── output.go      # Step(), Success(), Error(), Warn(), Info() functions
├── format.go      # FormatSkillRow(), FormatSummary(), FormatError() etc
└── json.go        # JSON output mode wrapper
```

```go
// Step output
ui.Success("Connected to %s", url)
// Output: ✓ Connected to skills.company.com

ui.Download("code-review", "v4 → v5")
// Output: ↓ code-review                            v4 → v5

ui.New("api-patterns", "v1")
// Output: + api-patterns                           v1 (new)

ui.Warn("Hook installation skipped for gemini (not yet supported)")
// Output: ! Hook installation skipped for gemini (not yet supported)

ui.Error(ui.ErrorDetail{
    Message:    "Cannot connect to skills.company.com",
    Context:    "Connection refused. The platform might not be running.",
    Suggestion: "curl -s https://skills.company.com/api/health",
})

ui.Summary("3 updated", "0 removed", "23 total")
// Output: 3 updated · 0 removed · 23 total

// In JSON mode, all of these write to a buffer and flush as JSON at exit
```

Commands never call `fmt.Println` directly for user-facing output. All output goes through the `ui` package.

## Reference CLIs

Study these for output quality benchmarks:

- **`gh`** (GitHub CLI) — clean step output, good error messages with suggestions, `--json` on everything
- **`pnpm`** — fast, dense, colour-coded dependency output, progress without spinners
- **`bun`** — minimal, fast, uses colour sparingly but effectively
- **`railway`** — deployment steps with checkmarks, clean error recovery
- **`turso`** — database CLI with good setup flow, styled output
