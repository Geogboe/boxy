import { test, expect } from "@playwright/test";

const adminPassword = process.env.BOXY_E2E_ADMIN_PASSWORD;

test.describe("Dashboard — UI enabled", () => {
  test.beforeEach(async ({ page }) => {
    test.skip(!adminPassword, "set BOXY_E2E_ADMIN_PASSWORD for the session-authenticated dashboard");
    await page.goto("/login");
    await page.getByRole("textbox", { name: "Username" }).fill("admin");
    await page.getByRole("textbox", { name: "Password" }).fill(adminPassword!);
    await page.getByRole("button", { name: "Log in" }).click();
    await expect(page).toHaveURL(/\/$/);
  });

  test("home page renders with layout and stats", async ({ page }) => {
    await page.goto("/");

    // Layout elements
    await expect(page.locator(".sidebar-brand")).toHaveText("Boxy");
    await expect(page.locator('.sidebar-brand img[src="/static/favicon.svg"]')).toHaveCount(1);
    await expect(page.locator('.app-header a[href="https://github.com/Geogboe/boxy"]')).toHaveCount(1);
    await expect(page.locator(".version-label")).toHaveText("dev");
    // Help is always available alongside the role-dependent operator links.
    await expect(page.locator('.sidebar-nav a[href="/ui/help"]')).toHaveCount(1);

    // Active nav
    await expect(page.locator('.sidebar-nav a.active')).toHaveText("Home");

    // Page content
    await expect(page.locator(".page-title")).toHaveText("Overview");

    // Stat cards
    const stats = page.locator(".stat-card");
    await expect(stats).toHaveCount(3);
    await expect(stats.nth(0).locator(".stat-label")).toHaveText("Pools");
    await expect(stats.nth(1).locator(".stat-label")).toHaveText("Sandboxes");
    await expect(stats.nth(2).locator(".stat-label")).toHaveText("Resources");

    // All counts should be 0 on empty store
    for (let i = 0; i < 3; i++) {
      await expect(stats.nth(i).locator(".stat-value")).toHaveText("0");
    }
  });

  test("pools page renders empty state", async ({ page }) => {
    await page.goto("/ui/pools");

    await expect(page.locator('.sidebar-nav a.active')).toHaveText("Pools");
    await expect(page.locator(".page-title")).toHaveText("Pools");
    await expect(page.locator(".table-card-header").filter({ hasText: "Active pools" })).toContainText("Active pools");
    await expect(page.locator(".empty")).toHaveText("No pools configured");
    await expect(page.locator('.filter-pill[href="/ui/pools?view=history"]')).toHaveCount(1);
  });

  test("sandboxes page renders empty state", async ({ page }) => {
    await page.goto("/ui/sandboxes");

    await expect(page.locator('.sidebar-nav a.active')).toHaveText("Sandboxes");
    await expect(page.locator(".page-title")).toHaveText("Sandboxes");
    await expect(page.locator(".table-card-header")).toContainText("All Sandboxes");
    await expect(page.locator(".empty")).toHaveText("No sandboxes created");
  });

  test("navigation between pages works", async ({ page }) => {
    await page.goto("/");
    await expect(page.locator(".page-title")).toHaveText("Overview");

    await page.click('a[href="/ui/pools"]');
    await expect(page.locator(".page-title")).toHaveText("Pools");
    await expect(page.locator('.sidebar-nav a.active')).toHaveText("Pools");

    await page.click('a[href="/ui/sandboxes"]');
    await expect(page.locator(".page-title")).toHaveText("Sandboxes");
    await expect(page.locator('.sidebar-nav a.active')).toHaveText("Sandboxes");

    await page.click('a[href="/ui/help"]');
    await expect(page.locator(".help-content h1")).toHaveText("Boxy package help");
    await expect(page.locator('.sidebar-nav a.active')).toHaveText("Help");

    await page.click('a[href="/"]');
    await expect(page.locator(".page-title")).toHaveText("Overview");
  });

  test("static assets load correctly", async ({ page }) => {
    await page.goto("/");

    // CSS is applied — sidebar should have a background color (not transparent)
    const sidebar = page.locator(".sidebar");
    const bgColor = await sidebar.evaluate(
      (el) => getComputedStyle(el).backgroundColor
    );
    expect(bgColor).not.toBe("rgba(0, 0, 0, 0)");

    // HTMX is loaded — check window.htmx exists
    const htmxLoaded = await page.evaluate(() => typeof (window as any).htmx !== "undefined");
    expect(htmxLoaded).toBe(true);
  });

  test("HTMX polling attributes are present", async ({ page }) => {
    await page.goto("/");

    // Stats container has hx-get and hx-trigger for polling
    const statsDiv = page.locator("#stats-fragment");
    await expect(statsDiv).toHaveCount(1);
    await expect(statsDiv).toHaveAttribute("hx-get", "/ui/fragments/stats");
    await expect(statsDiv).toHaveAttribute("hx-trigger", "every 5s");
    await expect(statsDiv).toHaveAttribute("hx-swap", "innerHTML");
  });

  test("manual refresh button re-fetches the fragment on click", async ({ page }) => {
    await page.goto("/ui/pools");

    const fragment = page.locator("#pools-fragment");
    const refreshBtn = page.locator('.refresh-btn[hx-target="#pools-fragment"]');
    await expect(refreshBtn).toHaveAttribute("hx-get", "/ui/fragments/pools-table");

    const [response] = await Promise.all([
      page.waitForResponse("**/ui/fragments/pools-table"),
      refreshBtn.click(),
    ]);
    expect(response.ok()).toBe(true);
    // The 5s auto-poll is untouched — the button is an additional trigger,
    // not a replacement.
    await expect(fragment).toHaveAttribute("hx-trigger", "every 5s");
  });

  test("dark theme is applied", async ({ page }) => {
    await page.goto("/");

    // Body background should be dark
    const bgColor = await page.locator("body").evaluate(
      (el) => getComputedStyle(el).backgroundColor
    );
    // Parse rgb values — dark theme bg should have low values
    const match = bgColor.match(/(\d+)/g);
    expect(match).not.toBeNull();
    const [r, g, b] = match!.map(Number);
    expect(r).toBeLessThan(50);
    expect(g).toBeLessThan(50);
    expect(b).toBeLessThan(50);
  });

  test("theme toggle persists light and dark modes", async ({ page }) => {
    await page.goto("/");
    const toggle = page.locator("[data-theme-toggle]");
    await expect(toggle).toHaveAttribute("aria-label", "Switch to light theme");
    await toggle.click();
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
    await expect(toggle).toHaveAttribute("aria-label", "Switch to dark theme");
    await toggle.click();
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  });

  test("admin navigation exposes diagnostics and service keys", async ({ page }) => {
    await page.goto("/");
    await expect(page.locator('.sidebar-nav a[href="/ui/diagnostics"]')).toHaveCount(1);
    await expect(page.locator('.sidebar-nav a[href="/ui/service-keys"]')).toHaveCount(1);
    await page.goto("/ui/diagnostics");
    await expect(page.locator('.sidebar-nav a[href="/ui/service-keys"]')).toHaveCount(1);
    await page.goto("/ui/service-keys");
    await expect(page.locator('.sidebar-nav a[href="/ui/diagnostics"]')).toHaveCount(1);
  });
});
