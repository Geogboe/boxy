import { test, expect } from "@playwright/test";

test.describe("Dashboard — UI enabled", () => {
  test("home page renders with layout and stats", async ({ page }) => {
    await page.goto("/");

    // Layout elements
    await expect(page.locator(".sidebar-brand")).toHaveText("Boxy");
    await expect(page.locator(".sidebar-nav a")).toHaveCount(3);

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
    await expect(page.locator(".table-card-header")).toHaveText("All Pools");
    await expect(page.locator(".empty")).toHaveText("No pools configured");
  });

  test("sandboxes page renders empty state", async ({ page }) => {
    await page.goto("/ui/sandboxes");

    await expect(page.locator('.sidebar-nav a.active')).toHaveText("Sandboxes");
    await expect(page.locator(".page-title")).toHaveText("Sandboxes");
    await expect(page.locator(".table-card-header")).toHaveText("All Sandboxes");
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
    const statsDiv = page.locator('[hx-get="/ui/fragments/stats"]');
    await expect(statsDiv).toHaveCount(1);
    await expect(statsDiv).toHaveAttribute("hx-trigger", "every 5s");
    await expect(statsDiv).toHaveAttribute("hx-swap", "innerHTML");
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
});
