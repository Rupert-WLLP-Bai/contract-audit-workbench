import { describe, expect, it } from "vitest";
import {
  DEFAULT_OPERATOR_AUDIT_FLAGS,
  DEMO_OPERATOR_AUDIT_FLAGS,
  anyOperatorAuditFeatureEnabled,
} from "./feature-flags";
import { availableObservation, capabilityPacks } from "./fixture";

const sources = import.meta.glob(["./*.ts", "./*.tsx"], {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

function productionSources(): [string, string][] {
  return Object.entries(sources).filter(
    ([path]) =>
      !path.includes(".test.") &&
      !path.endsWith("fixture.ts") &&
      !path.endsWith("test-utils.tsx"),
  );
}

describe("operator audit feature flags", () => {
  it("defaults every extension surface to off", () => {
    expect(DEFAULT_OPERATOR_AUDIT_FLAGS).toEqual({
      capabilityPacksEnabled: false,
      externalEvidenceMode: "off",
      fulfillmentChecksEnabled: false,
      reviewImpactMode: "off",
      incrementalReuseEnabled: false,
    });
    expect(anyOperatorAuditFeatureEnabled(DEFAULT_OPERATOR_AUDIT_FLAGS)).toBe(
      false,
    );
  });

  it("keeps the demo profile explicit and reuse disabled", () => {
    expect(DEMO_OPERATOR_AUDIT_FLAGS).not.toBe(
      DEFAULT_OPERATOR_AUDIT_FLAGS,
    );
    expect(DEMO_OPERATOR_AUDIT_FLAGS.incrementalReuseEnabled).toBe(false);
    expect(anyOperatorAuditFeatureEnabled(DEMO_OPERATOR_AUDIT_FLAGS)).toBe(
      true,
    );
  });

  it("never imports test fixtures from a production module", () => {
    for (const [path, source] of productionSources()) {
      expect(source, path).not.toMatch(/from ["']\.\/fixture["']/);
    }
  });

  it("keeps every fixture explicitly synthetic", () => {
    expect(capabilityPacks.every((pack) => pack.dataClass === "synthetic")).toBe(
      true,
    );
    expect(availableObservation.dataClass).toBe("synthetic");
  });
});
