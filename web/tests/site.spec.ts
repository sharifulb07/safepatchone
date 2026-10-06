import { test, expect } from "@playwright/test";

test("landing and account pages navigate, hydrate under nonce CSP, and fit mobile", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", e => errors.push(e.message));
  await page.route("**/v1/onboarding/session", r => r.fulfill({ status: 401, json: { error: "AUTHENTICATION_REQUIRED" } }));
  const response = await page.goto("/");
  const csp = response!.headers()["content-security-policy"];
  expect(csp).toContain("'nonce-");
  expect(csp.split(";").find(v => v.trim().startsWith("script-src"))).not.toContain("unsafe-inline");
  await expect(page.getByRole("heading", { level: 1 })).toContainText("Move forward.");
  await page.getByRole("link", { name: "Get started" }).click();
  await expect(page).toHaveURL(/\/signup$/);
  await expect(page.getByRole("link", { name: "Sign up with GitHub" })).toHaveAttribute("href", "/v1/auth/github/start");
  await page.locator(".auth-card").getByRole("link", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("link", { name: "Sign in with GitHub" })).toHaveAttribute("href", "/v1/auth/github/start");
  for (const path of ["/", "/signin", "/signup", "/downloads"]) {
    await page.route("**/v1/downloads/public", r => r.fulfill({ json: { items: [] } }));
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(path);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
    await page.screenshot({ path: `test-results/site-${path.slice(1) || "home"}-mobile.png`, fullPage: true });
  }
  expect(errors).toEqual([]);
});

test("downloads use server catalog and recheck organization access before signing", async ({ page }) => {
  const item = { id: "windows", filename: "safepatch_0.2.0_windows_amd64.zip", version: "0.2.0", channel: "stable", bytes: 1048576, sha256: "a".repeat(64) };
  const bundle = { ...item, id: "host", filename: "selfhost.zip" };
  let entitled = true, issued = 0;
  await page.route("**/v1/onboarding/session", r => r.fulfill({ json: { organizations: [{ organization_id: "org", name: "Acme" }] } }));
  await page.route("**/v1/downloads/public", r => r.fulfill({ json: { items: [item] } }));
  await page.route("**/v1/organizations/org/downloads", r => r.fulfill({ json: { items: [bundle] } }));
  await page.route("**/v1/organizations/org/downloads/host", r => {
    expect(r.request().method()).toBe("POST");
    expect(r.request().headers()["x-csrf-token"]).toBeDefined();
    issued++;
    return r.fulfill(entitled ? { json: { ...bundle, url: "https://objects.example.test/selfhost.zip?signature=test", expires_at: new Date(Date.now() + 30000).toISOString() } } : { status: 403, json: { error: "DOWNLOAD_UNAVAILABLE" } });
  });
  await page.goto("/downloads?edition=enterprise&paid=true");
  await expect(page.locator(".platform-card").filter({ hasText: "Windows" })).toContainText(item.filename);
  await expect(page.getByText("selfhost.zip", { exact: true })).toHaveCount(0);
  await page.getByLabel("Processor").selectOption("arm64");
  await expect(page.getByText("No published release for this platform yet.")).toHaveCount(3);
  await page.getByRole("combobox", { name: "Organization", exact: true }).selectOption("org");
  const downloads = page.locator(".organization-downloads");
  await downloads.getByRole("button", { name: "Prepare download" }).click();
  await expect(page.getByRole("link", { name: "Download now" })).toHaveAttribute("href", "https://objects.example.test/selfhost.zip?signature=test");
  entitled = false;
  await downloads.getByRole("button", { name: "Prepare download" }).click();
  await expect(page.locator(".service-alert")).toContainText("does not have access");
  await expect(page.getByRole("link", { name: "Download now" })).toHaveCount(0);
  expect(issued).toBe(2);
});

test("download and authentication outages stay visible", async ({ page }) => {
  await page.route("**/v1/**", r => r.fulfill({ status: 503, json: { error: "UNAVAILABLE" } }));
  await page.goto("/downloads");
  await expect(page.getByRole("alert").first()).toContainText("temporarily unavailable");
  await expect(page.getByText("No published release for this platform yet.")).toHaveCount(0);
  await page.goto("/signin");
  await expect(page.locator(".service-alert")).toContainText("Sign-in is temporarily unavailable");
});

test("proxy does not expose unrelated organization or webhook routes", async ({ request }) => {
  for (const path of ["/v1/organizations/org/billing", "/v1/billing/stripe/webhook", "/v1/downloads/private"]) {
    expect((await request.get(path)).status()).toBe(404);
  }
});

test("browser proxy rejects bearer credentials instead of silently switching identity", async ({ request }) => {
  for (const path of ["/v1/onboarding/session", "/v1/organizations/org/downloads"]) {
    const response = await request.get(path, { headers: { Authorization: "Bearer different-principal", Cookie: "__Host-safepatch_session=opaque" } });
    expect(response.status()).toBe(400);
    expect(await response.json()).toEqual({ error: "AUTHENTICATION_REJECTED" });
    expect(response.headers()["cache-control"]).toBe("no-store");
  }
});
