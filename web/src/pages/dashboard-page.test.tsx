import { describe, expect, it } from "vitest";
import { DashboardPage } from "./dashboard-page";
import { HomePage } from "./home-page";

describe("DashboardPage", () => {
  it("reuses the existing workbench implementation until the redesign lands", () => {
    expect(DashboardPage).toBe(HomePage);
  });
});
