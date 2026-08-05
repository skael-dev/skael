import { describe, it, expect } from "vitest";
import { http, HttpResponse } from "msw";
import { server } from "@/test/handlers";
import { renderWithProviders, screen, waitFor, userEvent } from "@/test/render";
import { SkillList } from "./skill-list";
import type { SkillAnalytics } from "@/api/types.gen";

// ── Test helpers for quality sort/filter assertions ─────────────
let lastQuery: Record<string, string> = {};

function mockAnalyticsSkills(skills: SkillAnalytics[]) {
  server.use(
    http.get("/api/analytics/skills", ({ request }) => {
      const url = new URL(request.url);
      lastQuery = Object.fromEntries(url.searchParams.entries());
      return HttpResponse.json({ skills, total: skills.length });
    }),
  );
}

function lastAnalyticsQuery() {
  return lastQuery;
}

function renderSkillList() {
  return renderWithProviders(<SkillList />);
}

describe("SkillList", () => {
  it("renders skill names from API data", async () => {
    renderWithProviders(<SkillList />);

    // The skill-list component fetches from /api/analytics/skills and renders SkillCard components
    // which display skill.name as text
    expect(await screen.findByText("code-review")).toBeInTheDocument();
    expect(screen.getByText("test-writer")).toBeInTheDocument();
    expect(screen.getByText("doc-generator")).toBeInTheDocument();
  });

  it("search input filters the list", async () => {
    const user = userEvent.setup();
    renderWithProviders(<SkillList />);

    // Wait for data to load
    expect(await screen.findByText("code-review")).toBeInTheDocument();

    // Type into the search input with placeholder "Filter skills..."
    const searchInput = screen.getByPlaceholderText("Filter skills...");
    await user.type(searchInput, "review");

    // Filtering is server-side + debounced; wait for the settled filtered state
    // (code-review present, others gone) to avoid the refetch loading gap.
    await waitFor(() => {
      expect(screen.getByText("code-review")).toBeInTheDocument();
      expect(screen.queryByText("test-writer")).not.toBeInTheDocument();
    });
    expect(screen.queryByText("doc-generator")).not.toBeInTheDocument();
  });

  it("shows onboarding when no skills exist", async () => {
    server.use(
      http.get("/api/analytics/skills", () => {
        return HttpResponse.json({ skills: [], total: 0 });
      }),
      http.get("/api/analytics/overview", () => {
        return HttpResponse.json({
          total_skills: 0,
          active_skills: 0,
          total_activations: 0,
          security: { clean: 0, warning: 0, critical: 0 },
        });
      }),
    );

    renderWithProviders(<SkillList />);

    // The Onboarding component shows "Welcome to Skael" heading
    expect(await screen.findByText("Welcome to Skael")).toBeInTheDocument();
  });

  it("loading skeleton appears before data loads", async () => {
    // Make the API response hang so the loading state is visible
    server.use(
      http.get("/api/analytics/skills", () => {
        return new Promise(() => {
          // Never resolve - keeps loading state
        });
      }),
    );

    const { container } = renderWithProviders(<SkillList />);

    // The loading skeleton renders Skeleton components with specific class
    // Look for the skeleton container structure
    await waitFor(() => {
      // The skeleton has multiple elements with the Skeleton component class
      const skeletons = container.querySelectorAll('[class*="animate-pulse"]');
      expect(skeletons.length).toBeGreaterThan(0);
    });
  });

  it("shows error state when skills endpoint returns 500", async () => {
    server.use(
      http.get("/api/analytics/skills", () => {
        return HttpResponse.json({ detail: "internal server error" }, { status: 500 });
      }),
    );

    renderWithProviders(<SkillList />);

    expect(await screen.findByText(/couldn't load skills/i)).toBeInTheDocument();
  });

  it("stat tiles show numbers from overview data", async () => {
    renderWithProviders(<SkillList />);

    // Wait for data to load and check stat tile values
    // The overview has total_activations: 468, active_skills: 2
    // StatTile renders value with toLocaleString()
    expect(await screen.findByText("468")).toBeInTheDocument();
    expect(screen.getByText("2")).toBeInTheDocument();

    // Check labels
    expect(screen.getByText("Invocations - 30d")).toBeInTheDocument();
    expect(screen.getByText("Active skills")).toBeInTheDocument();
    expect(screen.getByText("Needs attention")).toBeInTheDocument();
  });

  it("shows a quality badge on a scored card and a neutral mark on an unscored one", async () => {
    mockAnalyticsSkills([
      {
        name: "scored",
        description: "",
        author: "",
        spec_compliance: "full",
        activations: 0,
        unique_devs: 0,
        last_triggered: null,
        latest_version: 2,
        reviewed_at: null,
        security_status: "clean",
        tags: [],
        updated_at: "2026-05-01T10:00:00Z",
        quality: { version: 2, headline_score: 74, verified: true, panel_complete: true, scored_at: "2026-08-01T00:00:00Z" },
      } as SkillAnalytics,
      {
        name: "unscored",
        description: "",
        author: "",
        spec_compliance: "full",
        activations: 0,
        unique_devs: 0,
        last_triggered: null,
        latest_version: 1,
        reviewed_at: null,
        security_status: "clean",
        tags: [],
        updated_at: "2026-05-01T10:00:00Z",
      } as SkillAnalytics,
    ]);
    renderSkillList();
    expect(await screen.findByText("74")).toBeInTheDocument();
    expect(screen.getByTitle(/not yet scored/i)).toBeInTheDocument();
  });

  it("asks the server to sort by score", async () => {
    mockAnalyticsSkills([
      {
        name: "code-review",
        description: "",
        author: "",
        spec_compliance: "full",
        activations: 0,
        unique_devs: 0,
        last_triggered: null,
        latest_version: 1,
        reviewed_at: null,
        security_status: "clean",
        tags: [],
        updated_at: "2026-05-01T10:00:00Z",
      } as SkillAnalytics,
    ]);
    renderSkillList();
    await screen.findByText("code-review");
    await userEvent.selectOptions(screen.getByLabelText(/sort/i), "quality");
    await waitFor(() => {
      expect(lastAnalyticsQuery()).toMatchObject({ sort: "quality" });
    });
  });

  it("asks the server for unscored skills only", async () => {
    mockAnalyticsSkills([
      {
        name: "code-review",
        description: "",
        author: "",
        spec_compliance: "full",
        activations: 0,
        unique_devs: 0,
        last_triggered: null,
        latest_version: 1,
        reviewed_at: null,
        security_status: "clean",
        tags: [],
        updated_at: "2026-05-01T10:00:00Z",
      } as SkillAnalytics,
    ]);
    renderSkillList();
    await screen.findByText("code-review");
    await userEvent.click(screen.getByRole("button", { name: /unscored only/i }));
    await waitFor(() => {
      expect(lastAnalyticsQuery()).toMatchObject({ scored: "no" });
    });
  });
});
