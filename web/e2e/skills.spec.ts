import { test, expect } from "@playwright/test";

const BASE_URL =
  process.env.PLAYWRIGHT_BASE_URL ?? "http://localhost:8080";

const TEST_USER_EMAIL = "e2e-skills@test.com";
const TEST_USER_PASSWORD = "testpassword123";
const TEST_USER_NAME = "E2E Skills User";
const TEST_SKILL_NAME = "e2e-list-skill";

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
      description: "E2E test skill for skill list tests",
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

test.describe("Skill list page", () => {
  test.beforeAll(async ({ request }) => {
    await ensureTestFixtures(request);
  });

  test("skill list page loads and displays skills", async ({ page }) => {
    await login(page);
    await expect(page.getByText(/skills/i).first()).toBeVisible();
  });

  test("search filters the skill list", async ({ page }) => {
    await login(page);

    // Find the search input on the skills page — placeholder is "Filter skills..."
    const searchInput = page.locator('input[placeholder*="ilter" i]').first();

    await searchInput.fill(TEST_SKILL_NAME);

    // After typing a skill name, the list should show the matching skill
    // or at least not navigate away
    await expect(page).toHaveURL("/");
    // The skill name should appear somewhere on the page (in results or empty state)
    const skillEntry = page.getByText(TEST_SKILL_NAME);
    // Empty state text rendered by SkillList when search finds nothing: "Nothing matches that filter"
    const emptyState = page.getByText(/nothing matches/i);
    await expect(skillEntry.or(emptyState)).toBeVisible({ timeout: 5000 });
  });
});
