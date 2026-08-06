import { describe, it, expect } from "vitest";
import { http, HttpResponse } from "msw";
import { server } from "@/test/handlers";
import { renderWithProviders, screen, waitFor, userEvent } from "@/test/render";
import { Ownership } from "./ownership";
import { UserPicker } from "./user-picker";

type MockMember = { id: string; name: string; email: string };

function mockRules(rules: Array<{ id: string; pattern: string; members: MockMember[] }>) {
  server.use(
    http.get("/api/ownership/rules", () => {
      return HttpResponse.json({ rules });
    }),
  );
}

// Mirrors the real server's declared limits (internal/skill/routes.go:
// `Limit int \`query:"limit" default:"20" minimum:"1" maximum:"100"\``) —
// a request over 100 gets a 422 here exactly like it would from Huma's own
// request validation, so a client that ever asks for more fails the test
// instead of silently degrading.
const SERVER_MAX_LIMIT = 100;

function mockSkillsWithOwners(
  skills: Array<{ name: string; unowned: boolean; rule_pattern?: string }>,
  requestedLimits: number[] = [],
) {
  server.use(
    http.get("/api/skills", ({ request }) => {
      const url = new URL(request.url);
      const limit = Number(url.searchParams.get("limit") ?? "20");
      const offset = Number(url.searchParams.get("offset") ?? "0");
      requestedLimits.push(limit);
      if (limit > SERVER_MAX_LIMIT) {
        return HttpResponse.json(
          { detail: `limit must be <= ${SERVER_MAX_LIMIT}` },
          { status: 422 },
        );
      }
      const page = skills.slice(offset, offset + limit).map((s, i) => ({
        id: `skill-${offset + i}`,
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
      }));
      return HttpResponse.json({ skills: page, total: skills.length });
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
  it("lists rules with their hydrated member names", async () => {
    mockRules([
      {
        id: "rule-1",
        pattern: "payments:*",
        members: [
          { id: "user-alice-001", name: "Alice Anderson", email: "alice@test.com" },
          { id: "user-bob-002", name: "Bob Baker", email: "bob@test.com" },
        ],
      },
      {
        id: "rule-2",
        pattern: "docs:readme",
        members: [{ id: "user-carol-003", name: "Carol Chen", email: "carol@test.com" }],
      },
    ]);
    mockSkillsWithOwners([]);

    renderWithProviders(<Ownership />);

    expect(await screen.findByText("payments:*")).toBeInTheDocument();
    expect(screen.getByText("docs:readme")).toBeInTheDocument();

    // Members are rendered by name, not as a truncated raw id — a rule with
    // members must never make the reviewer chase a UUID to know who it is.
    expect(screen.getByText("Alice Anderson")).toBeInTheDocument();
    expect(screen.getByText("Bob Baker")).toBeInTheDocument();
    expect(screen.getByText("Carol Chen")).toBeInTheDocument();
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

  // Regression for a real bug: fetching the full skill list with a single
  // `limit: 1000` request 422s against the real server, which enforces
  // `maximum:"100"` (internal/skill/routes.go) via Huma request validation
  // before the handler even runs — a constraint this MSW mock now also
  // enforces, so a client that ever asks for more than the server's max
  // fails loudly here instead of silently showing "0 unowned" in
  // production.
  it("never requests more skills per page than the server's declared maximum, and pages through all of them", async () => {
    const requestedLimits: number[] = [];
    const skills = Array.from({ length: 130 }, (_, i) => ({
      name: `skill-${i}`,
      // Every 10th skill is unowned — 13 total across 130 skills.
      unowned: i % 10 === 0,
    }));
    mockRules([]);
    mockSkillsWithOwners(skills, requestedLimits);

    renderWithProviders(<Ownership />);

    await waitFor(() => {
      expect(screen.getByText("13")).toBeInTheDocument();
    });

    expect(requestedLimits.length).toBeGreaterThan(1); // it took more than one page
    for (const limit of requestedLimits) {
      expect(limit).toBeLessThanOrEqual(100);
    }
  });

  it("shows an error rather than a false '0' when the skill list can't be loaded", async () => {
    mockRules([]);
    server.use(
      http.get("/api/skills", () => {
        return HttpResponse.json({ detail: "internal server error" }, { status: 500 });
      }),
    );

    renderWithProviders(<Ownership />);

    expect(await screen.findByText(/couldn't load/i)).toBeInTheDocument();
    expect(screen.queryByText("0")).not.toBeInTheDocument();
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
    mockRules([
      {
        id: "rule-1",
        pattern: "payments:*",
        members: [{ id: "user-alice-001", name: "Alice Anderson", email: "alice@test.com" }],
      },
    ]);
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
