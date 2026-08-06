import { describe, it, expect } from "vitest";
import { http, HttpResponse } from "msw";
import { server } from "@/test/handlers";
import { renderWithProviders, screen, waitFor, userEvent } from "@/test/render";
import { Ownership } from "./ownership";
import { UserPicker } from "./user-picker";

function mockRules(rules: Array<{ id: string; pattern: string; members: string[] }>) {
  server.use(
    http.get("/api/ownership/rules", () => {
      return HttpResponse.json({ rules });
    }),
  );
}

function mockSkillsWithOwners(
  skills: Array<{ name: string; unowned: boolean; rule_pattern?: string }>,
) {
  server.use(
    http.get("/api/skills", () => {
      return HttpResponse.json({
        skills: skills.map((s, i) => ({
          id: `skill-${i}`,
          name: s.name,
          description: "",
          frontmatter: {},
          author: "",
          license: "",
          compatibility: "",
          spec_compliance: "",
          tags: [],
          latest_version: 1,
          reviewed_at: null,
          reviewed_by: "",
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
        })),
        total: skills.length,
      });
    }),
    http.get("/api/skills/:name/owners", ({ params }) => {
      const s = skills.find((sk) => sk.name === params.name);
      if (!s) {
        return HttpResponse.json({ owners: [], unowned: true });
      }
      return HttpResponse.json({
        owners: [],
        unowned: s.unowned,
        rule_pattern: s.rule_pattern ?? "",
      });
    }),
  );
}

describe("Ownership settings page", () => {
  it("lists rules with their members", async () => {
    mockRules([
      { id: "rule-1", pattern: "payments:*", members: ["user-alice-001", "user-bob-002"] },
      { id: "rule-2", pattern: "docs:readme", members: ["user-carol-003"] },
    ]);
    mockSkillsWithOwners([]);

    renderWithProviders(<Ownership />);

    expect(await screen.findByText("payments:*")).toBeInTheDocument();
    expect(screen.getByText("docs:readme")).toBeInTheDocument();

    // Members are rendered (as identifiers — the rules API returns member
    // ids, not names) so a rule with members never reads as empty.
    expect(screen.getByText(/user-ali/)).toBeInTheDocument();
    expect(screen.getByText(/user-bob/)).toBeInTheDocument();
    expect(screen.getByText(/user-car/)).toBeInTheDocument();
  });

  it("shows the unowned backlog count and links to the filtered list", async () => {
    mockRules([]);
    mockSkillsWithOwners([
      { name: "owned-skill", unowned: false, rule_pattern: "owned-skill" },
      { name: "unowned-skill-a", unowned: true },
      { name: "unowned-skill-b", unowned: true },
    ]);

    renderWithProviders(<Ownership />);

    await waitFor(() => {
      expect(screen.getByText("2")).toBeInTheDocument();
    });

    const link = screen.getByRole("link", { name: /view unowned skills/i });
    expect(link).toHaveAttribute("href", "/?unowned=true");
  });

  // A member who cannot manage a rule sees the control disabled WITH THE
  // REASON, not hidden — the same call Phase 6 made for the eval button, so
  // people can see the capability exists and go ask.
  it("disables manage with a reason rather than hiding it", async () => {
    server.use(
      http.get("/api/auth/me", () => {
        return HttpResponse.json({
          id: "user-002",
          email: "dana@test.com",
          name: "Dana Dev",
          role: "member",
        });
      }),
    );
    mockRules([{ id: "rule-1", pattern: "payments:*", members: ["user-alice-001"] }]);
    mockSkillsWithOwners([]);

    renderWithProviders(<Ownership />);

    const manageBtn = await screen.findByRole("button", { name: /manage/i });
    expect(manageBtn).toBeDisabled();
    expect(screen.getByText(/only an owner of this namespace/i)).toBeInTheDocument();
  });

  // The typeahead → /api/users/search, debounced, minimum 2 characters
  // matching the server's floor exactly.
  it("searches users by name or email in the picker", async () => {
    let searchCalls: string[] = [];
    server.use(
      http.get("/api/users/search", ({ request }) => {
        const url = new URL(request.url);
        const q = url.searchParams.get("q") ?? "";
        searchCalls.push(q);
        if (q.length < 2) {
          return HttpResponse.json({ users: [] });
        }
        return HttpResponse.json({
          users: [{ id: "user-eve-004", name: "Eve Example", email: "eve@test.com" }],
        });
      }),
    );

    const user = userEvent.setup();
    renderWithProviders(<UserPicker onSelect={() => {}} />);

    const input = screen.getByLabelText(/search users/i);

    // A single character never fires a request the server would ignore.
    await user.type(input, "e");
    await waitFor(() => {
      expect(screen.getByText(/type at least 2 characters/i)).toBeInTheDocument();
    });
    expect(searchCalls).not.toContain("e");

    // Two characters, after the debounce, does.
    await user.type(input, "ve");
    expect(await screen.findByText("Eve Example")).toBeInTheDocument();
    expect(screen.getByText("eve@test.com")).toBeInTheDocument();
  });
});
