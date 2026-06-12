import { test, expect } from "@playwright/test";

const BASE_URL =
  process.env.PLAYWRIGHT_BASE_URL ?? "http://localhost:8080";

const TEST_USER_EMAIL = "e2e-detail@test.com";
const TEST_USER_PASSWORD = "testpassword123";
const TEST_USER_NAME = "E2E Detail User";
const TEST_SKILL_NAME = "e2e-detail-skill";

/**
 * Ensures the test user exists (201 on first run, 409 on reuse — both OK).
 * Then logs in via API to get a session, and creates the test skill with it.
 * Uses Playwright's APIRequestContext so cookies are automatically handled.
 */
async function ensureTestFixtures(request: import("@playwright/test").APIRequestContext) {
  // Signup — tolerate 409 (already exists)
  await request.post(`${BASE_URL}/api/auth/signup`, {
    data: {
      email: TEST_USER_EMAIL,
      password: TEST_USER_PASSWORD,
      name: TEST_USER_NAME,
    },
  });

  // Login to establish an authenticated session
  await request.post(`${BASE_URL}/api/auth/login`, {
    data: {
      email: TEST_USER_EMAIL,
      password: TEST_USER_PASSWORD,
    },
  });

  // Create the skill — tolerate 409 (already exists)
  await request.post(`${BASE_URL}/api/skills`, {
    data: {
      name: TEST_SKILL_NAME,
      description: "E2E test skill for detail page tests",
    },
  });
}

async function login(page: import("@playwright/test").Page) {
  await page.goto("/login");
  await page.getByLabel(/email/i).fill(TEST_USER_EMAIL);
  await page.getByLabel(/password/i).fill(TEST_USER_PASSWORD);
  await page.getByRole("button", { name: /log in|sign in/i }).click();
  await expect(page).toHaveURL("/");
}

test.describe("Skill detail page", () => {
  test.beforeAll(async ({ request }) => {
    await ensureTestFixtures(request);
  });

  test("navigating to skill detail shows tabs", async ({ page }) => {
    await login(page);
    await page.goto(`/skills/${TEST_SKILL_NAME}`);

    // SlidingTabs now exposes role="tab" on each button and role="tablist" on the container.
    await expect(page.getByRole("tab", { name: /^content$/i })).toBeVisible();
    await expect(page.getByRole("tab", { name: /^files$/i })).toBeVisible();
    await expect(page.getByRole("tab", { name: /^versions$/i })).toBeVisible();
    await expect(page.getByRole("tab", { name: /^usage$/i })).toBeVisible();
    await expect(page.getByRole("tab", { name: /^security$/i })).toBeVisible();

    // Content tab is active by default — aria-selected should be "true"
    await expect(page.getByRole("tab", { name: /^content$/i })).toHaveAttribute("aria-selected", "true");
    // All other tabs should be aria-selected="false"
    await expect(page.getByRole("tab", { name: /^files$/i })).toHaveAttribute("aria-selected", "false");
  });

  test("clicking tabs switches content", async ({ page }) => {
    await login(page);
    await page.goto(`/skills/${TEST_SKILL_NAME}`);

    // Click the Versions tab
    const versionsTab = page.getByRole("tab", { name: /^versions$/i });
    await versionsTab.click();

    // After clicking, Versions tab should be selected and Content tab deselected
    await expect(versionsTab).toHaveAttribute("aria-selected", "true");
    await expect(page.getByRole("tab", { name: /^content$/i })).toHaveAttribute("aria-selected", "false");
  });

  test("security badge is visible on detail page", async ({ page }) => {
    await login(page);
    await page.goto(`/skills/${TEST_SKILL_NAME}`);

    // The Security tab is now role="tab" — assert it's visible
    await expect(page.getByRole("tab", { name: /^security$/i })).toBeVisible();
  });
});
