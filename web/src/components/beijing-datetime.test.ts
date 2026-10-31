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
