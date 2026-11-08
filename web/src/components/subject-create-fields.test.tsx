// File metadata (manifest mirror):
// Purpose: Checks the web behavior for “subject create input”; representative assertions cover sends asserted identity claims without client verification fields, keeps a missing code optional and rejects format-only values that are not complete. It protects the rendered/API state expected by this component without starting the full browser journey.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-08
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { subjectCreateInput } from "./subject-create-fields";

describe("subject create input", () => {
  it("sends asserted identity claims without client verification fields", () => {
    expect(subjectCreateInput({
      display_name: " 示例公司 ",
      legal_name: "示例有限公司",
      identifier_value: "9132000000000001x3",
    })).toEqual({
      display_name: "示例公司",
      legal_name: "示例有限公司",
      identifiers: [{
        namespace: "cn.gov.samr.uscc",
        identifier_type: "unified_social_credit_code",
        value: "9132000000000001X3",
      }],
    });
  });

  it("keeps a missing code optional and rejects format-only values that are not complete", () => {
    expect(subjectCreateInput({ display_name: "新主体" })).toEqual({ display_name: "新主体" });
    expect(() => subjectCreateInput({ display_name: "新主体", identifier_value: "not a code" })).toThrow(/请输入18位统一社会信用代码/);
    expect(() => subjectCreateInput({ display_name: " " })).toThrow(/主体名称/);
  });
});
