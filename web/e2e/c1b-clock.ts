// File metadata (manifest mirror):
// Purpose: Converts the fixed C1-B business instant between UTC and Asia/Shanghai input fields so browser scenarios assert the same contract business time on every host.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-03
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import assert from "node:assert/strict";
import type { Page } from "playwright";

export const C1B_BUSINESS_TIME_ZONE = "Asia/Shanghai";
export const C1B_BUSINESS_AT_UTC = "2026-01-15T01:00:00.000Z";

export function c1bBusinessAtInput(timeZone = C1B_BUSINESS_TIME_ZONE): string {
  const parts = new Intl.DateTimeFormat("en-CA", {
    timeZone,
    calendar: "gregory",
    numberingSystem: "latn",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).formatToParts(new Date(C1B_BUSINESS_AT_UTC));
  const value = (type: Intl.DateTimeFormatPartTypes) => parts.find((part) => part.type === type)?.value;
  const year = value("year");
  const month = value("month");
  const day = value("day");
  const hour = value("hour");
  const minute = value("minute");
  assert.ok(year && month && day && hour && minute, `could not format C1-B business time for ${timeZone}`);
  return `${year}-${month}-${day}T${hour}:${minute}`;
}

export async function enterC1BBusinessAt(page: Page): Promise<void> {
  const actualTimeZone = await page.evaluate(() => Intl.DateTimeFormat().resolvedOptions().timeZone);
  assert.equal(actualTimeZone, C1B_BUSINESS_TIME_ZONE, "C1-B browser timezone is not pinned to Asia/Shanghai");
  const input = page.getByLabel("审查业务时间（北京时间）", { exact: true });
  await input.fill(c1bBusinessAtInput());
  const submittedInstant = await input.evaluate((element) => new Date((element as HTMLInputElement).value).toISOString());
  assert.equal(submittedInstant, C1B_BUSINESS_AT_UTC, "C1-B business time did not resolve to its frozen UTC instant");
}
