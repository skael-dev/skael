import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import {
  mockUser,
  mockSkills,
  mockSkillAnalytics,
  mockOverview,
  mockActivations,
  mockVersions,
  mockApiKeys,
  mockScanReport,
  mockTeamUsers,
} from "./fixtures";

export const handlers = [
  // Auth
  http.get("/api/auth/me", () => {
    return HttpResponse.json(mockUser);
  }),

  http.post("/api/auth/login", async ({ request }) => {
    const body = (await request.json()) as { email: string; password: string };
    if (body.email === "admin@test.com" && body.password === "password123") {
      return HttpResponse.json(mockUser);
    }
    return HttpResponse.json(
      { detail: "Invalid credentials" },
      { status: 401 },
    );
  }),

  http.post("/api/auth/signup", async ({ request }) => {
    const body = (await request.json()) as {
      email: string;
      name: string;
      password: string;
    };
    return HttpResponse.json(
      {
        id: "user-new",
        email: body.email,
        name: body.name,
        role: "member",
      },
      { status: 201 },
    );
  }),

  http.post("/api/auth/logout", () => {
    return new HttpResponse(null, { status: 204 });
  }),

  // API keys
  http.get("/api/auth/keys", () => {
    return HttpResponse.json({ keys: mockApiKeys });
  }),

  http.post("/api/auth/keys", async ({ request }) => {
    const body = (await request.json()) as { name: string };
    return HttpResponse.json(
      {
        id: "key-new",
        name: body.name,
        prefix: "sk_live_new",
        key: "sk_live_new_supersecretfullkey123456",
        created_at: new Date().toISOString(),
      },
      { status: 201 },
    );
  }),

  http.delete("/api/auth/keys/:id", () => {
    return new HttpResponse(null, { status: 204 });
  }),

  // Team (owner only)
  http.get("/api/admin/users", () => {
    return HttpResponse.json({ users: mockTeamUsers });
  }),

  http.put("/api/admin/users/:id/role", async ({ request, params }) => {
    const body = (await request.json()) as { role: string };
    const user = mockTeamUsers.find((u) => u.id === params.id);
    if (!user) {
      return HttpResponse.json({ detail: "user not found" }, { status: 404 });
    }
    if (user.role === "owner") {
      return HttpResponse.json(
        { detail: "the owner's role cannot be changed" },
        { status: 403 },
      );
    }
    if (body.role !== "admin" && body.role !== "member") {
      return HttpResponse.json(
        { detail: 'role must be "admin" or "member"' },
        { status: 422 },
      );
    }
    return HttpResponse.json({ ...user, role: body.role });
  }),

  http.post("/api/admin/reset-password", () => {
    return HttpResponse.json({ temporary_password: "temp-password-123" });
  }),

  // Analytics
  http.get("/api/analytics/overview", () => {
    return HttpResponse.json(mockOverview);
  }),

  http.get("/api/analytics/skills", ({ request }) => {
    const url = new URL(request.url);
    const q = (url.searchParams.get("q") ?? "").toLowerCase();
    const limit = Number(url.searchParams.get("limit") ?? "50");
    const offset = Number(url.searchParams.get("offset") ?? "0");
    let items = mockSkillAnalytics as Array<{ name: string; description?: string | null }>;
    if (q) {
      items = items.filter(
        (s) =>
          s.name.toLowerCase().includes(q) ||
          (s.description ?? "").toLowerCase().includes(q),
      );
    }
    const total = items.length;
    const page = items.slice(offset, offset + limit);
    return HttpResponse.json({ skills: page, total });
  }),

  http.get("/api/skills/tags", () => {
    return HttpResponse.json({ tags: [] });
  }),

  // Skills
  http.get("/api/skills", () => {
    return HttpResponse.json({ skills: mockSkills, total: mockSkills.length });
  }),

  http.get("/api/skills/review", () => {
    // This path would conflict with /api/skills/:name — MSW matches in order
    // so this handler must come before the :name handler.
    return HttpResponse.json({ reviewed: 2 });
  }),

  http.get("/api/skills/:name", ({ params }) => {
    const skill = mockSkills.find((s) => s.name === params.name);
    if (!skill) {
      return HttpResponse.json({ detail: "skill not found" }, { status: 404 });
    }
    return HttpResponse.json(skill);
  }),

  http.get("/api/skills/:name/activations", () => {
    return HttpResponse.json(mockActivations);
  }),

  http.get("/api/skills/:name/versions", () => {
    return HttpResponse.json({ versions: mockVersions });
  }),

  http.get("/api/skills/:name/scan", () => {
    return HttpResponse.json(mockScanReport);
  }),

  // Ownership (Task 11) and version diff (Task 13) are both raw Chi routes,
  // not in the generated SDK. Default to the "nothing to report" shape so
  // tests that don't care about either don't have to mock them — matches
  // the pattern already set for /quality/series above.
  http.get("/api/skills/:name/owners", () => {
    return HttpResponse.json({ owners: [], unowned: true });
  }),

  http.get("/api/skills/:name/versions/:version/diff", () => {
    return HttpResponse.json({ against: 0, skill_md: "", files: [] });
  }),

  // Default: no quality history. Tests exercising the trend override this
  // per-test with server.use(...).
  http.get("/api/skills/:name/quality/series", () => {
    return HttpResponse.json({ series: [] });
  }),

  http.put("/api/skills/review", () => {
    return HttpResponse.json({ reviewed: 2 });
  }),

  http.put("/api/skills/:name/review", ({ params }) => {
    const skill = mockSkills.find((s) => s.name === params.name);
    if (!skill) {
      return HttpResponse.json({ detail: "skill not found" }, { status: 404 });
    }
    return HttpResponse.json({
      ...skill,
      reviewed_at: new Date().toISOString(),
      reviewed_by: mockUser.email,
    });
  }),
];

export const server = setupServer(...handlers);
