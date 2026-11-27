import { expect, test } from "@playwright/test";

import { admin, mockPortal, regularUser } from "./helpers";

test("renders workspaces and filters them by search", async ({ page }) => {
  await mockPortal(page);
  await page.goto("/");

  await expect(page.getByRole("heading", { name: "Workspaces", level: 1 })).toBeVisible();
  await expect(page.getByRole("button", { name: "alice-base" })).toBeVisible();
  await expect(page.getByRole("button", { name: "bob-python" })).toBeVisible();
  await expect(page.locator("#count")).toHaveText("2 workspaces");

  await page
    .getByPlaceholder("Search workspaces by name, template, owner, or status")
    .fill("python");

  await expect(page.getByRole("button", { name: "bob-python" })).toBeVisible();
  await expect(page.getByRole("button", { name: "alice-base" })).toHaveCount(0);
  await expect(page.locator("#count")).toHaveText("1 matching workspace");
});

test("switches to the list view and remembers the choice", async ({ page }) => {
  await mockPortal(page);
  await page.goto("/");

  await page.getByRole("button", { name: "List" }).first().click();

  await expect(page.locator("#workspace-list-wrap")).toBeVisible();
  await expect(page.locator("#workspace-list")).toContainText("alice-base");
  await expect(page.locator("#workspace-list")).toContainText("bob-python");

  await page.reload();

  await expect(page.locator("#workspace-list-wrap")).toBeVisible();
  await expect(page.locator("#grid")).toBeHidden();
});

test("shows the empty state when there are no workspaces", async ({ page }) => {
  await mockPortal(page, { workspaces: [] });
  await page.goto("/");

  await expect(page.locator("#empty")).toBeVisible();
  await expect(page.locator("#empty-title")).toHaveText("No workspaces yet");
  await expect(page.locator("#count")).toHaveText("0 workspaces");
});

test("shows administration navigation for admins", async ({ page }) => {
  await mockPortal(page, { me: admin });
  await page.goto("/");

  await expect(page.locator("#user-role")).toHaveText("admin");
  await page.locator("#manage-menu summary").click();

  await expect(page.locator("#manage-users")).toBeVisible();
  await expect(page.locator("#manage-teams")).toBeVisible();
  await expect(page.locator("#manage-audit")).toBeVisible();
});

test("opens a team in a dedicated page", async ({ page }) => {
  await mockPortal(page);
  await page.goto("/");

  await page.locator("#manage-menu summary").click();
  await page.locator("#manage-teams").click();

  await expect(page.locator("#teams-view")).toBeVisible();
  await page.getByRole("button", { name: "backend" }).click();

  await expect(page.locator("#team-detail-view")).toBeVisible();
  await expect(page.locator("#team-form-title")).toHaveText("Edit backend");
  await expect(page.locator("#team-members")).toContainText("alice@example.com");

  await page.locator("#team-detail-back").click();
  await expect(page.locator("#teams-view")).toBeVisible();
});

test("opens a user detail page with memberships and quota", async ({ page }) => {
  await mockPortal(page);
  await page.goto("/");

  await page.locator("#manage-menu summary").click();
  await page.locator("#manage-users").click();

  await expect(page.locator("#users-view")).toBeVisible();
  await page.getByRole("button", { name: "alice@example.com" }).click();

  await expect(page.locator("#user-detail-view")).toBeVisible();
  await expect(page.locator("#user-detail-title")).toHaveText("alice@example.com");
  await expect(page.locator("#user-detail-teams")).toContainText("backend");
  await expect(page.locator("#user-quota-form")).toBeVisible();
  await expect(page.locator("#user-quota-workspaces")).toHaveValue("1");

  await page.locator("#user-detail-back").click();
  await expect(page.locator("#users-view")).toBeVisible();
});

test("hides administration navigation for regular users", async ({ page }) => {
  await mockPortal(page, { me: regularUser });
  await page.goto("/");

  await expect(page.locator("#user-role")).toBeHidden();
  await page.locator("#manage-menu summary").click();

  await expect(page.locator("#manage-users")).toBeHidden();
  await expect(page.locator("#manage-teams")).toBeHidden();
  await expect(page.locator("#manage-audit")).toBeHidden();
});

test("toggles the theme and persists it", async ({ page }) => {
  await mockPortal(page);
  await page.goto("/");

  const html = page.locator("html");
  await expect(html).toHaveAttribute("data-theme", /^(light|dark)$/);
  const before = await html.getAttribute("data-theme");

  await page.locator("#theme-toggle").click();
  const after = await html.getAttribute("data-theme");

  expect(after).not.toBe(before);
  await page.reload();
  await expect(html).toHaveAttribute("data-theme", after!);
});

test("keeps volume summary cards readable in dark mode", async ({ page }) => {
  await mockPortal(page);
  await page.emulateMedia({ colorScheme: "light" });
  await page.goto("/");
  await page.locator("#theme-toggle").click();

  await page.locator("#manage-menu summary").click();
  await page.locator("#manage-volumes").click();

  const card = page.locator(".volume-summary-card").first();
  await expect(card).toBeVisible();
  const background = await card.evaluate((element) => getComputedStyle(element).backgroundImage);
  expect(background).not.toContain("255, 255, 255");
});

test("submits the create workspace form", async ({ page }) => {
  let created: Record<string, unknown> | null = null;
  await mockPortal(page, {
    onWorkspaceCreate: (body) => {
      created = body;
    },
  });
  await page.goto("/");

  await page.locator("#new-workspace").click();
  await expect(page.locator("#create-dialog")).toBeVisible();
  await page.locator("#workspace-name").fill("demo-app");
  await page.locator("#create-submit").click();

  await expect(page.locator("#create-dialog")).toBeHidden();
  await expect(page.locator(".toast.success")).toContainText("Workspace demo-app is being created.");
  expect(created).toMatchObject({ name: "demo-app", template: "base" });
  expect(Array.isArray(created!.apps)).toBe(true);
});
