import { expect, test } from "@playwright/test";

const transaction = "txn_01h04vsc0qhwtsbsxh3422wjs4";
test("checkout loads only after session verification and completion grants no access", async ({ page }) => {
  const mutations: string[] = [];
  page.on("request", r => { if (r.method() === "POST" && r.url().includes("/v1/")) mutations.push(r.url()); });
  await page.route("**/v1/onboarding/session", r => r.fulfill({ json: { user_id: "user", organizations: [] } }));
  await page.route("https://cdn.paddle.com/paddle/v2/paddle.js", r => r.fulfill({ contentType: "application/javascript", body: `
    window.paddleTest = {};
    window.Paddle = {
      Environment: { set(value) { window.paddleTest.environment = value; } },
      Initialize(settings) { window.paddleTest.token = settings.token; window.paddleTest.success = settings.checkout.settings.successUrl; settings.eventCallback({ name: 'checkout.completed' }); },
      Checkout: { open(settings) { window.paddleTest.transaction = settings.transactionId; } }
    };
  ` }));
  const response = await page.goto(`/checkout?_ptxn=${transaction}&paid=true&edition=enterprise`);
  const csp = response?.headers()["content-security-policy"] ?? "";
  expect(csp).toContain("frame-src https://*.paddle.com");
  expect(csp.split("script-src")[1].split(";")[0]).not.toContain("unsafe-inline");
  await expect(page.getByRole("status")).toContainText("before enabling paid access");
  await page.getByRole("button", { name: "Reopen payment window" }).click();
  const captured = await page.evaluate(() => (window as unknown as { paddleTest: object }).paddleTest);
  expect(captured).toEqual({ environment: "sandbox", token: "test_fixtureOnlyNotARealToken", success: "http://127.0.0.1:3100/onboarding", transaction });
  expect(mutations).toEqual([]);
  const home = await page.goto("/");
  expect(home?.headers()["content-security-policy"]).toContain("frame-src 'none'");
  expect(home?.headers()["content-security-policy"]).not.toContain("paddle.com");
});

test("checkout rejects invalid transaction and signed-out sessions before loading Paddle", async ({ page }) => {
  let scripts = 0;
  await page.route("https://cdn.paddle.com/**", r => { scripts++; return r.abort(); });
  await page.goto("/checkout?_ptxn=../../other&paid=true");
  await expect(page.locator(".service-alert")).toContainText("Start checkout");
  await page.route("**/v1/onboarding/session", r => r.fulfill({ status: 401, json: { error: "AUTHENTICATION_REQUIRED" } }));
  await page.goto(`/checkout?_ptxn=${transaction}`);
  await expect(page.locator(".service-alert")).toContainText("Sign in");
  expect(scripts).toBe(0);
});

test("payment script failure stays visible and does not report payment success", async ({ page }) => {
  await page.route("**/v1/onboarding/session", r => r.fulfill({ json: { user_id: "user" } }));
  await page.route("https://cdn.paddle.com/**", r => r.abort());
  await page.goto(`/checkout?_ptxn=${transaction}`);
  await expect(page.locator(".service-alert")).toContainText("payment service could not load");
  await expect(page.getByRole("button", { name: "Reopen payment window" })).toHaveCount(0);
});
