import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { C1B_BUSINESS_AT_UTC, C1B_BUSINESS_TIME_ZONE, c1bBusinessAtInput } from "./c1b-clock";

describe("C1-B frozen business time", () => {
  it("produces the same Shanghai wall time and UTC instant on every host", () => {
    assert.equal(C1B_BUSINESS_TIME_ZONE, "Asia/Shanghai");
    assert.equal(C1B_BUSINESS_AT_UTC, "2026-01-15T01:00:00.000Z");
    assert.equal(c1bBusinessAtInput(), "2026-01-15T09:00");
    assert.equal(c1bBusinessAtInput("UTC"), "2026-01-15T01:00");
  });
});
