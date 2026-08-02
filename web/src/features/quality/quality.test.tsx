import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect } from "vitest";
import { http, HttpResponse } from "msw";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AuthProvider } from "@/app/auth-provider";
import { server } from "@/test/handlers";
import { QualityBadge } from "./quality-badge";
import { EvalStatus } from "./eval-status";
import { QualityReport } from "./quality-report";
import { QualityTrend } from "./quality-trend";
import type { JobOutput, RecordOutput, Series } from "@/api/types.gen";

const verified = {
  version: 3, headline_score: 74.2, verified: true,
  panel_complete: true, scored_at: "2026-08-01T00:00:00Z",
};

describe("QualityBadge", () => {
  it("reads neutral when the skill has never been scored", () => {
    render(<QualityBadge quality={null} latestVersion={3} />);
    expect(screen.getByTitle(/not yet scored/i)).toBeInTheDocument();
    // The failure this guards: an unscored skill rendered as a zero.
    expect(screen.queryByText("0")).not.toBeInTheDocument();
  });

  it("shows the rounded score when verified", () => {
    render(<QualityBadge quality={verified} latestVersion={3} />);
    expect(screen.getByText("74")).toBeInTheDocument();
    expect(screen.getByTitle(/verified/i)).toBeInTheDocument();
  });

  it("distinguishes attested from verified", () => {
    const { container } = render(
      <QualityBadge quality={{ ...verified, verified: false }} latestVersion={3} />,
    );
    expect(screen.getByTitle(/attested/i)).toBeInTheDocument();
    expect(container.querySelector("[data-track='hairline']")).toBeTruthy();
  });

  it("shows an incomplete panel as incomplete, never as a low score", () => {
    render(
      <QualityBadge
        quality={{ ...verified, panel_complete: false, headline_score: 12 }}
        latestVersion={3}
      />,
    );
    expect(screen.getByTitle(/incomplete panel/i)).toBeInTheDocument();
    expect(screen.getByText(/~/)).toBeInTheDocument();
  });

  it("marks a score that was computed on an older version", () => {
    render(<QualityBadge quality={verified} latestVersion={7} />);
    expect(screen.getByTitle(/scored on v3.*current v7/i)).toBeInTheDocument();
  });

  it("does not mark a current score as stale", () => {
    render(<QualityBadge quality={verified} latestVersion={3} />);
    expect(screen.queryByTitle(/current v/i)).not.toBeInTheDocument();
  });
});

function withQuery(ui: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <QueryClientProvider client={qc}>
      <AuthProvider>{ui}</AuthProvider>
    </QueryClientProvider>
  );
}

function mockEvals(jobs: Partial<JobOutput>[]) {
  server.use(
    http.get("/api/skills/:name/evals", () => {
      return HttpResponse.json({ jobs });
    }),
  );
}

describe("EvalStatus", () => {
  it("shows queue position rather than a spinner", async () => {
    // Wire value is 0-indexed; display is 1-indexed (a human is "first",
    // not "zeroth"). queue_position: 3 -> "position 4".
    mockEvals([{ id: "j1", status: "queued", queue_position: 3, enqueued_at: "2026-08-01T00:00:00Z" }]);
    render(withQuery(<EvalStatus skillName="s" quality={null} latestVersion={1} />));
    expect(await screen.findByText(/position 4/i)).toBeInTheDocument();
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
  });

  it("shows the front of the queue as position 1, not position 0", async () => {
    mockEvals([{ id: "j1", status: "queued", queue_position: 0, enqueued_at: "2026-08-01T00:00:00Z" }]);
    render(withQuery(<EvalStatus skillName="s" quality={null} latestVersion={1} />));
    expect(await screen.findByText(/position 1/i)).toBeInTheDocument();
    expect(screen.queryByText(/position 0/i)).not.toBeInTheDocument();
  });

  it("shows elapsed time while running", async () => {
    mockEvals([{
      id: "j1", status: "running", queue_position: 0,
      enqueued_at: "2026-08-01T00:00:00Z",
      started_at: new Date(Date.now() - 12 * 60_000).toISOString(),
    }]);
    render(withQuery(<EvalStatus skillName="s" quality={null} latestVersion={1} />));
    expect(await screen.findByText(/12m elapsed/i)).toBeInTheDocument();
  });

  it("offers to run an eval when there is no score and no job", async () => {
    mockEvals([]);
    render(withQuery(<EvalStatus skillName="s" quality={null} latestVersion={1} />));
    expect(await screen.findByRole("button", { name: /run eval/i })).toBeInTheDocument();
  });

  it("names both versions and offers a re-run when the score is stale", async () => {
    mockEvals([]);
    render(withQuery(
      <EvalStatus
        skillName="s"
        quality={{ version: 3, headline_score: 74, verified: true, panel_complete: true, scored_at: "2026-08-01T00:00:00Z" }}
        latestVersion={7}
      />,
    ));
    expect(await screen.findByText(/scored on v3 · current v7/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /re-run eval/i })).toBeInTheDocument();
  });

  it("surfaces a failed job's error", async () => {
    mockEvals([{ id: "j1", status: "failed", queue_position: 0, last_error: "sandbox image missing", enqueued_at: "2026-08-01T00:00:00Z" }]);
    render(withQuery(<EvalStatus skillName="s" quality={null} latestVersion={1} />));
    expect(await screen.findByText(/sandbox image missing/i)).toBeInTheDocument();
  });

  it("surfaces an enqueue failure so a silent click isn't mistaken for a no-op", async () => {
    const user = userEvent.setup();
    mockEvals([]);
    server.use(
      http.post("/api/skills/:name/evals", () => {
        return HttpResponse.json({ detail: "queue is full" }, { status: 500 });
      }),
    );
    render(withQuery(<EvalStatus skillName="s" quality={null} latestVersion={1} />));

    const button = await screen.findByRole("button", { name: /run eval/i });
    await user.click(button);

    expect(await screen.findByText(/failed|queue is full/i)).toBeInTheDocument();
  });
});

const DEFAULT_RECORD: RecordOutput = {
  critical_forbid_violations: 0,
  drift_breakdown: {},
  engine_version: "1",
  headline_ci_high: 0,
  headline_ci_low: 0,
  headline_score: 0,
  model_panel: {},
  panel_complete: true,
  panel_matrix: [],
  pillar_breakdown: {},
  scored_at: "2026-08-01T00:00:00Z",
  skill_id: "skill-1",
  suite_ref: "sha256:abcdef0123456789",
  tier: "standard",
  verified: true,
  version: 1,
};

// mockQuality mocks the summary endpoint (GET .../quality) only, with no
// report — for tests focused on the aggregate fields the summary carries on
// its own.
function mockQuality(overrides: Partial<RecordOutput>) {
  const record: RecordOutput = { ...DEFAULT_RECORD, ...overrides };
  server.use(
    http.get("/api/skills/:name/quality", () => HttpResponse.json(record)),
    http.get("/api/skills/:name/quality/:version", () =>
      HttpResponse.json({ ...record, report: null }),
    ),
  );
}

// mockQualityVersion mocks both the summary and the version-detail endpoint
// with the same aggregate fields, so the detail's `report` (or its absence)
// is what the test exercises.
function mockQualityVersion(
  overrides: Partial<RecordOutput> & { report?: unknown },
) {
  const record = { ...DEFAULT_RECORD, ...overrides };
  server.use(
    http.get("/api/skills/:name/quality", () => HttpResponse.json(record)),
    http.get("/api/skills/:name/quality/:version", () => HttpResponse.json(record)),
  );
}

// A 404 from the quality endpoint means "never scored" — a state, not an
// error.
function mockQualityNotFound() {
  server.use(
    http.get("/api/skills/:name/quality", () =>
      HttpResponse.json({ detail: "not scored" }, { status: 404 }),
    ),
  );
}

describe("QualityReport", () => {
  it("renders the pillars, panel and drift for a scored version", async () => {
    mockQuality({
      version: 3,
      headline_score: 74.2,
      headline_ci_low: 70,
      headline_ci_high: 78,
      verified: true,
      panel_complete: true,
      robustness_gap: 6.5,
      drift_grade: "B",
      suite_ref: "sha256:abcdef0123456789",
    });
    render(withQuery(<QualityReport skillName="s" latestVersion={3} />));
    // Headline renders rounded, matching the badge (Math.round), not the
    // raw geometric-mean float.
    expect(await screen.findByText("74")).toBeInTheDocument();
    expect(screen.getByText(/70.*78/)).toBeInTheDocument();
    expect(screen.getByText(/6\.5/)).toBeInTheDocument();
    expect(screen.getByText("B")).toBeInTheDocument();
  });

  it("says a null robustness gap was not measured, never zero", async () => {
    mockQuality({
      version: 3,
      headline_score: 74.2,
      verified: true,
      panel_complete: true,
      robustness_gap: undefined,
      drift_grade: "B",
    });
    render(withQuery(<QualityReport skillName="s" latestVersion={3} />));
    expect(await screen.findByText(/not measured/i)).toBeInTheDocument();
    // The bug this exists to prevent: "the floor model kept up" shown for
    // "we could not tell".
    expect(screen.queryByText(/^0$/)).not.toBeInTheDocument();
  });

  it("renders aggregates when the stored report is absent", async () => {
    mockQualityVersion({ version: 1, headline_score: 50, verified: true, panel_complete: true, report: null });
    render(withQuery(<QualityReport skillName="s" latestVersion={1} />));
    expect(await screen.findByText(/50/)).toBeInTheDocument();
    expect(screen.getByText(/detailed report not available/i)).toBeInTheDocument();
  });

  it("renders judge evidence when the report has it", async () => {
    mockQualityVersion({
      version: 1, headline_score: 50, verified: true, panel_complete: true,
      report: { tasks: [{ task_id: "t1", judge: [{ model: "m", winner: "with_skill", margin: 0.3, evidence: ["cited the contract"], votes: 2 }] }] },
    });
    render(withQuery(<QualityReport skillName="s" latestVersion={1} />));
    expect(await screen.findByText(/cited the contract/i)).toBeInTheDocument();
  });

  it("shows an unscored skill as unscored, not as a failure", async () => {
    mockQualityNotFound();
    render(withQuery(<QualityReport skillName="s" latestVersion={1} />));
    expect(await screen.findByText(/not scored/i)).toBeInTheDocument();
    expect(screen.queryByText(/error/i)).not.toBeInTheDocument();
  });

  it("never renders a spec version", async () => {
    mockQualityVersion({ version: 1, headline_score: 50, verified: true, panel_complete: true, report: { spec_version: 1 } });
    render(withQuery(<QualityReport skillName="s" latestVersion={1} />));
    expect(screen.queryByText(/spec version/i)).not.toBeInTheDocument();
  });

  it("sorts contract violations critical-first and counts the truncated remainder", async () => {
    const violations = Array.from({ length: 12 }, (_, i) => ({
      rule_id: `r${i}`,
      severity: i === 11 ? "critical" : "minor",
      message: `violation ${i}`,
    }));
    mockQualityVersion({
      version: 1, headline_score: 50, verified: true, panel_complete: true,
      report: { tasks: [{ task_id: "t1", drift: [{ violations }] }] },
    });
    render(withQuery(<QualityReport skillName="s" latestVersion={1} />));
    expect(await screen.findByText(/violation 11/)).toBeInTheDocument();
    expect(await screen.findByText(/2 more violations? not shown/i)).toBeInTheDocument();
  });

  // panel_matrix is a JSON ARRAY of MemberReport (internal/quality/ingest.go
  // marshals `members` directly), not an object keyed by member. A test
  // using the wrong shape is exactly what let a raw-JSON-dump regression
  // through once already.
  it("renders panel_matrix as one row per array element, keyed by agent/model", async () => {
    mockQuality({
      version: 3,
      panel_matrix: [
        {
          member: { agent: "claude-code", model: "strong" },
          pillars: { TriggerF1: 0.9 },
          effectiveness: 82.4,
          drift_grade: "A",
          healthy: true,
        },
        {
          member: { agent: "codex", model: "floor" },
          effectiveness: 10.1,
          drift_grade: "D",
          healthy: false,
          detail: "sandbox crashed on task 3",
        },
      ],
    });
    render(withQuery(<QualityReport skillName="s" latestVersion={3} />));
    expect(await screen.findByText("claude-code/strong")).toBeInTheDocument();
    expect(screen.getByText("codex/floor")).toBeInTheDocument();
    // An unhealthy member contributed nothing to the headline — that must
    // read as unhealthy, never as its (possibly low, possibly zero)
    // effectiveness number.
    expect(screen.getByText(/unhealthy/i)).toBeInTheDocument();
    expect(screen.getByText(/sandbox crashed on task 3/)).toBeInTheDocument();
    // 0-100 scale, formatted via the same formatDriftScale used elsewhere
    // in this file — not a bare String(v) and not a [0,1] percentage.
    expect(screen.getByText("82.4")).toBeInTheDocument();
    expect(screen.queryByText(/^10\.1$/)).not.toBeInTheDocument();
    expect(screen.queryByText("8240.0%")).not.toBeInTheDocument();
  });

  // pillar_breakdown is keyed "agent/model" (memberKey, ingest.go:189); its
  // values are score.Pillars, which has no JSON tags and so serialises with
  // capitalised Go field names.
  it("renders pillar_breakdown's capitalised Go field names as labelled columns", async () => {
    mockQuality({
      version: 3,
      pillar_breakdown: {
        "claude-code/strong": {
          TriggerF1: 0.9,
          Reliability: 0.8,
          Uplift: 0.7,
          Efficiency: 0.6,
        },
      },
    });
    render(withQuery(<QualityReport skillName="s" latestVersion={3} />));
    expect(await screen.findByText("claude-code/strong")).toBeInTheDocument();
    expect(screen.getByText("Trigger F1")).toBeInTheDocument();
    expect(screen.getByText("90%")).toBeInTheDocument();
    expect(screen.getByText("80%")).toBeInTheDocument();
    expect(screen.getByText("70%")).toBeInTheDocument();
    expect(screen.getByText("60%")).toBeInTheDocument();
  });

  // drift_breakdown is keyed the same way; its values (drift.Agg) are also
  // untagged AND on a 0-100 scale, not [0,1] — this exact field has already
  // been run through a [0,1] percentage formatter once and rendered 87.5 as
  // "8750.0%". It must never share a formatter with the pillar rates.
  it("renders drift_breakdown's 0-100 scale values without a percentage formatter", async () => {
    mockQuality({
      version: 3,
      drift_breakdown: {
        "claude-code/strong": { Mean: 87.5, Worst: 60.2, Sigma: 4.1, N: 5 },
      },
    });
    render(withQuery(<QualityReport skillName="s" latestVersion={3} />));
    expect(await screen.findByText("87.5")).toBeInTheDocument();
    expect(screen.queryByText(/8750/)).not.toBeInTheDocument();
    expect(screen.queryByText("87.5%")).not.toBeInTheDocument();
  });

  // N is a count of runs, not a 0-100 measurement — it must render as a
  // plain integer ("6"), not run through formatDriftScale like Mean/Worst/
  // Sigma ("6.0").
  it("renders drift_breakdown's N as an integer, not a decimal", async () => {
    mockQuality({
      version: 3,
      drift_breakdown: {
        "claude-code/strong": { Mean: 87.5, Worst: 60.2, Sigma: 4.1, N: 6 },
      },
    });
    render(withQuery(<QualityReport skillName="s" latestVersion={3} />));
    expect(await screen.findByText("6")).toBeInTheDocument();
    expect(screen.queryByText("6.0")).not.toBeInTheDocument();
  });

  // judge_kappa is a *float64 in Go (report.go:135): nil means no judge was
  // calibrated, a different fact from a judge calibrated at κ=0. It lives on
  // `report`, alongside judge_labeled_by as provenance.
  it("renders judge_kappa and judge_labeled_by from the report", async () => {
    mockQualityVersion({
      version: 1, headline_score: 50, verified: true, panel_complete: true,
      report: { judge_kappa: 0.75, judge_labeled_by: "author", tasks: [] },
    });
    render(withQuery(<QualityReport skillName="s" latestVersion={1} />));
    expect(await screen.findByText(/0\.75/)).toBeInTheDocument();
    expect(screen.getByText(/labeled by author/i)).toBeInTheDocument();
  });

  it("says a null judge_kappa was not measured, never zero — and can be negative when present", async () => {
    mockQualityVersion({
      version: 1, headline_score: 50, verified: true, panel_complete: true,
      robustness_gap: 1.2, drift_grade: "C",
      report: { judge_kappa: null, tasks: [] },
    });
    render(withQuery(<QualityReport skillName="s" latestVersion={1} />));
    expect(await screen.findByText(/not measured/i)).toBeInTheDocument();
    expect(screen.queryByText(/κ = 0$/)).not.toBeInTheDocument();
  });

  it("renders a negative judge_kappa as negative, not clamped to zero", async () => {
    mockQualityVersion({
      version: 1, headline_score: 50, verified: true, panel_complete: true,
      report: { judge_kappa: -0.2, tasks: [] },
    });
    render(withQuery(<QualityReport skillName="s" latestVersion={1} />));
    expect(await screen.findByText(/-0\.20/)).toBeInTheDocument();
  });

  // A failed detail fetch (500, network drop) must not be indistinguishable
  // from a legitimate `report: null` — one is "nothing to show", the other
  // is "we couldn't ask".
  it("distinguishes a failed report fetch from a genuine null report", async () => {
    const record = { ...DEFAULT_RECORD, version: 1, headline_score: 50 };
    server.use(
      http.get("/api/skills/:name/quality", () => HttpResponse.json(record)),
      http.get("/api/skills/:name/quality/:version", () =>
        HttpResponse.json({ detail: "internal error" }, { status: 500 }),
      ),
    );
    render(withQuery(<QualityReport skillName="s" latestVersion={1} />));
    expect(await screen.findByText(/could not load the detailed report/i)).toBeInTheDocument();
    expect(screen.queryByText(/detailed report not available/i)).not.toBeInTheDocument();
  });
});

function mockSeries(series: Series[]) {
  server.use(
    http.get("/api/skills/:name/quality/series", () =>
      HttpResponse.json({ series }),
    ),
  );
}

describe("QualityTrend", () => {
  it("charts the current series", async () => {
    mockSeries([
      {
        key: "s0",
        current: true,
        reason: "",
        points: [
          { version: 3, headline_score: 74, headline_ci_low: 70, headline_ci_high: 78, verified: true, scored_at: "2026-07-01T00:00:00Z" },
          { version: 4, headline_score: 78, headline_ci_low: 74, headline_ci_high: 82, verified: true, scored_at: "2026-08-01T00:00:00Z" },
        ],
      },
    ]);
    render(withQuery(<QualityTrend skillName="s" />));
    expect(await screen.findByText(/v3/)).toBeInTheDocument();
    expect(screen.getByText(/v4/)).toBeInTheDocument();
  });

  it("does not chart an incomparable series, and says why", async () => {
    mockSeries([
      {
        key: "s0",
        current: true,
        reason: "",
        points: [
          { version: 4, headline_score: 78, headline_ci_low: 74, headline_ci_high: 82, verified: true, scored_at: "2026-08-01T00:00:00Z" },
        ],
      },
      {
        key: "s1",
        current: false,
        reason: "different model panels: a score change could be the models rather than the skill",
        points: [
          { version: 3, headline_score: 30, headline_ci_low: 20, headline_ci_high: 40, verified: true, scored_at: "2026-07-01T00:00:00Z" },
        ],
      },
    ]);
    const { container } = render(withQuery(<QualityTrend skillName="s" />));
    expect(await screen.findByText(/different model panels/i)).toBeInTheDocument();
    // The 30 must not appear as a plotted point on the current line.
    expect(container.querySelectorAll("[data-point]")).toHaveLength(1);
  });

  it("says a single score is not yet a trend", async () => {
    mockSeries([
      {
        key: "s0",
        current: true,
        reason: "",
        points: [
          { version: 1, headline_score: 50, headline_ci_low: 40, headline_ci_high: 60, verified: true, scored_at: "2026-08-01T00:00:00Z" },
        ],
      },
    ]);
    render(withQuery(<QualityTrend skillName="s" />));
    expect(await screen.findByText(/one score so far/i)).toBeInTheDocument();
  });

  it("renders nothing loud when there is no history", async () => {
    mockSeries([]);
    render(withQuery(<QualityTrend skillName="s" />));
    expect(await screen.findByText(/no scores yet/i)).toBeInTheDocument();
  });

  it("selects the current series by its flag, not by position", async () => {
    mockSeries([
      {
        key: "s0",
        current: false,
        reason: "different model panels: a score change could be the models rather than the skill",
        points: [
          { version: 2, headline_score: 10, headline_ci_low: 5, headline_ci_high: 15, verified: true, scored_at: "2026-06-01T00:00:00Z" },
        ],
      },
      {
        key: "s1",
        current: true,
        reason: "",
        points: [
          { version: 3, headline_score: 74, headline_ci_low: 70, headline_ci_high: 78, verified: true, scored_at: "2026-07-01T00:00:00Z" },
          { version: 4, headline_score: 78, headline_ci_low: 74, headline_ci_high: 82, verified: true, scored_at: "2026-08-01T00:00:00Z" },
        ],
      },
    ]);
    const { container } = render(withQuery(<QualityTrend skillName="s" />));
    expect(await screen.findByText(/different model panels/i)).toBeInTheDocument();
    const points = container.querySelectorAll("[data-point]");
    expect(points).toHaveLength(2);
    const titles = Array.from(points).map((p) => p.getAttribute("title") ?? p.querySelector("title")?.textContent);
    expect(titles).toEqual(["v3 · 74", "v4 · 78"]);
  });

  it("renders the quiet no-trend state when no series is flagged current", async () => {
    mockSeries([
      {
        key: "s0",
        current: false,
        reason: "different task suites: a score change could be the tasks rather than the skill",
        points: [
          { version: 2, headline_score: 10, headline_ci_low: 5, headline_ci_high: 15, verified: true, scored_at: "2026-06-01T00:00:00Z" },
        ],
      },
      {
        key: "s1",
        current: false,
        reason: "different model panels: a score change could be the models rather than the skill",
        points: [
          { version: 3, headline_score: 74, headline_ci_low: 70, headline_ci_high: 78, verified: true, scored_at: "2026-07-01T00:00:00Z" },
        ],
      },
    ]);
    const { container } = render(withQuery(<QualityTrend skillName="s" />));
    expect(await screen.findByText(/no scores yet/i)).toBeInTheDocument();
    expect(container.querySelectorAll("[data-point]")).toHaveLength(0);
  });
});
