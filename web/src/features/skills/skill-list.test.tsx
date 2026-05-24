import { describe, it, expect } from "vitest";
import { http, HttpResponse } from "msw";
import { server } from "@/test/handlers";
import { renderWithProviders, screen, waitFor, userEvent } from "@/test/render";
import { SkillList } from "./skill-list";

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

    // code-review matches "review" in its name; others should not appear
    expect(screen.getByText("code-review")).toBeInTheDocument();
    expect(screen.queryByText("test-writer")).not.toBeInTheDocument();
    expect(screen.queryByText("doc-generator")).not.toBeInTheDocument();
  });

  it("shows onboarding when no skills exist", async () => {
    server.use(
      http.get("/api/analytics/skills", () => {
        return HttpResponse.json([]);
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
});
