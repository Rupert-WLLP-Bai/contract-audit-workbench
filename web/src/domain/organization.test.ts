// File metadata (manifest mirror):
// Purpose: Checks the web behavior for “organizationName”; representative assertions cover names the demonstration organisation without hiding unknown scopes. It protects the rendered/API state expected by this component without starting the full browser journey.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-28
// Updated: 2026-11-14
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { describe, expect, it } from "vitest";
import {
  DISTRICT_ORGANIZATION_NAMES,
  organizationName,
  presentedPersonName,
  presentOrganizationMembers,
} from "./organization";

const DISTRICTS = [
  "万州区",
  "黔江区",
  "涪陵区",
  "渝中区",
  "大渡口区",
  "沙坪坝区",
  "九龙坡区",
  "南岸区",
  "北碚区",
  "巴南区",
  "长寿区",
  "江津区",
  "合川区",
  "永川区",
  "南川区",
  "綦江区",
  "大足区",
  "璧山区",
  "铜梁区",
  "潼南区",
  "荣昌区",
  "开州区",
  "梁平区",
  "武隆区",
  "两江新区",
];

describe("organizationName", () => {
  it("names the demonstration organisation without hiding unknown scopes", () => {
    expect(organizationName("demo-org")).toBe("两江新区");
    expect(organizationName("demo-other-org")).toBe("渝中区");
    expect(organizationName("cq-wanzhou")).toBe("万州区");
    expect(organizationName("branch-sales")).toBe("branch-sales");
    expect(organizationName(null)).toBe("正在确认");
    expect(DISTRICT_ORGANIZATION_NAMES).toEqual(DISTRICTS);
  });

  it("presents the district roster for the legacy local demo directory only", () => {
    const legacy = [
      {
        id: "demo-admin",
        display_name: "演示管理员",
        role: "admin",
        tenant_id: "local-demo",
        org_id: "demo-org",
      },
      {
        id: "demo-clerk",
        display_name: "演示经办",
        role: "clerk",
        tenant_id: "local-demo",
        org_id: "demo-org",
      },
      {
        id: "demo-reviewer",
        display_name: "演示审查员",
        role: "reviewer",
        tenant_id: "local-demo",
        org_id: "demo-org",
      },
      {
        id: "demo-viewer",
        display_name: "演示只读",
        role: "viewer",
        tenant_id: "local-demo",
        org_id: "demo-org",
      },
      {
        id: "demo-otherorg-clerk",
        display_name: "演示其他组织经办",
        role: "clerk",
        tenant_id: "local-demo",
        org_id: "demo-other-org",
      },
    ];
    const presented = presentOrganizationMembers(legacy);
    expect(presented.some((member) => member.display_name.includes("演示"))).toBe(false);
    expect(presented.find((member) => member.id === "demo-admin")).toMatchObject({
      display_name: "James Carter",
      org_id: "demo-org",
    });
    for (const district of ["cq-wanzhou", "demo-org", "demo-other-org"]) {
      const count = presented.filter((member) => member.org_id === district).length;
      expect(count).toBeGreaterThanOrEqual(2);
      expect(count).toBeLessThanOrEqual(4);
    }
    const supplied = [
      {
        id: "reviewer-1",
        display_name: "审查员甲",
        role: "reviewer",
        tenant_id: "tenant-public",
        org_id: "org-alpha",
      },
    ];
    expect(presentOrganizationMembers(supplied)).toEqual(supplied);
  });

  it("replaces only the previous demo person labels", () => {
    expect(presentedPersonName("demo-admin", "演示管理员")).toBe("James Carter");
    expect(presentedPersonName("demo-admin", "James Carter")).toBe("James Carter");
    expect(presentedPersonName("actor-1", "审查员甲")).toBe("审查员甲");
  });
});
