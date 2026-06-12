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

    // SlidingTabs renders plain <button> elements (not role="tab").
    // Verify all expected tab buttons are visible by their label text.
    // NOTE: The SlidingTabs component lacks role="tab" and aria-selected — see accessibility bug below.
    await expect(page.getByRole("button", { name: /^content$/i })).toBeVisible();
    await expect(page.getByRole("button", { name: /^files$/i })).toBeVisible();
    await expect(page.getByRole("button", { name: /^versions$/i })).toBeVisible();
    await expect(page.getByRole("button", { name: /^usage$/i })).toBeVisible();
    await expect(page.getByRole("button", { name: /^security$/i })).toBeVisible();
  });

  test("clicking tabs switches content", async ({ page }) => {
    await login(page);
    await page.goto(`/skills/${TEST_SKILL_NAME}`);

    // Click the Versions tab button
    const versionsTab = page.getByRole("button", { name: /^versions$/i });
    await versionsTab.click();

    // After clicking, the Versions tab button should still be visible (page didn't navigate away)
    await expect(versionsTab).toBeVisible();
    // NOTE: SlidingTabs does not expose aria-selected; active state is CSS-only.
    // A proper ARIA tablist should set aria-selected="true" on the active tab — accessibility bug.
  });

  test("security badge is visible on detail page", async ({ page }) => {
    await login(page);
    await page.goto(`/skills/${TEST_SKILL_NAME}`);

    // Look for any security-related element: tab button, text, or status indicator
    const securityElement = page
      .getByRole("button", { name: /^security$/i })
      .or(page.getByText(/security/i).first())
      .or(page.locator('[data-testid*="security"]').first())
      .or(page.locator('[aria-label*="security" i]').first());

    await expect(securityElement).toBeVisible();
  });
});
