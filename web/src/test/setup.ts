import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterAll, afterEach, beforeAll } from "vitest";
import { server } from "./handlers";
import { client } from "@/api/client.gen";

// Configure SDK client baseUrl so Request() gets absolute URLs in jsdom/Node
client.setConfig({ baseUrl: "http://localhost:3000" });

// Stub IntersectionObserver which jsdom does not implement
class MockIntersectionObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.IntersectionObserver =
  MockIntersectionObserver as unknown as typeof IntersectionObserver;

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => {
  cleanup();
  server.resetHandlers();
});
afterAll(() => server.close());
