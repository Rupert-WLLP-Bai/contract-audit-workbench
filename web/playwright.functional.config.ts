// File metadata (manifest mirror):
// Purpose: Configures Playwright functional projects, browser launch, test timeouts and per-run evidence identity for the explicitly invoked browser suite.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-10-31
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { defineConfig } from "playwright/test";
import { chromiumExecutable } from "./e2e/chromium";

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
    ...(process.env.E2E_API ? { extraHTTPHeaders: { Referer: process.env.E2E_API } } : {}),
    browserName: "chromium",
    headless: true,
    viewport: { width: 1440, height: 1000 },
    launchOptions: { executablePath: chromiumExecutable(), args: ["--no-sandbox", "--disable-dev-shm-usage"] },
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
});
