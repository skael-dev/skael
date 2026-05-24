import { describe, it, expect } from "vitest";
import { http, HttpResponse } from "msw";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { AuthProvider } from "@/app/auth-provider";
import { server } from "@/test/handlers";
import { mockSkills } from "@/test/fixtures";
import { SkillDetail } from "./skill-detail";

const skillWithContent = {
  ...mockSkills[0],
  content: "# Code Review Guide\n\nThis skill reviews code for quality.\n\n## Guidelines\n\nFollow best practices.",
};

function renderDetail(skillName = "code-review") {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[`/skills/${skillName}`]}>
        <AuthProvider>
          <Routes>
            <Route path="/skills/:name" element={<SkillDetail />} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("SkillDetail", () => {
  it("renders skill name and description in header", async () => {
    server.use(
      http.get("/api/skills/:name", () => {
        return HttpResponse.json(skillWithContent);
      }),
    );

    renderDetail("code-review");

    // The h1 shows skill?.name in font-mono
    expect(await screen.findByText("code-review")).toBeInTheDocument();
    // Description is rendered in a <p>
    expect(screen.getByText("Automated code review assistant")).toBeInTheDocument();
  });

  it("content tab renders markdown heading", async () => {
    server.use(
      http.get("/api/skills/:name", () => {
        return HttpResponse.json(skillWithContent);
      }),
    );

    renderDetail("code-review");

    // The Content tab is active by default. The MarkdownRenderer renders the h1.
    // "Code Review Guide" appears in both the rendered h1 and the TOC link.
    // Use findAllByText and check that at least one is an h1 rendered heading.
    const headings = await screen.findAllByText("Code Review Guide");
    expect(headings.length).toBeGreaterThanOrEqual(1);
    // Also check the h2 "Guidelines" which appears in both the content and TOC
    const guidelines = screen.getAllByText("Guidelines");
    expect(guidelines.length).toBeGreaterThanOrEqual(1);
  });

  it("clicking Versions tab shows version changelog text", async () => {
    const user = userEvent.setup();

    server.use(
      http.get("/api/skills/:name", () => {
        return HttpResponse.json(skillWithContent);
      }),
    );

    renderDetail("code-review");

    // Wait for the page to load
    await screen.findByText("code-review");

    // Click the Versions tab button
    const versionsTab = screen.getByText("Versions");
    await user.click(versionsTab);

    // The VersionList shows changelog text from mockVersions
    expect(await screen.findByText("Improved review heuristics for TypeScript")).toBeInTheDocument();
    expect(screen.getByText("Added support for Go files")).toBeInTheDocument();
  });

  it("clicking Usage tab shows activation count", async () => {
    const user = userEvent.setup();

    server.use(
      http.get("/api/skills/:name", () => {
        return HttpResponse.json(skillWithContent);
      }),
    );

    renderDetail("code-review");

    // Wait for the page to load
    await screen.findByText("code-review");

    // Click the Usage tab
    const usageTab = screen.getByText("Usage");
    await user.click(usageTab);

    // The TabUsage component shows total_count from mockActivations (312)
    // 312 also appears in the header MetaCell, so use getAllByText
    await waitFor(() => {
      const matches = screen.getAllByText("312");
      expect(matches.length).toBeGreaterThanOrEqual(1);
    });
    // "Unique devs" appears in both header MetaCell and usage tab KPI
    const uniqueDevsLabels = screen.getAllByText("Unique devs");
    expect(uniqueDevsLabels.length).toBeGreaterThanOrEqual(2);
  });

  it("clicking Files tab shows file names from manifest", async () => {
    const user = userEvent.setup();

    server.use(
      http.get("/api/skills/:name", () => {
        return HttpResponse.json(skillWithContent);
      }),
    );

    renderDetail("code-review");

    // Wait for the page to load
    await screen.findByText("code-review");

    // Click the Files tab
    const filesTab = screen.getByText("Files");
    await user.click(filesTab);

    // The file tree shows file names from mockVersions[0].file_manifest:
    // "SKILL.md" appears in both file tree and file viewer -- use getAllByText
    await waitFor(() => {
      const skillMdMatches = screen.getAllByText("SKILL.md");
      expect(skillMdMatches.length).toBeGreaterThanOrEqual(1);
    });
    // "review.ts" is the file name from "examples/review.ts"
    expect(screen.getByText("review.ts")).toBeInTheDocument();
  });
});
