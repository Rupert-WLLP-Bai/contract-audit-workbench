import { defineConfig } from "playwright/test";

const runID = process.env.E2E_RUN_ID ||= `run-${Date.now()}`;

export default defineConfig({
  testDir: "./e2e/functional",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 180_000,
  expect: { timeout: 15_000 },
  outputDir: `../artifacts/documents-functional/${runID}/playwright`,
  reporter: [["list"], ["json", { outputFile: `../artifacts/documents-functional/${runID}/playwright-results.json` }], ["./e2e/functional/reporter.ts"]],
  use: {
    baseURL: process.env.E2E_WEB,
    browserName: "chromium",
    headless: true,
    viewport: { width: 1440, height: 1000 },
    launchOptions: { executablePath: process.env.CHROMIUM_BIN || "/usr/bin/chromium", args: ["--no-sandbox", "--disable-dev-shm-usage"] },
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
});
