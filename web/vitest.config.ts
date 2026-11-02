// File metadata (manifest mirror):
// Purpose: Configures Vitest unit-test discovery and DOM environment; the settings are consumed by the web toolchain and stay separate from product runtime configuration.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-10-30
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [react()],
  test: {
    environment: "happy-dom",
    globals: true,
    // Concurrent DOM journeys can starve asynchronous page rendering before
    // the existing query deadlines. Run every file with one worker.
    maxWorkers: 1,
    // V8 coverage instrumentation and parallel test files push the heaviest
    // form journeys past the 5s default even when they pass without coverage.
    // Keep the checks and the coverage gates unchanged; only allow those
    // journeys to finish on a loaded machine.
    testTimeout: 30_000,
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
    clearMocks: true,
    restoreMocks: true,
    coverage: {
      provider: "v8",
      // Include files that no test imported. A loaded-files-only denominator
      // hides untested pages and is unsuitable as a release gate.
      include: ["src/**/*.{ts,tsx}"],
      exclude: ["src/**/*.test.*", "src/**/*.d.ts", "src/test/**", "src/main.tsx"],
      reporter: ["text", "json-summary", "lcov", "html"],
      reportsDirectory: "./coverage",
    },
  },
});
