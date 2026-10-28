import { existsSync } from "node:fs";
import { chromium } from "playwright";

const SYSTEM_FALLBACKS = [
  "/usr/bin/chromium",
  "/usr/bin/chromium-browser",
  "/usr/bin/google-chrome",
  "/usr/bin/google-chrome-stable",
];

// Resolve the browser on any machine without baking in one workstation path:
// an explicit CHROMIUM_BIN wins, then the Playwright-managed Chromium, then a
// conventional system package. A missing browser fails with the install hint
// instead of launching a path that only exists on the original machine.
export function chromiumExecutable(): string {
  const explicit = process.env.CHROMIUM_BIN?.trim();
  if (explicit) return explicit;
  const bundled = chromium.executablePath();
  if (bundled && existsSync(bundled)) return bundled;
  for (const candidate of SYSTEM_FALLBACKS) {
    if (existsSync(candidate)) return candidate;
  }
  throw new Error(
    "Chromium unavailable: run `cd web && bunx playwright install chromium`, or set CHROMIUM_BIN",
  );
}
