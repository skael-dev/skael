import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect } from "vitest";
import { http, HttpResponse } from "msw";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { server } from "@/test/handlers";
import { QualityBadge } from "./quality-badge";
import { EvalStatus } from "./eval-status";
import { QualityReport } from "./quality-report";
import type { JobOutput, RecordOutput } from "@/api/types.gen";

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
  return <QueryClientProvider client={qc}>{ui}</QueryClientProvider>;
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
  panel_matrix: {},
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
    expect(await screen.findByText(/74\.2/)).toBeInTheDocument();
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
});
