// File metadata (manifest mirror):
// Purpose: Checks the web behavior for “subject identity labels”; representative assertions cover keeps synthetic controlled verification distinct from ordinary verified status, uses case-role vocabulary without changing domain values. It protects the rendered/API state expected by this component without starting the full browser journey.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-10-31
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { subjectBindingStateLabel, subjectRoleLabel, subjectVerificationLabel } from "./subjects";

describe("subject identity labels", () => {
  it("keeps synthetic controlled verification distinct from ordinary verified status", () => {
    expect(subjectVerificationLabel("verified", "synthetic_controlled")).toBe("已对照核验（受控来源，合成演示）");
    expect(subjectVerificationLabel("verified", "official_controlled")).toBe("已对照核验（受控来源）");
    expect(subjectVerificationLabel("pending_verification", "synthetic_controlled")).toBe("待核验");
    expect(subjectVerificationLabel("inactive", "synthetic_controlled")).toBe("已停用");
  });

  it("uses case-role vocabulary without changing domain values", () => {
    expect(subjectRoleLabel("customer")).toBe("客户");
    expect(subjectRoleLabel("service_provider")).toBe("服务提供方");
    expect(subjectBindingStateLabel("pending_confirmation")).toBe("待确认绑定");
  });
});
