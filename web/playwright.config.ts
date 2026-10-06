import { defineConfig } from "@playwright/test";
import { existsSync } from "node:fs";

const edge = "C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe";
export default defineConfig({
  testDir: "./tests", fullyParallel: false, workers: 1,
  use: {
    baseURL: "http://127.0.0.1:3100",
    launchOptions: existsSync(edge) ? { executablePath: edge } : {},
  },
  webServer: {
    env: { SAFEPATCH_PADDLE_ENVIRONMENT: "sandbox", SAFEPATCH_PADDLE_CLIENT_TOKEN: "test_fixtureOnlyNotARealToken" },
    command: "node node_modules/next/dist/bin/next start --hostname 127.0.0.1 --port 3100",
    url: "http://127.0.0.1:3100/pricing", reuseExistingServer: false, timeout: 60000,
  },
});
