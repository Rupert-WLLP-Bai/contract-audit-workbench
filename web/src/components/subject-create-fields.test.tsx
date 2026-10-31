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
    expect(() => subjectCreateInput({ display_name: "新主体", identifier_value: "not a code" })).toThrow(/格式正确不代表身份已核验/);
    expect(() => subjectCreateInput({ display_name: " " })).toThrow(/主体名称/);
  });
});
