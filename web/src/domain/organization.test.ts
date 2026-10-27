import { describe, expect, it } from "vitest";
import { organizationName } from "./organization";

describe("organizationName", () => {
  it("names the demonstration organisation without hiding unknown scopes", () => {
    expect(organizationName("demo-org")).toBe("两江新区分公司");
    expect(organizationName("branch-sales")).toBe("branch-sales");
    expect(organizationName(null)).toBe("正在确认");
  });
});
