import { describe, it, expect, vi } from "vitest";
import { handleUnauthorized } from "./query";

function res(status: number, url: string): Response {
  return { status, url } as Response;
}

describe("handleUnauthorized", () => {
  it("redirects to /login on 401 from API routes", () => {
    const redirect = vi.fn();
    handleUnauthorized(res(401, "http://x/api/skills"), redirect);
    expect(redirect).toHaveBeenCalledWith("/login");
  });

  it("ignores 401 from auth endpoints (login page probes /api/auth/me)", () => {
    const redirect = vi.fn();
    handleUnauthorized(res(401, "http://x/api/auth/me"), redirect);
    expect(redirect).not.toHaveBeenCalled();
  });

  it("ignores non-401 responses", () => {
    const redirect = vi.fn();
    handleUnauthorized(res(500, "http://x/api/skills"), redirect);
    expect(redirect).not.toHaveBeenCalled();
  });
});
