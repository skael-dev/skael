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

  // Analytics
  http.get("/api/analytics/overview", () => {
    return HttpResponse.json(mockOverview);
  }),

  http.get("/api/analytics/skills", () => {
    return HttpResponse.json(mockSkillAnalytics);
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
