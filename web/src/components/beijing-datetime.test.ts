// File metadata (manifest mirror):
// Purpose: Checks the web behavior for “Beijing business-time inputs”; representative assertions cover converts the same Beijing wall time to one UTC instant outside Shanghai, rejects impossible or incomplete wall times. It protects the rendered/API state expected by this component without starting the full browser journey.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-10-31
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { afterEach, describe, expect, it, vi } from "vitest";
import { beijingInputToUtc, toBeijingInputValue } from "./beijing-datetime";

describe("Beijing business-time inputs", () => {
  afterEach(() => vi.unstubAllEnvs());

  it("converts the same Beijing wall time to one UTC instant outside Shanghai", () => {
    vi.stubEnv("TZ", "America/Los_Angeles");

    expect(beijingInputToUtc("2026-10-31T09:30")).toBe("2026-10-31T01:30:00.000Z");
    expect(toBeijingInputValue("2026-10-31T01:30:00.000Z")).toBe("2026-10-31T09:30");
  });

  it("rejects impossible or incomplete wall times", () => {
    expect(beijingInputToUtc("2026-02-30T09:30")).toBe("");
    expect(beijingInputToUtc("2026-10-31T9:30")).toBe("");
  });
});
