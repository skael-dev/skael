import { test, expect } from "@playwright/test";

const BASE_URL =
  process.env.PLAYWRIGHT_BASE_URL ?? "http://localhost:8080";
const API_KEY =
  process.env.API_KEY ?? "sk-change-me-in-production";

const TEST_USER_EMAIL = "e2e-detail@test.com";
const TEST_USER_PASSWORD = "testpassword123";
const TEST_USER_NAME = "E2E Detail User";
const TEST_SKILL_NAME = "e2e-detail-skill";

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
      description: "E2E test skill for detail page tests",
      content: "# E2E Detail Skill\n\nThis skill is used for detail page E2E testing.",
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

test.describe("Skill detail page", () => {
  test.beforeAll(async () => {
    await ensureTestUserExists();
    await ensureTestSkillExists();
  });

  test("navigating to skill detail shows tabs", async ({ page }) => {
    await login(page);
    await page.goto(`/skills/${TEST_SKILL_NAME}`);

    // Verify all expected tabs are visible
    await expect(page.getByRole("tab", { name: /content/i })).toBeVisible();
    await expect(page.getByRole("tab", { name: /files/i })).toBeVisible();
    await expect(page.getByRole("tab", { name: /versions/i })).toBeVisible();
    await expect(page.getByRole("tab", { name: /usage/i })).toBeVisible();
    await expect(page.getByRole("tab", { name: /security/i })).toBeVisible();
  });

  test("clicking tabs switches content", async ({ page }) => {
    await login(page);
    await page.goto(`/skills/${TEST_SKILL_NAME}`);

    // Click the Versions tab and verify it becomes active / content changes
    const versionsTab = page.getByRole("tab", { name: /versions/i });
    await versionsTab.click();

    // The tab should be selected after clicking
    await expect(versionsTab).toHaveAttribute("aria-selected", "true");
  });

  test("security badge is visible on detail page", async ({ page }) => {
    await login(page);
    await page.goto(`/skills/${TEST_SKILL_NAME}`);

    // Look for any security-related element: badge, tab, icon, or status indicator
    const securityElement = page
      .getByRole("tab", { name: /security/i })
      .or(page.getByText(/security/i).first())
      .or(page.locator('[data-testid*="security"]').first())
      .or(page.locator('[aria-label*="security" i]').first());

    await expect(securityElement).toBeVisible();
  });
});
