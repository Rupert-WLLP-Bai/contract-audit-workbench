// File metadata (manifest mirror):
// Purpose: Defines operator-audit extension flags with all surfaces off by default; panel mounting remains conditional so disabled deployments gain no write controls.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-29
// Updated: 2026-10-29
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

/**
 * Feature switches for the operator-audit extension.
 *
 * Everything defaults to off. Panels return `null` while their switch is off,
 * so an unmounted or accidentally imported module cannot add a clickable write
 * path to the existing workbench.
 *
 * The demo profile below is for tests and an explicit demo configuration only.
 * Nothing reads it automatically; the Lead wires real server/config values.
 */

export type ExternalEvidenceMode = "off" | "fixture" | "live";
export type ReviewImpactMode = "off" | "explain" | "shadow";

export interface OperatorAuditFlags {
  capabilityPacksEnabled: boolean;
  externalEvidenceMode: ExternalEvidenceMode;
  fulfillmentChecksEnabled: boolean;
  reviewImpactMode: ReviewImpactMode;
  incrementalReuseEnabled: boolean;
}

/** Production default: every extension surface is disabled. */
export const DEFAULT_OPERATOR_AUDIT_FLAGS: OperatorAuditFlags = Object.freeze({
  capabilityPacksEnabled: false,
  externalEvidenceMode: "off",
  fulfillmentChecksEnabled: false,
  reviewImpactMode: "off",
  incrementalReuseEnabled: false,
});

/**
 * Explicit demo/test profile. Not a production default and never read from the
 * environment by this module. Reuse stays off so the UI cannot imply skipping.
 */
export const DEMO_OPERATOR_AUDIT_FLAGS: OperatorAuditFlags = Object.freeze({
  capabilityPacksEnabled: true,
  externalEvidenceMode: "fixture",
  fulfillmentChecksEnabled: true,
  reviewImpactMode: "explain",
  incrementalReuseEnabled: false,
});

export function externalEvidenceEnabled(flags: OperatorAuditFlags): boolean {
  return flags.externalEvidenceMode !== "off";
}

export function anyOperatorAuditFeatureEnabled(
  flags: OperatorAuditFlags = DEFAULT_OPERATOR_AUDIT_FLAGS,
): boolean {
  return (
    flags.capabilityPacksEnabled ||
    externalEvidenceEnabled(flags) ||
    flags.fulfillmentChecksEnabled ||
    flags.reviewImpactMode !== "off"
  );
}
