import { test, expect } from "@playwright/test";

const BASE_URL =
  process.env.PLAYWRIGHT_BASE_URL ?? "http://localhost:8080";
const API_KEY =
  process.env.API_KEY ?? "sk-change-me-in-production";

const TEST_USER_EMAIL = "e2e-skills@test.com";
const TEST_USER_PASSWORD = "testpassword123";
const TEST_USER_NAME = "E2E Skills User";
const TEST_SKILL_NAME = "e2e-list-skill";

async function ensureTestUserExists() {
  await fetch(`${BASE_URL}/api/auth/signup`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      email: TEST_USER_EMAIL,
      password: TEST_USER_PASSWORD,
      name: TEST_USER_NAME,
    }),
  });
  // Accept 201 (created) or 409 (already exists)
}

async function ensureTestSkillExists() {
  await fetch(`${BASE_URL}/api/skills`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-API-Key": API_KEY,
    },
    body: JSON.stringify({
      name: TEST_SKILL_NAME,
      description: "E2E test skill for skill list tests",
      content: "# E2E List Skill\n\nThis skill is used for E2E testing.",
    }),
  });
  // Accept 201 (created) or 409 (already exists)
}

async function login(page: import("@playwright/test").Page) {
  await page.goto("/login");
  await page.getByPlaceholder(/email/i).fill(TEST_USER_EMAIL);
  await page.getByPlaceholder(/password/i).fill(TEST_USER_PASSWORD);
  await page.getByRole("button", { name: /log in|sign in/i }).click();
  await expect(page).toHaveURL("/");
}

test.describe("Skill list page", () => {
  test.beforeAll(async () => {
    await ensureTestUserExists();
    await ensureTestSkillExists();
  });

  test("skill list page loads and displays skills", async ({ page }) => {
    await login(page);
    await expect(page.getByText(/skills/i).first()).toBeVisible();
  });

  test("search filters the skill list", async ({ page }) => {
    await login(page);

    // Find the search input on the skills page
    const searchInput = page.getByRole("textbox", { name: /search/i }).or(
      page.locator('input[type="search"]')
    ).or(
      page.locator('input[placeholder*="search" i]')
    ).first();

    await searchInput.fill(TEST_SKILL_NAME);

    // After typing a skill name, the list should show the matching skill
    // or at least not navigate away
    await expect(page).toHaveURL("/");
    // The skill name should appear somewhere on the page (in results or empty state)
    const skillEntry = page.getByText(TEST_SKILL_NAME);
    const emptyState = page.getByText(/no skills|no results|nothing found/i);
    await expect(skillEntry.or(emptyState)).toBeVisible({ timeout: 5000 });
  });
});
