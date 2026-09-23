import { test, expect } from "@playwright/test";
import type { ProductCatalog } from "../lib/catalog";

test("five permanent editions render the Go catalog and requested actions", async ({ page, request }) => {
  const errors: string[] = [];
  page.on("pageerror", error => errors.push(error.message));
  const response = await request.get("/v1/product-catalog");
  expect(response.status()).toBe(200);
  const catalog: ProductCatalog = await response.json();
  expect(catalog.editions.map(e => e.name)).toEqual(["Community", "Developer", "Team", "Business", "Enterprise"]);
  await page.goto("/pricing");
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await expect(page.locator("article.plan")).toHaveCount(5);
  for (const [index, edition] of catalog.editions.entries()) {
    const card = page.locator("article.plan").nth(index);
    await expect(card.getByRole("heading")).toHaveText(edition.name);
    await expect(card.locator(".action")).toHaveText(new RegExp(edition.action.label));
    await expect(card.locator(".feature-count")).toContainText(`${edition.features.length} catalog features`);
    // Every enabled mapping is visible in the full comparison, with its source
    // availability, not an inferred entitlement from an edition name.
    for (const feature of edition.features) {
      const cell = page.locator("tbody tr").filter({ has: page.getByText(feature.name, { exact: true }) }).locator("td").nth(index);
      await expect(cell).toContainText(feature.availability === "SDK" ? "SDK" : "Planned");
    }
  }
  expect(errors).toEqual([]);
});

test("all unconfigured actions explain availability and restore keyboard focus", async ({ page, request }) => {
  const catalog: ProductCatalog = await (await request.get("/v1/product-catalog")).json();
  await page.goto("/pricing");
  for (const edition of catalog.editions) {
    const action = page.getByRole(edition.action.href ? "link" : "button", { name: `${edition.action.label} — ${edition.name}`, exact: true });
    if (edition.action.href) { await expect(action).toHaveAttribute("href", edition.action.href); continue; }
    await action.focus(); await page.keyboard.press("Enter");
    await expect(page.getByRole("dialog")).toBeVisible();
    await expect(page.getByRole("dialog")).toContainText(edition.action.unavailable_message);
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog")).not.toBeVisible();
    await expect(action).toBeFocused();
  }
});

test("feature search, planned filter, empty state and FAQ work", async ({ page }) => {
  await page.goto("/pricing");
  const search = page.getByRole("searchbox", { name: "Search features" });
  await search.fill("proof");
  await expect(page.locator("tbody tr")).not.toHaveCount(0);
  await page.getByRole("checkbox", { name: "Include planned features" }).uncheck();
  await expect(page.locator("tbody .future")).toHaveCount(0);
  await search.fill("<script>not-a-feature</script>");
  await expect(page.getByText("No matching features")).toBeVisible();
  await page.getByRole("button", { name: "Reset filters" }).click();
  await expect(search).toHaveValue("");
  await expect(page.getByRole("checkbox")).toBeChecked();
  await page.getByText("What happens when an organization trial ends?", { exact: true }).click();
  await expect(page.getByText(/Trial grants expire automatically/)).toBeVisible();
});

test("desktop and mobile layouts remain readable without page overflow", async ({ page }) => {
  for (const width of [1440, 768, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    await page.goto("/pricing");
    await page.screenshot({ path: `test-results/pricing-${width}.png`, fullPage: true });
    const overflow = await page.evaluate(() => ({
      viewport: innerWidth, document: document.documentElement.scrollWidth,
      elements: [...document.querySelectorAll("body *")].filter(el => !el.closest(".table-scroll") && el.getBoundingClientRect().right > innerWidth).map(el => ({ tag: el.tagName, class: el.className, right: el.getBoundingClientRect().right })).slice(0, 10),
    }));
    expect(overflow.document, JSON.stringify(overflow)).toBeLessThanOrEqual(overflow.viewport);
    await expect(page.locator("article.plan")).toHaveCount(5);
    const table = page.getByRole("region", { name: /Feature comparison/ });
    await expect(table).toBeVisible();
  }
});
