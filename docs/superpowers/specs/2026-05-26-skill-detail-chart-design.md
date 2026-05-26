# Skill Detail Page — Per-Agent Activations Chart

## Summary

Add a stacked area chart to the skill detail page's Usage tab, showing daily activations broken down by coding agent (claude-code, cursor, codex, opencode). Placed between the period toggle and the existing agent breakdown table.

## Motivation

The Usage tab currently shows KPI cards and a static agent breakdown table. There's no way to see activation trends over time for a specific skill. The global analytics dashboard has a timeseries chart, but it's aggregate — not per-skill or per-agent. Teams need to see whether a skill's adoption is growing, which agents drive usage, and how that changes over time.

## API

### New endpoint: `GET /api/skills/{name}/timeseries`

**Query params:**
- `days` — integer, 1-365, default 30

**Response:** `200 OK` — array of flat objects, one per day:

```json
[
  { "date": "2026-05-20", "claude-code": 5, "cursor": 2, "codex": 0, "opencode": 1 },
  { "date": "2026-05-21", "claude-code": 3, "cursor": 4, "codex": 1, "opencode": 0 }
]
```

- Each row is a calendar day within the window
- Agent names are dynamic keys — only agents with >= 1 activation in the window appear
- Days with zero total activations are included (gap-filled)
- Uses the same skill alias resolution as the existing `ActivationSummary` query
- SQL: `GROUP BY date_trunc('day', created_at), agent` then pivot to flat rows in Go

**Error responses:**
- `404` — skill not found (consistent with other skill endpoints)
- `422` — invalid `days` param

## Frontend

### New component: `SkillActivationsChart`

**Location:** `web/src/features/skills/skill-activations-chart.tsx`

**Behavior:**
- Fetches `GET /api/skills/{name}/timeseries?days={period}`
- Period is passed as a prop (shared state with parent `TabUsage`)
- Renders a Recharts `AreaChart` inside the existing `ChartContainer` wrapper
- Each agent is a stacked `<Area>` with gradient fill (matching `activations-chart.tsx` style)
- Agent colors use CSS variables: `--color-chart-1` through `--color-chart-4`
- Inline legend: small colored dots + agent names, top-right of chart card
- Tooltip: date + all agent counts on hover
- Loading state: 180px skeleton with pulse animation
- Empty state: "No activation data for this period" centered text
- Height: 180px (consistent with analytics dashboard chart)

### Placement in `TabUsage`

```
KPI Cards (4 across)
Period Toggle [7d] [30d] [90d]
─────────────────────────────
SkillActivationsChart  ← NEW
─────────────────────────────
Agent Breakdown table  ← existing
```

### Agent color mapping

| Agent | CSS Variable | Approx color |
|-------|-------------|--------------|
| claude-code | `--color-chart-1` | Accent/primary |
| cursor | `--color-chart-2` | Secondary |
| codex | `--color-chart-3` | Tertiary |
| opencode | `--color-chart-4` | Quaternary |

Colors are assigned by sorting agents alphabetically for consistency. If a new agent appears in the future, it picks up the next available chart color variable.

## Scope

### In scope
- New backend endpoint with per-agent daily timeseries
- New frontend chart component
- Integration into skill detail Usage tab
- OpenAPI spec generation via Huma

### Out of scope
- Changes to the global analytics dashboard chart
- Per-developer breakdown
- Aggregation by week/month (daily only for now)
- Export/download of chart data
