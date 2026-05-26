# Skill Detail Chart Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a stacked area chart to the skill detail page's Usage tab, showing daily activations broken down by coding agent.

**Architecture:** New `GetSkillTimeSeries` store method queries `skill_events` grouped by day and agent, returns flat rows. New Huma endpoint at `GET /api/skills/{name}/timeseries` serves this data. New `SkillActivationsChart` React component renders a Recharts stacked area chart. SDK types are auto-generated via `just generate`.

**Tech Stack:** Go (pgx, Huma v2), React (Recharts, TanStack Query), openapi-ts for SDK generation

---

### Task 1: Backend — Store method `GetSkillTimeSeries`

**Files:**
- Modify: `internal/analytics/event.go` (add type + method after `GetTimeSeries` at line ~400)
- Test: `internal/analytics/event_test.go` (add tests at end of file)

- [ ] **Step 1: Write the failing test for `GetSkillTimeSeries` with data**

Add to `internal/analytics/event_test.go`:

```go
func TestStore_GetSkillTimeSeries(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	store := analytics.NewStore(pool)
	ctx := context.Background()

	events := []analytics.Event{
		{SkillName: "code-review", Agent: "claude-code", TriggerType: "auto", ProjectHash: "p1", DeveloperHash: "d1"},
		{SkillName: "code-review", Agent: "claude-code", TriggerType: "auto", ProjectHash: "p2", DeveloperHash: "d2"},
		{SkillName: "code-review", Agent: "cursor", TriggerType: "auto", ProjectHash: "p1", DeveloperHash: "d1"},
		{SkillName: "other-skill", Agent: "claude-code", TriggerType: "auto", ProjectHash: "p3", DeveloperHash: "d3"},
	}
	for _, e := range events {
		require.NoError(t, store.Insert(ctx, e))
	}

	series, err := store.GetSkillTimeSeries(ctx, "code-review", 30)
	require.NoError(t, err)
	require.NotEmpty(t, series)

	// All events inserted today — find today's row
	today := time.Now().Format("2006-01-02")
	var todayRow *analytics.AgentDailyCount
	for i := range series {
		if series[i].Date == today {
			todayRow = &series[i]
			break
		}
	}
	require.NotNil(t, todayRow, "expected a row for today")
	require.Equal(t, 2, todayRow.Agents["claude-code"])
	require.Equal(t, 1, todayRow.Agents["cursor"])

	// "other-skill" events should not appear
	for _, row := range series {
		_, hasOther := row.Agents["other-skill-agent"]
		require.False(t, hasOther)
	}

	// Gap-fill: every day in the 30-day window should be present
	require.Equal(t, 31, len(series)) // 30 days + today
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `just test-run TestStore_GetSkillTimeSeries`
Expected: FAIL — `store.GetSkillTimeSeries` does not exist

- [ ] **Step 3: Write the `AgentDailyCount` type and `GetSkillTimeSeries` method**

Add to `internal/analytics/event.go` after the `GetTimeSeries` method:

```go
// AgentDailyCount holds per-agent activation counts for a single day.
type AgentDailyCount struct {
	Date   string         `json:"date"`
	Agents map[string]int `json:"-"`
}

// GetSkillTimeSeries returns daily per-agent activation counts for a specific
// skill over the last `days` days. Days with zero activations are included.
func (s *Store) GetSkillTimeSeries(ctx context.Context, skillName string, days int) ([]AgentDailyCount, error) {
	const q = `
		WITH days AS (
			SELECT generate_series(
				(now() - make_interval(days => $2))::date,
				now()::date,
				'1 day'::interval
			)::date AS day
		),
		counts AS (
			SELECT se.created_at::date AS day, se.agent, COUNT(*)::int AS cnt
			FROM skill_events se
			LEFT JOIN skill_aliases a ON a.alias = se.skill_name
			WHERE COALESCE(a.canonical, se.skill_name) = $1
			  AND se.created_at > now() - make_interval(days => $2)
			GROUP BY se.created_at::date, se.agent
		)
		SELECT d.day::text, COALESCE(c.agent, ''), COALESCE(c.cnt, 0)
		FROM days d
		LEFT JOIN counts c ON c.day = d.day
		ORDER BY d.day, c.agent
	`
	rows, err := s.pool.Query(ctx, q, skillName, days)
	if err != nil {
		return nil, fmt.Errorf("analytics.Store.GetSkillTimeSeries query: %w", err)
	}
	defer rows.Close()

	dayMap := make(map[string]*AgentDailyCount)
	var orderedDates []string

	for rows.Next() {
		var date, agent string
		var count int
		if err := rows.Scan(&date, &agent, &count); err != nil {
			return nil, fmt.Errorf("analytics.Store.GetSkillTimeSeries scan: %w", err)
		}
		entry, exists := dayMap[date]
		if !exists {
			entry = &AgentDailyCount{Date: date, Agents: make(map[string]int)}
			dayMap[date] = entry
			orderedDates = append(orderedDates, date)
		}
		if agent != "" && count > 0 {
			entry.Agents[agent] = count
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("analytics.Store.GetSkillTimeSeries rows: %w", err)
	}

	results := make([]AgentDailyCount, 0, len(orderedDates))
	for _, d := range orderedDates {
		results = append(results, *dayMap[d])
	}
	if results == nil {
		results = []AgentDailyCount{}
	}
	return results, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `just test-run TestStore_GetSkillTimeSeries`
Expected: PASS

- [ ] **Step 5: Write the empty-data test**

Add to `internal/analytics/event_test.go`:

```go
func TestStore_GetSkillTimeSeries_NoEvents(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	store := analytics.NewStore(pool)
	ctx := context.Background()

	series, err := store.GetSkillTimeSeries(ctx, "nonexistent", 7)
	require.NoError(t, err)
	require.NotNil(t, series)
	require.Equal(t, 8, len(series)) // 7 days + today, all with empty agents
	for _, row := range series {
		require.Empty(t, row.Agents)
	}
}
```

- [ ] **Step 6: Run all analytics tests**

Run: `just test-pkg internal/analytics`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/analytics/event.go internal/analytics/event_test.go
git commit -m "feat(analytics): add GetSkillTimeSeries store method

Per-agent daily activation counts for a specific skill, with gap-filling."
```

---

### Task 2: Backend — Huma endpoint `GET /api/skills/{name}/timeseries`

**Files:**
- Modify: `internal/analytics/routes.go` (add endpoint after the existing activations endpoint at line ~123)
- Modify: `internal/analytics/event.go` (add `MarshalJSON` for flat JSON shape)
- Test: `internal/analytics/routes_test.go` (add HTTP test)

- [ ] **Step 1: Write the failing HTTP test**

Add to `internal/analytics/routes_test.go`:

```go
func TestGetSkillTimeSeries_ViaHTTP(t *testing.T) {
	handler, store := setupAnalyticsAPI(t)
	ctx := context.Background()

	// Need a registered skill for the endpoint to return data
	pool := testutil.SetupTestDB(t)
	skillStore := skill.NewStore(pool)
	insertTestSkill(t, ctx, skillStore, "ts-skill", "timeseries test")

	require.NoError(t, store.Insert(ctx, analytics.Event{
		SkillName: "ts-skill", Agent: "claude-code", TriggerType: "auto",
		ProjectHash: "p1", DeveloperHash: "d1",
	}))

	rr := doJSONAnalytics(t, handler, http.MethodGet, "/api/skills/ts-skill/timeseries?days=7", nil)
	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())

	var series []map[string]interface{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &series))
	require.NotEmpty(t, series)

	// Each entry should have a "date" key; agent keys are dynamic
	for _, entry := range series {
		_, hasDate := entry["date"]
		require.True(t, hasDate, "each entry must have a date key")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `just test-run TestGetSkillTimeSeries_ViaHTTP`
Expected: FAIL — 404, route not registered

- [ ] **Step 3: Add `MarshalJSON` to `AgentDailyCount` for flat output**

Add to `internal/analytics/event.go` after the `AgentDailyCount` struct:

```go
// MarshalJSON produces a flat object: {"date":"2026-05-20","claude-code":5,"cursor":2}
func (a AgentDailyCount) MarshalJSON() ([]byte, error) {
	m := make(map[string]interface{}, len(a.Agents)+1)
	m["date"] = a.Date
	for agent, count := range a.Agents {
		m[agent] = count
	}
	return json.Marshal(m)
}
```

- [ ] **Step 4: Register the Huma endpoint**

Add to `internal/analytics/routes.go` after the activations endpoint block (after line ~123):

```go
	// -----------------------------------------------------------------
	// GET /api/skills/{name}/timeseries?days=30 — per-agent daily counts
	// -----------------------------------------------------------------
	type skillTimeseriesInput struct {
		Name string `path:"name"`
		Days int    `query:"days" default:"30" minimum:"1" maximum:"365"`
	}
	type skillTimeseriesOutput struct {
		Body []AgentDailyCount
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-skill-timeseries",
		Method:      http.MethodGet,
		Path:        "/api/skills/{name}/timeseries",
		Summary:     "Get per-agent daily activation counts for a skill",
	}, func(ctx context.Context, input *skillTimeseriesInput) (*skillTimeseriesOutput, error) {
		days := input.Days
		if days == 0 {
			days = 30
		}
		series, err := store.GetSkillTimeSeries(ctx, input.Name, days)
		if err != nil {
			return nil, fmt.Errorf("get skill timeseries: %w", err)
		}
		return &skillTimeseriesOutput{Body: series}, nil
	})
```

- [ ] **Step 5: Run test to verify it passes**

Run: `just test-run TestGetSkillTimeSeries_ViaHTTP`
Expected: PASS

Note: The test uses `setupAnalyticsAPI` which creates its own DB pool. The test in Step 1 creates a separate pool for inserting the skill — this won't work because the handler's store uses a different pool. Fix by using the store returned from `setupAnalyticsAPI` directly, and inserting the skill through the handler's pool. If the test fails for this reason, adjust to use a single pool:

```go
func TestGetSkillTimeSeries_ViaHTTP(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	store := analytics.NewStore(pool)
	skillStore := skill.NewStore(pool)
	ctx := context.Background()

	r := chi.NewMux()
	api := humachi.New(r, huma.DefaultConfig("Test API", "1.0.0"))
	analytics.RegisterRoutes(api, store)

	insertTestSkill(t, ctx, skillStore, "ts-skill", "timeseries test")
	require.NoError(t, store.Insert(ctx, analytics.Event{
		SkillName: "ts-skill", Agent: "claude-code", TriggerType: "auto",
		ProjectHash: "p1", DeveloperHash: "d1",
	}))

	rr := doJSONAnalytics(t, r, http.MethodGet, "/api/skills/ts-skill/timeseries?days=7", nil)
	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())

	var series []map[string]interface{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &series))
	require.NotEmpty(t, series)

	for _, entry := range series {
		_, hasDate := entry["date"]
		require.True(t, hasDate, "each entry must have a date key")
	}
}
```

- [ ] **Step 6: Run all analytics tests**

Run: `just test-pkg internal/analytics`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/analytics/event.go internal/analytics/routes.go internal/analytics/routes_test.go
git commit -m "feat(analytics): add GET /api/skills/{name}/timeseries endpoint

Per-agent daily activation counts with flat JSON shape for Recharts."
```

---

### Task 3: Regenerate frontend SDK types

**Files:**
- Regenerate: `web/openapi.json`, `web/src/api/types.gen.ts`, `web/src/api/sdk.gen.ts`

- [ ] **Step 1: Regenerate the OpenAPI spec and SDK**

Run: `just generate`

This runs `go run ./cmd/server --openapi > web/openapi.json` then `cd web && npm run generate`. The new `get-skill-timeseries` endpoint will appear in the generated SDK.

- [ ] **Step 2: Verify the new SDK function exists**

Run: `grep -n "getSkillTimeseries\|skillTimeseries" web/src/api/sdk.gen.ts`
Expected: A function like `getSkillTimeseries` should appear

- [ ] **Step 3: Verify the response type exists**

Run: `grep -n "AgentDailyCount\|SkillTimeseries" web/src/api/types.gen.ts`
Expected: Types for the timeseries response should appear. Note: since `AgentDailyCount` uses a custom `MarshalJSON`, the OpenAPI spec may generate a generic type. Check the actual generated type and use it in the next task.

- [ ] **Step 4: Commit**

```bash
git add web/openapi.json web/src/api/types.gen.ts web/src/api/sdk.gen.ts web/src/api/client.gen.ts
git commit -m "chore: regenerate frontend SDK with skill timeseries endpoint"
```

---

### Task 4: Frontend — `SkillActivationsChart` component

**Files:**
- Create: `web/src/features/skills/skill-activations-chart.tsx`

- [ ] **Step 1: Create the chart component**

Create `web/src/features/skills/skill-activations-chart.tsx`:

```tsx
import { useQuery } from "@tanstack/react-query";
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";
import {
  type ChartConfig,
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart";

type AgentDailyRow = Record<string, string | number>;

const AGENT_COLORS: Record<string, string> = {
  "claude-code": "var(--color-chart-1)",
  codex: "var(--color-chart-2)",
  cursor: "var(--color-chart-3)",
  opencode: "var(--color-chart-4)",
};

function getAgentColor(agent: string, index: number): string {
  return AGENT_COLORS[agent] ?? `var(--color-chart-${(index % 4) + 1})`;
}

async function fetchSkillTimeSeries(
  name: string,
  days: number
): Promise<AgentDailyRow[]> {
  const res = await fetch(
    `/api/skills/${encodeURIComponent(name)}/timeseries?days=${days}`,
    { credentials: "include" }
  );
  if (!res.ok) return [];
  return res.json();
}

function extractAgents(series: AgentDailyRow[]): string[] {
  const agentSet = new Set<string>();
  for (const row of series) {
    for (const key of Object.keys(row)) {
      if (key !== "date") agentSet.add(key);
    }
  }
  return Array.from(agentSet).sort();
}

export function SkillActivationsChart({
  skillName,
  days,
}: {
  skillName: string;
  days: number;
}) {
  const { data, isLoading } = useQuery({
    queryKey: ["skill-timeseries", skillName, days],
    queryFn: () => fetchSkillTimeSeries(skillName, days),
  });

  if (isLoading) {
    return (
      <div className="h-[200px] bg-bg-secondary border border-border rounded-lg animate-pulse-soft mb-6" />
    );
  }

  const series = data ?? [];
  const agents = extractAgents(series);
  const hasData = series.some((row) =>
    agents.some((a) => (row[a] as number) > 0)
  );

  if (!hasData) {
    return (
      <div className="h-[200px] bg-bg-secondary border border-border rounded-lg flex items-center justify-center mb-6">
        <p className="text-sm text-text-tertiary">
          No activation data for this period
        </p>
      </div>
    );
  }

  const chartConfig: ChartConfig = {};
  agents.forEach((agent, i) => {
    chartConfig[agent] = {
      label: agent,
      color: getAgentColor(agent, i),
    };
  });

  return (
    <div className="bg-bg-secondary border border-border rounded-lg p-4 mb-6">
      <div className="flex items-center justify-between mb-3">
        <div className="text-[10px] uppercase tracking-[0.08em] text-text-tertiary">
          Activations by agent
        </div>
        <div className="flex items-center gap-3">
          {agents.map((agent, i) => (
            <div key={agent} className="flex items-center gap-1.5">
              <div
                className="size-2 rounded-full"
                style={{ backgroundColor: getAgentColor(agent, i) }}
              />
              <span className="text-[11px] text-text-tertiary">{agent}</span>
            </div>
          ))}
        </div>
      </div>
      <ChartContainer config={chartConfig} className="h-[180px] w-full">
        <AreaChart
          accessibilityLayer
          data={series}
          margin={{ left: 0, right: 8, top: 4, bottom: 0 }}
        >
          <defs>
            {agents.map((agent) => (
              <linearGradient
                key={agent}
                id={`fill-${agent}`}
                x1="0"
                y1="0"
                x2="0"
                y2="1"
              >
                <stop
                  offset="5%"
                  stopColor={`var(--color-${agent})`}
                  stopOpacity={0.5}
                />
                <stop
                  offset="95%"
                  stopColor={`var(--color-${agent})`}
                  stopOpacity={0}
                />
              </linearGradient>
            ))}
          </defs>
          <CartesianGrid
            vertical={false}
            stroke="var(--color-border)"
            strokeDasharray="3 3"
          />
          <XAxis
            dataKey="date"
            tickLine={false}
            axisLine={false}
            tickMargin={8}
            minTickGap={40}
            tick={{ fontSize: 11, fill: "var(--color-text-tertiary)" }}
            tickFormatter={(value: string) => {
              const d = new Date(value + "T00:00:00");
              return d.toLocaleDateString("en-US", {
                month: "short",
                day: "numeric",
              });
            }}
          />
          <YAxis
            tickLine={false}
            axisLine={false}
            width={32}
            tick={{ fontSize: 11, fill: "var(--color-text-tertiary)" }}
            allowDecimals={false}
          />
          <ChartTooltip
            cursor={false}
            content={
              <ChartTooltipContent
                indicator="dot"
                labelFormatter={(value: string) =>
                  new Date(value + "T00:00:00").toLocaleDateString("en-US", {
                    weekday: "short",
                    month: "short",
                    day: "numeric",
                  })
                }
              />
            }
          />
          {agents.map((agent) => (
            <Area
              key={agent}
              dataKey={agent}
              type="monotone"
              fill={`url(#fill-${agent})`}
              stroke={`var(--color-${agent})`}
              strokeWidth={2}
              stackId="agents"
            />
          ))}
        </AreaChart>
      </ChartContainer>
    </div>
  );
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd web && npx tsc --noEmit`
Expected: No type errors

- [ ] **Step 3: Commit**

```bash
git add web/src/features/skills/skill-activations-chart.tsx
git commit -m "feat(ui): add SkillActivationsChart component

Stacked area chart with per-agent color coding using Recharts."
```

---

### Task 5: Frontend — Integrate chart into Usage tab

**Files:**
- Modify: `web/src/features/skills/skill-detail.tsx` (lines 310-450, the `TabUsage` component)

- [ ] **Step 1: Add import at the top of skill-detail.tsx**

Add after the existing imports (around line 16):

```tsx
import { SkillActivationsChart } from "@/features/skills/skill-activations-chart";
```

- [ ] **Step 2: Insert the chart between the period toggle and agent breakdown**

In the `TabUsage` component, add the chart after the period toggle `</div>` (after line 391) and before the agent breakdown section (line 393):

Find this code block:

```tsx
        </div>
      </div>

      {/* Agent breakdown */}
```

Replace with:

```tsx
        </div>
      </div>

      {/* Activations chart */}
      <SkillActivationsChart skillName={skill.name} days={period} />

      {/* Agent breakdown */}
```

- [ ] **Step 3: Verify it compiles**

Run: `cd web && npx tsc --noEmit`
Expected: No type errors

- [ ] **Step 4: Start the dev server and visually verify**

Run: `just dev` (in one terminal) and `cd web && npm run dev` (in another)

Navigate to a skill detail page's Usage tab. Verify:
- The stacked area chart appears between the period toggle and agent breakdown
- Agent colors are distinct
- Hovering shows tooltip with date + per-agent counts
- Inline legend appears top-right with colored dots
- Switching period (7d/30d/90d) updates the chart
- Loading state shows skeleton
- Empty state shows "No activation data" message

- [ ] **Step 5: Commit**

```bash
git add web/src/features/skills/skill-detail.tsx
git commit -m "feat(ui): integrate activations chart into skill detail Usage tab

Chart sits between the period toggle and agent breakdown table."
```

---

### Task 6: Final verification

- [ ] **Step 1: Run all backend tests**

Run: `just test`
Expected: All PASS

- [ ] **Step 2: Run frontend type check**

Run: `cd web && npx tsc --noEmit`
Expected: No errors

- [ ] **Step 3: Run the check suite**

Run: `just check`
Expected: vet + fmt-check + test all pass
