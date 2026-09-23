import { test, expect } from "@playwright/test";

test("onboarding follows server state through all steps without trusting checkout success", async ({ page }) => {
  let state = { organization_id: "org_a", revision: 1, edition: "", edition_version_id: "", installation_id: 0, repositories: [] as { id: string; full_name: string; private: boolean }[], policy_id: "", proof_id: "" };
  let paid = false;
  let proofStatus = "QUEUED";
  const writes: string[] = [];
  await page.route("**/v1/onboarding/**", async route => {
    const request = route.request(); const url = new URL(request.url()); const endpoint = url.pathname.split("/").pop();
    let body: unknown;
    if (request.method() === "POST") {
      writes.push(endpoint!); const input = request.postDataJSON();
      if (endpoint === "organizations") body = state;
      if (endpoint === "edition") { state = { ...state, edition: input.edition, edition_version_id: "team-2026-v2", revision: state.revision + 1 }; body = state; }
      if (endpoint === "billing") body = { redirect_url: "" }; // Even successful checkout/trial response is not authority.
      if (endpoint === "repositories") { state = { ...state, installation_id: 7, repositories: [{ id: "gh_1", full_name: "acme/service", private: false }], revision: state.revision + 1 }; body = state; }
      if (endpoint === "policy") { state = { ...state, policy_id: input.policy_id, revision: state.revision + 1 }; body = state; }
      if (endpoint === "proof") { state = { ...state, proof_id: "proof_1", revision: state.revision + 1 }; body = { id: "proof_1", status: proofStatus }; }
    } else if (endpoint === "session") body = { user_id: "user", organizations: [], github_install_url: "https://github.com/apps/safepatch-test/installations/new" };
    else if (endpoint === "installations") body = [{ id: 7, name: "acme" }];
    else if (endpoint === "repositories") body = [{ id: "gh_1", full_name: "acme/service", private: false }];
    else body = { state, access: { ready: paid, source: paid ? "SUBSCRIPTION" : "" }, policies: [{ id: "baseline-v1", name: "Baseline verification", description: "Required checks" }], ...(state.proof_id ? { proof: { id: state.proof_id, status: proofStatus } } : {}) };
    await route.fulfill({ json: body });
  });
  await page.goto("/onboarding?edition=team&success=true&subscription_state=ACTIVE");
  await page.getByLabel("New organization name").fill("Acme");
  await page.getByRole("button", { name: "Create organization" }).click();
  await page.getByRole("radio", { name: /Team/ }).check();
  await page.getByRole("button", { name: "Continue", exact: false }).click();
  await expect(page.getByRole("heading", { name: "Checkout or trial", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Continue to checkout" }).click();
  await expect(page.getByRole("heading", { name: "Checkout or trial", exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "Connect GitHub" })).toHaveCount(0);
  paid = true;
  await page.getByRole("button", { name: "Check payment or contract status" }).click();
  await expect(page.getByRole("link", { name: "Connect GitHub" })).toHaveAttribute("href", /github.com\/apps/);
  await page.getByRole("button", { name: "I’ve installed the app — continue" }).click();
  await page.getByLabel("GitHub installation").selectOption("7");
  await page.getByRole("checkbox", { name: /acme\/service/ }).check();
  await page.getByRole("button", { name: "Use 1 repositories" }).click();
  await page.getByRole("radio", { name: /Baseline verification/ }).check();
  await page.getByRole("button", { name: "Save policy" }).click();
  await page.getByRole("button", { name: "Run first proof" }).click();
  await expect(page.locator(".proof-status strong")).toHaveText("QUEUED");
  proofStatus = "UNKNOWN";
  await page.getByRole("button", { name: "Check proof status" }).click();
  await expect(page.locator(".proof-status strong")).toHaveText("UNKNOWN");
  expect(writes).toEqual(["organizations", "edition", "billing", "repositories", "policy", "proof"]);
});

test("signed-out onboarding offers signup/login and does not create local privileges", async ({ page }) => {
  await page.route("**/v1/onboarding/session", route => route.fulfill({ status: 401, json: { error: "AUTHENTICATION_REQUIRED" } }));
  await page.goto("/onboarding?success=true&edition=enterprise");
  await expect(page.getByRole("link", { name: "Sign up with GitHub" })).toHaveAttribute("href", "/v1/auth/github/start");
  await expect(page.getByRole("link", { name: "Log in with GitHub" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Run first proof" })).toHaveCount(0);
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  await page.screenshot({ path: "test-results/onboarding-mobile.png", fullPage: true });
});

test("backend failure is visible and never becomes completed onboarding", async ({ page }) => {
  await page.route("**/v1/onboarding/session", route => route.fulfill({ status: 503, json: { error: "ONBOARDING_UNAVAILABLE" } }));
  await page.goto("/onboarding");
  await expect(page.locator(".onboarding-alert")).toContainText("temporarily unavailable");
  await expect(page.getByRole("button", { name: "Run first proof" })).toHaveCount(0);
});
