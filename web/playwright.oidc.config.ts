import { defineConfig } from "playwright/test";

export default defineConfig({
  testDir: "./e2e/identity",
  workers: 1,
  retries: 0,
  timeout: 60_000,
  outputDir: "../artifacts/documents-functional/oidc-browser/playwright",
  reporter: [["list"], ["json", { outputFile: "../artifacts/documents-functional/oidc-browser/results.json" }]],
  use: {
    browserName: "chromium", headless: true,
    launchOptions: { executablePath: process.env.CHROMIUM_BIN || "/usr/bin/chromium", args: ["--no-sandbox", "--disable-dev-shm-usage"] },
    trace: "retain-on-failure", screenshot: "only-on-failure",
  },
});
