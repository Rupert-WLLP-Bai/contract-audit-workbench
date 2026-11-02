// File metadata (manifest mirror):
// Purpose: Configures Playwright OIDC browser-test projects and runtime settings; the settings are consumed by the web toolchain and stay separate from product runtime configuration.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-10-25
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

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
