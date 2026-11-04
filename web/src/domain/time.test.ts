// File metadata (manifest mirror):
// Purpose: Checks missing and malformed timestamp labels, calendar validation and precise Beijing-time formatting without changing original timestamp values.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-04
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { expect, it } from "vitest";
import { formatDateTime, preciseTime } from "./time";

it("distinguishes missing and invalid timestamps, and uses the historical Beijing offset", () => {
  expect(formatDateTime("")).toBe("时间未记录");
  expect(formatDateTime("bad-date")).toBe("时间格式异常");
  expect(preciseTime(null)).toBe("未记录");
  expect(preciseTime("bad-date")).toBe("时间格式异常");
  expect(preciseTime("2026-02-30T01:02:03Z")).toBe("时间格式异常");
  expect(preciseTime("2024-02-29T01:02:03Z")).toMatch(/UTC\+08:00/);
  expect(preciseTime("2026-11-05T01:02:03Z")).toMatch(/09:02:03.*UTC\+08:00/);
  expect(preciseTime("1940-06-01T01:02:03Z")).toMatch(/10:02:03.*UTC\+09:00/);
});
